package pgstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/1xxn/getafjob/internal/discovery"
	discoverystore "github.com/1xxn/getafjob/internal/discovery/adapters/pgstore"
	"github.com/1xxn/getafjob/internal/matching"
	"github.com/1xxn/getafjob/internal/matching/adapters/pgstore"
	"github.com/1xxn/getafjob/internal/platform/db"
	"github.com/1xxn/getafjob/internal/platform/db/dbtest"
)

type profiles map[uuid.UUID]matching.Criteria

func (p profiles) Users(context.Context) ([]uuid.UUID, error) {
	var out []uuid.UUID
	for id := range p {
		out = append(out, id)
	}
	return out, nil
}

func (p profiles) Criteria(_ context.Context, id uuid.UUID) (matching.Criteria, error) {
	return p[id], nil
}

func user(t *testing.T, pool *pgxpool.Pool, email string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := db.InSystemTx(t.Context(), pool, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), "INSERT INTO users (email) VALUES ($1) RETURNING id", email).Scan(&id)
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

type stored struct {
	title, decision string
	score           int
	at              time.Time
}

func matches(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID) map[string]stored {
	t.Helper()
	out := map[string]stored{}
	if err := db.InUserTx(t.Context(), pool, userID, func(tx pgx.Tx) error {
		rows, err := tx.Query(t.Context(), `SELECT j.title, m.decision, m.score, m.computed_at FROM matches m
			JOIN jobs j ON j.id = m.job_id WHERE m.reasons @> '[{"code":"title.exact"}]' OR m.decision = 'skip'`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var s stored
			if err := rows.Scan(&s.title, &s.decision, &s.score, &s.at); err != nil {
				return err
			}
			out[s.title] = s
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRunStoresResultsPerUser(t *testing.T) {
	pool := dbtest.AppPool(t)
	ctx := t.Context()
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	ds := discoverystore.New(pool)
	run, err := ds.StartRun(ctx, []string{}, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []discovery.Posting{
		{Source: "arbeitnow", ExternalID: "1", Company: "Acme GmbH", Title: "Senior Go Developer", Location: "Berlin",
			URL: "https://example.org/1", Description: "Go, PostgreSQL and Kubernetes. We are looking for you."},
		{Source: "arbeitnow", ExternalID: "2", Company: "Muster AG", Title: "Sales Manager", Location: "Berlin",
			URL: "https://example.org/2", Description: "Sales."},
	} {
		if _, _, err := ds.Upsert(ctx, run, p, now); err != nil {
			t.Fatal(err)
		}
	}

	max, erika := user(t, pool, "max@example.org"), user(t, pool, "erika@example.org")
	st := pgstore.New(pool)
	svc := matching.NewService(profiles{max: {Titles: []string{"Go Developer"}, Locations: []string{"Berlin"},
		Skills: []string{"Go", "PostgreSQL", "Kubernetes"}, YearsTotal: "7"}}, st, st).WithClock(func() time.Time { return now })
	stats, err := svc.Run(ctx, nil)
	if err != nil || stats.Users != 1 || stats.Jobs != 2 {
		t.Fatalf("Run = %+v, %v", stats, err)
	}
	got := matches(t, pool, max)
	if got["Senior Go Developer"].decision != "match" || got["Sales Manager"].decision != "skip" {
		t.Fatalf("matches = %+v", got)
	}
	if len(matches(t, pool, erika)) != 0 {
		t.Fatal("erika sees max's matches")
	}

	// An unchanged result is not written again.
	svc.WithClock(func() time.Time { return now.Add(time.Hour) })
	if _, err := svc.Run(ctx, []uuid.UUID{max}); err != nil {
		t.Fatal(err)
	}
	if again := matches(t, pool, max); !again["Senior Go Developer"].at.Equal(now) {
		t.Fatalf("rewritten: %+v", again)
	}
}
