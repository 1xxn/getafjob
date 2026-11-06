package web_test

import (
	"net/http"
	"slices"
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
)

// TestJobListLatency: the job pages answer within 200 ms at the 95th percentile on the default demo data
// (2000 postings). Measured without the race detector, which slows everything down several times.
func TestJobListLatency(t *testing.T) {
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
	c := matching.Criteria{Titles: p.Preferences.Titles, ExcludeTitles: p.Preferences.ExcludeTitles,
		Locations: p.Preferences.Locations, Remote: p.Preferences.Remote, RemoteRegions: p.Preferences.RemoteRegions,
		MinSalary: p.Preferences.MinSalary, Skills: p.Profile.Skills, YearsTotal: p.Profile.Work.YearsTotal}
	ms := matchingstore.New(a.pool)
	if _, err := matching.NewService(criteria{user: c}, ms, ms).Run(ctx, nil); err != nil {
		t.Fatal(err)
	}

	paths := []string{"/jobs", "/jobs?q=kubernetes", "/jobs?decision=all&page=10", "/jobs?decision=all&workplace=remote",
		"/jobs/" + strconv.FormatInt(jobID, 10)}
	for _, path := range paths {
		var took []time.Duration
		for range 40 {
			start := time.Now()
			rec := a.do(http.MethodGet, path, nil, session, nil)
			took = append(took, time.Since(start))
			if rec.Code != http.StatusOK {
				t.Fatalf("%s: %d", path, rec.Code)
			}
		}
		slices.Sort(took)
		p95 := took[len(took)*95/100-1]
		t.Logf("%-40s p50 %v p95 %v", path, took[len(took)/2], p95)
		if !raceEnabled && p95 > 200*time.Millisecond {
			t.Errorf("%s: p95 %v over 200 ms", path, p95)
		}
	}
}
