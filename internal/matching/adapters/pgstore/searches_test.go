package pgstore_test

import (
	"context"
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/1xxn/getafjob/internal/matching"
	"github.com/1xxn/getafjob/internal/matching/adapters/pgstore"
	"github.com/1xxn/getafjob/internal/platform/db"
	"github.com/1xxn/getafjob/internal/platform/db/dbtest"
)

// A saved search keeps its filters per user; its alert counts only jobs first seen after the last check, at most
// once a day, and a user never sees another user's searches.
func TestSavedSearchesAndAlerts(t *testing.T) {
	pool := dbtest.AppPool(t)
	ctx := t.Context()
	st := pgstore.New(pool)
	svc := matching.NewSavedSearches(st, matching.NewListings(st))
	now := time.Now().Truncate(time.Second)
	svc.SetNow(func() time.Time { return now })
	max := user(t, pool, "max@example.org")
	erika := user(t, pool, "erika@example.org")

	// insert adds an open job first seen at seen; job also matches it for Max, computed at seen.
	insert := func(fp, title string, seen time.Time) int64 {
		t.Helper()
		var id int64
		if err := db.InSystemTx(ctx, pool, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `INSERT INTO jobs (fingerprint, match_key, source, external_id, company, title, url,
				content_hash, first_seen, last_seen) VALUES ($1, $1, 'lever', $1, 'Muster GmbH', $2, 'https://example.org/' || $1,
				'h', $3, $3) RETURNING id`, fp, title, seen).Scan(&id)
		}); err != nil {
			t.Fatal(err)
		}
		return id
	}
	match := func(id int64, at time.Time) {
		t.Helper()
		if err := st.SaveMatches(ctx, max, []matching.Result{{JobID: id, Score: 90, Decision: matching.Match}}, at); err != nil {
			t.Fatal(err)
		}
	}
	job := func(fp, title string, seen time.Time) { match(insert(fp, title, seen), seen) }
	job("old-go", "Go Developer", now.Add(-time.Hour))

	if _, err := svc.Save(ctx, max, "  ", "q=go", true); !errors.Is(err, matching.ErrInvalidSearch) {
		t.Fatalf("empty name: %v", err)
	}
	id, err := svc.Save(ctx, max, " Go  remote ", "?q=go", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Save(ctx, max, "Java", "q=java", false); err != nil {
		t.Fatal(err)
	}
	list, _ := svc.List(ctx, max)
	if len(list) != 2 || list[0].Name != "Go remote" || list[0].Query != "q=go" || !list[0].Alert || list[1].Alert {
		t.Fatalf("list: %+v", list)
	}
	if other, _ := svc.List(ctx, erika); len(other) != 0 {
		t.Fatalf("another user sees %d searches", len(other))
	}
	if ok, _ := svc.Rename(ctx, erika, id, "mine"); ok {
		t.Error("another user renamed the search")
	}

	filter := func(query string) matching.Filter {
		v, _ := url.ParseQuery(query)
		return matching.Filter{Query: v.Get("q"), Decisions: []matching.Decision{matching.Match, matching.Maybe}}
	}
	var sent []matching.Alert
	send := func(_ context.Context, a matching.Alert) error { sent = append(sent, a); return nil }

	// Not due yet: the alert was set up just now.
	if n, err := svc.CheckAlerts(ctx, filter, send); err != nil || n != 0 {
		t.Fatalf("check right away: %d, %v", n, err)
	}

	// A day later two new Go jobs and a new Java job: only the Go jobs count, the old one does not.
	job("new-go-1", "Senior Go Developer", now.Add(time.Hour))
	job("new-go-2", "Go Engineer", now.Add(2*time.Hour))
	job("new-java", "Java Developer", now.Add(2*time.Hour))
	now = now.Add(25 * time.Hour)
	if n, err := svc.CheckAlerts(ctx, filter, send); err != nil || n != 1 || sent[0].New != 2 || sent[0].Search.Name != "Go remote" {
		t.Fatalf("first alert: %d, %v, %+v", n, err, sent)
	}
	// Checked: within the day nothing is sent again, and after it only newer jobs count.
	now = now.Add(time.Hour)
	if n, _ := svc.CheckAlerts(ctx, filter, send); n != 0 {
		t.Fatal("second alert within a day")
	}
	now = now.Add(24 * time.Hour)
	if n, _ := svc.CheckAlerts(ctx, filter, send); n != 0 {
		t.Fatal("alert without new jobs")
	}

	// Crawl and matching run apart: a job first seen after the last matching has no result yet. The check leaves it
	// for later instead of skipping it, and the check after the next matching counts it once.
	oldGo := insert("old-go-2", "Go Platform Engineer", now.Add(-48*time.Hour))
	match(oldGo, now.Add(-time.Minute))
	late := insert("late-go", "Go Backend Developer", now)
	now = now.Add(25 * time.Hour)
	if n, _ := svc.CheckAlerts(ctx, filter, send); n != 0 {
		t.Fatal("alert for a job the matching has not reached")
	}
	match(late, now)
	match(oldGo, now)
	now = now.Add(25 * time.Hour)
	if n, err := svc.CheckAlerts(ctx, filter, send); err != nil || n != 1 || sent[len(sent)-1].New != 1 {
		t.Fatalf("late job: %d, %v, %+v", n, err, sent)
	}

	if ok, _ := svc.SetAlert(ctx, max, id, false); !ok {
		t.Fatal("alert off")
	}
	if ok, _ := svc.Delete(ctx, max, id); !ok {
		t.Fatal("delete")
	}
	if list, _ := svc.List(ctx, max); len(list) != 1 {
		t.Fatalf("after delete: %+v", list)
	}
}
