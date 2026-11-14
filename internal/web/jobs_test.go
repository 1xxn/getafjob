package web_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/1xxn/getafjob/internal/discovery"
	discoverystore "github.com/1xxn/getafjob/internal/discovery/adapters/pgstore"
	"github.com/1xxn/getafjob/internal/matching"
	matchingstore "github.com/1xxn/getafjob/internal/matching/adapters/pgstore"
	"github.com/1xxn/getafjob/internal/platform/db"
)

type criteria map[uuid.UUID]matching.Criteria

func (c criteria) Users(context.Context) ([]uuid.UUID, error) {
	var out []uuid.UUID
	for id := range c {
		out = append(out, id)
	}
	return out, nil
}

func (c criteria) Criteria(_ context.Context, id uuid.UUID) (matching.Criteria, error) {
	return c[id], nil
}

func TestJobPages(t *testing.T) {
	a := newApp(t)
	ctx := t.Context()
	if rec := a.do(http.MethodGet, "/jobs", nil, "", nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("jobs without session: %d", rec.Code)
	}
	session := signedIn(t, a)
	if body := a.do(http.MethodGet, "/jobs", nil, session, nil).Body.String(); !strings.Contains(body, "No results yet.") {
		t.Fatal("no pending notice")
	}

	ds := discoverystore.New(a.pool)
	now := time.Now()
	run, err := ds.StartRun(ctx, []string{}, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []discovery.Posting{
		{Source: "arbeitnow", ExternalID: "1", Company: "Acme GmbH", Title: "Go Developer", Location: "Berlin",
			URL: "https://example.org/1", Description: "We use Go and <b>PostgreSQL</b> with our team."},
		{Source: "arbeitnow", ExternalID: "2", Company: "Muster AG", Title: "Sales Manager", Location: "Berlin",
			URL: "javascript:alert(1)", Description: "Sales."},
	} {
		if _, _, err := ds.Upsert(ctx, run, p, now); err != nil {
			t.Fatal(err)
		}
	}
	var max uuid.UUID
	var goID, salesID int64
	if err := db.InSystemTx(ctx, a.pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, "SELECT id FROM users WHERE email = 'max.mustermann@example.com'").Scan(&max); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, "SELECT id FROM jobs WHERE title = 'Go Developer'").Scan(&goID); err != nil {
			return err
		}
		return tx.QueryRow(ctx, "SELECT id FROM jobs WHERE title = 'Sales Manager'").Scan(&salesID)
	}); err != nil {
		t.Fatal(err)
	}
	ms := matchingstore.New(a.pool)
	c := criteria{max: {Titles: []string{"Go Developer"}, Locations: []string{"Berlin"}, Skills: []string{"Go", "PostgreSQL"},
		Languages: []matching.Language{{Name: "English", Level: "C1"}}}}
	if _, err := matching.NewService(c, ms, ms).Run(ctx, nil); err != nil {
		t.Fatal(err)
	}

	rec := a.do(http.MethodGet, "/jobs", nil, session, nil)
	checkNonces(t, rec)
	list := rec.Body.String()
	if !strings.Contains(list, `hx-nonce="`) || !strings.Contains(list, `name="htmx-config"`) {
		t.Fatal("list without htmx")
	}
	if !strings.Contains(list, `href="/jobs?job=`) || !strings.Contains(list, "Go Developer") || strings.Contains(list, "Sales Manager") {
		t.Fatalf("default list:\n%s", list)
	}
	if body := a.do(http.MethodGet, "/jobs?decision=skip", nil, session, nil).Body.String(); !strings.Contains(body, "Sales Manager") {
		t.Fatal("skip filter")
	}
	if body := a.do(http.MethodGet, "/jobs?q=postgresql&decision=all", nil, session, nil).Body.String(); strings.Contains(body, "Sales Manager") ||
		!strings.Contains(body, "Go Developer") || !strings.Contains(body, `value="postgresql"`) {
		t.Fatal("search")
	}
	if rec := a.do(http.MethodGet, "/jobs?q="+strings.Repeat("%22", 300)+"&page=-4&workplace=x", nil, session, nil); rec.Code != http.StatusOK {
		t.Fatalf("odd input: %d", rec.Code)
	}

	// htmx gets only the results; a history restore gets the whole page.
	part := a.do(http.MethodGet, "/jobs?page=1", nil, session, map[string]string{"HX-Request": "true", "HX-Request-Type": "partial"})
	checkNonces(t, part)
	if body := part.Body.String(); !strings.HasPrefix(strings.TrimSpace(body), `<div id="results"`) || strings.Contains(body, "<html") ||
		!strings.Contains(part.Header().Get("Vary"), "HX-Request") {
		t.Fatalf("partial: %s", body)
	}
	// A filter change also hands the save search dialog the new filters (it lives outside #results).
	filtered := a.do(http.MethodGet, "/jobs?q=Go&workplace=remote", nil, session, map[string]string{"HX-Request": "true", "HX-Request-Type": "partial"}).Body.String()
	if !strings.Contains(filtered, `id="save-search-query"`) || !strings.Contains(filtered, `hx-swap-oob="true"`) ||
		!strings.Contains(filtered, "q=Go") || !strings.Contains(filtered, "workplace=remote") {
		t.Fatalf("filters for the save search dialog missing: %s", filtered)
	}
	restore := a.do(http.MethodGet, "/jobs", nil, session, map[string]string{"HX-Request": "true", "HX-History-Restore-Request": "true"})
	if !strings.Contains(restore.Body.String(), "<html") {
		t.Fatal("history restore got a fragment")
	}

	// The job beside the list: as a fragment, and rendered into the page for ?job= (back button, shared links).
	goStr := strconv.FormatInt(goID, 10)
	if panel := a.do(http.MethodGet, "/jobs/"+goStr+"/panel?job="+goStr, nil, session, nil).Body.String(); !strings.Contains(panel, `id="job-panel-title"`) ||
		!strings.Contains(panel, "Go Developer") || !strings.Contains(panel, `href="/jobs"`) {
		t.Fatalf("panel:\n%s", panel)
	}
	// The panel shows five reasons; the hard one that skipped a job is among them although it carries no points.
	salesStr := strconv.FormatInt(salesID, 10)
	if panel := a.do(http.MethodGet, "/jobs/"+salesStr+"/panel?job="+salesStr, nil, session, nil).Body.String(); !strings.Contains(panel,
		"The title matches none of your wanted titles.") {
		t.Fatalf("panel of a skipped job lacks its hard reason:\n%s", panel)
	}
	if page := a.do(http.MethodGet, "/jobs?q=go&job="+goStr, nil, session, nil).Body.String(); !strings.Contains(page, `id="job-panel-title"`) ||
		!strings.Contains(page, `<a href="/jobs" class="jobs-chip"`) {
		t.Fatal("page with an open job and a removable search chip")
	}

	// Bulk actions: save, then skip; the list shows the state, the flash says what happened.
	bulk := func(action string, ids ...int64) *httptest.ResponseRecorder {
		form := url.Values{"action": {action}, "back": {"/jobs?decision=all"}}
		for _, id := range ids {
			form.Add("ids", strconv.FormatInt(id, 10))
		}
		return a.do(http.MethodPost, "/jobs/bulk", form, session, nil)
	}
	if rec := bulk("save", goID); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/jobs?decision=all" ||
		!strings.Contains(rec.Header().Get("Set-Cookie"), "flash=flash.jobs.save") {
		t.Fatalf("save: %d %q %q", rec.Code, rec.Header().Get("Location"), rec.Header().Get("Set-Cookie"))
	}
	if body := a.do(http.MethodGet, "/jobs?decision=all", nil, session, nil).Body.String(); !strings.Contains(body, `ui-tone-neutral">Saved<`) {
		t.Fatal("saved state not shown")
	}
	if rec := bulk("skip", goID, salesID); !strings.Contains(rec.Header().Get("Set-Cookie"), "flash=flash.jobs.skip") {
		t.Fatalf("skip: %q", rec.Header().Get("Set-Cookie"))
	}
	if body := a.do(http.MethodGet, "/jobs?decision=all", nil, session, nil).Body.String(); strings.Count(body, `ui-tone-neutral">Skipped<`) != 2 {
		t.Fatal("skipped state not shown for both")
	}
	if rec := bulk("launch", goID); rec.Header().Get("Set-Cookie") != "" {
		t.Fatal("unknown action did something")
	}
	if rec := a.do(http.MethodPost, "/jobs/bulk", url.Values{"action": {"save"}, "ids": {strconv.FormatInt(goID, 10)},
		"back": {"https://evil.example/"}}, session, nil); rec.Header().Get("Location") != "/jobs" {
		t.Fatalf("foreign back: %q", rec.Header().Get("Location"))
	}

	drec := a.do(http.MethodGet, "/jobs/"+strconv.FormatInt(goID, 10), nil, session, nil)
	checkNonces(t, drec)
	detail := drec.Body.String()
	for _, want := range []string{`The title matches &#34;Go Developer&#34;.`, `href="https://example.org/1"`, "&lt;b&gt;PostgreSQL",
		"The posting is in English, a language you work in."} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail lacks %q", want)
		}
	}
	if strings.Contains(detail, "match.reason.") {
		t.Error("untranslated reason")
	}
	if sales := a.do(http.MethodGet, "/jobs/"+strconv.FormatInt(salesID, 10), nil, session, nil).Body.String(); strings.Contains(sales, "javascript:") {
		t.Error("unsafe link rendered")
	}
	for _, path := range []string{"/jobs/0", "/jobs/abc", "/jobs/999999"} {
		if rec := a.do(http.MethodGet, path, nil, session, nil); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", path, rec.Code)
		}
	}

	// Filter pills: location, workplace, source, min_salary, freshness, sort
	if body := a.do(http.MethodGet, "/jobs?location=Berlin", nil, session, nil).Body.String(); !strings.Contains(body, "Go Developer") {
		t.Fatal("filter by location Berlin failed")
	}
	if body := a.do(http.MethodGet, "/jobs?location=Munich", nil, session, nil).Body.String(); !strings.Contains(body, "No jobs fit this filter.") {
		t.Fatal("filter by location Munich should have 0 results")
	}
	if body := a.do(http.MethodGet, "/jobs?freshness=24h&sort=newest", nil, session, nil).Body.String(); !strings.Contains(body, "Go Developer") {
		t.Fatal("filter by freshness and sort failed")
	}

	// Employer & keyword blocklist: add Acme GmbH to blocklist
	blkRec := a.do(http.MethodPost, "/jobs/blocklist", url.Values{"kind": {"company"}, "pattern": {"Acme GmbH"}}, session, nil)
	if blkRec.Code != http.StatusSeeOther {
		t.Fatalf("add blocklist: expected redirect, got %d", blkRec.Code)
	}

	// Now Acme GmbH ("Go Developer") must be hidden from normal discovery!
	normList := a.do(http.MethodGet, "/jobs", nil, session, nil).Body.String()
	if strings.Contains(normList, "Go Developer") {
		t.Fatal("blocked company Acme GmbH still visible in discovery list")
	}
	if !strings.Contains(normList, "Blocked") {
		t.Fatal("expected Blocked indicator in discovery counts")
	}

	// Blocked tab displays blocked job
	blkList := a.do(http.MethodGet, "/jobs?decision=blocked", nil, session, nil).Body.String()
	if !strings.Contains(blkList, "Go Developer") || !strings.Contains(blkList, `ui-tone-danger">Blocked<`) {
		t.Fatal("blocked job not displayed in decision=blocked view")
	}

	// Unblock via delete
	delRec := a.do(http.MethodPost, "/jobs/blocklist/delete", url.Values{"kind": {"company"}, "pattern": {"Acme GmbH"}}, session, nil)
	if delRec.Code != http.StatusSeeOther {
		t.Fatalf("delete blocklist: expected redirect, got %d", delRec.Code)
	}

	// Acme GmbH visible again
	unblkList := a.do(http.MethodGet, "/jobs", nil, session, nil).Body.String()
	if !strings.Contains(unblkList, "Go Developer") {
		t.Fatal("unblocked company Acme GmbH not visible in discovery list")
	}

	// HTMX partial addition to blocklist
	htmxRec := a.do(http.MethodPost, "/jobs/blocklist", url.Values{"kind": {"keyword"}, "pattern": {"PostgreSQL"}}, session, map[string]string{"HX-Request": "true"})
	if htmxRec.Code != http.StatusOK || !strings.Contains(htmxRec.Body.String(), `id="results"`) {
		t.Fatalf("htmx blocklist response: %d, body: %s", htmxRec.Code, htmxRec.Body.String())
	}
}

