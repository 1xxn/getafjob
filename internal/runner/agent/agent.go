// Package agent is the runner's loop: it registers at the gateway, leases one run at a time, runs it in a fresh
// browser with only that run's documents, and reports every step over the run's token.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"

	runnerv1 "github.com/1xxn/getafjob/internal/gen/proto/runner/v1"
	"github.com/1xxn/getafjob/internal/gen/proto/runner/v1/runnerv1connect"
	"github.com/1xxn/getafjob/internal/platform/buildinfo"
	"github.com/1xxn/getafjob/internal/runner/ats"
	"github.com/1xxn/getafjob/internal/runner/browser"
	"github.com/1xxn/getafjob/internal/runner/engine"
	"github.com/1xxn/getafjob/internal/runner/executor"
	"github.com/1xxn/getafjob/internal/runner/planner"
)

// Agent leases and runs applications.
type Agent struct {
	Client      runnerv1connect.RunnerServiceClient
	Secret      string
	Browser     browser.Options
	Logger      *slog.Logger
	Kind        runnerv1.RunnerKind // server or home runner
	Poll        time.Duration       // between leases when there is nothing to do; 0: 15 s
	Wait        time.Duration       // per click; 0: the engine's default
	HandoffHold time.Duration       // how long a handoff keeps the browser open; 0: 15 min
	// QuestionHold is the hold when every question can be answered in the form (no captcha, code or sign-in); the
	// browser is then closed to free the runner and the answer starts a new run (#201). 0: 2 min.
	QuestionHold time.Duration
	Parallel     int // runs that work at the same time; runs waiting for the user do not count; 0: 1
}

// slots counts the runs of an agent. A run waiting for the user keeps its browser but frees its slot, so other
// applications go on; open browsers stay below twice the slots to bound memory.
type slots struct {
	mu       sync.Mutex
	parallel int
	open     int // browsers, working or waiting
	waiting  int
	changed  chan struct{}
}

func newSlots(parallel int) *slots {
	return &slots{parallel: max(parallel, 1), changed: make(chan struct{}, 1)}
}

func (s *slots) free() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.open-s.waiting < s.parallel && s.open < 2*s.parallel
}

func (s *slots) update(open, waiting int) {
	s.mu.Lock()
	s.open += open
	s.waiting += waiting
	s.mu.Unlock()
	select {
	case s.changed <- struct{}{}:
	default:
	}
}

type waitKey struct{}

// markWaiting tells the agent's slots that the run waits for the user (true) or works again (false).
func markWaiting(ctx context.Context, waiting bool) {
	if s, ok := ctx.Value(waitKey{}).(*slots); ok {
		if waiting {
			s.update(0, 1)
		} else {
			s.update(0, -1)
		}
	}
}

func auth(ctx context.Context, token string) context.Context {
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set("Authorization", "Bearer "+token)
	return ctx
}

// Run registers and works until ctx ends.
func (a *Agent) Run(ctx context.Context) error {
	poll := a.Poll
	if poll == 0 {
		poll = 15 * time.Second
	}
	kind := a.Kind
	if kind == runnerv1.RunnerKind_RUNNER_KIND_UNSPECIFIED {
		kind = runnerv1.RunnerKind_RUNNER_KIND_SERVER
	}
	var id string
	for id == "" { // the gateway may start after the runner
		reg, err := a.Client.Register(auth(ctx, a.Secret), &runnerv1.RegisterRequest{Version: buildinfo.Version,
			Kind: kind, MaxParallel: int32(max(min(a.Parallel, 64), 1))})
		if err == nil {
			id = reg.GetRunnerId()
			break
		}
		if connect.CodeOf(err) == connect.CodeUnauthenticated {
			return fmt.Errorf("register at the gateway: %w", err)
		}
		a.Logger.WarnContext(ctx, "gateway not reachable yet", "err", err)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(poll):
		}
	}
	a.Logger.InfoContext(ctx, "runner registered", "runner", id, "parallel", max(a.Parallel, 1))
	sl := newSlots(a.Parallel)
	var wg sync.WaitGroup
	defer wg.Wait()
	runCtx := context.WithValue(ctx, waitKey{}, sl)
	for {
		if !sl.free() {
			select {
			case <-ctx.Done():
				return nil
			case <-sl.changed:
			}
			continue
		}
		l, err := a.Client.Lease(auth(ctx, a.Secret), &runnerv1.LeaseRequest{RunnerId: id})
		switch {
		case ctx.Err() != nil:
			return nil
		case err != nil:
			a.Logger.WarnContext(ctx, "lease failed", "err", err)
		case l.GetRun() != nil:
			run := l.GetRun()
			sl.update(1, 0)
			wg.Go(func() {
				defer sl.update(-1, 0)
				if err := a.RunOne(runCtx, run); err != nil {
					a.Logger.WarnContext(ctx, "run ended with an error", "run", run.GetRunId(), "err", err)
				}
			})
			continue // ask for the next run at once
		}
		select {
		case <-ctx.Done():
			return nil
		case <-sl.changed:
		case <-time.After(poll):
		}
	}
}

