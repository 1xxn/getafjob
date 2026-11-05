package pgstore_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/1xxn/getafjob/internal/discovery"
	"github.com/1xxn/getafjob/internal/discovery/adapters/pgstore"
	"github.com/1xxn/getafjob/internal/platform/db/dbtest"
)

type source struct {
	name  string
	batch discovery.Batch
	err   error
}

func (s *source) Name() string { return s.name }

func (s *source) Fetch(context.Context) (discovery.Batch, error) { return s.batch, s.err }

func posting(src, id, company, title, location string) discovery.Posting {
	return discovery.Posting{
		Source: src, ExternalID: id, Company: company, Title: title, Location: location,
		URL: "https://jobs.example.org/" + src + "/" + id, ApplyURL: "https://jobs.example.org/" + src + "/" + id + "/apply",
		Description: "Build things with Max Mustermann.", Posted: "2026-11-01",
	}
}

type job struct {
	Status, Source, Title, URL, ApplyURL string
	AltURLs                              []string
}

func load(t *testing.T, pool *pgxpool.Pool, src, id string) (job, bool) {
	t.Helper()
	var j job
	err := pool.QueryRow(context.Background(),
		"SELECT status, source, title, url, apply_url, alt_urls FROM jobs WHERE fingerprint = $1",
		discovery.Fingerprint(src, id)).Scan(&j.Status, &j.Source, &j.Title, &j.URL, &j.ApplyURL, &j.AltURLs)
	if err != nil {
		return job{}, false
	}
	return j, true
}

func mustLoad(t *testing.T, pool *pgxpool.Pool, src, id string) job {
	t.Helper()
	j, ok := load(t, pool, src, id)
	if !ok {
		t.Fatalf("job %s/%s not found", src, id)
	}
	return j
}

