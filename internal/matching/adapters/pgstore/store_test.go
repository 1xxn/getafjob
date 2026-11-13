package pgstore_test

import (
	"context"
	"errors"
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

	// Read side: search, filters, detail; another user sees nothing.
	ls := matching.NewListings(st)
	l, err := ls.List(ctx, max, matching.Filter{Query: "kubernetes"})
	if err != nil || l.Total != 1 || l.Items[0].Title != "Senior Go Developer" || l.Counts[matching.Skip] != 1 {
		t.Fatalf("search = %+v, %v", l, err)
	}
	l, err = ls.List(ctx, max, matching.Filter{Decisions: []matching.Decision{matching.Skip}, Workplace: "bogus"})
	if err != nil || len(l.Items) != 1 || l.Items[0].Title != "Sales Manager" {
		t.Fatalf("skip filter = %+v, %v", l, err)
	}
	if l, _ = ls.List(ctx, erika, matching.Filter{}); len(l.Items) != 0 {
		t.Fatalf("erika's list = %+v", l)
	}
	full, _ := ls.List(ctx, max, matching.Filter{Query: "go"})
	d, err := ls.Detail(ctx, max, full.Items[0].JobID)
	if err != nil || !d.Open || d.URL != "https://example.org/1" || len(d.Reasons) == 0 || d.Reasons[0].Code != "title.exact" {
		t.Fatalf("detail = %+v, %v", d, err)
	}
	if _, err := ls.Detail(ctx, erika, d.JobID); !errors.Is(err, matching.ErrNotFound) {
		t.Fatalf("erika's detail: %v", err)
	}
}