// RunOne runs a leased application in a fresh browser; the documents live in a temporary directory that is
// removed afterwards.
func (a *Agent) RunOne(ctx context.Context, run *runnerv1.Run) error {
	ctx, cancel := context.WithDeadline(ctx, run.GetExpiresAt().AsTime())
	defer cancel()
	tok := run.GetToken()
	c := &coordinator{client: a.Client, token: tok}

	// Register handoff session with gateway immediately so screencast can be viewed at any time (#157).
	_, _ = a.Client.Handoff(auth(ctx, tok), &runnerv1.HandoffRequest{})
	defer func() {
		_, _ = a.Client.Handoff(auth(context.WithoutCancel(ctx), tok), &runnerv1.HandoffRequest{Done: true})
	}()

	dir, err := os.MkdirTemp("", "getafjob-run-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	k, err := a.knowledge(ctx, run, dir)
	if err != nil {
		return c.Park(ctx, "documents: "+err.Error(), nil)
	}
	docs, err := browser.NewDocuments(dir)
	if err != nil {
		return err
	}
	b, err := browser.Launch(ctx, a.Browser)
	if err != nil {
		return c.Park(ctx, "browser: "+err.Error(), nil)
	}
	defer func() { _ = b.Close(context.WithoutCancel(ctx)) }()
	p, err := b.Page(proto.TargetCreateTarget{})
	if err != nil {
		return err
	}
	stopStream := a.startLiveScreencast(ctx, p, tok)
	defer func() { stopStream() }()

	if len(run.GetSessionCookiesJson()) > 0 {
		var cookies []*proto.NetworkCookieParam
		if err := json.Unmarshal(run.GetSessionCookiesJson(), &cookies); err == nil && len(cookies) > 0 {
			_ = (proto.NetworkSetCookies{Cookies: cookies}).Call(p)
		}
	}
	ad := ats.For("", append([]string{run.GetStartUrl()}, run.GetOrigins()...)...)
	e := ad.NewEngine(&executor.Executor{Docs: docs}, &planner.Planner{K: k}, c, a.Wait)
	if err := p.Context(ctx).Navigate(run.GetStartUrl()); err != nil {
		return c.Park(ctx, "open the posting: "+err.Error(), nil)
	}
	_ = p.Context(ctx).WaitLoad()
	if (ats.IsPortal(ad.Name) || run.GetPortal() != "") && p.MustInfo() != nil {
		curURL := p.MustInfo().URL
		html, _ := p.HTML()
		if ats.IsSessionExpired(run.GetPortal(), curURL, html) {
			a.Logger.WarnContext(ctx, "portal session expired, requesting handoff", "portal", run.GetPortal())
			if err := c.Ask(ctx, []engine.Question{{
				Kind:  "session_expired",
				Label: fmt.Sprintf("Portal session expired for %s. Please sign in again in the remote browser.", run.GetPortal()),
			}}); err != nil {
				return err
			}
			_, ok, herr := a.handoff(ctx, p, c, run)
			if herr != nil {
				return herr
			}
			if !ok {
				return nil
			}
			if res, err := (proto.NetworkGetCookies{}).Call(p); err == nil && len(res.Cookies) > 0 {
				params := proto.CookiesToParams(res.Cookies)
				if updatedJSON, err := json.Marshal(params); err == nil {
					_ = c.CheckpointWithCookies(ctx, engine.Checkpoint{
						Page: 1, Step: engine.StepPrepare, URL: p.MustInfo().URL,
					}, updatedJSON)
				}
			}
			if err := p.Context(ctx).Navigate(run.GetStartUrl()); err != nil {
				return c.Park(ctx, "open posting after sign in: "+err.Error(), nil)
			}
			_ = p.Context(ctx).WaitLoad()
		}
	}
	form, err := e.Prepare(ctx, p)
	if err != nil {
		shot, _ := p.Screenshot(false, nil)
		return c.Park(ctx, "find the form: "+err.Error(), shot)
	}
	if form != nil && form != p {
		stopStream()
		stopStream = a.startLiveScreencast(ctx, form, tok)
	}
	for {
		err = e.Run(ctx, form)
		switch {
		case errors.Is(err, engine.ErrAsked):
			ans, ok, herr := a.handoff(ctx, form, c, run)
			if herr != nil {
				return herr
			}
			if !ok {
				return nil
			}
			for _, an := range ans {
				k.Answers = append(k.Answers, planner.Answer{ID: an.GetId(), Question: an.GetQuestion(), Answer: an.GetAnswer()})
			}
			e.Planner = &planner.Planner{K: k}
		case err == nil, errors.Is(err, engine.ErrParked):
			return nil
		case c.submitting:
			// The submit was registered and the click may have gone out: no report; the server turns the run into
			// submit_unknown, which is reconciled and never retried.
			return err
		default:
			shot, _ := form.Screenshot(false, nil)
			return errors.Join(err, c.Park(ctx, "run: "+err.Error(), shot))
		}
	}
}

// startLiveScreencast streams the page to the gateway for the live view during the whole run. Frames are acked at
// once and sent from a goroutine that always takes the newest frame, so a slow gateway never stalls Chromium.
func (a *Agent) startLiveScreencast(ctx context.Context, p *rod.Page, tok string) func() {
	if p == nil {
		return func() {}
	}
	quality, maxW, maxH := 60, 1280, 1024
	if err := (proto.PageStartScreencast{
		Format:    proto.PageStartScreencastFormatJpeg,
		Quality:   &quality,
		MaxWidth:  &maxW,
		MaxHeight: &maxH,
	}).Call(p); err != nil {
		a.Logger.WarnContext(ctx, "live view unavailable", "err", err)
		return func() {}
	}
	sctx, stop := context.WithCancel(ctx)
	latest := make(chan []byte, 1)
	waitEvents := p.Context(sctx).EachEvent(func(e *proto.PageScreencastFrame) {
		_ = (proto.PageScreencastFrameAck{SessionID: e.SessionID}).Call(p)
		select {
		case <-latest: // replace an unsent frame with the newer one
		default:
		}
		select {
		case latest <- e.Data:
		default:
		}
	})
	var wg sync.WaitGroup
	wg.Go(waitEvents)
	wg.Go(func() {
		// A still page sends no frames; the keepalive tells the gateway the run's browser is alive (#160).
		keepalive := time.NewTicker(20 * time.Second)
		defer keepalive.Stop()
		for {
			select {
			case <-sctx.Done():
				return
			case f := <-latest:
				_, _ = a.Client.Handoff(auth(sctx, tok), &runnerv1.HandoffRequest{Frame: f})
			case <-keepalive.C:
				_, _ = a.Client.Handoff(auth(sctx, tok), &runnerv1.HandoffRequest{})
			}
		}
	})
	return func() {
		stop()
		_ = (proto.PageStopScreencast{}).Call(p)
		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	}
}

// handoff holds the browser session for the user (default 15 min) and dispatches user input back to Chromium
// without ever logging or storing the input; the run's live stream keeps running meanwhile.
func (a *Agent) handoff(ctx context.Context, p *rod.Page, c *coordinator, run *runnerv1.Run) ([]*runnerv1.Answer, bool, error) {
	hold, reason := a.HandoffHold, "handoff window closed"
	if hold <= 0 {
		hold = 15 * time.Minute
	}
	if formAnswerable(c.asked) {
		hold, reason = a.QuestionHold, "waiting for your answers; the browser was closed to free the runner"
		if hold <= 0 {
			hold = 2 * time.Minute
		}
	}
	hctx, cancel := context.WithTimeout(ctx, hold)
	defer cancel()
	markWaiting(ctx, true)
	defer markWaiting(ctx, false)

	tok := run.GetToken()
	if _, err := a.Client.Handoff(auth(hctx, tok), &runnerv1.HandoffRequest{}); err != nil {
		if connect.CodeOf(err) == connect.CodeUnimplemented {
			return nil, false, nil
		}
		return nil, false, err
	}

	for {
		res, err := a.Client.Handoff(auth(hctx, tok), &runnerv1.HandoffRequest{Wait: true})
		if err != nil {
			if connect.CodeOf(err) == connect.CodeUnimplemented {
				return nil, false, nil
			}
			if hctx.Err() != nil {
				shot, _ := p.Screenshot(false, nil)
				return nil, false, c.Park(ctx, reason, shot)
			}
			return nil, false, err
		}
		for _, ev := range res.GetEvents() {
			switch {
			case ev.GetResume():
				return ev.GetAnswers(), true, nil
			case ev.GetPark():
				shot, _ := p.Screenshot(false, nil)
				return nil, false, c.Park(ctx, "parked by user during handoff", shot)
			case ev.GetMouse() != nil:
				m := ev.GetMouse()
				_ = (proto.InputDispatchMouseEvent{
					Type:       proto.InputDispatchMouseEventType(m.GetType()),
					X:          m.GetX(),
					Y:          m.GetY(),
					Button:     proto.InputMouseButton(m.GetButton()),
					ClickCount: int(m.GetClickCount()),
				}).Call(p)
			case ev.GetKey() != nil:
				k := ev.GetKey()
				_ = (proto.InputDispatchKeyEvent{
					Type:                  proto.InputDispatchKeyEventType(k.GetType()),
					Text:                  k.GetText(),
					Key:                   k.GetKey(),
					Code:                  k.GetCode(),
					WindowsVirtualKeyCode: int(k.GetWindowsVirtualKeyCode()),
				}).Call(p)
			}
		}
	}
}

// knowledge fetches the run's documents into dir and builds the planner's knowledge.
func (a *Agent) knowledge(ctx context.Context, run *runnerv1.Run, dir string) (planner.Knowledge, error) {
	k := planner.Knowledge{Profile: run.GetProfile(), CV: run.GetCvDocumentId(), Language: run.GetLanguage(),
		Origins: run.GetOrigins()}
	for _, an := range run.GetAnswers() {
		k.Answers = append(k.Answers, planner.Answer{ID: an.GetId(), Question: an.GetQuestion(), Answer: an.GetAnswer()})
	}
	for _, t := range run.GetTexts() {
		k.Texts = append(k.Texts, planner.Text{ID: t.GetId(), Name: t.GetName(), Language: t.GetLanguage(), Body: t.GetBody()})
	}
	for _, w := range run.GetWorkExperiences() {
		k.WorkExperiences = append(k.WorkExperiences, planner.WorkExperience{
			ID:                 w.GetId(),
			Company:            w.GetCompany(),
			Title:              w.GetTitle(),
			Location:           w.GetLocation(),
			WorkplaceType:      w.GetWorkplaceType(),
			StartDate:          w.GetStartDate(),
			EndDate:            w.GetEndDate(),
			IsCurrent:          w.GetIsCurrent(),
			DescriptionBullets: w.GetDescriptionBullets(),
			Description:        strings.Join(w.GetDescriptionBullets(), "\n• "),
			SortOrder:          int(w.GetSortOrder()),
		})
	}
	for _, e := range run.GetEducations() {
		k.Educations = append(k.Educations, planner.EducationStation{
			ID:           e.GetId(),
			Institution:  e.GetInstitution(),
			Degree:       e.GetDegree(),
			FieldOfStudy: e.GetFieldOfStudy(),
			StartDate:    e.GetStartDate(),
			EndDate:      e.GetEndDate(),
			IsCurrent:    e.GetIsCurrent(),
			Grade:        e.GetGrade(),
			SortOrder:    int(e.GetSortOrder()),
		})
	}
	used := map[string]bool{}
	for _, d := range run.GetDocuments() {
		name := safeName(d.GetFileName(), d.GetId())
		if used[name] {
			name = d.GetId() + "-" + name
		}
		used[name] = true
		resp, err := a.Client.FetchDocument(auth(ctx, run.GetToken()), &runnerv1.FetchDocumentRequest{DocumentId: d.GetId()})
		if err != nil {
			return k, err
		}
		if err := os.WriteFile(filepath.Join(dir, name), resp.GetContent(), 0o600); err != nil {
			return k, err
		}
		k.Documents = append(k.Documents, planner.Document{ID: d.GetId(), File: name, Kind: d.GetKind(), Language: d.GetLanguage()})
	}
	return k, nil
}

// safeName keeps a file name as the form will show it, without any path; fallback is the document ID.
func safeName(name, id string) string {
	name = filepath.Base(strings.ReplaceAll(name, `\`, "/"))
	if name == "." || name == "/" || name == ".." || strings.TrimSpace(name) == "" || len(name) > 200 {
		return id + ".pdf"
	}
	return name
}

// coordinator reports a run to the gateway.
type coordinator struct {
	client     runnerv1connect.RunnerServiceClient
	token      string
	submitting bool
	asked      []engine.Question // the last questions, to pick the handoff hold
}

// formAnswerable reports whether the user can answer every asked question in the web form, without the browser.
func formAnswerable(qs []engine.Question) bool {
	for _, q := range qs {
		if q.Kind != "field" {
			return false
		}
	}
	return len(qs) > 0
}

var _ engine.Coordinator = (*coordinator)(nil)

func (c *coordinator) Checkpoint(ctx context.Context, cp engine.Checkpoint) error {
	data, err := json.Marshal(cp)
	if err != nil {
		return err
	}
	if len(data) > 200<<10 { // page data is capped by the gateway; keep the step, drop the details
		data, _ = json.Marshal(map[string]any{"step": cp.Step, "page": cp.Page, "truncated": true})
	}
	_, err = c.client.Checkpoint(auth(ctx, c.token), &runnerv1.CheckpointRequest{Step: string(cp.Step), Page: int32(min(cp.Page, 100)), //nolint:gosec // bounded
		Url: cp.URL, DataJson: data, Screenshot: cp.Screenshot})
	return err
}

func (c *coordinator) CheckpointWithCookies(ctx context.Context, cp engine.Checkpoint, cookiesJSON []byte) error {
	data, err := json.Marshal(cp)
	if err != nil {
		return err
	}
	if len(data) > 200<<10 {
		data, _ = json.Marshal(map[string]any{"step": cp.Step, "page": cp.Page, "truncated": true})
	}
	_, err = c.client.Checkpoint(auth(ctx, c.token), &runnerv1.CheckpointRequest{
		Step:                      string(cp.Step),
		Page:                      int32(min(cp.Page, 100)), //nolint:gosec // bounded
		Url:                       cp.URL,
		DataJson:                  data,
		Screenshot:                cp.Screenshot,
		UpdatedSessionCookiesJson: cookiesJSON,
	})
	return err
}

func (c *coordinator) Ask(ctx context.Context, qs []engine.Question) error {
	c.asked = qs
	req := &runnerv1.AskRequest{}
	for _, q := range qs {
		req.Questions = append(req.Questions, &runnerv1.Question{Kind: q.Kind, Label: q.Label, Draft: q.Draft,
			Input: q.Input, Options: q.Options})
	}
	_, err := c.client.Ask(auth(ctx, c.token), req)
	return err
}

func (c *coordinator) BeginSubmit(ctx context.Context) error {
	if _, err := c.client.BeginSubmit(auth(ctx, c.token), &runnerv1.BeginSubmitRequest{}); err != nil {
		return err
	}
	c.submitting = true
	return nil
}

func (c *coordinator) Submitted(ctx context.Context, confirmed bool, eff browser.Effect) error {
	b, _ := json.Marshal(eff)
	_, err := c.client.Submitted(auth(ctx, c.token), &runnerv1.SubmittedRequest{Confirmed: confirmed, EffectJson: b})
	if err == nil {
		c.submitting = false
	}
	return err
}

func (c *coordinator) Park(ctx context.Context, reason string, screenshot []byte) error {
	_, err := c.client.Park(auth(ctx, c.token), &runnerv1.ParkRequest{Reason: reason, Screenshot: screenshot})
	return err
}
