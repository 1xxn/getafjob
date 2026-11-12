package web_test

import (
	"bytes"
	"compress/gzip"
	"html"
	"net/http"
	"regexp"
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

// pageBudget is the server time a page may take at the 95th percentile (docs/ui/phase-8-brief.md).
const pageBudget = 150 * time.Millisecond

// TestJobListLatency: the pages answer within 150 ms at the 95th percentile on the default demo data (2000
// postings). Measured without the race detector, which slows everything down several times; CI runs it on its own
// (go tool task test:budget).
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

	paths := []string{"/", "/jobs", "/jobs?q=kubernetes", "/jobs?decision=all&page=10", "/jobs?decision=all&workplace=remote",
		"/jobs/" + strconv.FormatInt(jobID, 10), "/applications", "/pipeline", "/approvals", "/needs-input", "/live",
		"/activity", "/notifications/bell", "/settings/search", "/profile", "/setup/matches"}
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
		if !raceEnabled && p95 > pageBudget {
			t.Errorf("%s: p95 %v over %v", path, p95, pageBudget)
		}
	}
}

// assetBudget bounds the scripts and style sheets a page loads, gzip compressed (docs/ui/phase-8-brief.md).
const assetBudget = 100 << 10

// TestAssetBudget: the JS and CSS that a signed-in page loads stay under 100 KB compressed.
func TestAssetBudget(t *testing.T) {
	a := newApp(t)
	session := signedIn(t, a)
	page := a.do(http.MethodGet, "/", nil, session, nil).Body.String()
	refs := regexp.MustCompile(`<(?:script[^>]*\ssrc|link[^>]*rel="stylesheet"[^>]*\shref)="(/static/[^"]+)"`).FindAllStringSubmatch(page, -1)
	if len(refs) < 4 {
		t.Fatalf("found only %d scripts and style sheets: %s", len(refs), page)
	}
	total := 0
	for _, m := range refs {
		rec := a.do(http.MethodGet, html.UnescapeString(m[1]), nil, session, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d", m[1], rec.Code)
		}
		var buf bytes.Buffer
		zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
		_, _ = zw.Write(rec.Body.Bytes())
		_ = zw.Close()
		t.Logf("%-50s %6d bytes, %6d gzip", m[1], rec.Body.Len(), buf.Len())
		total += buf.Len()
	}
	t.Logf("total %d bytes gzip, budget %d", total, assetBudget)
	if total > assetBudget {
		t.Errorf("scripts and style sheets take %d bytes compressed, over %d", total, assetBudget)
	}
}
