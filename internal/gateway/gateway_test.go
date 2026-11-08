package gateway_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/1xxn/getafjob/internal/application"
	"github.com/1xxn/getafjob/internal/application/adapters/pgstore"
	"github.com/1xxn/getafjob/internal/gateway"
	runnerv1 "github.com/1xxn/getafjob/internal/gen/proto/runner/v1"
	"github.com/1xxn/getafjob/internal/gen/proto/runner/v1/runnerv1connect"
	"github.com/1xxn/getafjob/internal/platform/db"
	"github.com/1xxn/getafjob/internal/platform/db/dbtest"
)

const secret = "test-runner-secret-0123456789abcdef"

type data struct{}

func (data) RunData(context.Context, uuid.UUID, int64) (*runnerv1.Run, error) {
	return &runnerv1.Run{StartUrl: "https://jobs.example.org/1", Origins: []string{"https://jobs.example.org"},
		Profile:   map[string]string{"personal.first_name": "Max"},
		Documents: []*runnerv1.Document{{Id: "0192f3a4-5b6c-7d8e-9f01-0000000000c1", FileName: "cv.pdf", Kind: "cv"}}}, nil
}

func (data) Document(context.Context, uuid.UUID, string) ([]byte, error) {
	return []byte("%PDF-1.4"), nil
}

type shots struct{ keys []string }

func (s *shots) Put(_ context.Context, _ uuid.UUID, key string, _ []byte) error {
	s.keys = append(s.keys, key)
	return nil
}

func setup(t *testing.T) (*pgxpool.Pool, *application.Service, runnerv1connect.RunnerServiceClient, *shots) {
	pool := dbtest.AppPool(t)
	st := pgstore.New(pool)
	apps := application.NewService(st)
	sh := &shots{}
	srv := &gateway.Server{Runs: application.NewRuns(apps, st), Apps: apps, Data: data{}, Shots: sh, Secret: secret,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	mux := http.NewServeMux()
	mux.Handle(srv.Handler())
	hs := httptest.NewServer(mux)
	t.Cleanup(hs.Close)
	return pool, apps, runnerv1connect.NewRunnerServiceClient(hs.Client(), hs.URL), sh
}

func with[T any](msg *T, token string) *connect.Request[T] {
	r := connect.NewRequest(msg)
	if token != "" {
		r.Header().Set("Authorization", "Bearer "+token)
	}
	return r
}

func codeOf(err error) connect.Code { return connect.CodeOf(err) }

func queue(t *testing.T, pool *pgxpool.Pool, apps *application.Service) uuid.UUID {
	t.Helper()
	ctx := t.Context()
	var u uuid.UUID
	var job int64
	if err := db.InSystemTx(ctx, pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, "INSERT INTO users (email) VALUES ('max@example.org') RETURNING id").Scan(&u); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `INSERT INTO jobs (fingerprint, match_key, source, external_id, company, title, url, content_hash,
			first_seen, last_seen) VALUES ('f', 'acme|go', 'greenhouse:acme', '1', 'Acme', 'Go Developer',
			'https://jobs.example.org/1', 'h', now(), now()) RETURNING id`).Scan(&job)
	}); err != nil {
		t.Fatal(err)
	}
	id, err := apps.Create(ctx, u, job)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := apps.Approve(ctx, u, id); err != nil {
		t.Fatal(err)
	}
	return u
}

var png = []byte("\x89PNG\r\n\x1a\nfake")

