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

	"connectrpc.com/connect"
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
	Runs         *application.Runs
	Apps         *application.Service
	Data         Data
	Shots        Screenshots
	Secret       string // shared by the server's runners; empty: no runner can register
	Logger       *slog.Logger
	Notify       func(ctx context.Context, userID, appID uuid.UUID)
	ATSMetrics   *ats.Tracker
	Portals      PortalSessionSaver
	StepDuration metric.Float64Histogram
	handoffs     sync.Map // map[uuid.UUID]*HandoffSession
	runners      sync.Map // map[string]runnerSession
	runSteps     sync.Map // map[uuid.UUID]runStepState
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

// HandoffSession coordinates a live remote browser screencast between a runner and the web UI.
type HandoffSession struct {
	Frames chan []byte
	Input  chan *runnerv1.HandoffEvent
	Done   chan struct{}
	once   sync.Once
	mu     sync.Mutex
	last   []byte
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

// SessionFor returns the active handoff session of an application, if any.
func (s *Server) SessionFor(appID uuid.UUID) (*HandoffSession, bool) {
	v, ok := s.handoffs.Load(appID)
	if !ok {
		return nil, false
	}
	return v.(*HandoffSession), true
}

var _ runnerv1connect.RunnerServiceHandler = (*Server)(nil)

// Handler returns the path and handler of the service.
func (s *Server) Handler() (string, http.Handler) {
	return runnerv1connect.NewRunnerServiceHandler(s, connect.WithReadMaxBytes(MaxMessage))
}

func bearer(h http.Header) string {
	v := h.Get("Authorization")
	if t, ok := strings.CutPrefix(v, "Bearer "); ok {
		return strings.TrimSpace(t)
	}
	return ""
}

// runner checks the runner secret (constant time) or a user's home runner token.
func (s *Server) runner(ctx context.Context, h http.Header) (runnerIdentity, error) {
	tok := bearer(h)
	if tok == "" {
		return runnerIdentity{}, connect.NewError(connect.CodeUnauthenticated, errors.New("missing token"))
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
	return runnerIdentity{}, connect.NewError(connect.CodeUnauthenticated, errors.New("runner not authenticated"))
}

// run returns the run of the call's token.
func (s *Server) run(ctx context.Context, h http.Header) (application.RunRef, error) {
	ref, err := s.Runs.Authorize(ctx, bearer(h))
	if errors.Is(err, application.ErrNoRun) {
		return ref, connect.NewError(connect.CodeUnauthenticated, err)
	}
	return ref, err
}

// code maps application errors to Connect codes.
func code(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, application.ErrTransition), errors.Is(err, application.ErrAlreadySubmitted),
		errors.Is(err, application.ErrConflict):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	case errors.Is(err, application.ErrNoRun):
		return connect.NewError(connect.CodeUnauthenticated, err)
	default:
		return err
	}
}

