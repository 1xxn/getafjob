// Package gateway serves the runner gateway (ADR 0007): runners lease one run at a time over Connect RPC with only
// the data it needs and a short-lived token; they never reach the database.
package gateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/google/uuid"
	gotel "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/1xxn/getafjob/internal/application"
	runnerv1 "github.com/1xxn/getafjob/internal/gen/proto/runner/v1"
	"github.com/1xxn/getafjob/internal/gen/proto/runner/v1/runnerv1connect"
	"github.com/1xxn/getafjob/internal/runner/ats"
)

// Limits of run calls.
const (
	MaxMessage    = 12 << 20 // a screenshot plus page data
	maxScreenshot = 8 << 20
	maxData       = 256 << 10
)

// Data assembles what a run needs (profile facts, answers, texts, documents) without the run's ID and token.
type Data interface {
	RunData(ctx context.Context, userID uuid.UUID, jobID int64) (*runnerv1.Run, error)
	Document(ctx context.Context, userID uuid.UUID, documentID string) ([]byte, error)
}

// Screenshots keeps run screenshots sealed per user (blob store).
type Screenshots interface {
	Put(ctx context.Context, userID uuid.UUID, key string, data []byte) error
}

// PortalSessionSaver stores updated portal session cookies captured during a run.
type PortalSessionSaver interface {
	SaveCookies(ctx context.Context, userID uuid.UUID, portal string, cookiesJSON []byte, dailyLimit int) error
}

// Server implements the runner service.
type Server struct {
	Runs   *application.Runs
	Apps   *application.Service
	Data   Data
	Shots  Screenshots
	Secret string // shared by the server's runners; empty: no runner can register
	Logger *slog.Logger
	Notify func(ctx context.Context, userID, appID uuid.UUID)
	// NotifyApproval tells the user that a filled application waits for the approval (nil: no notice).
	NotifyApproval func(ctx context.Context, userID, appID uuid.UUID)
	ATSMetrics     *ats.Tracker
	Portals        PortalSessionSaver
	StepDuration   metric.Float64Histogram
	handoffs       sync.Map // map[uuid.UUID]*HandoffSession
	runners        sync.Map // map[string]runnerSession
	runSteps       sync.Map // map[uuid.UUID]runStepState
	frameAuth      sync.Map // map[string]frameGrant, token -> run for screencast frames
	seen           sync.Map // map[uuid.UUID]time.Time, run -> last call of its runner
	sessionOf      sync.Map // map[string]uuid.UUID, run token -> application of its live session
}

// Lost runs: a runner calls at least every keepalive interval (agent: 20 s); after reapIdle without a call the
// run's browser is gone and its application is parked.
const (
	reapIdle  = 2 * time.Minute
	reapEvery = 30 * time.Second
)

// ReapLost parks, every reapEvery until ctx ends, the applications of runs whose runner stopped calling (#160).
// After a server restart every unfinished run gets reapIdle from the start to call again.
func (s *Server) ReapLost(ctx context.Context) {
	since := time.Now()
	lastSeen := func(id uuid.UUID) (time.Time, bool) {
		v, ok := s.seen.Load(id)
		if !ok {
			return time.Time{}, false
		}
		return v.(time.Time), true
	}
	t := time.NewTicker(reapEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		n, err := s.Runs.ReapLost(ctx, lastSeen, since, reapIdle)
		if n > 0 && s.Logger != nil {
			s.Logger.WarnContext(ctx, "parked runs whose runner stopped", "count", n)
		}
		if err != nil && s.Logger != nil {
			s.Logger.WarnContext(ctx, "reap lost runs", "err", err)
		}
	}
}

// frameGrantTTL bounds how long a frame call trusts an earlier token check; frames only publish to viewers.
const frameGrantTTL = 10 * time.Second

type frameGrant struct {
	ref     application.RunRef
	expires time.Time
}

type runStepState struct {
	at      time.Time
	adapter string
}

type runnerIdentity struct {
	kind   runnerv1.RunnerKind
	userID uuid.UUID
}

type runnerSession struct {
	runnerID string
	kind     runnerv1.RunnerKind
	userID   uuid.UUID
}

