package web

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/platform/events"
)

func TestEventStreamSendsTheUsersChanges(t *testing.T) {
	hub := events.NewHub()
	u := &identity.User{ID: uuid.New()}
	srv := &Server{Events: hub}
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { srv.eventStream(w, r, u) }))
	defer hs.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, hs.URL, nil)
	res, err := hs.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if ct := res.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	app := uuid.New()
	go func() {
		time.Sleep(200 * time.Millisecond)
		hub.Publish(events.Event{User: uuid.New(), App: uuid.New(), To: "running"}) // someone else
		hub.Publish(events.Event{User: u.ID, App: app, Event: "start", From: "queued", To: "running"})
	}()
	sc := bufio.NewScanner(res.Body)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "data: ") {
			if !strings.Contains(line, app.String()) || !strings.Contains(line, `"to":"running"`) || strings.Contains(line, "user") {
				t.Fatalf("data %q", line)
			}
			return
		}
	}
	t.Fatalf("stream ended: %v", sc.Err())
}
