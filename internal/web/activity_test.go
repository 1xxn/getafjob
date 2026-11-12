package web_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/1xxn/getafjob/internal/platform/db"
)

// The bell counts unread entries and links each to its page; marking read clears the count; the feed page groups
// the entries by day.
func TestBellAndActivity(t *testing.T) {
	a := newApp(t)
	session := signedIn(t, a)
	var appID uuid.UUID
	now := time.Now()
	if err := db.InSystemTx(t.Context(), a.pool, func(tx pgx.Tx) error {
		var userID uuid.UUID
		if err := tx.QueryRow(t.Context(), `SELECT id FROM users WHERE email = 'max.mustermann@example.com'`).Scan(&userID); err != nil {
			return err
		}
		var jobID int64
		if err := tx.QueryRow(t.Context(), `INSERT INTO jobs (fingerprint, match_key, source, external_id, company, title, url,
			content_hash, first_seen, last_seen) VALUES ('fp-bell', 'k', 'test', 'fp-bell', 'Muster GmbH', 'Platform Engineer',
			'https://example.org', 'hash', $1, $1) RETURNING id`, now).Scan(&jobID); err != nil {
			return err
		}
		if err := tx.QueryRow(t.Context(), `INSERT INTO applications (user_id, job_id, state, created_at, updated_at)
			VALUES ($1, $2, 'needs_input', $3, $3) RETURNING id`, userID, jobID, now).Scan(&appID); err != nil {
			return err
		}
		_, err := tx.Exec(t.Context(), `INSERT INTO application_events (application_id, user_id, event, from_state, to_state, actor, at)
			VALUES ($1, $2, 'transition', 'running', 'needs_input', 'worker', $3)`, appID, userID, now.Add(-time.Minute))
		return err
	}); err != nil {
		t.Fatal(err)
	}

	if home := a.do(http.MethodGet, "/", nil, session, nil).Body.String(); !strings.Contains(home, `hx-get="/notifications/bell"`) {
		t.Fatal("no bell in the top bar")
	}
	bell := a.do(http.MethodGet, "/notifications/bell", nil, session, map[string]string{"HX-Request": "true"}).Body.String()
	for _, want := range []string{`data-unread="1"`, "Notifications, 1 unread", `href="/needs-input?app=` + appID.String() + `"`,
		"Needs you", "Platform Engineer · Muster GmbH", "data-mark-read"} {
		if !strings.Contains(bell, want) {
			t.Fatalf("bell lacks %q: %s", want, bell)
		}
	}
	page := a.do(http.MethodGet, "/activity", nil, session, nil).Body.String()
	if !strings.Contains(page, "<h2 id=\"act-day-0\">Today</h2>") || !strings.Contains(page, `class="act-item is-unread"`) {
		t.Fatalf("activity page: %s", page)
	}
	rec := a.do(http.MethodPost, "/notifications/read", nil, session, map[string]string{"HX-Request": "true"})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `data-unread="0"`) || strings.Contains(rec.Body.String(), "data-mark-read") {
		t.Fatalf("mark read %d: %s", rec.Code, rec.Body.String())
	}
	if rec := a.do(http.MethodPost, "/notifications/read", nil, session, nil); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/activity" {
		t.Errorf("mark read without htmx %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if rec := a.do(http.MethodGet, "/activity?before=nonsense", nil, session, nil); rec.Code != http.StatusNotFound {
		t.Errorf("bad cursor %d", rec.Code)
	}
}
