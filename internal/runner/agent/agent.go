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
			Kind: kind, MaxParallel: 1})
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
	a.Logger.InfoContext(ctx, "runner registered", "runner", id)
	for {
		l, err := a.Client.Lease(auth(ctx, a.Secret), &runnerv1.LeaseRequest{RunnerId: id})
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case err != nil:
			a.Logger.WarnContext(ctx, "lease failed", "err", err)
		case l.GetRun() != nil:
			if err := a.RunOne(ctx, l.GetRun()); err != nil {
				a.Logger.WarnContext(ctx, "run ended with an error", "run", l.GetRun().GetRunId(), "err", err)
			}
			continue // ask for the next run at once
		}
		select {
		case <-ctx.Done():
			return nil
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
	defer func() {
		if stopStream != nil {
			stopStream()
		}
	}()

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
			if stopStream != nil {
				stopStream()
				stopStream = nil
			}
			_, ok, herr := a.handoff(ctx, p, c, run)
			if herr != nil {
				return herr
			}
			if !ok {
				return nil
			}
			stopStream = a.startLiveScreencast(ctx, p, tok)
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
		if stopStream != nil {
			stopStream()
		}
		stopStream = a.startLiveScreencast(ctx, form, tok)
	}
	for {
		err = e.Run(ctx, form)
		switch {
		case errors.Is(err, engine.ErrAsked):
			if stopStream != nil {
				stopStream()
				stopStream = nil
			}
			ans, ok, herr := a.handoff(ctx, form, c, run)
			if herr != nil {
				return herr
			}
			if !ok {
				return nil
			}
			stopStream = a.startLiveScreencast(ctx, form, tok)
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

// startLiveScreencast streams CDP screencast frames to the gateway in the background throughout the run (#157).
func (a *Agent) startLiveScreencast(ctx context.Context, p *rod.Page, tok string) func() {
	if p == nil {
		return func() {}
	}
	quality := 70
	if err := (proto.PageStartScreencast{
		Format:  proto.PageStartScreencastFormatJpeg,
		Quality: &quality,
	}).Call(p); err != nil {
		return func() {}
	}
	sctx, stopScreencast := context.WithCancel(ctx)
	waitEvents := p.Context(sctx).EachEvent(func(e *proto.PageScreencastFrame) {
		_, _ = a.Client.Handoff(auth(sctx, tok), &runnerv1.HandoffRequest{Frame: e.Data})
		_ = (proto.PageScreencastFrameAck{SessionID: e.SessionID}).Call(p)
	})
	eventsDone := make(chan struct{})
	go func() {
		waitEvents()
		close(eventsDone)
	}()
	return func() {
		stopScreencast()
		_ = (proto.PageStopScreencast{}).Call(p)
		select {
		case <-eventsDone:
		case <-time.After(2 * time.Second):
		}
	}
}

// handoff holds the browser session for the user (default 15 min), streams screencast frames to the gateway,
// and dispatches user input back to Chromium without ever logging or storing the input.
func (a *Agent) handoff(ctx context.Context, p *rod.Page, c *coordinator, run *runnerv1.Run) ([]*runnerv1.Answer, bool, error) {
	hold := a.HandoffHold
	if hold <= 0 {
		hold = 15 * time.Minute
	}
	hctx, cancel := context.WithTimeout(ctx, hold)
	defer cancel()

	tok := run.GetToken()
	if _, err := a.Client.Handoff(auth(hctx, tok), &runnerv1.HandoffRequest{}); err != nil {
		if connect.CodeOf(err) == connect.CodeUnimplemented {
			return nil, false, nil
		}
		return nil, false, err
	}

	quality := 70
	if err := (proto.PageStartScreencast{
		Format:  proto.PageStartScreencastFormatJpeg,
		Quality: &quality,
	}).Call(p); err != nil {
		return nil, false, err
	}
	defer func() { _ = (proto.PageStopScreencast{}).Call(p) }()

	sctx, stopScreencast := context.WithCancel(hctx)
	waitEvents := p.Context(sctx).EachEvent(func(e *proto.PageScreencastFrame) {
		_, _ = a.Client.Handoff(auth(sctx, tok), &runnerv1.HandoffRequest{Frame: e.Data})
		_ = (proto.PageScreencastFrameAck{SessionID: e.SessionID}).Call(p)
	})
	eventsDone := make(chan struct{})
	go func() {
		waitEvents()
		close(eventsDone)
	}()
	defer func() {
		stopScreencast()
		<-eventsDone
	}()

	for {
		res, err := a.Client.Handoff(auth(hctx, tok), &runnerv1.HandoffRequest{Wait: true})
		if err != nil {
			if connect.CodeOf(err) == connect.CodeUnimplemented {
				return nil, false, nil
			}
			if hctx.Err() != nil {
				shot, _ := p.Screenshot(false, nil)
				return nil, false, c.Park(ctx, "handoff window closed", shot)
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
	req := &runnerv1.AskRequest{}
	for _, q := range qs {
		req.Questions = append(req.Questions, &runnerv1.Question{Kind: q.Kind, Label: q.Label, Draft: q.Draft})
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
