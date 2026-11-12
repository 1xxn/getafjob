package web

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
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
	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/platform/blob"
	"github.com/1xxn/getafjob/internal/platform/webpush"
	"github.com/1xxn/getafjob/internal/web/views"
)

type fakeHub struct {
	sess   *gateway.HandoffSession
	active []uuid.UUID
}

func (f fakeHub) SessionFor(uuid.UUID) (*gateway.HandoffSession, bool) {
	return f.sess, f.sess != nil
}

func (f fakeHub) ActiveRuns(uuid.UUID) []uuid.UUID {
	return f.active
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

// liveServer serves the live view socket of one application in the given state.
func liveServer(t *testing.T, state application.State, sess *gateway.HandoffSession, logs io.Writer) (*identity.User, string) {
	t.Helper()
	u := &identity.User{ID: uuid.New(), DisplayName: "Max"}
	appID := uuid.New()
	st := &fakeAppStore{userID: u.ID, app: application.App{ID: appID, JobID: 42, State: state, Version: 1}}
	srv := &Server{Apps: application.NewService(st), Handoffs: fakeHub{sess: sess},
		Logger: slog.New(slog.NewTextHandler(logs, nil))}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /applications/{id}/screen", func(w http.ResponseWriter, r *http.Request) {
		srv.screenWebSocket(w, r, u)
	})
	hs := httptest.NewServer(mux)
	t.Cleanup(hs.Close)
	return u, "ws" + strings.TrimPrefix(hs.URL, "http") + "/applications/" + appID.String() + "/screen"
}

func dialLive(ctx context.Context, t *testing.T, url string) *websocket.Conn {
	t.Helper()
	ws, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{Subprotocols: []string{"getafjob-screencast"}})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = ws.CloseNow() })
	return ws
}

// readStatus reads messages until a status message arrives and returns its interactive flag.
func readStatus(ctx context.Context, t *testing.T, ws *websocket.Conn) bool {
	t.Helper()
	for {
		typ, data, err := ws.Read(ctx)
		if err != nil {
			t.Fatalf("read status: %v", err)
		}
		if typ == websocket.MessageText {
			var st screenStatus
			if err := json.Unmarshal(data, &st); err != nil || st.Type != "status" {
				t.Fatalf("status = %s, %v", data, err)
			}
			return st.Interactive
		}
	}
}

func readFrame(ctx context.Context, t *testing.T, ws *websocket.Conn) []byte {
	t.Helper()
	for {
		typ, data, err := ws.Read(ctx)
		if err != nil {
			t.Fatalf("read frame: %v", err)
		}
		if typ == websocket.MessageBinary {
			return data
		}
	}
}

func TestScreenWebSocketStreamsToEveryViewer(t *testing.T) {
	sess := gateway.NewHandoffSession(uuid.Nil)
	_, url := liveServer(t, application.Running, sess, io.Discard)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	a, b := dialLive(ctx, t, url), dialLive(ctx, t, url)
	if readStatus(ctx, t, a) || readStatus(ctx, t, b) {
		t.Fatal("a running run must be view only")
	}
	frame := []byte{0xff, 0xd8, 0xff, 0xd9}
	sess.Publish(frame)
	if !bytes.Equal(readFrame(ctx, t, a), frame) || !bytes.Equal(readFrame(ctx, t, b), frame) {
		t.Fatal("both viewers must get the frame")
	}
}

func TestScreenWebSocketPassesInputOnlyWhileTheRunWaits(t *testing.T) {
	var logs bytes.Buffer
	sess := gateway.NewHandoffSession(uuid.Nil)
	_, url := liveServer(t, application.NeedsInput, sess, &logs)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ws := dialLive(ctx, t, url)
	_ = readStatus(ctx, t, ws)

	click := []byte(`{"type":"mouse","action":"mousePressed","x":120,"y":240,"button":"left","clickCount":1}`)
	if err := ws.Write(ctx, websocket.MessageText, click); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	select {
	case ev := <-sess.Input:
		t.Fatalf("input while the runner does not wait: %+v", ev)
	default:
	}

	sess.MarkWaiting()
	if !readStatus(ctx, t, ws) {
		t.Fatal("status must turn interactive")
	}
	key := []byte(`{"type":"key","action":"keyDown","key":"s","code":"KeyS","text":"secret-code-123","windowsVirtualKeyCode":83}`)
	for _, m := range [][]byte{key, click} {
		if err := ws.Write(ctx, websocket.MessageText, m); err != nil {
			t.Fatal(err)
		}
	}
	if ev := <-sess.Input; ev.GetKey().GetText() != "secret-code-123" {
		t.Fatalf("key = %+v", ev)
	}
	if ev := <-sess.Input; ev.GetMouse().GetX() != 120 || ev.GetMouse().GetY() != 240 {
		t.Fatalf("mouse = %+v", ev)
	}
	if strings.Contains(logs.String(), "secret-code-123") {
		t.Fatalf("input leaked to logs: %s", logs.String())
	}
}

