package pgstore_test

import (
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/1xxn/getafjob/internal/discovery"
	discoverystore "github.com/1xxn/getafjob/internal/discovery/adapters/pgstore"
	"github.com/1xxn/getafjob/internal/matching/adapters/pgstore"
	"github.com/1xxn/getafjob/internal/platform/db"
	"github.com/1xxn/getafjob/internal/platform/db/dbtest"
)

// Only the user's own choices count as feedback: a queue by the planner or a rejected writing suggestion does not.
func TestRatedJobs(t *testing.T) {
	pool := dbtest.AppPool(t)
	ctx := t.Context()
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	ds := discoverystore.New(pool)
	ids := map[string]int64{}
	for i, title := range []string{"Bookmarked", "Skipped", "Planned", "Assist", "Approved"} {
		id, _, err := ds.UpsertPosting(ctx, discovery.Posting{Source: "arbeitnow", ExternalID: strconv.Itoa(i),
			Company: "Muster GmbH", Title: title, Location: "Berlin", URL: "https://example.org/" + strconv.Itoa(i),
			Description: "Go."}, now)
		if err != nil {
			t.Fatal(err)
		}
		ids[title] = id
	}
	max, erika := user(t, pool, "max@example.org"), user(t, pool, "erika@example.org")
	apps := []struct {
		title, state string
		events       [][3]string // event, actor, detail
	}{
		{"Bookmarked", "matched", nil},
		{"Skipped", "skipped", [][3]string{{"skip", "user", `{"reason":"skipped by user"}`}}},
		{"Planned", "queued", [][3]string{{"queue", "worker", `{}`}}},
		{"Assist", "skipped", [][3]string{{"skip", "user", `{"reason":"assist_rejected"}`}}},
		{"Approved", "queued", [][3]string{{"queue", "worker", `{}`}, {"approve", "user", `{}`}}},
	}
	if err := db.InUserTx(ctx, pool, max, func(tx pgx.Tx) error {
		for _, a := range apps {
			var appID string
			if err := tx.QueryRow(ctx, `INSERT INTO applications (user_id, job_id, state, created_at, updated_at)
				VALUES ($1, $2, $3, $4, $4) RETURNING id`, max, ids[a.title], a.state, now).Scan(&appID); err != nil {
				return err
			}
			for _, e := range a.events {
				if _, err := tx.Exec(ctx, `INSERT INTO application_events (application_id, user_id, event, from_state,
					to_state, actor, detail, at) VALUES ($1, $2, $3, 'matched', $4, $5, $6, $7)`,
					appID, max, e[0], a.state, e[1], e[2], now); err != nil {
					return err
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	st := pgstore.New(pool)
	rated, err := st.Rated(ctx, max, 100)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, r := range rated {
		got[r.Job.Title] = r.Liked
	}
	want := map[string]bool{"Bookmarked": true, "Skipped": false, "Approved": true}
	if len(got) != len(want) {
		t.Fatalf("rated = %v, want %v", got, want)
	}
	for title, liked := range want {
		if l, ok := got[title]; !ok || l != liked {
			t.Fatalf("rated = %v, want %v", got, want)
		}
	}
	if other, err := st.Rated(ctx, erika, 100); err != nil || len(other) != 0 {
		t.Fatalf("erika sees max's feedback: %v, %v", other, err)
	}
}