func TestJobIngestionAndCrawlControls(t *testing.T) {
	a := newApp(t)
	session := signedIn(t, a)

	// 1. Ingest job from HTTP URL
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!DOCTYPE html>
<html>
<head>
  <title>Distributed Systems Engineer - Acme Cloud Corp</title>
  <script type="application/ld+json">
  {
    "@context": "https://schema.org/",
    "@type": "JobPosting",
    "title": "Distributed Systems Engineer",
    "description": "<p>Design distributed consensus algorithms in Go.</p>",
    "identifier": {
      "@type": "PropertyValue",
      "value": "acme-cloud-99"
    },
    "hiringOrganization": {
      "@type": "Organization",
      "name": "Acme Cloud Corp"
    },
    "jobLocation": {
      "@type": "Place",
      "address": {
        "@type": "PostalAddress",
        "addressLocality": "Berlin"
      }
    },
    "baseSalary": {
      "@type": "MonetaryAmount",
      "currency": "EUR",
      "value": {
        "@type": "QuantitativeValue",
        "minValue": 85000,
        "maxValue": 105000,
        "unitText": "YEAR"
      }
    }
  }
  </script>
</head>
<body>
  <h1>Distributed Systems Engineer</h1>
</body>
</html>`))
	}))
	defer ts.Close()

	// Import job via standard form POST
	importRec := a.do(http.MethodPost, "/jobs/import", url.Values{"url": {ts.URL + "/jobs/99"}}, session, nil)
	if importRec.Code != http.StatusSeeOther {
		t.Fatalf("job import redirect code: got %d, want %d", importRec.Code, http.StatusSeeOther)
	}
	loc := importRec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/jobs/") {
		t.Fatalf("job import Location = %q, want /jobs/{id}", loc)
	}

	// Verify job detail page
	detailRec := a.do(http.MethodGet, loc, nil, session, nil)
	if detailRec.Code != http.StatusOK {
		t.Fatalf("job detail page: got %d, want 200", detailRec.Code)
	}
	detailBody := detailRec.Body.String()
	if !strings.Contains(detailBody, "Distributed Systems Engineer") {
		t.Errorf("detail body missing job title")
	}
	if !strings.Contains(detailBody, "Acme Cloud Corp") {
		t.Errorf("detail body missing company name")
	}

	// Import job via HTMX
	htmxImport := a.do(http.MethodPost, "/jobs/import", url.Values{"url": {ts.URL + "/jobs/99"}}, session, map[string]string{"HX-Request": "true"})
	if htmxImport.Code != http.StatusOK || htmxImport.Header().Get("HX-Location") == "" {
		t.Fatalf("htmx job import failed: code %d, HX-Location %q", htmxImport.Code, htmxImport.Header().Get("HX-Location"))
	}

	// 2. Trigger crawl
	crawlRec := a.do(http.MethodPost, "/jobs/crawl", nil, session, map[string]string{"HX-Request": "true"})
	if crawlRec.Code != http.StatusOK {
		t.Fatalf("trigger crawl code: got %d, want 200", crawlRec.Code)
	}
	crawlBody := crawlRec.Body.String()
	if !strings.Contains(crawlBody, `id="crawl-status"`) {
		t.Errorf("trigger crawl body missing #crawl-status: %s", crawlBody)
	}

	// Crawl status polling
	statusRec := a.do(http.MethodGet, "/jobs/crawl/status", nil, session, nil)
	if statusRec.Code != http.StatusOK || !strings.Contains(statusRec.Body.String(), `id="crawl-status"`) {
		t.Fatalf("crawl status polling failed: code %d, body %s", statusRec.Code, statusRec.Body.String())
	}

	// 3. Add Career Site
	boardRec := a.do(http.MethodPost, "/jobs/boards", url.Values{"url": {"https://jobs.lever.co/syntheticcorp"}}, session, map[string]string{"HX-Request": "true"})
	if boardRec.Code != http.StatusOK {
		t.Fatalf("add board code: got %d, want 200", boardRec.Code)
	}
	boardBody := boardRec.Body.String()
	if !strings.Contains(boardBody, "Syntheticcorp") || !strings.Contains(boardBody, "lever") {
		t.Errorf("add board response missing company or ats: %s", boardBody)
	}
}

func TestFitInspectorAndCVVariantSwitcher(t *testing.T) {
	a := newApp(t)
	ctx := t.Context()
	session := signedIn(t, a)

	// 1. Upload 2 CV variants
	pdf := []byte("%PDF-1.7\nCV of Max Mustermann")
	if rec := a.upload("cv", "cv-backend.pdf", pdf, session); rec.Code != http.StatusSeeOther {
		t.Fatalf("upload backend cv: %d", rec.Code)
	}
	docPage := a.do(http.MethodGet, "/documents", nil, session, nil).Body.String()
	idx1 := strings.Index(docPage, `href="/documents/`)
	if idx1 < 0 {
		t.Fatal("no backend cv found")
	}
	cv1ID := docPage[idx1+len(`href="/documents/`) : idx1+len(`href="/documents/`)+36]
	if rec := a.do(http.MethodPost, "/documents/"+cv1ID+"/variant", url.Values{
		"label-" + cv1ID:    {"Backend Specialist"},
		"language-" + cv1ID: {"en"},
		"focus-" + cv1ID:    {"golang, postgresql"},
	}, session, nil); rec.Code != http.StatusOK || rec.Header().Get("HX-Replace-Url") != "/documents" {
		t.Fatalf("set variant 1: %d, address %q", rec.Code, rec.Header().Get("HX-Replace-Url"))
	}

	if rec := a.upload("cv", "cv-frontend.pdf", pdf, session); rec.Code != http.StatusSeeOther {
		t.Fatalf("upload frontend cv: %d", rec.Code)
	}
	docPage2 := a.do(http.MethodGet, "/documents", nil, session, nil).Body.String()
	idx2 := strings.LastIndex(docPage2, `href="/documents/`)
	if idx2 < 0 || idx2 == idx1 {
		t.Fatal("no frontend cv found")
	}
	cv2ID := docPage2[idx2+len(`href="/documents/`) : idx2+len(`href="/documents/`)+36]
	if rec := a.do(http.MethodPost, "/documents/"+cv2ID+"/variant", url.Values{
		"label-" + cv2ID:    {"Frontend Specialist"},
		"language-" + cv2ID: {"en"},
		"focus-" + cv2ID:    {"react, typescript"},
	}, session, nil); rec.Code != http.StatusOK {
		t.Fatalf("set variant 2: %d", rec.Code)
	}

	// 2. Set profile initial skills: Go
	profForm := url.Values{
		"version":    {"0"},
		"first_name": {"Max"},
		"last_name":  {"Mustermann"},
		"email":      {"max.mustermann@example.com"},
		"skills":     {"Go"},
		"languages":  {"English: C1"},
	}
	if rec := a.do(http.MethodPost, "/profile", profForm, session, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("save profile: %d", rec.Code)
	}

	// 3. Insert job requiring Go, PostgreSQL, Docker
	ds := discoverystore.New(a.pool)
	now := time.Now()
	run, err := ds.StartRun(ctx, []string{}, now)
	if err != nil {
		t.Fatal(err)
	}
	p := discovery.Posting{
		Source:      "arbeitnow",
		ExternalID:  "100",
		Company:     "Acme Backend Tech",
		Title:       "Senior Go Developer",
		Location:    "Berlin",
		Workplace:   "remote",
		URL:         "https://example.org/jobs/100",
		Description: "We are seeking a Senior Go Developer. You will build distributed systems using Go, PostgreSQL, and Docker.",
	}
	if _, _, err := ds.Upsert(ctx, run, p, now); err != nil {
		t.Fatal(err)
	}

	var maxID uuid.UUID
	var jobID int64
	if err := db.InSystemTx(ctx, a.pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, "SELECT id FROM users WHERE email = 'max.mustermann@example.com'").Scan(&maxID); err != nil {
			return err
		}
		return tx.QueryRow(ctx, "SELECT id FROM jobs WHERE external_id = '100'").Scan(&jobID)
	}); err != nil {
		t.Fatal(err)
	}

	ms := matchingstore.New(a.pool)
	crit := criteria{maxID: {
		Titles:     []string{"Go Developer"},
		Locations:  []string{"Berlin"},
		Skills:     []string{"Go"},
		Languages:  []matching.Language{{Name: "English", Level: "C1"}},
		Remote:     true,
		YearsTotal: "5+",
	}}
	if _, err := matching.NewService(crit, ms, ms).Run(ctx, nil); err != nil {
		t.Fatal(err)
	}

	// 4. GET /jobs/{id}
	jobPath := "/jobs/" + strconv.FormatInt(jobID, 10)
	detailRec := a.do(http.MethodGet, jobPath, nil, session, nil)
	if detailRec.Code != http.StatusOK {
		t.Fatalf("job detail %d", detailRec.Code)
	}
	body := detailRec.Body.String()

	// Check Fit Inspector
	if !strings.Contains(body, `id="fit-inspector"`) {
		t.Fatal("fit inspector missing")
	}
	if !strings.Contains(body, "Matching skills") || !strings.Contains(body, "Go") {
		t.Fatalf("matching skills missing in body:\n%s", body)
	}
	if !strings.Contains(body, "Missing keywords &amp; skills") {
		t.Fatalf("missing skills title not found")
	}
	if !strings.Contains(body, "PostgreSQL") || !strings.Contains(body, "Docker") {
		t.Fatalf("missing skills PostgreSQL / Docker not found")
	}
	if !strings.Contains(body, "Score breakdown") {
		t.Fatal("score breakdown title not found")
	}
	if !strings.Contains(body, "Skills overlap") || !strings.Contains(body, "Seniority level") {
		t.Fatal("breakdown dimensions missing")
	}

	// Check CV Variant Switcher
	if !strings.Contains(body, `id="cv-switcher"`) {
		t.Fatal("cv switcher missing")
	}
	if !strings.Contains(body, "Backend Specialist") || !strings.Contains(body, "Frontend Specialist") {
		t.Fatal("cv variants not listed")
	}
	if !strings.Contains(body, "Recommended") {
		t.Fatal("recommended badge missing")
	}

	// 5. 1-Click Skill Addition: POST /jobs/{id}/skills (HTMX partial)
	addSkillRec := a.do(http.MethodPost, jobPath+"/skills", url.Values{"skill": {"Docker"}}, session, map[string]string{
		"HX-Request": "true",
	})
	if addSkillRec.Code != http.StatusOK {
		t.Fatalf("add skill status: %d", addSkillRec.Code)
	}
	addSkillBody := addSkillRec.Body.String()
	if !strings.Contains(addSkillBody, `id="fit-inspector"`) {
		t.Fatal("add skill response missing fit-inspector")
	}
	if !strings.Contains(addSkillBody, "Added &#34;Docker&#34; to your profile") {
		t.Fatalf("add skill success notice missing in body:\n%s", addSkillBody)
	}
	if !strings.Contains(addSkillBody, `ui-tone-success">Docker<`) {
		t.Fatalf("Docker not in matching skills badge list:\n%s", addSkillBody)
	}

	// 6. CV Variant Switcher: POST /jobs/{id}/cv (HTMX partial)
	setCVRec := a.do(http.MethodPost, jobPath+"/cv", url.Values{"cv_id": {cv2ID}}, session, map[string]string{
		"HX-Request": "true",
	})
	if setCVRec.Code != http.StatusOK {
		t.Fatalf("set cv status: %d", setCVRec.Code)
	}
	setCVBody := setCVRec.Body.String()
	if !strings.Contains(setCVBody, `id="cv-switcher"`) {
		t.Fatal("set cv response missing cv-switcher")
	}
	if !strings.Contains(setCVBody, "CV variant choice saved.") {
		t.Fatalf("set cv success notice missing in body:\n%s", setCVBody)
	}

	// Verify application record in database has selected cv_document_id == cv2ID
	var storedCvID *uuid.UUID
	if err := db.InSystemTx(ctx, a.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT cv_document_id FROM applications WHERE user_id = $1 AND job_id = $2", maxID, jobID).Scan(&storedCvID)
	}); err != nil {
		t.Fatal(err)
	}
	if storedCvID == nil || storedCvID.String() != cv2ID {
		t.Fatalf("stored cv_document_id = %v, want %s", storedCvID, cv2ID)
	}

	// 7. Non-HTMX fallback for /jobs/{id}/skills and /jobs/{id}/cv redirects to /jobs/{id}
	redirectRec := a.do(http.MethodPost, jobPath+"/skills", url.Values{"skill": {"Kubernetes"}}, session, nil)
	if redirectRec.Code != http.StatusSeeOther || redirectRec.Header().Get("Location") != jobPath {
		t.Fatalf("non-htmx add skill redirect: %d, location: %s", redirectRec.Code, redirectRec.Header().Get("Location"))
	}
	redirectCVRec := a.do(http.MethodPost, jobPath+"/cv", url.Values{"cv_id": {cv1ID}}, session, nil)
	if redirectCVRec.Code != http.StatusSeeOther || redirectCVRec.Header().Get("Location") != jobPath {
		t.Fatalf("non-htmx set cv redirect: %d, location: %s", redirectCVRec.Code, redirectCVRec.Header().Get("Location"))
	}
}