func changes(t *testing.T, pool *pgxpool.Pool, src, id string) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT s.change FROM job_snapshots s JOIN jobs j ON j.id = s.job_id WHERE j.fingerprint = $1 ORDER BY s.id`,
		discovery.Fingerprint(src, id))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatal(err)
		}
		out = append(out, c)
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestCrawl(t *testing.T) {
	pool := dbtest.AppPool(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	svc := discovery.NewService(pgstore.New(pool)).WithClock(func() time.Time { return now })

	acme := &source{name: "greenhouse:acme", batch: discovery.Batch{Complete: true, Postings: []discovery.Posting{
		posting("greenhouse:acme", "1", "ACME GmbH", "Backend Engineer (m/w/d)", "Berlin"),
		posting("greenhouse:acme", "2", "ACME GmbH", "Site Reliability Engineer", "Hamburg"),
	}}}
	board := &source{name: "arbeitnow", batch: discovery.Batch{Postings: []discovery.Posting{
		posting("arbeitnow", "a1", "Acme", "Backend Engineer", "Berlin"),         // copy of acme 1
		posting("arbeitnow", "a2", "Globex AG", "Data Engineer (f/m/d)", "Köln"), // only here
		posting("arbeitnow", "a3", "Initech", "Platform Engineer", "München"),
		posting("arbeitnow", "a4", "Initech", "Platform Engineer", "München"), // reposted under a new ID
		{Source: "arbeitnow", ExternalID: "bad"},                              // invalid
	}}}

	// Crawl 1: new jobs; job-board copies of company jobs are duplicates.
	rep, err := svc.Crawl(ctx, []discovery.Source{board, acme})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Stats.Changes[discovery.ChangeNew] != 4 || rep.Stats.Duplicates != 2 || rep.Stats.Invalid != 1 || rep.Stats.Fetched != 7 {
		t.Fatalf("crawl 1 stats = %+v", rep.Stats)
	}
	if _, ok := load(t, pool, "arbeitnow", "a1"); ok {
		t.Fatal("job-board copy stored")
	}

	// Crawl 2: the company board of Globex appears and replaces the job-board entry; acme drops job 1 and renames
	// job 2; a failing source changes nothing.
	acme.batch.Postings = []discovery.Posting{
		posting("greenhouse:acme", "2", "ACME GmbH", "Senior Site Reliability Engineer", "Hamburg"),
	}
	globex := &source{name: "greenhouse:globex", batch: discovery.Batch{Complete: true, Postings: []discovery.Posting{
		posting("greenhouse:globex", "g1", "Globex", "Data Engineer", "Köln"),
	}}}
	globex.batch.Postings[0].ApplyURL = ""
	broken := &source{name: "lever:umbrella", err: &discovery.SourceError{Source: "lever:umbrella", Code: "http_503"}}
	now = now.Add(time.Hour)
	rep, err = svc.Crawl(ctx, []discovery.Source{acme, board, globex, broken})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Errors["lever:umbrella"] != "http_503" {
		t.Fatalf("errors = %v", rep.Errors)
	}
	if rep.Stats.Changes[discovery.ChangeMerged] != 1 || rep.Stats.Changes[discovery.ChangeUpdated] != 1 || rep.Stats.Closed != 1 {
		t.Fatalf("crawl 2 stats = %+v", rep.Stats)
	}
	g := mustLoad(t, pool, "greenhouse:globex", "g1")
	if g.Source != "greenhouse:globex" || g.Status != "open" || g.ApplyURL != "" ||
		!equal(g.AltURLs, []string{"https://jobs.example.org/arbeitnow/a2"}) {
		t.Fatalf("merged job = %+v", g)
	}
	if got := changes(t, pool, "greenhouse:globex", "g1"); !equal(got, []string{"new", "merged"}) {
		t.Fatalf("globex snapshots = %v", got)
	}
	if j := mustLoad(t, pool, "greenhouse:acme", "1"); j.Status != "closed" {
		t.Fatalf("acme 1 = %+v, want closed (complete source)", j)
	}
	if j := mustLoad(t, pool, "greenhouse:acme", "2"); j.Title != "Senior Site Reliability Engineer" {
		t.Fatalf("acme 2 = %+v", j)
	}

	// Crawl 3: a capped source closes unlisted jobs only after StaleAfter.
	board.batch.Postings = nil
	now = now.Add(24 * time.Hour)
	if _, err := svc.Crawl(ctx, []discovery.Source{board}); err != nil {
		t.Fatal(err)
	}
	if j := mustLoad(t, pool, "arbeitnow", "a3"); j.Status != "open" {
		t.Fatalf("a3 closed after one day: %+v", j)
	}
	now = now.Add(discovery.StaleAfter)
	rep, err = svc.Crawl(ctx, []discovery.Source{board})
	if err != nil {
		t.Fatal(err)
	}
	if j := mustLoad(t, pool, "arbeitnow", "a3"); j.Status != "closed" || rep.Stats.Closed != 1 {
		t.Fatalf("a3 = %+v, closed = %d", j, rep.Stats.Closed)
	}

	// Crawl 4: a closed job that comes back is reopened; an unchanged one records nothing.
	acme.batch.Postings = append(acme.batch.Postings, posting("greenhouse:acme", "1", "ACME GmbH", "Backend Engineer (m/w/d)", "Berlin"))
	now = now.Add(time.Hour)
	rep, err = svc.Crawl(ctx, []discovery.Source{acme})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Stats.Changes[discovery.ChangeReopened] != 1 || rep.Stats.Changes[discovery.ChangeUnchanged] != 1 {
		t.Fatalf("crawl 4 stats = %+v", rep.Stats)
	}
	if got := changes(t, pool, "greenhouse:acme", "1"); !equal(got, []string{"new", "closed", "reopened"}) {
		t.Fatalf("acme 1 snapshots = %v", got)
	}
	if got := changes(t, pool, "greenhouse:acme", "2"); !equal(got, []string{"new", "updated"}) {
		t.Fatalf("acme 2 snapshots = %v", got)
	}

	var stats, errs []byte
	if err := pool.QueryRow(ctx, "SELECT stats, errors FROM crawl_runs WHERE id = $1", rep.RunID).Scan(&stats, &errs); err != nil {
		t.Fatal(err)
	}
	var st discovery.Stats
	if err := json.Unmarshal(stats, &st); err != nil || st.Changes[discovery.ChangeReopened] != 1 || string(errs) != "{}" {
		t.Fatalf("run = %s %s (%v)", stats, errs, err)
	}
}

type detailed struct {
	source
	calls atomic.Int32
}

func (d *detailed) Details(_ context.Context, p *discovery.Posting) error {
	d.calls.Add(1)
	if p.ExternalID == "broken" {
		return errors.New("http_500")
	}
	p.Description = "Details for " + p.Title
	return nil
}

func TestCrawlLoadsDetailsOnce(t *testing.T) {
	pool := dbtest.AppPool(t)
	ctx := context.Background()
	svc := discovery.NewService(pgstore.New(pool))
	list := func() []discovery.Posting {
		out := []discovery.Posting{
			posting("arbeitsagentur", "10001", "Musterfirma GmbH", "Go Developer", "Berlin, Deutschland"),
			posting("arbeitsagentur", "broken", "Beispiel AG", "Data Analyst", "Bonn, Deutschland"),
		}
		for i := range out {
			out[i].Description = ""
		}
		return out
	}
	src := &detailed{source: source{name: "arbeitsagentur", batch: discovery.Batch{Postings: list()}}}

	rep, err := svc.Crawl(ctx, []discovery.Source{src})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Stats.Details != 1 || rep.Stats.DetailsFailed != 1 || src.calls.Load() != 2 {
		t.Fatalf("crawl 1 stats = %+v, calls = %d", rep.Stats, src.calls.Load())
	}

	src.batch.Postings = list()
	src.calls.Store(0)
	rep, err = svc.Crawl(ctx, []discovery.Source{src})
	if err != nil {
		t.Fatal(err)
	}
	// Only the job without a stored description is asked again; the described one stays unchanged.
	if src.calls.Load() != 1 || rep.Stats.Changes[discovery.ChangeUnchanged] != 2 {
		t.Fatalf("crawl 2 stats = %+v, calls = %d", rep.Stats, src.calls.Load())
	}
	var desc string
	if err := pool.QueryRow(ctx, "SELECT description FROM jobs WHERE fingerprint = $1",
		discovery.Fingerprint("arbeitsagentur", "10001")).Scan(&desc); err != nil || desc != "Details for Go Developer" {
		t.Fatalf("description = %q (%v)", desc, err)
	}
}

func TestCrawlCanceled(t *testing.T) {
	pool := dbtest.AppPool(t)
	ctx, cancel := context.WithCancel(context.Background())
	svc := discovery.NewService(pgstore.New(pool))
	src := &source{name: "greenhouse:acme", batch: discovery.Batch{Complete: true, Postings: []discovery.Posting{
		posting("greenhouse:acme", "1", "ACME GmbH", "Backend Engineer", "Berlin"),
	}}}
	cancel()
	if _, err := svc.Crawl(ctx, []discovery.Source{src}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want canceled", err)
	}
}

func TestImportBoards(t *testing.T) {
	pool := dbtest.AppPool(t)
	ctx := context.Background()
	st := pgstore.New(pool)
	boards := []discovery.Board{
		{ATS: "greenhouse", Slug: "acme", Company: "ACME"},
		{ATS: "eightfold", Slug: "example.org", Company: "Example", Config: []byte(`{"host":"careers.example.org","api":"v2"}`)},
	}
	if in, up, err := st.ImportBoards(ctx, boards); err != nil || in != 2 || up != 0 {
		t.Fatalf("first import: %d added, %d updated, %v", in, up, err)
	}
	if _, err := pool.Exec(ctx, "UPDATE boards SET enabled = false WHERE slug = 'acme'"); err != nil {
		t.Fatal(err)
	}
	boards[1].Company = "Example Streaming"
	if in, up, err := st.ImportBoards(ctx, boards); err != nil || in != 0 || up != 2 {
		t.Fatalf("second import: %d added, %d updated, %v", in, up, err)
	}
	got, err := st.EnabledBoards(ctx)
	if err != nil || len(got) != 1 || got[0].Company != "Example Streaming" || string(got[0].Config) == "{}" {
		t.Fatalf("enabled = %+v, %v (a disabled board stays disabled)", got, err)
	}
	if _, _, err := st.ImportBoards(ctx, []discovery.Board{{ATS: "taleo", Slug: "x", Company: "X"}}); err == nil {
		t.Fatal("unknown system accepted")
	}
}