// HandoffSession is the live view of one run: the runner publishes screencast frames for the whole run, any number
// of viewers subscribe, and viewer input reaches the runner while it waits for the user.
type HandoffSession struct {
	UserID uuid.UUID
	Input  chan *runnerv1.HandoffEvent
	Done   chan struct{}
	once   sync.Once
	mu     sync.Mutex
	last   []byte
	subs   map[chan []byte]struct{}
	waited time.Time // last time the runner waited for input, i.e. the user has control
}

// interactiveFor is how long after its last wait call the runner still counts as waiting for input; the runner
// repeats the call at least every 3 seconds while it holds the handoff.
const interactiveFor = 5 * time.Second

// MarkWaiting records that the runner waits for the user right now.
func (h *HandoffSession) MarkWaiting() {
	h.mu.Lock()
	h.waited = time.Now()
	h.mu.Unlock()
}

// Interactive reports whether the runner currently waits for the user, so viewer input reaches the page.
func (h *HandoffSession) Interactive() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return time.Since(h.waited) < interactiveFor
}

// NewHandoffSession starts the live view session of one run of the user.
func NewHandoffSession(userID uuid.UUID) *HandoffSession {
	return &HandoffSession{UserID: userID, Input: make(chan *runnerv1.HandoffEvent, 50), Done: make(chan struct{}),
		subs: map[chan []byte]struct{}{}}
}

func (h *HandoffSession) closeDone() {
	h.once.Do(func() { close(h.Done) })
}

// LastFrame returns the most recent screencast frame of the session, if any.
func (h *HandoffSession) LastFrame() []byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.last
}

// Subscribe returns a channel with the newest frames for one viewer; a slow viewer skips frames instead of
// slowing down others. Call the returned func to unsubscribe.
func (h *HandoffSession) Subscribe() (<-chan []byte, func()) {
	ch := make(chan []byte, 2)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
	}
}

// Publish stores the newest frame and hands it to every viewer.
func (h *HandoffSession) Publish(f []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.last = f
	for ch := range h.subs {
		select {
		case ch <- f:
		default: // drop the oldest queued frame, keep the newest
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- f:
			default:
			}
		}
	}
}

// SessionFor returns the active handoff session of an application, if any.
func (s *Server) SessionFor(appID uuid.UUID) (*HandoffSession, bool) {
	v, ok := s.handoffs.Load(appID)
	if !ok {
		return nil, false
	}
	return v.(*HandoffSession), true
}

// ActiveRuns returns the application IDs of the user's runs that currently stream a live view.
func (s *Server) ActiveRuns(userID uuid.UUID) []uuid.UUID {
	var ids []uuid.UUID
	s.handoffs.Range(func(key, value any) bool {
		if id, ok := key.(uuid.UUID); ok && value.(*HandoffSession).UserID == userID {
			ids = append(ids, id)
		}
		return true
	})
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	return ids
}

var _ runnerv1connect.RunnerServiceHandler = (*Server)(nil)

// Mount mounts the service onto mux.
func (s *Server) Mount(mux *http.ServeMux) {
	srv := connect.NewServer()
	runnerv1connect.RegisterRunnerServiceHandler(srv, s)
	connecthttp.Mount(mux, srv, connecthttp.WithReadMaxBytes(MaxMessage))
}

func bearer(h *connect.Header) string {
	if h == nil {
		return ""
	}
	v := h.Get("Authorization")
	if t, ok := strings.CutPrefix(v, "Bearer "); ok {
		return strings.TrimSpace(t)
	}
	return ""
}

// runner checks the runner secret (constant time) or a user's home runner token.
func (s *Server) runner(ctx context.Context) (runnerIdentity, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	var h *connect.Header
	if info != nil {
		h = info.RequestHeader()
	}
	tok := bearer(h)
	if tok == "" {
		return runnerIdentity{}, connect.NewError(connect.CodeUnauthenticated, "missing token")
	}
	if s.Secret != "" {
		got, want := sha256.Sum256([]byte(tok)), sha256.Sum256([]byte(s.Secret))
		if subtle.ConstantTimeCompare(got[:], want[:]) == 1 {
			return runnerIdentity{kind: runnerv1.RunnerKind_RUNNER_KIND_SERVER}, nil
		}
	}
	if s.Runs != nil {
		hash := sha256.Sum256([]byte(tok))
		if userID, err := s.Runs.UserByHomeRunnerToken(ctx, hash[:]); err == nil && userID != uuid.Nil {
			return runnerIdentity{kind: runnerv1.RunnerKind_RUNNER_KIND_HOME, userID: userID}, nil
		}
	}
	return runnerIdentity{}, connect.NewError(connect.CodeUnauthenticated, "runner not authenticated")
}

