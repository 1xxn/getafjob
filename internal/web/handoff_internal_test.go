package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/application"
	"github.com/1xxn/getafjob/internal/gateway"
	"github.com/1xxn/getafjob/internal/identity"
)

// A thumbnail asking for 2 frames per second gets at most one frame per half second, and the newest frame held back
// arrives when the gap ends; without a frame rate every frame passes.
func TestFrameGate(t *testing.T) {
	start := time.Unix(0, 0)
	at := func(ms int) time.Time { return start.Add(time.Duration(ms) * time.Millisecond) }

	g := newFrameGate("2")
	defer g.stop()
	if g.tick() == nil {
		t.Fatal("no ticker for a frame rate")
	}
	sent := 0
	for ms := 0; ms < 1000; ms += 40 { // 25 frames per second from the browser
		if g.offer([]byte{byte(ms / 40)}, at(ms)) != nil {
			sent++
		}
	}
	if sent != 2 {
		t.Errorf("sent %d frames in one second, want 2", sent)
	}
	if f := g.flush(at(1100)); f == nil || f[0] != 24 {
		t.Errorf("flush sent %v, want the newest frame 24", f)
	}
	if f := g.flush(at(1700)); f != nil {
		t.Errorf("flush sent %v twice", f)
	}

	for _, fps := range []string{"", "0", "-1", "x", "30", "120"} {
		g := newFrameGate(fps)
		if g.tick() != nil || g.offer([]byte{1}, at(0)) == nil || g.offer([]byte{2}, at(1)) == nil {
			t.Errorf("fps %q drops frames", fps)
		}
	}
}

type recordingAppStore struct {
	fakeAppStore
	changes []application.Change
}

func (r *recordingAppStore) Apply(_ context.Context, _ uuid.UUID, _ application.App, c application.Change) error {
	r.changes = append(r.changes, c)
	r.app.State = c.To
	return nil
}

func TestScreenEventResumeGuardsFieldQuestions(t *testing.T) {
	ctx := t.Context()
	userID := uuid.New()
	appID := uuid.New()
	u := &identity.User{ID: userID}

	if ev := (&Server{}).screenEvent(ctx, u, appID, screenInputMsg{Type: "resume"}); ev == nil || !ev.GetResume() {
		t.Fatalf("screenEvent without Apps = %v, want resume event", ev)
	}

	st := &recordingAppStore{fakeAppStore: fakeAppStore{
		userID: userID,
		app:    application.App{ID: appID, State: application.NeedsInput},
		qs:     []application.Question{{Kind: application.QuestionField, Label: "Salary"}},
	}}
	srv := &Server{Apps: application.NewService(st)}
	if ev := srv.screenEvent(ctx, u, appID, screenInputMsg{Type: "resume"}); ev != nil {
		t.Fatalf("screenEvent with open field questions = %v, want nil", ev)
	}
	if len(st.changes) != 0 {
		t.Fatalf("unexpected state changes: %+v", st.changes)
	}

	st.qs = []application.Question{{Kind: "captcha", Label: "Solve CAPTCHA"}}
	if ev := srv.screenEvent(ctx, u, appID, screenInputMsg{Type: "resume"}); ev == nil || !ev.GetResume() {
		t.Fatalf("screenEvent with login question = %v, want resume event", ev)
	}
	if len(st.changes) != 1 || st.changes[0].Event != application.EvAnswer {
		t.Fatalf("changes = %+v, want single EvAnswer", st.changes)
	}
}

func TestApplicationAnswerRaceFallback(t *testing.T) {
	userID := uuid.New()
	appID := uuid.New()
	u := &identity.User{ID: userID}

	// Simulate race: sess still looks interactive when lookup happens, but app already parked on timeout.
	sess := gateway.NewHandoffSession(userID)
	sess.MarkWaiting()
	st := &recordingAppStore{fakeAppStore: fakeAppStore{
		userID: userID,
		app:    application.App{ID: appID, State: application.Parked},
	}}
	srv := &Server{
		Apps:     application.NewService(st),
		Handoffs: fakeHub{sess: sess},
	}

	form := url.Values{
		"question-0": {"Work authorization"},
		"input-0":    {"text"},
		"answer-0":   {"Yes"},
		"save-0":     {"0", "1"},
	}
	req := httptest.NewRequest(http.MethodPost, "/applications/"+appID.String()+"/answer", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", appID.String())
	rec := httptest.NewRecorder()
	srv.applicationAnswer(rec, req, u)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	select {
	case ev := <-sess.Input:
		t.Fatalf("unexpected live input pushed after failed EvAnswer: %+v", ev)
	default:
	}
	if len(st.changes) != 1 || st.changes[0].Event != application.EvRequeue || st.app.State != application.Queued {
		t.Fatalf("changes = %+v, state = %s; want single EvRequeue to queued", st.changes, st.app.State)
	}
}
