package web

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/application"
	"github.com/1xxn/getafjob/internal/gateway"
	runnerv1 "github.com/1xxn/getafjob/internal/gen/proto/runner/v1"
	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/platform/blob"
	"github.com/1xxn/getafjob/internal/platform/webpush"
)

type fakeHub struct {
	sess *gateway.HandoffSession
}

func (f fakeHub) SessionFor(uuid.UUID) (*gateway.HandoffSession, bool) {
	return f.sess, f.sess != nil
}

type memPushStore struct {
	subs map[uuid.UUID][]webpush.Subscription
}

func (m *memPushStore) Upsert(_ context.Context, userID uuid.UUID, sub webpush.Subscription) error {
	m.subs[userID] = append(m.subs[userID], sub)
	return nil
}

func (m *memPushStore) Delete(_ context.Context, userID uuid.UUID, endpoint string) error {
	var kept []webpush.Subscription
	for _, s := range m.subs[userID] {
		if s.Endpoint != endpoint {
			kept = append(kept, s)
		}
	}
	m.subs[userID] = kept
	return nil
}

func (m *memPushStore) DeleteEndpoint(_ context.Context, _ string) error {
	return nil
}

func (m *memPushStore) ByUser(_ context.Context, userID uuid.UUID) ([]webpush.Subscription, error) {
	return m.subs[userID], nil
}

func TestPushEndpoints(t *testing.T) {
	store := &memPushStore{subs: make(map[uuid.UUID][]webpush.Subscription)}
	srv := &Server{
		PushStore:      store,
		VAPIDPublicKey: "test-vapid-public-key-base64",
	}
	mux := http.NewServeMux()
	srv.Register(mux)

	// GET /push/key
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/push/key", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /push/key: got %d, want %d", rec.Code, http.StatusOK)
	}
	var keyResp map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &keyResp)
	if keyResp["publicKey"] != "test-vapid-public-key-base64" {
		t.Errorf("expected public key in response, got: %v", keyResp)
	}

	// GET /sw.js
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/sw.js", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /sw.js: got %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Header().Get("Service-Worker-Allowed") != "/" {
		t.Errorf("expected Service-Worker-Allowed: /, got %s", rec.Header().Get("Service-Worker-Allowed"))
	}
	if !strings.Contains(rec.Body.String(), "showNotification") {
		t.Error("sw.js content missing showNotification")
	}

	// POST /push/subscribe requires auth
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/push/subscribe", strings.NewReader(`{"endpoint":"https://push.example.com/1","keys":{"p256dh":"k","auth":"a"}}`))
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized without user, got %d", rec.Code)
	}

	// POST /push/subscribe with user
	u := &identity.User{ID: uuid.New(), DisplayName: "Alice"}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/push/subscribe", strings.NewReader(`{"endpoint":"https://push.example.com/1","keys":{"p256dh":"k","auth":"a"}}`))
	srv.pushSubscribe(rec, req, u)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204 NoContent, got %d", rec.Code)
	}
	if len(store.subs[u.ID]) != 1 {
		t.Fatalf("expected 1 subscription stored, got %d", len(store.subs[u.ID]))
	}

	// DELETE /push/subscribe with user
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/push/subscribe", strings.NewReader(`{"endpoint":"https://push.example.com/1"}`))
	srv.pushUnsubscribe(rec, req, u)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204 NoContent, got %d", rec.Code)
	}
	if len(store.subs[u.ID]) != 0 {
		t.Fatalf("expected subscription deleted, got %d", len(store.subs[u.ID]))
	}
}

func TestScreenWebSocketStreamsFramesAndNeverLogsInput(t *testing.T) {
	var logs bytes.Buffer
	sess := &gateway.HandoffSession{
		Frames: make(chan []byte, 4),
		Input:  make(chan *runnerv1.HandoffEvent, 10),
		Done:   make(chan struct{}),
	}
	u := &identity.User{ID: uuid.New(), DisplayName: "Max"}
	appID := uuid.New()
	srv := &Server{
		Handoffs: fakeHub{sess: sess},
		Logger:   slog.New(slog.NewTextHandler(&logs, nil)),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /applications/{id}/screen", func(w http.ResponseWriter, r *http.Request) {
		srv.screenWebSocket(w, r, u)
	})
	hs := httptest.NewServer(mux)
	defer hs.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(hs.URL, "http") + "/applications/" + appID.String() + "/screen"
	ws, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{Subprotocols: []string{"getafjob-screencast"}})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = ws.CloseNow() }()

	sess.Frames <- []byte{0xff, 0xd8, 0xff, 0xd9}
	typ, frame, err := ws.Read(ctx)
	if err != nil || typ != websocket.MessageBinary || !bytes.Equal(frame, []byte{0xff, 0xd8, 0xff, 0xd9}) {
		t.Fatalf("frame = %v, %v, %v", typ, frame, err)
	}

	if err := ws.Write(ctx, websocket.MessageText, []byte(`{"type":"key","action":"keyDown","key":"s","code":"KeyS","text":"secret-code-123","windowsVirtualKeyCode":83}`)); err != nil {
		t.Fatal(err)
	}
	if err := ws.Write(ctx, websocket.MessageText, []byte(`{"type":"mouse","action":"mousePressed","x":120,"y":240,"button":"left","clickCount":1}`)); err != nil {
		t.Fatal(err)
	}
	msg1 := <-sess.Input
	if msg1.GetKey().GetText() != "secret-code-123" {
		t.Fatalf("key = %+v", msg1)
	}
	msg2 := <-sess.Input
	if msg2.GetMouse().GetX() != 120 || msg2.GetMouse().GetY() != 240 {
		t.Fatalf("mouse = %+v", msg2)
	}
	if strings.Contains(logs.String(), "secret-code-123") {
		t.Fatalf("input leaked to logs: %s", logs.String())
	}
}