// run returns the run of the call's token.
func (s *Server) run(ctx context.Context) (application.RunRef, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	var h *connect.Header
	if info != nil {
		h = info.RequestHeader()
	}
	ref, err := s.Runs.Authorize(ctx, bearer(h))
	if errors.Is(err, application.ErrNoRun) {
		return ref, connect.NewError(connect.CodeUnauthenticated, err.Error())
	}
	if err == nil {
		s.seen.Store(ref.RunID, time.Now())
	}
	return ref, err
}

// callToken returns the bearer token of a run call.
func callToken(ctx context.Context) string {
	info, _ := connect.CallInfoForServerContext(ctx)
	if info == nil {
		return ""
	}
	return bearer(info.RequestHeader())
}

// frameRun authorizes a screencast frame call, reusing a check of the same token from the last frameGrantTTL.
func (s *Server) frameRun(ctx context.Context) (application.RunRef, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	var h *connect.Header
	if info != nil {
		h = info.RequestHeader()
	}
	key := bearer(h)
	if v, ok := s.frameAuth.Load(key); ok && time.Now().Before(v.(frameGrant).expires) {
		s.seen.Store(v.(frameGrant).ref.RunID, time.Now())
		return v.(frameGrant).ref, nil
	}
	ref, err := s.run(ctx)
	if err != nil {
		s.frameAuth.Delete(key)
		return ref, err
	}
	s.frameAuth.Store(key, frameGrant{ref: ref, expires: time.Now().Add(frameGrantTTL)})
	return ref, nil
}

// code maps application errors to Connect codes.
func code(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, application.ErrTransition), errors.Is(err, application.ErrAlreadySubmitted),
		errors.Is(err, application.ErrConflict):
		return connect.NewError(connect.CodeFailedPrecondition, err.Error())
	case errors.Is(err, application.ErrNoRun):
		return connect.NewError(connect.CodeUnauthenticated, err.Error())
	case errors.Is(err, application.ErrNeedsApproval):
		return connect.NewError(connect.CodeAborted, err.Error())
	case errors.Is(err, application.ErrExcluded), errors.Is(err, application.ErrDuplicate),
		errors.Is(err, application.ErrCompanyWindow):
		return connect.NewError(connect.CodePermissionDenied, err.Error())
	default:
		return err
	}
}

// Register announces a runner; the ID only names it in logs and leases.
func (s *Server) Register(ctx context.Context, req *runnerv1.RegisterRequest) (*runnerv1.RegisterResponse, error) {
	ident, err := s.runner(ctx)
	if err != nil {
		return nil, err
	}
	kind := ident.kind
	if req.GetKind() == runnerv1.RunnerKind_RUNNER_KIND_HOME {
		kind = runnerv1.RunnerKind_RUNNER_KIND_HOME
	}
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	runnerID := "runner-" + hex.EncodeToString(b)
	s.runners.Store(runnerID, runnerSession{
		runnerID: runnerID,
		kind:     kind,
		userID:   ident.userID,
	})
	return &runnerv1.RegisterResponse{RunnerId: runnerID,
		HeartbeatInterval: durationpb.New(30 * time.Second)}, nil
}

// Heartbeat keeps a runner registered.
func (s *Server) Heartbeat(ctx context.Context, req *runnerv1.HeartbeatRequest) (*runnerv1.HeartbeatResponse, error) {
	if _, err := s.runner(ctx); err != nil {
		return nil, err
	}
	return &runnerv1.HeartbeatResponse{}, nil
}

