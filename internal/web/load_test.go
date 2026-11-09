package web_test

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/1xxn/getafjob/internal/demo"
	discoverystore "github.com/1xxn/getafjob/internal/discovery/adapters/pgstore"
	"github.com/1xxn/getafjob/internal/matching"
	matchingstore "github.com/1xxn/getafjob/internal/matching/adapters/pgstore"
	"github.com/1xxn/getafjob/internal/platform/db"
	"github.com/1xxn/getafjob/internal/platform/loadtest"
	"github.com/1xxn/getafjob/internal/web"
)

// TestConcurrentLoadSLO executes a concurrent load test with virtual users against the full web stack
// with 2000 demo job postings, asserting that p95 latency stays under 200 ms (ADR 0021).
func TestConcurrentLoadSLO(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}

	a := newApp(t)
	ctx := t.Context()
	session := signedIn(t, a)
	now := time.Now()

	ds := discoverystore.New(a.pool)
	run, err := ds.StartRun(ctx, []string{demo.Source}, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range demo.Postings(2000, 1, now) {
		if _, _, err := ds.Upsert(ctx, run, p, now); err != nil {
			t.Fatal(err)
		}
	}

	var user uuid.UUID
	var jobID int64
	if err := db.InSystemTx(ctx, a.pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, "SELECT id FROM users WHERE email = 'max.mustermann@example.com'").Scan(&user); err != nil {
			return err
		}
		return tx.QueryRow(ctx, "SELECT id FROM jobs ORDER BY id DESC LIMIT 1").Scan(&jobID)
	}); err != nil {
		t.Fatal(err)
	}

	p := demo.People()[0]
	c := matching.Criteria{
		Titles:        p.Preferences.Titles,
		ExcludeTitles: p.Preferences.ExcludeTitles,
		Locations:     p.Preferences.Locations,
		Remote:        p.Preferences.Remote,
		RemoteRegions: p.Preferences.RemoteRegions,
		MinSalary:     p.Preferences.MinSalary,
		Skills:        p.Profile.Skills,
		YearsTotal:    p.Profile.Work.YearsTotal,
	}
	ms := matchingstore.New(a.pool)
	if _, err := matching.NewService(criteria{user: c}, ms, ms).Run(ctx, nil); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(a.h)
	defer srv.Close()

	paths := []loadtest.WeightedPath{
		{Path: "/jobs", Weight: 30},
		{Path: "/jobs?q=kubernetes", Weight: 20},
		{Path: "/jobs?decision=all&page=10", Weight: 15},
		{Path: "/jobs?decision=all&workplace=remote", Weight: 15},
		{Path: "/jobs/" + strconv.FormatInt(jobID, 10), Weight: 10},
		{Path: "/profile", Weight: 5},
		{Path: "/preferences", Weight: 5},
	}

	cfg := loadtest.Config{
		BaseURL: srv.URL,
		Cookies: []*http.Cookie{
			{Name: web.SessionCookie, Value: session},
		},
		Paths:       paths,
		Concurrency: 8,
		Duration:    2 * time.Second,
		TargetSLO:   200 * time.Millisecond,
	}

	stats, err := loadtest.Run(ctx, cfg)
	if err != nil {
		t.Fatalf("load test run failed: %v", err)
	}

	t.Logf("\n%s", stats.Summary())

	if stats.TotalCount < 20 {
		t.Errorf("expected at least 20 requests during load test, got %d", stats.TotalCount)
	}
	if stats.ErrorCount > 0 {
		t.Errorf("expected 0 errors during load test, got %d: %+v", stats.ErrorCount, stats.StatusCounts)
	}
	if !raceEnabled && !stats.PassedSLO {
		t.Errorf("load test failed to meet SLO: p95 %v > %v", stats.P95, stats.TargetSLO)
	}
}
