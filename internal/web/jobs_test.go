package web_test

import (
	"context"
	"net/http"
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

	list := a.do(http.MethodGet, "/jobs", nil, session, nil).Body.String()
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

	detail := a.do(http.MethodGet, "/jobs/"+strconv.FormatInt(goID, 10), nil, session, nil).Body.String()
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
}