// Lease hands out the next run.
func (s *Server) Lease(ctx context.Context, req *runnerv1.LeaseRequest) (*runnerv1.LeaseResponse, error) {
	ident, err := s.runner(ctx)
	if err != nil {
		return nil, err
	}
	runnerID := req.GetRunnerId()
	if runnerID == "" || len(runnerID) > 100 {
		return nil, connect.NewError(connect.CodeInvalidArgument, "runner id")
	}
	// Kind and user come from the token: a home runner leases only its own user's runs, whatever runner id it
	// sends. Only the server secret may use a session registered as a home runner without a user.
	choice := application.RunnerServer
	var userPtr *uuid.UUID
	switch ident.kind {
	case runnerv1.RunnerKind_RUNNER_KIND_HOME:
		if ident.userID == uuid.Nil {
			return nil, connect.NewError(connect.CodeUnauthenticated, "home runner without a user")
		}
		choice, userPtr = application.RunnerHomeRunner, &ident.userID
	default:
		if v, ok := s.runners.Load(runnerID); ok && v.(runnerSession).kind == runnerv1.RunnerKind_RUNNER_KIND_HOME {
			choice = application.RunnerHomeRunner
		}
	}
	l, ok, err := s.Runs.Lease(ctx, runnerID, choice, userPtr)
	if err != nil || !ok {
		return &runnerv1.LeaseResponse{}, err
	}
	ref := application.RunRef{RunID: l.RunID, AppID: l.AppID, UserID: l.UserID}
	run, err := s.Data.RunData(ctx, l.UserID, l.JobID)
	if err != nil {
		_ = s.Runs.Park(ctx, ref, "run data: "+err.Error(), "")
		return nil, err
	}
	run.RunId, run.Token, run.ExpiresAt = l.RunID.String(), l.Token, timestamppb.New(l.ExpiresAt)
	urls := append([]string{run.GetStartUrl()}, run.GetOrigins()...)
	adapter := ats.Detect("", urls...)
	_ = s.Runs.SetAdapter(ctx, ref, adapter)
	s.runSteps.Store(ref.RunID, runStepState{at: time.Now(), adapter: adapter})
	s.Logger.InfoContext(ctx, "run leased", "run", l.RunID, "runner", runnerID, "host", l.Host)
	return &runnerv1.LeaseResponse{Run: run}, nil
}

// FetchDocument returns a document that belongs to the run.
func (s *Server) FetchDocument(ctx context.Context, req *runnerv1.FetchDocumentRequest) (*runnerv1.FetchDocumentResponse, error) {
	ref, err := s.run(ctx)
	if err != nil {
		return nil, err
	}
	app, err := s.Apps.Get(ctx, ref.UserID, ref.AppID)
	if err != nil {
		return nil, err
	}
	run, err := s.Data.RunData(ctx, ref.UserID, app.JobID)
	if err != nil {
		return nil, err
	}
	id := req.GetDocumentId()
	if !slices.ContainsFunc(run.GetDocuments(), func(d *runnerv1.Document) bool { return d.GetId() == id }) {
		return nil, connect.NewError(connect.CodePermissionDenied, "not a document of this run")
	}
	b, err := s.Data.Document(ctx, ref.UserID, id)
	if err != nil {
		return nil, err
	}
	return &runnerv1.FetchDocumentResponse{Content: b}, nil
}

// screenshot stores a PNG of the run and returns its key; empty input stores nothing.
func (s *Server) screenshot(ctx context.Context, ref application.RunRef, png []byte) (string, error) {
	if len(png) == 0 {
		return "", nil
	}
	if len(png) > maxScreenshot || !bytes.HasPrefix(png, []byte("\x89PNG\r\n\x1a\n")) {
		return "", connect.NewError(connect.CodeInvalidArgument, "screenshot must be a PNG of at most 8 MB")
	}
	key := fmt.Sprintf("runs/%s/%d.png", ref.RunID, time.Now().UnixNano())
	return key, s.Shots.Put(ctx, ref.UserID, key, png)
}

// Checkpoint records a step.
func (s *Server) Checkpoint(ctx context.Context, req *runnerv1.CheckpointRequest) (*runnerv1.CheckpointResponse, error) {
	ref, err := s.run(ctx)
	if err != nil {
		return nil, err
	}
	m := req
	data := m.GetDataJson()
	if len(data) == 0 {
		data = []byte("{}")
	}
	if len(data) > maxData || !json.Valid(data) || len(m.GetStep()) > 20 || len(m.GetUrl()) > 2000 {
		return nil, connect.NewError(connect.CodeInvalidArgument, "checkpoint too large or malformed")
	}
	key, err := s.screenshot(ctx, ref, m.GetScreenshot())
	if err != nil {
		return nil, err
	}
	if cookies := m.GetUpdatedSessionCookiesJson(); len(cookies) > 0 && s.Portals != nil && s.Apps != nil && s.Data != nil {
		if app, err := s.Apps.Get(ctx, ref.UserID, ref.AppID); err == nil {
			if run, err := s.Data.RunData(ctx, ref.UserID, app.JobID); err == nil && run.GetPortal() != "" {
				_ = s.Portals.SaveCookies(ctx, ref.UserID, run.GetPortal(), cookies, 0)
			}
		}
	}
	s.recordStepDuration(ctx, ref, m.GetStep())
	err = s.Runs.Checkpoint(ctx, ref, application.Checkpoint{Step: m.GetStep(), Page: int(m.GetPage()), URL: m.GetUrl(),
		Data: data, Screenshot: key})
	return &runnerv1.CheckpointResponse{}, err
}