func TestJobApply_PreviewAndSubmit(t *testing.T) {
	a := newApp(t)
	ctx := t.Context()

	// 1. Unauthenticated access redirects to /login
	if rec := a.do(http.MethodGet, "/jobs/1/apply", nil, "", nil); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Fatalf("get apply unauthenticated: %d, location: %s", rec.Code, rec.Header().Get("Location"))
	}
	if rec := a.do(http.MethodPost, "/jobs/1/apply", nil, "", nil); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Fatalf("post apply unauthenticated: %d, location: %s", rec.Code, rec.Header().Get("Location"))
	}

	session := signedIn(t, a)

	// 2. Invalid job IDs return 404
	for _, path := range []string{"/jobs/0/apply", "/jobs/-1/apply", "/jobs/999999/apply"} {
		if rec := a.do(http.MethodGet, path, nil, session, nil); rec.Code != http.StatusNotFound {
			t.Errorf("get %s: want 404, got %d", path, rec.Code)
		}
		if rec := a.do(http.MethodPost, path, nil, session, nil); rec.Code != http.StatusNotFound {
			t.Errorf("post %s: want 404, got %d", path, rec.Code)
		}
	}

	// 3. Setup user profile data:
	// a) Profile contact
	profForm := url.Values{
		"version":    {"0"},
		"first_name": {"Max"},
		"last_name":  {"Mustermann"},
		"email":      {"max.mustermann@example.com"},
		"phone":      {"+49 170 1234567"},
		"city":       {"Berlin"},
		"country":    {"Germany"},
		"linkedin":   {"linkedin.com/in/maxmustermann"},
		"github":     {"github.com/maxmustermann"},
		"skills":     {"Go", "PostgreSQL"},
		"languages":  {"English: C1"},
	}
	if rec := a.do(http.MethodPost, "/profile", profForm, session, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("save profile: %d", rec.Code)
	}

	// b) Add Work Experience
	addExpRec := a.do(http.MethodPost, "/profile/experiences", url.Values{
		"title":          {"Senior Go Engineer"},
		"company":        {"Acme Software GmbH"},
		"location":       {"Berlin"},
		"workplace_type": {"remote"},
		"start_date":     {"2021-01"},
		"is_current":     {"on"},
		"bullets":        {"Built distributed indexing pipelines in Go\nOptimized PostgreSQL queries"},
	}, session, nil)
	if addExpRec.Code != http.StatusSeeOther {
		t.Fatalf("add experience: %d", addExpRec.Code)
	}

	// c) Add Education
	addEduRec := a.do(http.MethodPost, "/profile/educations", url.Values{
		"institution":    {"TU Berlin"},
		"degree":         {"Master of Science"},
		"field_of_study": {"Computer Science"},
		"start_date":     {"2015-10"},
		"end_date":       {"2019-09"},
		"grade":          {"1.3"},
	}, session, nil)
	if addEduRec.Code != http.StatusSeeOther {
		t.Fatalf("add education: %d", addEduRec.Code)
	}

	// d) Add Answer Bank Item
	saveAnsRec := a.do(http.MethodPost, "/answers", url.Values{
		"question": {"Earliest start date?"},
		"answer":   {"Within 2 weeks notice"},
	}, session, nil)
	if saveAnsRec.Code != http.StatusSeeOther {
		t.Fatalf("save answer: %d", saveAnsRec.Code)
	}

	// e) Upload CV variant
	pdf := []byte("%PDF-1.7\nCV of Max Mustermann")
	if rec := a.upload("cv", "cv-lead.pdf", pdf, session); rec.Code != http.StatusSeeOther {
		t.Fatalf("upload cv: %d", rec.Code)
	}
	docPage := a.do(http.MethodGet, "/documents", nil, session, nil).Body.String()
	idxCV := strings.Index(docPage, `href="/documents/`)
	if idxCV < 0 {
		t.Fatal("no cv document found")
	}
	cvDocID := docPage[idxCV+len(`href="/documents/`) : idxCV+len(`href="/documents/`)+36]
	if rec := a.do(http.MethodPost, "/documents/"+cvDocID+"/variant", url.Values{
		"label-" + cvDocID:    {"Lead Architect CV"},
		"language-" + cvDocID: {"en"},
		"focus-" + cvDocID:    {"distributed systems"},
	}, session, nil); rec.Code != http.StatusOK {
		t.Fatalf("set variant: %d", rec.Code)
	}

	// 4. Create Job Posting & Match Run
	ds := discoverystore.New(a.pool)
	now := time.Now()
	run, err := ds.StartRun(ctx, []string{}, now)
	if err != nil {
		t.Fatal(err)
	}
	p := discovery.Posting{
		Source:      "arbeitnow",
		ExternalID:  "apply-test-1",
		Company:     "Cloud Systems AG",
		Title:       "Senior Distributed Systems Engineer",
		Location:    "Berlin",
		Workplace:   "remote",
		URL:         "https://example.org/jobs/apply-1",
		Description: "Looking for an engineer experienced in Go and PostgreSQL.",
	}
	if _, _, err := ds.Upsert(ctx, run, p, now); err != nil {
		t.Fatal(err)
	}
	var maxID uuid.UUID
	var jobID int64
	if err := db.InSystemTx(ctx, a.pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, "SELECT id FROM users WHERE email = 'max.mustermann@example.com'").Scan(&maxID); err != nil {
			return err
		}
		return tx.QueryRow(ctx, "SELECT id FROM jobs WHERE external_id = 'apply-test-1'").Scan(&jobID)
	}); err != nil {
		t.Fatal(err)
	}
	ms := matchingstore.New(a.pool)
	crit := criteria{maxID: {
		Titles:    []string{"Distributed Systems Engineer"},
		Locations: []string{"Berlin"},
		Skills:    []string{"Go", "PostgreSQL"},
		Languages: []matching.Language{{Name: "English", Level: "C1"}},
		Remote:    true,
	}}
	if _, err := matching.NewService(crit, ms, ms).Run(ctx, nil); err != nil {
		t.Fatal(err)
	}

	jobApplyPath := "/jobs/" + strconv.FormatInt(jobID, 10) + "/apply"

	// 5. Test Full Page Preview (GET /jobs/{id}/apply)
	previewRec := a.do(http.MethodGet, jobApplyPath, nil, session, nil)
	if previewRec.Code != http.StatusOK {
		t.Fatalf("get apply preview: %d", previewRec.Code)
	}
	checkNonces(t, previewRec)
	previewHTML := previewRec.Body.String()
	for _, expected := range []string{
		"Instant apply preview",
		"Senior Distributed Systems Engineer",
		"Cloud Systems AG",
		"Max Mustermann",
		"max.mustermann@example.com",
		"+49 170 1234567",
		"Berlin, Germany",
		"linkedin.com/in/maxmustermann",
		"github.com/maxmustermann",
		"Lead Architect CV",
		"No cover letter from an AI, no made up facts",
		"Safety guards passed",
		"Senior Go Engineer",
		"Acme Software GmbH",
		"TU Berlin",
		"Computer Science",
		"Earliest start date?",
		"Within 2 weeks notice",
		"Confirm &amp; start application",
	} {
		if !strings.Contains(previewHTML, expected) {
			t.Errorf("preview HTML missing expected substring: %q", expected)
		}
	}

	// 6. Test HTMX Partial Preview (GET /jobs/{id}/apply with HX-Request: true)
	htmxRec := a.do(http.MethodGet, jobApplyPath, nil, session, map[string]string{
		"HX-Request": "true",
	})
	if htmxRec.Code != http.StatusOK {
		t.Fatalf("htmx preview: %d", htmxRec.Code)
	}
	htmxBody := htmxRec.Body.String()
	if !strings.Contains(htmxBody, `id="apply-preview"`) {
		t.Fatal("htmx preview missing #apply-preview element")
	}
	if strings.Contains(htmxBody, "<html") || strings.Contains(htmxBody, "<!DOCTYPE") {
		t.Fatal("htmx preview returned full HTML document instead of partial")
	}

	// 7. Instant Apply Execution (POST /jobs/{id}/apply)
	submitRec := a.do(http.MethodPost, jobApplyPath, url.Values{
		"cv_id": {cvDocID},
	}, session, nil)
	if submitRec.Code != http.StatusSeeOther {
		t.Fatalf("apply submit code: got %d, want %d", submitRec.Code, http.StatusSeeOther)
	}
	loc := submitRec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/live?run=") {
		t.Fatalf("apply submit redirect Location = %q, want /live?run={id}", loc)
	}
	appIDStr := strings.TrimPrefix(loc, "/live?run=")
	appID, err := uuid.Parse(appIDStr)
	if err != nil {
		t.Fatalf("invalid application UUID in Location: %v", err)
	}

	// Verify database record
	var appState string
	var appCvID *uuid.UUID
	if err := db.InSystemTx(ctx, a.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT state, cv_document_id FROM applications WHERE id = $1 AND user_id = $2 AND job_id = $3",
			appID, maxID, jobID).Scan(&appState, &appCvID)
	}); err != nil {
		t.Fatalf("query application record: %v", err)
	}
	if appState != "queued" {
		t.Errorf("application state = %q, want queued", appState)
	}
	if appCvID == nil || appCvID.String() != cvDocID {
		t.Errorf("application cv_document_id = %v, want %s", appCvID, cvDocID)
	}

	// 8. Safety Guard: Duplicate Prevention on Preview & Submit
	// GET /jobs/{id}/apply now indicates blocked
	dupPreviewRec := a.do(http.MethodGet, jobApplyPath, nil, session, nil)
	if dupPreviewRec.Code != http.StatusOK {
		t.Fatalf("duplicate apply preview: %d", dupPreviewRec.Code)
	}
	dupBody := dupPreviewRec.Body.String()
	if !strings.Contains(dupBody, "You already have an active or submitted application for this job.") {
		t.Errorf("duplicate preview missing duplicate error message, body:\n%s", dupBody)
	}
	if !strings.Contains(dupBody, "Application blocked by safety guard") {
		t.Error("duplicate preview missing blocked guard button label")
	}

	// POST /jobs/{id}/apply now redirects back to /jobs/{id} without queueing
	dupSubmitRec := a.do(http.MethodPost, jobApplyPath, url.Values{"cv_id": {cvDocID}}, session, nil)
	if dupSubmitRec.Code != http.StatusSeeOther || dupSubmitRec.Header().Get("Location") != "/jobs/"+strconv.FormatInt(jobID, 10) {
		t.Fatalf("duplicate submit redirect = %s, want /jobs/%d", dupSubmitRec.Header().Get("Location"), jobID)
	}

	// Verify count of applications in DB for this user/job is still exactly 1
	var appCount int
	if err := db.InSystemTx(ctx, a.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT count(*) FROM applications WHERE user_id = $1 AND job_id = $2", maxID, jobID).Scan(&appCount)
	}); err != nil {
		t.Fatal(err)
	}
	if appCount != 1 {
		t.Fatalf("applications count = %d, want 1", appCount)
	}

	// 9. Verify Job Detail page now displays "View application" linking to the active application
	jobDetailRec := a.do(http.MethodGet, "/jobs/"+strconv.FormatInt(jobID, 10), nil, session, nil)
	if jobDetailRec.Code != http.StatusOK {
		t.Fatalf("job detail code: %d", jobDetailRec.Code)
	}
	jobDetailHTML := jobDetailRec.Body.String()
	if !strings.Contains(jobDetailHTML, "View application") || !strings.Contains(jobDetailHTML, "/applications/"+appIDStr) {
		t.Errorf("job detail missing 'View Application' button linking to %s", appIDStr)
	}
}

