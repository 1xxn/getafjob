package pgstore_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/1xxn/getafjob/internal/application"
	"github.com/1xxn/getafjob/internal/platform/events"
)

// A state change written by any process reaches the user's event stream through the database (#166).
func TestStateChangesReachTheEventHub(t *testing.T) {
	pool, _, svc, _ := setup(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	hub := events.NewHub()
	go events.Listen(ctx, pool, hub, slog.New(slog.NewTextHandler(io.Discard, nil)))
	max := user(t, pool, "max@example.org")
	_, ch, unsubscribe := hub.Subscribe(max, 0)
	defer unsubscribe()
	time.Sleep(500 * time.Millisecond) // LISTEN is set up

	id, err := svc.Create(ctx, max, jobAt(t, pool, "https://jobs.lever.co/acme/1", "Acme", "acme|1"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Fire(ctx, max, id, application.EvQueue, application.ActorWorker, nil); err != nil {
		t.Fatal(err)
	}
	for {
		select {
		case e := <-ch:
			if e.App == id && e.To == string(application.Queued) {
				return
			}
		case <-ctx.Done():
			t.Fatal("no event for the queued application")
		}
	}
}