// Register announces a runner; the ID only names it in logs and leases.
func (s *Server) Register(ctx context.Context, req *connect.Request[runnerv1.RegisterRequest]) (*connect.Response[runnerv1.RegisterResponse], error) {
	ident, err := s.runner(ctx, req.Header())
	if err != nil {
		return nil, err
	}
	kind := ident.kind
	if req.Msg.GetKind() == runnerv1.RunnerKind_RUNNER_KIND_HOME {
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
	return connect.NewResponse(&runnerv1.RegisterResponse{RunnerId: runnerID,
		HeartbeatInterval: durationpb.New(30 * time.Second)}), nil
}

// Heartbeat keeps a runner registered.
func (s *Server) Heartbeat(ctx context.Context, req *connect.Request[runnerv1.HeartbeatRequest]) (*connect.Response[runnerv1.HeartbeatResponse], error) {
	if _, err := s.runner(ctx, req.Header()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&runnerv1.HeartbeatResponse{}), nil
}

// Lease hands out the next run.
func (s *Server) Lease(ctx context.Context, req *connect.Request[runnerv1.LeaseRequest]) (*connect.Response[runnerv1.LeaseResponse], error) {
	ident, err := s.runner(ctx, req.Header())
	if err != nil {
		return nil, err
	}
	runnerID := req.Msg.GetRunnerId()
	if runnerID == "" || len(runnerID) > 100 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("runner id"))
	}
	choice := application.RunnerServer
	var userPtr *uuid.UUID
	if v, ok := s.runners.Load(runnerID); ok {
		sess := v.(runnerSession)
		if sess.kind == runnerv1.RunnerKind_RUNNER_KIND_HOME {
			choice = application.RunnerHomeRunner
			if sess.userID != uuid.Nil {
				userPtr = &sess.userID
			}
		}
	} else if ident.kind == runnerv1.RunnerKind_RUNNER_KIND_HOME {
		choice = application.RunnerHomeRunner
		if ident.userID != uuid.Nil {
			userPtr = &ident.userID
		}
	}
	l, ok, err := s.Runs.Lease(ctx, runnerID, choice, userPtr)
	if err != nil || !ok {
		return connect.NewResponse(&runnerv1.LeaseResponse{}), err
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
	return connect.NewResponse(&runnerv1.LeaseResponse{Run: run}), nil
}

// FetchDocument returns a document that belongs to the run.
func (s *Server) FetchDocument(ctx context.Context, req *connect.Request[runnerv1.FetchDocumentRequest]) (*connect.Response[runnerv1.FetchDocumentResponse], error) {
	ref, err := s.run(ctx, req.Header())
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
	id := req.Msg.GetDocumentId()
	if !slices.ContainsFunc(run.GetDocuments(), func(d *runnerv1.Document) bool { return d.GetId() == id }) {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("not a document of this run"))
	}
	b, err := s.Data.Document(ctx, ref.UserID, id)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&runnerv1.FetchDocumentResponse{Content: b}), nil
}

// screenshot stores a PNG of the run and returns its key; empty input stores nothing.
func (s *Server) screenshot(ctx context.Context, ref application.RunRef, png []byte) (string, error) {
	if len(png) == 0 {
		return "", nil
	}
	if len(png) > maxScreenshot || !bytes.HasPrefix(png, []byte("\x89PNG\r\n\x1a\n")) {
		return "", connect.NewError(connect.CodeInvalidArgument, errors.New("screenshot must be a PNG of at most 8 MB"))
	}
	key := fmt.Sprintf("runs/%s/%d.png", ref.RunID, time.Now().UnixNano())
	return key, s.Shots.Put(ctx, ref.UserID, key, png)
}

// Checkpoint records a step.
func (s *Server) Checkpoint(ctx context.Context, req *connect.Request[runnerv1.CheckpointRequest]) (*connect.Response[runnerv1.CheckpointResponse], error) {
	ref, err := s.run(ctx, req.Header())
	if err != nil {
		return nil, err
	}
	m := req.Msg
	data := m.GetDataJson()
	if len(data) == 0 {
		data = []byte("{}")
	}
	if len(data) > maxData || !json.Valid(data) || len(m.GetStep()) > 20 || len(m.GetUrl()) > 2000 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("checkpoint too large or malformed"))
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
	return connect.NewResponse(&runnerv1.CheckpointResponse{}), err
}

// Ask hands the run to the user.
func (s *Server) Ask(ctx context.Context, req *connect.Request[runnerv1.AskRequest]) (*connect.Response[runnerv1.AskResponse], error) {
	ref, err := s.run(ctx, req.Header())
	if err != nil {
		return nil, err
	}
	s.runSteps.Delete(ref.RunID)
	qs := req.Msg.GetQuestions()
	if len(qs) == 0 || len(qs) > 50 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("between 1 and 50 questions"))
	}
	out := make([]map[string]string, len(qs))
	for i, q := range qs {
		out[i] = map[string]string{"kind": clip(q.GetKind(), 20), "label": clip(q.GetLabel(), 300), "draft": clip(q.GetDraft(), 5000)}
	}
	if err := s.Runs.Ask(ctx, ref, out); err != nil {
		return nil, code(err)
	}
	s.recordATSOutcome(ctx, ref, ats.OutcomeAsked)
	if s.Notify != nil {
		s.Notify(ctx, ref.UserID, ref.AppID)
	}
	return connect.NewResponse(&runnerv1.AskResponse{}), nil
}