// Ask hands the run to the user.
func (s *Server) Ask(ctx context.Context, req *runnerv1.AskRequest) (*runnerv1.AskResponse, error) {
	ref, err := s.run(ctx)
	if err != nil {
		return nil, err
	}
	s.runSteps.Delete(ref.RunID)
	qs := req.GetQuestions()
	if len(qs) == 0 || len(qs) > 50 {
		return nil, connect.NewError(connect.CodeInvalidArgument, "between 1 and 50 questions")
	}
	out := make([]application.Question, len(qs))
	for i, q := range qs {
		var opts []string
		for _, o := range q.GetOptions() {
			if len(opts) == 100 {
				break
			}
			opts = append(opts, clip(o, 200))
		}
		out[i] = application.Question{Kind: clip(q.GetKind(), 20), Label: clip(q.GetLabel(), 300),
			Input: clip(q.GetInput(), 20), Options: opts}
	}
	if err := s.Runs.Ask(ctx, ref, out); err != nil {
		return nil, code(err)
	}
	s.recordATSOutcome(ctx, ref, ats.OutcomeAsked)
	if s.Notify != nil {
		s.Notify(ctx, ref.UserID, ref.AppID)
	}
	return &runnerv1.AskResponse{}, nil
}

// BeginSubmit registers the submit.
func (s *Server) BeginSubmit(ctx context.Context, req *runnerv1.BeginSubmitRequest) (*runnerv1.BeginSubmitResponse, error) {
	ref, err := s.run(ctx)
	if err != nil {
		return nil, err
	}
	err = s.Runs.BeginSubmit(ctx, ref)
	if errors.Is(err, application.ErrNeedsApproval) && s.NotifyApproval != nil {
		s.NotifyApproval(ctx, ref.UserID, ref.AppID)
	}
	return &runnerv1.BeginSubmitResponse{}, code(err)
}

// Submitted records the submit.
func (s *Server) Submitted(ctx context.Context, req *runnerv1.SubmittedRequest) (*runnerv1.SubmittedResponse, error) {
	ref, err := s.run(ctx)
	if err != nil {
		return nil, err
	}
	var effect any
	if e := req.GetEffectJson(); len(e) > 0 && len(e) <= maxData && json.Valid(e) {
		effect = json.RawMessage(e)
	}
	if err := s.Runs.Submitted(ctx, ref, req.GetConfirmed(), effect); err != nil {
		return nil, code(err)
	}
	s.finishStepDuration(ctx, ref, "submit")
	s.recordATSOutcome(ctx, ref, ats.OutcomeSubmitted)
	return &runnerv1.SubmittedResponse{}, nil
}

// Park stops the run.
func (s *Server) Park(ctx context.Context, req *runnerv1.ParkRequest) (*runnerv1.ParkResponse, error) {
	ref, err := s.run(ctx)
	if err != nil {
		return nil, err
	}
	s.runSteps.Delete(ref.RunID)
	key, err := s.screenshot(ctx, ref, req.GetScreenshot())
	if err != nil {
		return nil, err
	}
	if err := s.Runs.Park(ctx, ref, clip(req.GetReason(), 1000), key); err != nil {
		return nil, code(err)
	}
	s.recordATSOutcome(ctx, ref, ats.OutcomeParked)
	return &runnerv1.ParkResponse{}, nil
}