func TestGateway(t *testing.T) {
	pool, apps, c, sh := setup(t)
	ctx := t.Context()

	for _, tok := range []string{"", "wrong"} {
		if _, err := c.Register(ctx, with(&runnerv1.RegisterRequest{}, tok)); codeOf(err) != connect.CodeUnauthenticated {
			t.Fatalf("register with %q: %v", tok, err)
		}
	}
	reg, err := c.Register(ctx, with(&runnerv1.RegisterRequest{Version: "test"}, secret))
	if err != nil || reg.Msg.GetRunnerId() == "" {
		t.Fatalf("register: %v", err)
	}
	runner := reg.Msg.GetRunnerId()
	if l, err := c.Lease(ctx, with(&runnerv1.LeaseRequest{RunnerId: runner}, secret)); err != nil || l.Msg.GetRun() != nil {
		t.Fatalf("empty lease: %v", err)
	}

	u := queue(t, pool, apps)
	l, err := c.Lease(ctx, with(&runnerv1.LeaseRequest{RunnerId: runner}, secret))
	if err != nil || l.Msg.GetRun() == nil {
		t.Fatalf("lease: %v", err)
	}
	run := l.Msg.GetRun()
	tok := run.GetToken()
	if run.GetProfile()["personal.first_name"] != "Max" || run.GetExpiresAt().AsTime().Before(time.Now()) {
		t.Fatalf("run = %+v", run)
	}

	// Run calls need the run's token, not the runner secret.
	if _, err := c.BeginSubmit(ctx, with(&runnerv1.BeginSubmitRequest{}, secret)); codeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("submit with the runner secret: %v", err)
	}
	if d, err := c.FetchDocument(ctx, with(&runnerv1.FetchDocumentRequest{DocumentId: run.GetDocuments()[0].GetId()}, tok)); err != nil ||
		string(d.Msg.GetContent()) != "%PDF-1.4" {
		t.Fatalf("document: %v", err)
	}
	if _, err := c.FetchDocument(ctx, with(&runnerv1.FetchDocumentRequest{DocumentId: uuid.NewString()}, tok)); codeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("foreign document: %v", err)
	}
	if _, err := c.Checkpoint(ctx, with(&runnerv1.CheckpointRequest{Step: "extract", Page: 1, Url: run.GetStartUrl(),
		DataJson: []byte(`{"fields":[]}`), Screenshot: png}, tok)); err != nil || len(sh.keys) != 1 {
		t.Fatalf("checkpoint: %v, %v", err, sh.keys)
	}
	for name, req := range map[string]*runnerv1.CheckpointRequest{
		"not a png":    {Step: "extract", Screenshot: []byte("GIF89a")},
		"invalid json": {Step: "extract", DataJson: []byte("{")},
	} {
		if _, err := c.Checkpoint(ctx, with(req, tok)); codeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := c.BeginSubmit(ctx, with(&runnerv1.BeginSubmitRequest{}, tok)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.BeginSubmit(ctx, with(&runnerv1.BeginSubmitRequest{}, tok)); codeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("second submit: %v", err)
	}
	if _, err := c.Submitted(ctx, with(&runnerv1.SubmittedRequest{Confirmed: true, EffectJson: []byte(`{"navigated":true}`)}, tok)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Park(ctx, with(&runnerv1.ParkRequest{Reason: "late"}, tok)); codeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("call after the run ended: %v", err)
	}
	var state string
	_ = db.InUserTx(ctx, pool, u, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT state FROM applications").Scan(&state)
	})
	if state != string(application.Confirmed) {
		t.Fatalf("state = %s", state)
	}
}