// BeginSubmit registers the submit.
func (s *Server) BeginSubmit(ctx context.Context, req *connect.Request[runnerv1.BeginSubmitRequest]) (*connect.Response[runnerv1.BeginSubmitResponse], error) {
	ref, err := s.run(ctx, req.Header())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&runnerv1.BeginSubmitResponse{}), code(s.Runs.BeginSubmit(ctx, ref))
}

// Submitted records the submit.
func (s *Server) Submitted(ctx context.Context, req *connect.Request[runnerv1.SubmittedRequest]) (*connect.Response[runnerv1.SubmittedResponse], error) {
	ref, err := s.run(ctx, req.Header())
	if err != nil {
		return nil, err
	}
	var effect any
	if e := req.Msg.GetEffectJson(); len(e) > 0 && len(e) <= maxData && json.Valid(e) {
		effect = json.RawMessage(e)
	}
	if err := s.Runs.Submitted(ctx, ref, req.Msg.GetConfirmed(), effect); err != nil {
		return nil, code(err)
	}
	s.finishStepDuration(ctx, ref, "submit")
	s.recordATSOutcome(ctx, ref, ats.OutcomeSubmitted)
	return connect.NewResponse(&runnerv1.SubmittedResponse{}), nil
}

// Park stops the run.
func (s *Server) Park(ctx context.Context, req *connect.Request[runnerv1.ParkRequest]) (*connect.Response[runnerv1.ParkResponse], error) {
	ref, err := s.run(ctx, req.Header())
	if err != nil {
		return nil, err
	}
	s.runSteps.Delete(ref.RunID)
	key, err := s.screenshot(ctx, ref, req.Msg.GetScreenshot())
	if err != nil {
		return nil, err
	}
	if err := s.Runs.Park(ctx, ref, clip(req.Msg.GetReason(), 1000), key); err != nil {
		return nil, code(err)
	}
	s.recordATSOutcome(ctx, ref, ats.OutcomeParked)
	return connect.NewResponse(&runnerv1.ParkResponse{}), nil
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
func (s *Server) Handoff(ctx context.Context, req *connect.Request[runnerv1.HandoffRequest]) (*connect.Response[runnerv1.HandoffResponse], error) {
	ref, err := s.run(ctx, req.Header())
	if err != nil {
		return nil, err
	}
	if req.Msg.GetDone() {
		if v, ok := s.handoffs.LoadAndDelete(ref.AppID); ok {
			v.(*HandoffSession).closeDone()
		}
		return connect.NewResponse(&runnerv1.HandoffResponse{}), nil
	}
	v, _ := s.handoffs.LoadOrStore(ref.AppID, &HandoffSession{
		Frames: make(chan []byte, 4),
		Input:  make(chan *runnerv1.HandoffEvent, 50),
		Done:   make(chan struct{}),
	})
	sess := v.(*HandoffSession)

	if f := req.Msg.GetFrame(); len(f) > 0 && len(f) <= maxScreenshot {
		sess.mu.Lock()
		sess.last = f
		sess.mu.Unlock()
		select {
		case sess.Frames <- f:
		default:
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
	drain()
	if len(out) == 0 && req.Msg.GetWait() {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(3 * time.Second):
		case ev := <-sess.Input:
			out = append(out, ev)
			drain()
		}
	}
	return connect.NewResponse(&runnerv1.HandoffResponse{Events: out}), nil
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