func (s *Server) recordATSOutcome(ctx context.Context, ref application.RunRef, outcome ats.Outcome) {
	if s.ATSMetrics == nil {
		return
	}
	var urls []string
	if s.Apps != nil && s.Data != nil {
		if app, err := s.Apps.Get(ctx, ref.UserID, ref.AppID); err == nil {
			if run, err := s.Data.RunData(ctx, ref.UserID, app.JobID); err == nil {
				urls = append([]string{run.GetStartUrl()}, run.GetOrigins()...)
			}
		}
	}
	s.ATSMetrics.Record(ctx, ats.Detect("", urls...), outcome)
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// Handoff exchanges live screencast frames and user input during an interactive handoff.
func (s *Server) Handoff(ctx context.Context, req *runnerv1.HandoffRequest) (*runnerv1.HandoffResponse, error) {
	frame := req.GetFrame()
	var (
		ref application.RunRef
		err error
	)
	if len(frame) > 0 && !req.GetWait() && !req.GetDone() {
		ref, err = s.frameRun(ctx) // frames arrive many times a second; authorize from a short cache
	} else {
		ref, err = s.run(ctx)
	}
	token := callToken(ctx)
	if err != nil && req.GetDone() {
		// The run was finished (parked, submitted) before its runner closed the live view; the token no longer
		// authorizes calls, but it still names the session to close.
		if app, ok := s.sessionOf.LoadAndDelete(token); ok {
			if v, ok := s.handoffs.LoadAndDelete(app.(uuid.UUID)); ok {
				v.(*HandoffSession).closeDone()
			}
		}
		return &runnerv1.HandoffResponse{}, nil
	}
	if err != nil {
		return nil, err
	}
	if req.GetDone() {
		s.sessionOf.Delete(token)
		if v, ok := s.handoffs.LoadAndDelete(ref.AppID); ok {
			v.(*HandoffSession).closeDone()
		}
		s.seen.Delete(ref.RunID)
		s.frameAuth.Range(func(k, v any) bool {
			if v.(frameGrant).ref.AppID == ref.AppID {
				s.frameAuth.Delete(k)
			}
			return true
		})
		return &runnerv1.HandoffResponse{}, nil
	}
	v, _ := s.handoffs.LoadOrStore(ref.AppID, NewHandoffSession(ref.UserID))
	s.sessionOf.Store(token, ref.AppID)
	sess := v.(*HandoffSession)

	if len(frame) > 0 {
		if len(frame) <= maxScreenshot {
			sess.Publish(frame)
		}
		if !req.GetWait() {
			// A frame call never takes viewer input; only the runner's wait loop does, so no input is lost.
			return &runnerv1.HandoffResponse{}, nil
		}
	}

	var out []*runnerv1.HandoffEvent
	drain := func() {
		for {
			select {
			case ev := <-sess.Input:
				out = append(out, ev)
			default:
				return
			}
		}
	}
	if req.GetWait() {
		sess.MarkWaiting()
	}
	drain()
	if len(out) == 0 && req.GetWait() {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(3 * time.Second):
		case ev := <-sess.Input:
			out = append(out, ev)
			drain()
		}
	}
	return &runnerv1.HandoffResponse{Events: out}, nil
}

func (s *Server) histogram() metric.Float64Histogram {
	if s.StepDuration != nil {
		return s.StepDuration
	}
	h, err := gotel.Meter("github.com/1xxn/getafjob/internal/gateway").Float64Histogram(
		"getafjob.run.step.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Duration of a run step in seconds."),
	)
	if err != nil {
		return nil
	}
	return h
}

func (s *Server) recordStepDuration(ctx context.Context, ref application.RunRef, step string) {
	val, ok := s.runSteps.Load(ref.RunID)
	if !ok {
		return
	}
	st := val.(runStepState)
	took := time.Since(st.at)
	s.runSteps.Store(ref.RunID, runStepState{at: time.Now(), adapter: st.adapter})
	if h := s.histogram(); h != nil {
		h.Record(ctx, took.Seconds(), metric.WithAttributes(
			attribute.String("step", step),
			attribute.String("adapter", st.adapter),
		))
	}
}

func (s *Server) finishStepDuration(ctx context.Context, ref application.RunRef, step string) {
	val, ok := s.runSteps.LoadAndDelete(ref.RunID)
	if !ok {
		return
	}
	st := val.(runStepState)
	took := time.Since(st.at)
	if h := s.histogram(); h != nil {
		h.Record(ctx, took.Seconds(), metric.WithAttributes(
			attribute.String("step", step),
			attribute.String("adapter", st.adapter),
		))
	}
}
