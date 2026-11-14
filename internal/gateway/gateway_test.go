package gateway_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	gotel "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

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
	p, a, c, s, _ := setupWithServer(t)
	return p, a, c, s
}

func setupWithServer(t *testing.T) (*pgxpool.Pool, *application.Service, runnerv1connect.RunnerServiceClient, *shots, *gateway.Server) {
	pool := dbtest.AppPool(t)
	st := pgstore.New(pool)
	apps := application.NewService(st)
	sh := &shots{}
	srv := &gateway.Server{Runs: application.NewRuns(apps, st), Apps: apps, Data: data{}, Shots: sh, Secret: secret,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	mux := http.NewServeMux()
	srv.Mount(mux)
	hs := httptest.NewServer(mux)
	t.Cleanup(hs.Close)
	client := connect.NewClient(connecthttp.NewTransport(hs.Client(), hs.URL))
	return pool, apps, runnerv1connect.NewRunnerServiceClient(client), sh, srv
}

func with(ctx context.Context, token string) context.Context {
	if token == "" {
		return ctx
	}
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set("Authorization", "Bearer "+token)
	return ctx
}

func codeOf(err error) connect.Code { return connect.CodeOf(err) }

func queue(t *testing.T, pool *pgxpool.Pool, apps *application.Service) uuid.UUID {
	t.Helper()
	return queueApp(t, pool, apps, true)
}

// queueApp queues an application of a new user; approved runs submit without asking.
func queueApp(t *testing.T, pool *pgxpool.Pool, apps *application.Service, approved bool) uuid.UUID {
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
	if !approved {
		if _, _, err := apps.Queue(ctx, u, job); err != nil {
			t.Fatal(err)
		}
		return u
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

// A submit that needs the user's approval answers aborted and tells the user at once.
func TestBeginSubmitAsksForApprovalAndNotifies(t *testing.T) {
	pool, apps, c, _, srv := setupWithServer(t)
	ctx := t.Context()
	var notified []uuid.UUID
	srv.NotifyApproval = func(_ context.Context, userID, _ uuid.UUID) { notified = append(notified, userID) }
	reg, err := c.Register(with(ctx, secret), &runnerv1.RegisterRequest{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	u := queueApp(t, pool, apps, false)
	l, err := c.Lease(with(ctx, secret), &runnerv1.LeaseRequest{RunnerId: reg.GetRunnerId()})
	if err != nil || l.GetRun() == nil {
		t.Fatalf("lease: %v", err)
	}
	if _, err := c.BeginSubmit(with(ctx, l.GetRun().GetToken()), &runnerv1.BeginSubmitRequest{}); codeOf(err) != connect.CodeAborted {
		t.Fatalf("submit without approval: %v", err)
	}
	if len(notified) != 1 || notified[0] != u {
		t.Fatalf("notified = %v, want %v", notified, u)
	}
}

var png = []byte("\x89PNG\r\n\x1a\nfake")

func TestGateway(t *testing.T) {
	pool, apps, c, sh := setup(t)
	ctx := t.Context()

	for _, tok := range []string{"", "wrong"} {
		if _, err := c.Register(with(ctx, tok), &runnerv1.RegisterRequest{}); codeOf(err) != connect.CodeUnauthenticated {
			t.Fatalf("register with %q: %v", tok, err)
		}
	}
	reg, err := c.Register(with(ctx, secret), &runnerv1.RegisterRequest{Version: "test"})
	if err != nil || reg.GetRunnerId() == "" {
		t.Fatalf("register: %v", err)
	}
	runner := reg.GetRunnerId()
	if l, err := c.Lease(with(ctx, secret), &runnerv1.LeaseRequest{RunnerId: runner}); err != nil || l.GetRun() != nil {
		t.Fatalf("empty lease: %v", err)
	}

	u := queue(t, pool, apps)
	l, err := c.Lease(with(ctx, secret), &runnerv1.LeaseRequest{RunnerId: runner})
	if err != nil || l.GetRun() == nil {
		t.Fatalf("lease: %v", err)
	}
	run := l.GetRun()
	tok := run.GetToken()
	if run.GetProfile()["personal.first_name"] != "Max" || run.GetExpiresAt().AsTime().Before(time.Now()) {
		t.Fatalf("run = %+v", run)
	}

	// Run calls need the run's token, not the runner secret.
	if _, err := c.BeginSubmit(with(ctx, secret), &runnerv1.BeginSubmitRequest{}); codeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("submit with the runner secret: %v", err)
	}
	if d, err := c.FetchDocument(with(ctx, tok), &runnerv1.FetchDocumentRequest{DocumentId: run.GetDocuments()[0].GetId()}); err != nil ||
		string(d.GetContent()) != "%PDF-1.4" {
		t.Fatalf("document: %v", err)
	}
	if _, err := c.FetchDocument(with(ctx, tok), &runnerv1.FetchDocumentRequest{DocumentId: uuid.NewString()}); codeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("foreign document: %v", err)
	}
	if _, err := c.Checkpoint(with(ctx, tok), &runnerv1.CheckpointRequest{Step: "extract", Page: 1, Url: run.GetStartUrl(),
		DataJson: []byte(`{"fields":[]}`), Screenshot: png}); err != nil || len(sh.keys) != 1 {
		t.Fatalf("checkpoint: %v, %v", err, sh.keys)
	}
	for name, req := range map[string]*runnerv1.CheckpointRequest{
		"not a png":    {Step: "extract", Screenshot: []byte("GIF89a")},
		"invalid json": {Step: "extract", DataJson: []byte("{")},
	} {
		if _, err := c.Checkpoint(with(ctx, tok), req); codeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := c.BeginSubmit(with(ctx, tok), &runnerv1.BeginSubmitRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.BeginSubmit(with(ctx, tok), &runnerv1.BeginSubmitRequest{}); codeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("second submit: %v", err)
	}
	if _, err := c.Submitted(with(ctx, tok), &runnerv1.SubmittedRequest{Confirmed: true, EffectJson: []byte(`{"navigated":true}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Park(with(ctx, tok), &runnerv1.ParkRequest{Reason: "late"}); codeOf(err) != connect.CodeUnauthenticated {
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
	pool, apps, c, _, srv := setupWithServer(t)
	ctx := t.Context()
	reg, err := c.Register(with(ctx, secret), &runnerv1.RegisterRequest{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	u := queue(t, pool, apps)
	l, err := c.Lease(with(ctx, secret), &runnerv1.LeaseRequest{RunnerId: reg.GetRunnerId()})
	if err != nil || l.GetRun() == nil {
		t.Fatalf("lease: %v", err)
	}
	tok := l.GetRun().GetToken()

	if _, err := c.Ask(with(ctx, tok), &runnerv1.AskRequest{Questions: []*runnerv1.Question{
		{Kind: "field", Label: "Earliest start date?"},
	}}); err != nil {
		t.Fatalf("ask: %v", err)
	}

	var appID uuid.UUID
	_ = db.InUserTx(ctx, pool, u, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT id FROM applications").Scan(&appID)
	})

	// Fresh service on the same pool simulates a server restart: questions are loaded from application_events.
	restarted := application.NewService(pgstore.New(pool))
	qs, err := restarted.Questions(ctx, u, appID)
	if err != nil || len(qs) != 1 || qs[0].Label != "Earliest start date?" {
		t.Fatalf("questions after restart = %+v, %v", qs, err)
	}

	// ActiveRuns is initially empty.
	if active := srv.ActiveRuns(u); len(active) != 0 {
		t.Fatalf("active sessions before handoff = %v, want empty", active)
	}

	// Handoff stays authorized while in needs_input.
	if _, err := c.Handoff(with(ctx, tok), &runnerv1.HandoffRequest{Frame: []byte{0xff, 0xd8, 0xff, 0xd9}}); err != nil {
		t.Fatalf("handoff frame: %v", err)
	}
	if active := srv.ActiveRuns(u); len(active) != 1 || active[0] != appID {
		t.Fatalf("active sessions after handoff frame = %v, want [%s]", active, appID)
	}

	// Viewer input waits for the runner's wait call; frame calls never take it.
	sess, _ := srv.SessionFor(appID)
	sess.Input <- &runnerv1.HandoffEvent{Msg: &runnerv1.HandoffEvent_Resume{Resume: true}}
	if res, err := c.Handoff(with(ctx, tok), &runnerv1.HandoffRequest{Frame: []byte{0xff, 0xd8}}); err != nil || len(res.GetEvents()) != 0 {
		t.Fatalf("frame call = %v, %v; want no events", res.GetEvents(), err)
	}
	if res, err := c.Handoff(with(ctx, tok), &runnerv1.HandoffRequest{Wait: true}); err != nil || len(res.GetEvents()) != 1 {
		t.Fatalf("wait call = %v, %v; want the resume", res.GetEvents(), err)
	}
	if !sess.Interactive() {
		t.Fatal("session must be interactive after a wait call")
	}

	if _, err := c.Handoff(with(ctx, tok), &runnerv1.HandoffRequest{Done: true}); err != nil {
		t.Fatalf("handoff done: %v", err)
	}
	if active := srv.ActiveRuns(u); len(active) != 0 {
		t.Fatalf("active sessions after handoff done = %v, want empty", active)
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
	if _, err := c.Register(with(ctx, "invalid-token-1234567890"), &runnerv1.RegisterRequest{
		Version: "test",
		Kind:    runnerv1.RunnerKind_RUNNER_KIND_HOME,
	}); codeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("register with bad token: %v", err)
	}

	// Register home runner with valid token.
	hreg, err := c.Register(with(ctx, token), &runnerv1.RegisterRequest{
		Version: "test",
		Kind:    runnerv1.RunnerKind_RUNNER_KIND_HOME,
	})
	if err != nil || hreg.GetRunnerId() == "" {
		t.Fatalf("register home runner: %v", err)
	}
	hrunnerID := hreg.GetRunnerId()

	// Register server runner with shared secret.
	sreg, err := c.Register(with(ctx, secret), &runnerv1.RegisterRequest{
		Version: "test",
		Kind:    runnerv1.RunnerKind_RUNNER_KIND_SERVER,
	})
	if err != nil || sreg.GetRunnerId() == "" {
		t.Fatalf("register server runner: %v", err)
	}
	srunnerID := sreg.GetRunnerId()

	// Server runner must NOT receive the application because runner_choice is home_runner.
	sl, err := c.Lease(with(ctx, secret), &runnerv1.LeaseRequest{RunnerId: srunnerID})
	if err != nil || sl.GetRun() != nil {
		t.Fatalf("server runner leased home runner job: %v, %+v", err, sl.GetRun())
	}

	// Home runner receives the user's application.
	hl, err := c.Lease(with(ctx, token), &runnerv1.LeaseRequest{RunnerId: hrunnerID})
	if err != nil || hl.GetRun() == nil {
		t.Fatalf("home runner lease: %v", err)
	}
	if hl.GetRun().GetProfile()["personal.first_name"] != "Max" {
		t.Fatalf("unexpected profile: %+v", hl.GetRun().GetProfile())
	}
}

// A home runner token leases only its own user's runs, also when it sends the server runner's id.
func TestGatewayHomeRunnerCannotUseAnotherRunnerID(t *testing.T) {
	pool, apps, c, _ := setup(t)
	ctx := t.Context()
	queue(t, pool, apps) // Max's run for the server runner
	var erika uuid.UUID
	if err := db.InSystemTx(ctx, pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "INSERT INTO users (email) VALUES ('erika@example.org') RETURNING id").Scan(&erika)
	}); err != nil {
		t.Fatal(err)
	}
	if err := apps.SaveSettings(ctx, erika, application.Settings{Level: application.LevelAutonomous,
		RunnerChoice: application.RunnerHomeRunner, DailyLimit: 10}); err != nil {
		t.Fatal(err)
	}
	token, err := apps.GenerateHomeRunnerToken(ctx, erika)
	if err != nil {
		t.Fatal(err)
	}
	sreg, err := c.Register(with(ctx, secret), &runnerv1.RegisterRequest{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	l, err := c.Lease(with(ctx, token), &runnerv1.LeaseRequest{RunnerId: sreg.GetRunnerId()})
	if err != nil || l.GetRun() != nil {
		t.Fatalf("home runner leased with the server runner id: %v, %+v", err, l.GetRun())
	}
}

func TestGatewayStepDurationMetrics(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	prev := gotel.GetMeterProvider()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	gotel.SetMeterProvider(mp)
	t.Cleanup(func() { gotel.SetMeterProvider(prev) })

	pool, apps, c, _ := setup(t)
	ctx := t.Context()

	reg, err := c.Register(with(ctx, secret), &runnerv1.RegisterRequest{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	runner := reg.GetRunnerId()

	_ = queue(t, pool, apps)
	l, err := c.Lease(with(ctx, secret), &runnerv1.LeaseRequest{RunnerId: runner})
	if err != nil || l.GetRun() == nil {
		t.Fatalf("lease: %v", err)
	}
	run := l.GetRun()
	tok := run.GetToken()

	if _, err := c.Checkpoint(with(ctx, tok), &runnerv1.CheckpointRequest{
		Step: "extract", Page: 1, Url: run.GetStartUrl(),
		DataJson: []byte(`{"fields":[]}`), Screenshot: png,
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := c.BeginSubmit(with(ctx, tok), &runnerv1.BeginSubmitRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Submitted(with(ctx, tok), &runnerv1.SubmittedRequest{Confirmed: true}); err != nil {
		t.Fatal(err)
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatal(err)
	}
	var steps []string
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == "getafjob.run.step.duration" {
				hist, ok := m.Data.(metricdata.Histogram[float64])
				if !ok {
					continue
				}
				for _, dp := range hist.DataPoints {
					st, _ := dp.Attributes.Value(attribute.Key("step"))
					steps = append(steps, st.AsString())
				}
			}
		}
	}
	if !slices.Contains(steps, "extract") || !slices.Contains(steps, "submit") {
		t.Fatalf("expected extract and submit steps in histogram, got: %v", steps)
	}
}

// The runner closes the live view after the run was parked; the token no longer authorizes, but the session must
// still go, or the page keeps showing a dead run as live.
func TestDoneAfterParkClosesTheLiveSession(t *testing.T) {
	pool, apps, c, _, srv := setupWithServer(t)
	ctx := t.Context()
	reg, err := c.Register(with(ctx, secret), &runnerv1.RegisterRequest{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	u := queue(t, pool, apps)
	l, err := c.Lease(with(ctx, secret), &runnerv1.LeaseRequest{RunnerId: reg.GetRunnerId()})
	if err != nil || l.GetRun() == nil {
		t.Fatalf("lease: %v", err)
	}
	tok := l.GetRun().GetToken()
	if _, err := c.Handoff(with(ctx, tok), &runnerv1.HandoffRequest{}); err != nil {
		t.Fatal(err)
	}
	if len(srv.ActiveRuns(u)) != 1 {
		t.Fatal("session missing")
	}
	if _, err := c.Park(with(ctx, tok), &runnerv1.ParkRequest{Reason: "test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Handoff(with(ctx, tok), &runnerv1.HandoffRequest{Done: true}); err != nil {
		t.Fatalf("done after park: %v", err)
	}
	if active := srv.ActiveRuns(u); len(active) != 0 {
		t.Fatalf("live sessions after the run ended: %v", active)
	}
}