func TestAdvancedDiscoveryFiltersSortingAndBlocklist(t *testing.T) {
	pool := dbtest.AppPool(t)
	ctx := t.Context()
	now := time.Now().Truncate(time.Hour)
	uID := user(t, pool, "discovery-tester@example.com")
	st := pgstore.New(pool)
	ls := matching.NewListings(st)

	insert := func(fp, comp, title, loc, wp, src string, minSal, maxSal int, daysAgo int) int64 {
		var id int64
		postedDate := now.Add(-time.Duration(daysAgo) * 24 * time.Hour)
		err := db.InSystemTx(ctx, pool, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `
				INSERT INTO jobs (fingerprint, match_key, source, external_id, company, title, location, workplace, url, description, posted, content_hash, first_seen, last_seen, salary_min, salary_max)
				VALUES ($1, $1, $2, $1, $3, $4, $5, $6, 'https://example.org/' || $1, 'Specialized role for ' || $4, $7, 'h', $8, $8, $9, $10)
				RETURNING id`,
				fp, src, comp, title, loc, wp, postedDate, postedDate, minSal, maxSal,
			).Scan(&id)
		})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}

	j1 := insert("job-1", "Zeta Tech", "DevOps Engineer", "Berlin", "remote", "greenhouse", 70000, 85000, 0)        // 0d, score 90
	j2 := insert("job-2", "Alpha Corp", "Site Reliability Engineer", "Munich", "hybrid", "lever", 80000, 95000, 2)  // 2d, score 80
	j3 := insert("job-3", "Beta Cloud", "Platform Architect", "Hamburg", "onsite", "ashby", 100000, 120000, 5)      // 5d, score 85
	j4 := insert("job-4", "Recruitment Pro", "Staff Engineer", "Berlin", "remote", "greenhouse", 60000, 75000, 10)  // 10d, score 70
	j5 := insert("job-5", "Delta Consulting", "Lead Engineer", "Frankfurt", "hybrid", "workday", 90000, 110000, 25) // 25d, score 60

	err := st.SaveMatches(ctx, uID, []matching.Result{
		{JobID: j1, Score: 90, Decision: matching.Match},
		{JobID: j2, Score: 80, Decision: matching.Match},
		{JobID: j3, Score: 85, Decision: matching.Match},
		{JobID: j4, Score: 70, Decision: matching.Maybe},
		{JobID: j5, Score: 60, Decision: matching.Maybe},
	}, now)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Freshness filter
	t.Run("freshness", func(t *testing.T) {
		// Past 24h (1 day): only j1
		l, err := ls.List(ctx, uID, matching.Filter{FreshnessDays: 1, Decisions: []matching.Decision{matching.Match, matching.Maybe}})
		if err != nil || len(l.Items) != 1 || l.Items[0].JobID != j1 {
			t.Fatalf("24h freshness: got %v items, want j1; err=%v", len(l.Items), err)
		}
		// Past 3 days: j1, j2
		l, err = ls.List(ctx, uID, matching.Filter{FreshnessDays: 3, Decisions: []matching.Decision{matching.Match, matching.Maybe}})
		if err != nil || len(l.Items) != 2 {
			t.Fatalf("3d freshness: got %v items, want 2; err=%v", len(l.Items), err)
		}
		// Past 7 days: j1, j2, j3
		l, err = ls.List(ctx, uID, matching.Filter{FreshnessDays: 7, Decisions: []matching.Decision{matching.Match, matching.Maybe}})
		if err != nil || len(l.Items) != 3 {
			t.Fatalf("7d freshness: got %v items, want 3; err=%v", len(l.Items), err)
		}
		// Past 14 days: j1, j2, j3, j4
		l, err = ls.List(ctx, uID, matching.Filter{FreshnessDays: 14, Decisions: []matching.Decision{matching.Match, matching.Maybe}})
		if err != nil || len(l.Items) != 4 {
			t.Fatalf("14d freshness: got %v items, want 4; err=%v", len(l.Items), err)
		}
		// Past 30 days: all 5
		l, err = ls.List(ctx, uID, matching.Filter{FreshnessDays: 30, Decisions: []matching.Decision{matching.Match, matching.Maybe}})
		if err != nil || len(l.Items) != 5 {
			t.Fatalf("30d freshness: got %v items, want 5; err=%v", len(l.Items), err)
		}
	})

	// 2. Sorting
	t.Run("sorting", func(t *testing.T) {
		// Best match (default): j1 (90), j3 (85), j2 (80), j4 (70), j5 (60)
		l, err := ls.List(ctx, uID, matching.Filter{Sort: "match", Decisions: []matching.Decision{matching.Match, matching.Maybe}})
		if err != nil || len(l.Items) != 5 || l.Items[0].JobID != j1 || l.Items[1].JobID != j3 || l.Items[2].JobID != j2 {
			t.Fatalf("sort match: got order [%d, %d, %d...], want [j1=%d, j3=%d, j2=%d...]", l.Items[0].JobID, l.Items[1].JobID, l.Items[2].JobID, j1, j3, j2)
		}

		// Newest first: j1 (0d), j2 (2d), j3 (5d), j4 (10d), j5 (25d)
		l, err = ls.List(ctx, uID, matching.Filter{Sort: "newest", Decisions: []matching.Decision{matching.Match, matching.Maybe}})
		if err != nil || len(l.Items) != 5 || l.Items[0].JobID != j1 || l.Items[1].JobID != j2 || l.Items[2].JobID != j3 {
			t.Fatalf("sort newest: got order [%d, %d, %d...], want [j1=%d, j2=%d, j3=%d...]", l.Items[0].JobID, l.Items[1].JobID, l.Items[2].JobID, j1, j2, j3)
		}

		// Highest salary: j3 (120k), j5 (110k), j2 (95k), j1 (85k), j4 (75k)
		l, err = ls.List(ctx, uID, matching.Filter{Sort: "salary", Decisions: []matching.Decision{matching.Match, matching.Maybe}})
		if err != nil || len(l.Items) != 5 || l.Items[0].JobID != j3 || l.Items[1].JobID != j5 || l.Items[2].JobID != j2 {
			t.Fatalf("sort salary: got order [%d, %d, %d...], want [j3=%d, j5=%d, j2=%d...]", l.Items[0].JobID, l.Items[1].JobID, l.Items[2].JobID, j3, j5, j2)
		}

		// Company name A-Z: Alpha Corp (j2), Beta Cloud (j3), Delta Consulting (j5), Recruitment Pro (j4), Zeta Tech (j1)
		l, err = ls.List(ctx, uID, matching.Filter{Sort: "company", Decisions: []matching.Decision{matching.Match, matching.Maybe}})
		if err != nil || len(l.Items) != 5 || l.Items[0].JobID != j2 || l.Items[1].JobID != j3 || l.Items[2].JobID != j5 {
			t.Fatalf("sort company: got order [%s, %s, %s...], want [Alpha, Beta, Delta...]", l.Items[0].Company, l.Items[1].Company, l.Items[2].Company)
		}
	})

	// 3. Filter pills
	t.Run("filter_pills", func(t *testing.T) {
		// Location filter: "Munich" -> only j2
		l, err := ls.List(ctx, uID, matching.Filter{Location: "Munich", Decisions: []matching.Decision{matching.Match, matching.Maybe}})
		if err != nil || len(l.Items) != 1 || l.Items[0].JobID != j2 {
			t.Fatalf("location Munich: got %d items, want j2", len(l.Items))
		}

		// Workplace: "remote" -> j1, j4
		l, err = ls.List(ctx, uID, matching.Filter{Workplace: "remote", Decisions: []matching.Decision{matching.Match, matching.Maybe}})
		if err != nil || len(l.Items) != 2 {
			t.Fatalf("workplace remote: got %d items, want 2", len(l.Items))
		}

		// ATS source: "greenhouse" -> j1, j4
		l, err = ls.List(ctx, uID, matching.Filter{Source: "greenhouse", Decisions: []matching.Decision{matching.Match, matching.Maybe}})
		if err != nil || len(l.Items) != 2 {
			t.Fatalf("source greenhouse: got %d items, want 2", len(l.Items))
		}

		// Minimum salary: 90000 -> j2 (95k max), j3 (120k max), j5 (110k max)
		l, err = ls.List(ctx, uID, matching.Filter{MinSalary: 90000, Decisions: []matching.Decision{matching.Match, matching.Maybe}})
		if err != nil || len(l.Items) != 3 {
			t.Fatalf("min salary 90000: got %d items, want 3", len(l.Items))
		}
	})

	// 4. Blocklist
	t.Run("blocklist", func(t *testing.T) {
		// Add "Recruitment Pro" (company) to blocklist
		err := ls.AddBlocklist(ctx, uID, "company", "Recruitment Pro")
		if err != nil {
			t.Fatalf("AddBlocklist company: %v", err)
		}

		// Add "consulting" (keyword) to blocklist
		err = ls.AddBlocklist(ctx, uID, "keyword", "consulting")
		if err != nil {
			t.Fatalf("AddBlocklist keyword: %v", err)
		}

		// Check active blocklist items
		bl, err := ls.Blocklist(ctx, uID)
		if err != nil || len(bl) != 2 {
			t.Fatalf("Blocklist items: got %d items, want 2; err=%v", len(bl), err)
		}

		// List jobs: j4 (Recruitment Pro) and j5 (Delta Consulting) should be excluded!
		// Normal list has only 3 jobs remaining: j1, j2, j3
		l, err := ls.List(ctx, uID, matching.Filter{Decisions: []matching.Decision{matching.Match, matching.Maybe}})
		if err != nil || len(l.Items) != 3 {
			t.Fatalf("active list after blocklist: got %d items, want 3", len(l.Items))
		}
		if l.BlockedCount != 2 {
			t.Fatalf("BlockedCount: got %d, want 2", l.BlockedCount)
		}

		// Patterns are words, not LIKE patterns: "_" and "%" block nothing more.
		for _, p := range []string{"_", "%"} {
			if err := ls.AddBlocklist(ctx, uID, "keyword", p); err != nil {
				t.Fatal(err)
			}
		}
		if l, err := ls.List(ctx, uID, matching.Filter{Decisions: []matching.Decision{matching.Match, matching.Maybe}}); err != nil ||
			len(l.Items) != 3 || l.BlockedCount != 2 {
			t.Fatalf("wildcard patterns: %d items, %d blocked, %v", len(l.Items), l.BlockedCount, err)
		}
		for _, p := range []string{"_", "%"} {
			if err := ls.RemoveBlocklistByPattern(ctx, uID, "keyword", p); err != nil {
				t.Fatal(err)
			}
		}

		// Detail on blocked job reports Blocked == true
		d4, err := ls.Detail(ctx, uID, j4)
		if err != nil || !d4.Blocked {
			t.Fatalf("Detail on blocked job: blocked=%v, want true; err=%v", d4.Blocked, err)
		}

		// View blocked jobs (Blocked: true)
		lblk, err := ls.List(ctx, uID, matching.Filter{Blocked: true})
		if err != nil || len(lblk.Items) != 2 {
			t.Fatalf("blocked list: got %d items, want 2; err=%v", len(lblk.Items), err)
		}

		// Remove "Recruitment Pro" by pattern
		err = ls.RemoveBlocklistByPattern(ctx, uID, "company", "Recruitment Pro")
		if err != nil {
			t.Fatalf("RemoveBlocklistByPattern: %v", err)
		}

		// Now only 1 job (j5) is blocked
		l, err = ls.List(ctx, uID, matching.Filter{Decisions: []matching.Decision{matching.Match, matching.Maybe}})
		if err != nil || len(l.Items) != 4 || l.BlockedCount != 1 {
			t.Fatalf("list after unblocking Recruitment Pro: items=%d (want 4), blocked=%d (want 1)", len(l.Items), l.BlockedCount)
		}

		// Remove remaining blocklist item by ID
		bl, _ = ls.Blocklist(ctx, uID)
		if len(bl) != 1 {
			t.Fatalf("remaining blocklist items: got %d, want 1", len(bl))
		}
		err = ls.RemoveBlocklist(ctx, uID, bl[0].ID)
		if err != nil {
			t.Fatalf("RemoveBlocklist by ID: %v", err)
		}

		// Blocklist is now empty
		bl, _ = ls.Blocklist(ctx, uID)
		if len(bl) != 0 {
			t.Fatalf("blocklist not empty: got %d", len(bl))
		}
	})

	// 5. Query performance verification (< 100ms)
	t.Run("query_performance", func(t *testing.T) {
		start := time.Now()
		_, err := ls.List(ctx, uID, matching.Filter{
			FreshnessDays: 7,
			Sort:          "newest",
			MinSalary:     70000,
			Workplace:     "remote",
		})
		elapsed := time.Since(start)
		if err != nil {
			t.Fatalf("performance query failed: %v", err)
		}
		if elapsed > 100*time.Millisecond {
			t.Fatalf("query took %v, want sub-100ms", elapsed)
		}
	})
}