func TestScreenWebSocketClosesForFinishedRuns(t *testing.T) {
	_, url := liveServer(t, application.Confirmed, nil, io.Discard)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ws := dialLive(ctx, t, url)
	_, _, err := ws.Read(ctx)
	if websocket.CloseStatus(err) != screenNotLive {
		t.Fatalf("close = %v, want %d", err, screenNotLive)
	}
}

func TestHandoffPageAnswerAndPark(t *testing.T) {
	u := &identity.User{ID: uuid.New(), DisplayName: "Max"}
	appID := uuid.New()
	sess := gateway.NewHandoffSession(u.ID)
	sess.MarkWaiting() // the runner waits for the answers right now
	st := &fakeAppStore{userID: u.ID, app: application.App{ID: appID, JobID: 42, State: application.NeedsInput, Version: 1}}
	srv := &Server{
		Apps:     application.NewService(st),
		Handoffs: fakeHub{sess: sess},
	}

	// Render handoff page
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/applications/"+appID.String()+"?tab=live", nil)
	req.SetPathValue("id", appID.String())
	srv.applicationPage(rec, req, u)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `live-screen-full`) {
		t.Fatalf("handoff page: %d %s", rec.Code, rec.Body.String())
	}

	// Submit answers
	form := url.Values{
		"question-0": {"Earliest start date?"},
		"input-0":    {"date"},
		"answer-0":   {"2027-01-01"},
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

func (f *fakeAppStore) ByJob(_ context.Context, userID uuid.UUID, jobID int64) (application.App, error) {
	if userID != f.userID || jobID != f.app.JobID {
		return application.App{}, application.ErrNotFound
	}
	return f.app, nil
}

func (f *fakeAppStore) SetCvDocument(_ context.Context, userID, appID uuid.UUID, cvID *uuid.UUID, _ time.Time) error {
	if userID != f.userID || appID != f.app.ID {
		return application.ErrNotFound
	}
	f.app.CvDocumentID = cvID
	return nil
}

func (f *fakeAppStore) SetJobCvDocument(_ context.Context, userID uuid.UUID, jobID int64, cvID *uuid.UUID, _ time.Time) (uuid.UUID, error) {
	if userID != f.userID {
		return uuid.Nil, application.ErrNotFound
	}
	f.app.CvDocumentID = cvID
	f.app.JobID = jobID
	return f.app.ID, nil
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

func (f *fakeAppStore) SaveHomeRunnerToken(context.Context, uuid.UUID, []byte, time.Time) error {
	return nil
}

func (f *fakeAppStore) UserByHomeRunnerToken(context.Context, []byte) (uuid.UUID, error) {
	return uuid.Nil, nil
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

func (f *fakeAppStore) PlanUsers(context.Context, bool) ([]uuid.UUID, error) {
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

func (f *fakeAppStore) UserApplications(context.Context, uuid.UUID) ([]application.ApplicationSummary, error) {
	return nil, nil
}

func (f *fakeAppStore) UserScreenshots(context.Context, uuid.UUID) ([]string, error) {
	return nil, nil
}

func (f *fakeAppStore) GetNotes(_ context.Context, _, appID uuid.UUID) (application.NoteData, error) {
	return application.NoteData{ApplicationID: appID}, nil
}

func (f *fakeAppStore) SaveNotes(_ context.Context, _, _ uuid.UUID, _, _ string, _, _ *time.Time, _ time.Time) error {
	return nil
}

func (f *fakeAppStore) PipelineApplications(_ context.Context, _ uuid.UUID) ([]application.PipelineApplication, error) {
	return nil, nil
}

func (f *fakeAppStore) QueueItems(context.Context, uuid.UUID) ([]application.QueueItem, error) {
	return nil, nil
}

func (f *fakeAppStore) MoveQueued(context.Context, uuid.UUID, uuid.UUID, bool) (bool, error) { return false, nil }

func (f *fakeAppStore) PauseQueued(context.Context, uuid.UUID, uuid.UUID, bool) (bool, error) { return false, nil }

func (f *fakeAppStore) BookmarkJob(_ context.Context, _ uuid.UUID, _ int64, _ time.Time) (uuid.UUID, error) {
	return uuid.New(), nil
}

func (f *fakeAppStore) MarkExternalApplied(_ context.Context, _ uuid.UUID, _ int64, _ time.Time) (uuid.UUID, error) {
	return uuid.New(), nil
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
		Apps:     application.NewService(st),
		Shots:    fakeShots{"runs/1/submit.png": pngBytes, "runs/1/confirm.png": pngBytes},
		Handoffs: fakeHub{active: []uuid.UUID{appID}},
		Logger:   slog.Default(),
	}

	tabs := map[string][]string{
		"fields":   {"First name", "profile:personal.first_name", "Salary expectation", "answer:0192f3a4"},
		"evidence": {"/applications/" + appID.String() + "/screenshots/10", "/applications/" + appID.String() + "/screenshots/11", "https://jobs.example.org/apply/thanks"},
		"timeline": {`data-to="confirmed"`},
		"overview": {`data-state="confirmed"`},
	}
	for tab, wants := range tabs {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/applications/"+appID.String()+"?tab="+tab, nil)
		req.SetPathValue("id", appID.String())
		srv.applicationPage(rec, req, u)
		body := rec.Body.String()
		if rec.Code != http.StatusOK {
			t.Fatalf("%s tab status = %d", tab, rec.Code)
		}
		for _, want := range wants {
			if !strings.Contains(body, want) {
				t.Fatalf("%s tab missing %q", tab, want)
			}
		}
		// The hub still lists the confirmed run, but a finished run is not live.
		if strings.Contains(body, "ui-pill-live") {
			t.Fatal("a confirmed application shows as live")
		}
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/applications/"+appID.String()+"/screenshots/10", nil)
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

func TestFormAnswerPerControl(t *testing.T) {
	cases := []struct {
		input  string
		values []string
		want   string
	}{
		{"checkbox", []string{"No"}, "No"},
		{"checkbox", []string{"No", "Yes"}, "Yes"},
		{"checkbox_group", []string{"Go", "Kubernetes"}, "Go; Kubernetes"},
		{"select", []string{""}, ""},
		{"radio", []string{"Berlin"}, "Berlin"},
		{"text", []string{"  Max  "}, "Max"},
		{"text", []string{string([]byte{'g', 'e', 'm', 0xe4, 0xdf})}, "gem"}, // invalid UTF-8 never reaches the runner
	}
	for _, c := range cases {
		if got := formAnswer(c.input, c.values); got != c.want {
			t.Errorf("%s %v = %q, want %q", c.input, c.values, got, c.want)
		}
	}
}

func TestQuestionsRenderMatchingControls(t *testing.T) {
	var b strings.Builder
	d := views.HandoffData{Name: "Max", AppID: uuid.NewString(), Questions: []views.HandoffQuestion{
		{Question: "I agree to the privacy policy*", Input: "checkbox"},
		{Question: "Work permit?", Input: "radio", Options: []string{"Yes", "No"}},
		{Question: "Country", Input: "select", Options: []string{"Germany", "Austria", "Switzerland", "France", "Spain", "Italy"}},
		{Question: "Start date", Input: "date"},
		{Question: "Resume", Input: "file"},
	}}
	if err := views.ApplicationPage(views.AppDetailData{HandoffData: d, Tab: "overview", StateKey: "apps.state.needs_input", NextKey: "apps.next.needs_input"}).Render(t.Context(), &b); err != nil {
		t.Fatal(err)
	}
	html := b.String()
	for _, want := range []string{`type="checkbox" id="q-0" name="answer-0" value="Yes"`, `type="radio" id="f-answer-1-0" name="answer-1" value="Yes"`,
		`id="q-2" name="answer-2"`, `type="date"`, `id="q-3" name="answer-3"`, `href="/documents"`, `name="save-0" value="1"`} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %s", want)
		}
	}
	if strings.Contains(html, "<textarea") {
		t.Error("no question here is free text")
	}
}

func TestExternalApplyNotice(t *testing.T) {
	var b strings.Builder
	if err := views.ExternalApply("portal", "www.arbeitsagentur.de", "https://www.arbeitsagentur.de/jobsuche/jobdetail/1", 42).
		Render(t.Context(), &b); err != nil {
		t.Fatal(err)
	}
	html := b.String()
	for _, want := range []string{"www.arbeitsagentur.de", `href="https://www.arbeitsagentur.de/jobsuche/jobdetail/1"`,
		`action="/jobs/42/apply-external"`, `rel="noopener noreferrer external"`} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %s in %s", want, html)
		}
	}
}

func (f *fakeAppStore) NavCounts(context.Context, uuid.UUID) (application.NavCounts, error) {
	return application.NavCounts{}, nil
}

func (f *fakeAppStore) ParkReasons(context.Context, uuid.UUID) (map[uuid.UUID]string, error) {
	return nil, nil
}

func (f *fakeAppStore) SubmitsPerDay(context.Context, uuid.UUID, time.Time) (map[time.Time]int, error) {
	return nil, nil
}

func (f *fakeAppStore) Briefs(context.Context, uuid.UUID, []uuid.UUID) ([]application.Brief, error) {
	return nil, nil
}

func (f *fakeAppStore) StatesByJob(context.Context, uuid.UUID, []int64) (map[int64]application.State, error) {
	return nil, nil
}

func (f *fakeAppStore) AnswerUsage(context.Context, uuid.UUID) (map[string]int, error) {
	return nil, nil
}