var (
	cspNonce = regexp.MustCompile(`script-src 'nonce-([A-Za-z0-9_-]+)'`)
	tagRx    = regexp.MustCompile(`<[a-z]+\s[^>]*>`)
	hxAttr   = regexp.MustCompile(`\shx-[a-z-]+=`)
	nonceRx  = regexp.MustCompile(`nonce="[^"]*"`)
)

// checkNonces: every script and every element with htmx attributes carries the response's CSP nonce.
func checkNonces(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	m := cspNonce.FindStringSubmatch(rec.Header().Get("Content-Security-Policy"))
	if m == nil {
		t.Fatalf("no nonce in CSP %q", rec.Header().Get("Content-Security-Policy"))
	}
	for _, tag := range tagRx.FindAllString(rec.Body.String(), -1) {
		if strings.HasPrefix(tag, "<script") && !strings.Contains(tag, `nonce="`+m[1]+`"`) {
			t.Errorf("script without nonce: %s", tag)
		}
		if hxAttr.MatchString(strings.ReplaceAll(tag, " hx-nonce=", " x=")) && !strings.Contains(tag, `hx-nonce="`+m[1]+`"`) {
			t.Errorf("htmx element without nonce: %s", tag)
		}
	}
}

func withoutNonces(s string) string { return nonceRx.ReplaceAllString(s, `nonce=""`) }