func TestHandoffPageAnswerAndPark(t *testing.T) {
	sess := &gateway.HandoffSession{
		Frames: make(chan []byte, 4),
		Input:  make(chan *runnerv1.HandoffEvent, 10),
		Done:   make(chan struct{}),
	}
	u := &identity.User{ID: uuid.New(), DisplayName: "Max"}
	appID := uuid.New()
	srv := &Server{
		Handoffs: fakeHub{sess: sess},
	}

	// Render handoff page
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/applications/"+appID.String(), nil)
	req.SetPathValue("id", appID.String())
	srv.applicationPage(rec, req, u)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `id="screencast"`) {
		t.Fatalf("handoff page: %d %s", rec.Code, rec.Body.String())
	}

	// Submit answers
	form := url.Values{
		"question": {"Earliest start date?"},
		"answer":   {"2027-01-01"},
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/applications/"+appID.String()+"/answer", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", appID.String())
	srv.applicationAnswer(rec, req, u)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("answer status = %d", rec.Code)
	}
	ev := <-sess.Input
	if !ev.GetResume() || len(ev.GetAnswers()) != 1 || ev.GetAnswers()[0].GetAnswer() != "2027-01-01" {
		t.Fatalf("resume event = %+v", ev)
	}

	// Park
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/applications/"+appID.String()+"/park", nil)
	req.SetPathValue("id", appID.String())
	srv.applicationPark(rec, req, u)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("park status = %d", rec.Code)
	}
	ev = <-sess.Input
	if !ev.GetPark() {
		t.Fatalf("park event = %+v", ev)
	}
}

type fakeAppStore struct {
	userID uuid.UUID
	app    application.App
	ev     application.Evidence
	shots  map[int64]string
}

func (f *fakeAppStore) Create(context.Context, uuid.UUID, int64, application.State, time.Time) (uuid.UUID, error) {
	return f.app.ID, nil
}

func (f *fakeAppStore) Get(_ context.Context, userID, id uuid.UUID) (application.App, error) {
	if userID != f.userID || id != f.app.ID {
		return application.App{}, application.ErrNotFound
	}
	return f.app, nil
}

func (f *fakeAppStore) Questions(context.Context, uuid.UUID, uuid.UUID) ([]application.Question, error) {
	return nil, nil
}

func (f *fakeAppStore) Evidence(_ context.Context, userID, id uuid.UUID) (application.Evidence, error) {
	if userID != f.userID || id != f.app.ID {
		return application.Evidence{}, application.ErrNotFound
	}
	return f.ev, nil
}

func (f *fakeAppStore) CheckpointScreenshot(_ context.Context, userID, appID uuid.UUID, checkpointID int64) (string, error) {
	if userID != f.userID || appID != f.app.ID {
		return "", application.ErrNotFound
	}
	k, ok := f.shots[checkpointID]
	if !ok || k == "" {
		return "", application.ErrNotFound
	}
	return k, nil
}

func (f *fakeAppStore) Apply(context.Context, uuid.UUID, application.App, application.Change) error {
	return nil
}

func (f *fakeAppStore) Candidate(context.Context, uuid.UUID, int64) (application.Candidate, error) {
	return application.Candidate{}, nil
}

func (f *fakeAppStore) StaleSubmitting(context.Context, time.Time) ([]application.Ref, error) {
	return nil, nil
}

func (f *fakeAppStore) HandoffsDue(context.Context, time.Time) ([]application.Handoff, error) {
	return nil, nil
}

func (f *fakeAppStore) MarkWarned(context.Context, application.Ref, time.Time) (bool, error) {
	return false, nil
}

func (f *fakeAppStore) ExpiredScreenshots(context.Context, time.Time) ([]application.ExpiredScreenshot, error) {
	return nil, nil
}

func (f *fakeAppStore) ClearScreenshot(context.Context, uuid.UUID, int64) error {
	return nil
}

func (f *fakeAppStore) Settings(context.Context, uuid.UUID) (application.Settings, error) {
	return application.DefaultSettings, nil
}

