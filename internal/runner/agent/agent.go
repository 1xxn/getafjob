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

	"connectrpc.com/connect"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"

	runnerv1 "github.com/1xxn/getafjob/internal/gen/proto/runner/v1"
	"github.com/1xxn/getafjob/internal/gen/proto/runner/v1/runnerv1connect"
	"github.com/1xxn/getafjob/internal/platform/buildinfo"
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
	Poll        time.Duration // between leases when there is nothing to do; 0: 15 s
	Wait        time.Duration // per click; 0: the engine's default
	HandoffHold time.Duration // how long a handoff keeps the browser open; 0: 15 min
}

func auth[T any](msg *T, token string) *connect.Request[T] {
	r := connect.NewRequest(msg)
	r.Header().Set("Authorization", "Bearer "+token)
	return r
}

// Run registers and works until ctx ends.
func (a *Agent) Run(ctx context.Context) error {
	poll := a.Poll
	if poll == 0 {
		poll = 15 * time.Second
	}
	var id string
	for id == "" { // the gateway may start after the runner
		reg, err := a.Client.Register(ctx, auth(&runnerv1.RegisterRequest{Version: buildinfo.Version,
			Kind: runnerv1.RunnerKind_RUNNER_KIND_SERVER, MaxParallel: 1}, a.Secret))
		if err == nil {
			id = reg.Msg.GetRunnerId()
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
		l, err := a.Client.Lease(ctx, auth(&runnerv1.LeaseRequest{RunnerId: id}, a.Secret))
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case err != nil:
			a.Logger.WarnContext(ctx, "lease failed", "err", err)
		case l.Msg.GetRun() != nil:
			if err := a.RunOne(ctx, l.Msg.GetRun()); err != nil {
				a.Logger.WarnContext(ctx, "run ended with an error", "run", l.Msg.GetRun().GetRunId(), "err", err)
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
	c := &coordinator{client: a.Client, token: run.GetToken()}
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
	e := &engine.Engine{Exec: &executor.Executor{Docs: docs}, Planner: &planner.Planner{K: k}, Coord: c, Wait: a.Wait}
	if err := p.Context(ctx).Navigate(run.GetStartUrl()); err != nil {
		return c.Park(ctx, "open the posting: "+err.Error(), nil)
	}
	_ = p.Context(ctx).WaitLoad()
	form, err := e.Prepare(ctx, p)
	if err != nil {
		shot, _ := p.Screenshot(false, nil)
		return c.Park(ctx, "find the form: "+err.Error(), shot)
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
	if _, err := a.Client.Handoff(hctx, auth(&runnerv1.HandoffRequest{}, tok)); err != nil {
		if connect.CodeOf(err) == connect.CodeUnimplemented {
			return nil, false, nil
		}
		return nil, false, err
	}
	defer func() {
		_, _ = a.Client.Handoff(context.WithoutCancel(ctx), auth(&runnerv1.HandoffRequest{Done: true}, tok))
	}()

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
		_, _ = a.Client.Handoff(sctx, auth(&runnerv1.HandoffRequest{Frame: e.Data}, tok))
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
		res, err := a.Client.Handoff(hctx, auth(&runnerv1.HandoffRequest{Wait: true}, tok))
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
		for _, ev := range res.Msg.GetEvents() {
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
	used := map[string]bool{}
	for _, d := range run.GetDocuments() {
		name := safeName(d.GetFileName(), d.GetId())
		if used[name] {
			name = d.GetId() + "-" + name
		}
		used[name] = true
		resp, err := a.Client.FetchDocument(ctx, auth(&runnerv1.FetchDocumentRequest{DocumentId: d.GetId()}, run.GetToken()))
		if err != nil {
			return k, err
		}
		if err := os.WriteFile(filepath.Join(dir, name), resp.Msg.GetContent(), 0o600); err != nil {
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
	_, err = c.client.Checkpoint(ctx, auth(&runnerv1.CheckpointRequest{Step: string(cp.Step), Page: int32(min(cp.Page, 100)), //nolint:gosec // bounded
		Url: cp.URL, DataJson: data, Screenshot: cp.Screenshot}, c.token))
	return err
}

func (c *coordinator) Ask(ctx context.Context, qs []engine.Question) error {
	req := &runnerv1.AskRequest{}
	for _, q := range qs {
		req.Questions = append(req.Questions, &runnerv1.Question{Kind: q.Kind, Label: q.Label, Draft: q.Draft})
	}
	_, err := c.client.Ask(ctx, auth(req, c.token))
	return err
}

func (c *coordinator) BeginSubmit(ctx context.Context) error {
	if _, err := c.client.BeginSubmit(ctx, auth(&runnerv1.BeginSubmitRequest{}, c.token)); err != nil {
		return err
	}
	c.submitting = true
	return nil
}

func (c *coordinator) Submitted(ctx context.Context, confirmed bool, eff browser.Effect) error {
	b, _ := json.Marshal(eff)
	_, err := c.client.Submitted(ctx, auth(&runnerv1.SubmittedRequest{Confirmed: confirmed, EffectJson: b}, c.token))
	if err == nil {
		c.submitting = false
	}
	return err
}

func (c *coordinator) Park(ctx context.Context, reason string, screenshot []byte) error {
	_, err := c.client.Park(ctx, auth(&runnerv1.ParkRequest{Reason: reason, Screenshot: screenshot}, c.token))
	return err
}
