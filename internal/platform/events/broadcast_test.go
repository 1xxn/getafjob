package events_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/1xxn/getafjob/internal/platform/db"
	"github.com/1xxn/getafjob/internal/platform/db/dbtest"
	"github.com/1xxn/getafjob/internal/platform/events"
)

// An event without a user reaches every subscriber and is not replayed.
func TestHubBroadcasts(t *testing.T) {
	hub := events.NewHub()
	a, b := uuid.New(), uuid.New()
	_, chA, stopA := hub.Subscribe(a, 0)
	defer stopA()
	_, chB, stopB := hub.Subscribe(b, 0)
	defer stopB()
	hub.Publish(events.Event{Topic: events.TopicCrawl, Event: "started"})
	for _, ch := range []<-chan events.Event{chA, chB} {
		select {
		case e := <-ch:
			if e.Topic != events.TopicCrawl {
				t.Fatalf("event %+v", e)
			}
		default:
			t.Fatal("a subscriber missed the broadcast")
		}
	}
	if past, _, stop := hub.Subscribe(a, 1); len(past) != 0 {
		stop()
		t.Fatalf("broadcast replayed: %+v", past)
	} else {
		stop()
	}
}

// A worker's notification (the crawl, for everyone) and a mailbox suggestion (database trigger, for its user)
// reach the hub through Postgres.
func TestNotifyAndSuggestionsReachTheHub(t *testing.T) {
	pool := dbtest.AppPool(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	hub := events.NewHub()
	go events.Listen(ctx, pool, hub, slog.New(slog.NewTextHandler(io.Discard, nil)))
	var user, app uuid.UUID
	if err := db.InSystemTx(ctx, pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, "INSERT INTO users (email) VALUES ('events@example.org') RETURNING id").Scan(&user); err != nil {
			return err
		}
		var job int64
		if err := tx.QueryRow(ctx, `INSERT INTO jobs (fingerprint, match_key, source, external_id, company, title, url, content_hash,
			first_seen, last_seen) VALUES ('fp-ev', 'k', 'demo', 'fp-ev', 'Muster GmbH', 'Go Developer', 'https://example.org', 'h', now(), now())
			RETURNING id`).Scan(&job); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `INSERT INTO applications (user_id, job_id, state, created_at, updated_at)
			VALUES ($1, $2, 'submitted', now(), now()) RETURNING id`, user, job).Scan(&app)
	}); err != nil {
		t.Fatal(err)
	}
	_, ch, stop := hub.Subscribe(user, 0)
	defer stop()
	time.Sleep(500 * time.Millisecond) // LISTEN is set up

	if err := events.Notify(ctx, pool, events.TopicCrawl, "finished", uuid.Nil); err != nil {
		t.Fatal(err)
	}
	if err := db.InSystemTx(ctx, pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO tracking_suggestions (id, user_id, application_id, message_uid, message_from,
			message_subject, message_date, category, suggested_state, created_at, updated_at)
			VALUES ($1, $2, $3, 1, 'hr@example.org', 'Update', now(), 'confirmation', 'confirmed', now(), now())`, uuid.New(), user, app)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for !seen[events.TopicCrawl] || !seen[events.TopicSuggestion] {
		select {
		case e := <-ch:
			seen[e.Topic] = true
			if e.Topic == events.TopicSuggestion && e.App != app {
				t.Fatalf("suggestion for %v, want %v", e.App, app)
			}
		case <-ctx.Done():
			t.Fatalf("events seen %v", seen)
		}
	}
}