func TestGatewayAskQuestionsSurviveRestartAndStreamHandoff(t *testing.T) {
	pool, apps, c, _ := setup(t)
	ctx := t.Context()
	reg, err := c.Register(ctx, with(&runnerv1.RegisterRequest{Version: "test"}, secret))
	if err != nil {
		t.Fatal(err)
	}
	u := queue(t, pool, apps)
	l, err := c.Lease(ctx, with(&runnerv1.LeaseRequest{RunnerId: reg.Msg.GetRunnerId()}, secret))
	if err != nil || l.Msg.GetRun() == nil {
		t.Fatalf("lease: %v", err)
	}
	tok := l.Msg.GetRun().GetToken()

	if _, err := c.Ask(ctx, with(&runnerv1.AskRequest{Questions: []*runnerv1.Question{
		{Kind: "field", Label: "Earliest start date?", Draft: "2027-01-01"},
	}}, tok)); err != nil {
		t.Fatalf("ask: %v", err)
	}

	var appID uuid.UUID
	_ = db.InUserTx(ctx, pool, u, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT id FROM applications").Scan(&appID)
	})

	// Fresh service on the same pool simulates a server restart: questions are loaded from application_events.
	restarted := application.NewService(pgstore.New(pool))
	qs, err := restarted.Questions(ctx, u, appID)
	if err != nil || len(qs) != 1 || qs[0].Label != "Earliest start date?" || qs[0].Draft != "2027-01-01" {
		t.Fatalf("questions after restart = %+v, %v", qs, err)
	}

	// Handoff stays authorized while in needs_input.
	if _, err := c.Handoff(ctx, with(&runnerv1.HandoffRequest{Frame: []byte{0xff, 0xd8, 0xff, 0xd9}}, tok)); err != nil {
		t.Fatalf("handoff frame: %v", err)
	}
	if _, err := c.Handoff(ctx, with(&runnerv1.HandoffRequest{Done: true}, tok)); err != nil {
		t.Fatalf("handoff done: %v", err)
	}
}

func TestGatewayHomeRunnerAuthenticationAndLeasing(t *testing.T) {
	pool, apps, c, _ := setup(t)
	ctx := t.Context()

	u := queue(t, pool, apps)
	if err := apps.SaveSettings(ctx, u, application.Settings{
		Level:        application.LevelAutonomous,
		RunnerChoice: application.RunnerHomeRunner,
		DailyLimit:   10,
		MinScore:     0,
	}); err != nil {
		t.Fatal(err)
	}

	token, err := apps.GenerateHomeRunnerToken(ctx, u)
	if err != nil {
		t.Fatal(err)
	}

	// Invalid home runner token is rejected.
	if _, err := c.Register(ctx, with(&runnerv1.RegisterRequest{
		Version: "test",
		Kind:    runnerv1.RunnerKind_RUNNER_KIND_HOME,
	}, "invalid-token-1234567890")); codeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("register with bad token: %v", err)
	}

	// Register home runner with valid token.
	hreg, err := c.Register(ctx, with(&runnerv1.RegisterRequest{
		Version: "test",
		Kind:    runnerv1.RunnerKind_RUNNER_KIND_HOME,
	}, token))
	if err != nil || hreg.Msg.GetRunnerId() == "" {
		t.Fatalf("register home runner: %v", err)
	}
	hrunnerID := hreg.Msg.GetRunnerId()

	// Register server runner with shared secret.
	sreg, err := c.Register(ctx, with(&runnerv1.RegisterRequest{
		Version: "test",
		Kind:    runnerv1.RunnerKind_RUNNER_KIND_SERVER,
	}, secret))
	if err != nil || sreg.Msg.GetRunnerId() == "" {
		t.Fatalf("register server runner: %v", err)
	}
	srunnerID := sreg.Msg.GetRunnerId()

	// Server runner must NOT receive the application because runner_choice is home_runner.
	sl, err := c.Lease(ctx, with(&runnerv1.LeaseRequest{RunnerId: srunnerID}, secret))
	if err != nil || sl.Msg.GetRun() != nil {
		t.Fatalf("server runner leased home runner job: %v, %+v", err, sl.Msg.GetRun())
	}

	// Home runner receives the user's application.
	hl, err := c.Lease(ctx, with(&runnerv1.LeaseRequest{RunnerId: hrunnerID}, token))
	if err != nil || hl.Msg.GetRun() == nil {
		t.Fatalf("home runner lease: %v", err)
	}
	if hl.Msg.GetRun().GetProfile()["personal.first_name"] != "Max" {
		t.Fatalf("unexpected profile: %+v", hl.Msg.GetRun().GetProfile())
	}
}
