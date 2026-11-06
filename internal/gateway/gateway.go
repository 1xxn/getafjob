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
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/1xxn/getafjob/internal/application"
	runnerv1 "github.com/1xxn/getafjob/internal/gen/proto/runner/v1"
	"github.com/1xxn/getafjob/internal/gen/proto/runner/v1/runnerv1connect"
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

// Server implements the runner service.
type Server struct {
	Runs   *application.Runs
	Apps   *application.Service
	Data   Data
	Shots  Screenshots
	Secret string // shared by the server's runners; empty: no runner can register
	Logger *slog.Logger
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

// runner checks the runner secret (constant time).
func (s *Server) runner(h http.Header) error {
	got, want := sha256.Sum256([]byte(bearer(h))), sha256.Sum256([]byte(s.Secret))
	if s.Secret == "" || subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("runner not authenticated"))
	}
	return nil
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
func (s *Server) Register(_ context.Context, req *connect.Request[runnerv1.RegisterRequest]) (*connect.Response[runnerv1.RegisterResponse], error) {
	if err := s.runner(req.Header()); err != nil {
		return nil, err
	}
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return connect.NewResponse(&runnerv1.RegisterResponse{RunnerId: "runner-" + hex.EncodeToString(b),
		HeartbeatInterval: durationpb.New(30 * time.Second)}), nil
}

// Heartbeat keeps a runner registered.
func (s *Server) Heartbeat(_ context.Context, req *connect.Request[runnerv1.HeartbeatRequest]) (*connect.Response[runnerv1.HeartbeatResponse], error) {
	if err := s.runner(req.Header()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&runnerv1.HeartbeatResponse{}), nil
}

// Lease hands out the next run.
func (s *Server) Lease(ctx context.Context, req *connect.Request[runnerv1.LeaseRequest]) (*connect.Response[runnerv1.LeaseResponse], error) {
	if err := s.runner(req.Header()); err != nil {
		return nil, err
	}
	runnerID := req.Msg.GetRunnerId()
	if runnerID == "" || len(runnerID) > 100 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("runner id"))
	}
	l, ok, err := s.Runs.Lease(ctx, runnerID)
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
	qs := req.Msg.GetQuestions()
	if len(qs) == 0 || len(qs) > 50 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("between 1 and 50 questions"))
	}
	out := make([]map[string]string, len(qs))
	for i, q := range qs {
		out[i] = map[string]string{"kind": clip(q.GetKind(), 20), "label": clip(q.GetLabel(), 300), "draft": clip(q.GetDraft(), 5000)}
	}
	return connect.NewResponse(&runnerv1.AskResponse{}), code(s.Runs.Ask(ctx, ref, out))
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
	return connect.NewResponse(&runnerv1.SubmittedResponse{}), code(s.Runs.Submitted(ctx, ref, req.Msg.GetConfirmed(), effect))
}

// Park stops the run.
func (s *Server) Park(ctx context.Context, req *connect.Request[runnerv1.ParkRequest]) (*connect.Response[runnerv1.ParkResponse], error) {
	ref, err := s.run(ctx, req.Header())
	if err != nil {
		return nil, err
	}
	key, err := s.screenshot(ctx, ref, req.Msg.GetScreenshot())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&runnerv1.ParkResponse{}), code(s.Runs.Park(ctx, ref, clip(req.Msg.GetReason(), 1000), key))
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
