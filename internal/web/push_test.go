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

	"github.com/1xxn/getafjob/internal/gateway"
	runnerv1 "github.com/1xxn/getafjob/internal/gen/proto/runner/v1"
	"github.com/1xxn/getafjob/internal/identity"
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
