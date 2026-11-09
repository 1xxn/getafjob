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
	if !strings.Contains(list, `href="/jobs/`) || !strings.Contains(list, "Go Developer") || strings.Contains(list, "Sales Manager") {
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
	if body := part.Body.String(); !strings.HasPrefix(strings.TrimSpace(body), `<div id="results">`) || strings.Contains(body, "<html") ||
		!strings.Contains(part.Header().Get("Vary"), "HX-Request") {
		t.Fatalf("partial: %s", body)
	}
	restore := a.do(http.MethodGet, "/jobs", nil, session, map[string]string{"HX-Request": "true", "HX-History-Restore-Request": "true"})
	if !strings.Contains(restore.Body.String(), "<html") {
		t.Fatal("history restore got a fragment")
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
	if !strings.Contains(blkList, "Go Developer") || !strings.Contains(blkList, "Unblock") {
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
	}, session, nil); rec.Code != http.StatusOK {
		t.Fatalf("set variant 1: %d", rec.Code)
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
	if !strings.Contains(body, "Matching Skills") || !strings.Contains(body, "Go") {
		t.Fatalf("matching skills missing in body:\n%s", body)
	}
	if !strings.Contains(body, "Missing Keywords &amp; Skills") {
		t.Fatalf("missing skills title not found")
	}
	if !strings.Contains(body, "PostgreSQL") || !strings.Contains(body, "Docker") {
		t.Fatalf("missing skills PostgreSQL / Docker not found")
	}
	if !strings.Contains(body, "Score Breakdown") {
		t.Fatal("score breakdown title not found")
	}
	if !strings.Contains(body, "Skills Overlap") || !strings.Contains(body, "Seniority Level") {
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
	if !strings.Contains(addSkillBody, "✓ Docker") {
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
