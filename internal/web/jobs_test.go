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