func (f *fakeAppStore) SaveSettings(context.Context, uuid.UUID, application.Settings, time.Time) error {
	return nil
}

func (f *fakeAppStore) RuleInput(context.Context, uuid.UUID, application.App, uuid.UUID, time.Time) (application.RuleInput, error) {
	return application.RuleInput{}, nil
}

func (f *fakeAppStore) Approve(context.Context, uuid.UUID, application.App, time.Time) error {
	return nil
}

func (f *fakeAppStore) Approvals(context.Context, uuid.UUID) ([]application.Approval, error) {
	return nil, nil
}

func (f *fakeAppStore) PlanUsers(context.Context) ([]uuid.UUID, error) {
	return nil, nil
}

func (f *fakeAppStore) ActiveApplicationsCount(context.Context, uuid.UUID) (int, error) {
	return 0, nil
}

func (f *fakeAppStore) UserLastPacedAt(context.Context, uuid.UUID) (time.Time, error) {
	return time.Time{}, nil
}

func (f *fakeAppStore) PlannerCandidates(context.Context, uuid.UUID, int) ([]application.PlannerCandidate, error) {
	return nil, nil
}

func (f *fakeAppStore) SubmitsSince(context.Context, uuid.UUID, time.Time) (int, error) {
	return 0, nil
}

type fakeShots map[string][]byte

func (s fakeShots) Get(_ context.Context, _ uuid.UUID, key string) ([]byte, error) {
	b, ok := s[key]
	if !ok {
		return nil, blob.ErrNotFound
	}
	return b, nil
}

func TestEvidencePageAndScreenshotEndpoint(t *testing.T) {
	u := &identity.User{ID: uuid.New(), DisplayName: "Max"}
	other := &identity.User{ID: uuid.New(), DisplayName: "Erika"}
	appID := uuid.New()
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

	st := &fakeAppStore{
		userID: u.ID,
		app:    application.App{ID: appID, JobID: 42, State: application.Confirmed, Version: 5, UpdatedAt: now},
		ev: application.Evidence{
			Fields: []application.EvidenceField{
				{Page: 1, ID: "f-1", Label: "First name", Value: "Max", Source: "profile:personal.first_name"},
				{Page: 1, ID: "f-2", Label: "Salary expectation", Value: "95000", Source: "answer:0192f3a4"},
			},
			BeforeSubmitCheckpointID: 10,
			ConfirmationCheckpointID: 11,
			ConfirmationURL:          "https://jobs.example.org/apply/thanks",
			Confirmed:                true,
			Timeline: []application.TimelineEntry{
				{At: now.Add(-2 * time.Minute), Kind: "checkpoint", Step: "execute", Page: 1, URL: "https://jobs.example.org/apply", CheckpointID: 9},
				{At: now.Add(-time.Minute), Kind: "checkpoint", Step: "submit", Page: 1, URL: "https://jobs.example.org/apply", CheckpointID: 10},
				{At: now, Kind: "event", Step: "confirm", FromState: application.Submitted, ToState: application.Confirmed, Actor: application.ActorRunner, Detail: "page"},
			},
		},
		shots: map[int64]string{
			10: "runs/1/submit.png",
			11: "runs/1/confirm.png",
		},
	}
	pngBytes := []byte("\x89PNG\r\n\x1a\ntest-png")
	srv := &Server{
		Apps:   application.NewService(st),
		Shots:  fakeShots{"runs/1/submit.png": pngBytes, "runs/1/confirm.png": pngBytes},
		Logger: slog.Default(),
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/applications/"+appID.String(), nil)
	req.SetPathValue("id", appID.String())
	srv.applicationPage(rec, req, u)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("evidence page status = %d", rec.Code)
	}
	for _, want := range []string{
		"First name",
		"profile:personal.first_name",
		"Salary expectation",
		"answer:0192f3a4",
		"/applications/" + appID.String() + "/screenshots/10",
		"/applications/" + appID.String() + "/screenshots/11",
		"https://jobs.example.org/apply/thanks",
		"submitted -&gt; confirmed",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("evidence page missing %q in body:\n%s", want, body)
		}
	}

	// Owner can fetch the decrypted screenshot.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/applications/"+appID.String()+"/screenshots/10", nil)
	req.SetPathValue("id", appID.String())
	req.SetPathValue("checkpoint", "10")
	srv.applicationScreenshot(rec, req, u)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" || !bytes.Equal(rec.Body.Bytes(), pngBytes) {
		t.Fatalf("screenshot response: status=%d type=%q body=%q", rec.Code, rec.Header().Get("Content-Type"), rec.Body.Bytes())
	}

	// Another user gets 404.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/applications/"+appID.String()+"/screenshots/10", nil)
	req.SetPathValue("id", appID.String())
	req.SetPathValue("checkpoint", "10")
	srv.applicationScreenshot(rec, req, other)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("other user screenshot status = %d, want 404", rec.Code)
	}
}
