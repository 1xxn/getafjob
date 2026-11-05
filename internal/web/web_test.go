package web_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/identity/adapters/pgstore"
	"github.com/1xxn/getafjob/internal/platform/db/dbtest"
	"github.com/1xxn/getafjob/internal/platform/httpx"
	"github.com/1xxn/getafjob/internal/web"
)

type mailbox struct {
	mu   sync.Mutex
	last identity.Mail
	n    int
}

func (m *mailbox) Send(_ context.Context, x identity.Mail) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.last, m.n = x, m.n+1
	return nil
}

type app struct {
	t    *testing.T
	h    http.Handler
	ids  *identity.Service
	mail *mailbox
}

func newApp(t *testing.T) *app {
	t.Helper()
	pool := dbtest.AppPool(t)
	base, _ := url.Parse("https://jobs.example.org")
	mb := &mailbox{}
	ids := identity.NewService(pgstore.New(pool), mb, base)
	mux := http.NewServeMux()
	(&web.Server{Identity: ids, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}).Register(mux)
	return &app{t: t, h: httpx.Protect(mux, 1<<20, true), ids: ids, mail: mb}
}

func (a *app) do(method, path string, form url.Values, cookie string, hdr map[string]string) *httptest.ResponseRecorder {
	a.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req := httptest.NewRequest(method, "https://jobs.example.org"+path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: web.SessionCookie, Value: cookie})
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	a.h.ServeHTTP(rec, req)
	return rec
}

func sessionFrom(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == web.SessionCookie && c.Value != "" {
			if !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" {
				t.Fatalf("cookie flags: %+v", c)
			}
			return c.Value
		}
	}
	t.Fatalf("no session cookie (status %d)", rec.Code)
	return ""
}

func TestInvitationAndSignOut(t *testing.T) {
	a := newApp(t)
	link, _, err := a.ids.Invite(t.Context(), identity.InviteRequest{Email: "max.mustermann@example.com", Role: identity.RoleUser})
	if err != nil {
		t.Fatal(err)
	}
	path := strings.TrimPrefix(link, "https://jobs.example.org")

	rec := a.do(http.MethodGet, path, nil, "", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "max.mustermann@example.com") {
		t.Fatalf("invite page %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}

	// Empty name: form again with an error, no account.
	if rec := a.do(http.MethodPost, path, url.Values{"name": {"  "}, "locale": {"en"}}, "", nil); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty name: %d", rec.Code)
	}
	rec = a.do(http.MethodPost, path, url.Values{"name": {"<script>alert(1)</script>"}, "locale": {"de"}}, "", nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/" {
		t.Fatalf("accept: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	session := sessionFrom(t, rec)

	home := a.do(http.MethodGet, "/", nil, session, nil)
	body := home.Body.String()
	if home.Code != http.StatusOK || !strings.Contains(body, "Willkommen") || !strings.Contains(body, `lang="de"`) {
		t.Fatalf("home %d: %s", home.Code, body)
	}
	if strings.Contains(body, "<script>alert(1)") || !strings.Contains(body, "&lt;script&gt;") {
		t.Fatal("display name not escaped")
	}

	// The link works once.
	if rec := a.do(http.MethodGet, path, nil, "", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("reused invitation: %d", rec.Code)
	}

	out := a.do(http.MethodPost, "/logout", url.Values{}, session, nil)
	if out.Code != http.StatusSeeOther || out.Header().Get("Location") != "/login" {
		t.Fatalf("logout: %d", out.Code)
	}
	if rec := a.do(http.MethodGet, "/", nil, session, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("home after logout: %d", rec.Code)
	}
}

func TestMagicLinkSignIn(t *testing.T) {
	a := newApp(t)
	link, _, _ := a.ids.Invite(t.Context(), identity.InviteRequest{Email: "max@example.com", Role: identity.RoleUser})
	if _, _, err := a.ids.AcceptInvitation(t.Context(), link[strings.LastIndex(link, "/")+1:], "Max", "en"); err != nil {
		t.Fatal(err)
	}

	known := a.do(http.MethodPost, "/login", url.Values{"email": {"max@example.com"}}, "", nil)
	unknown := a.do(http.MethodPost, "/login", url.Values{"email": {"nobody@example.com"}}, "", nil)
	if known.Code != http.StatusOK || known.Body.String() != unknown.Body.String() {
		t.Fatal("answers for known and unknown addresses differ")
	}
	if a.mail.n != 1 {
		t.Fatalf("mails = %d", a.mail.n)
	}
	var signin string
	for _, f := range strings.Fields(a.mail.last.Text) {
		if strings.HasPrefix(f, "https://jobs.example.org/login/link/") {
			signin = strings.TrimPrefix(f, "https://jobs.example.org")
		}
	}

	// Opening the link only shows a button; mail scanners that GET it cannot use it up.
	if rec := a.do(http.MethodGet, signin, nil, "", nil); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `method="post"`) {
		t.Fatalf("confirm page: %d", rec.Code)
	}
	rec := a.do(http.MethodPost, signin, url.Values{}, "", nil)
	session := sessionFrom(t, rec)
	if home := a.do(http.MethodGet, "/", nil, session, nil); home.Code != http.StatusOK || !strings.Contains(home.Body.String(), "Welcome, Max.") {
		t.Fatalf("home %d", home.Code)
	}
	if rec := a.do(http.MethodPost, signin, url.Values{}, "", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("reused link: %d", rec.Code)
	}
	if rec := a.do(http.MethodGet, "/login/link/not-a-token", nil, "", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed link: %d", rec.Code)
	}
}

func TestCrossSitePostIsRejected(t *testing.T) {
	a := newApp(t)
	rec := a.do(http.MethodPost, "/login", url.Values{"email": {"max@example.com"}}, "", map[string]string{"Sec-Fetch-Site": "cross-site"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-site POST: %d", rec.Code)
	}
}

func TestSignInIsRateLimited(t *testing.T) {
	a := newApp(t)
	codes := map[int]int{}
	for range 12 {
		rec := a.do(http.MethodPost, "/login", url.Values{"email": {"x@example.com"}}, "", map[string]string{"X-Forwarded-For": "203.0.113.7"})
		codes[rec.Code]++
	}
	if codes[http.StatusOK] != 10 || codes[http.StatusTooManyRequests] != 2 {
		t.Fatalf("codes = %v", codes)
	}
	// Another client is not affected.
	if rec := a.do(http.MethodPost, "/login", url.Values{"email": {"x@example.com"}}, "", map[string]string{"X-Forwarded-For": "203.0.113.8"}); rec.Code != http.StatusOK {
		t.Fatalf("other client: %d", rec.Code)
	}
}

func TestPublicPages(t *testing.T) {
	a := newApp(t)
	if rec := a.do(http.MethodGet, "/", nil, "", nil); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Fatalf("home signed out: %d", rec.Code)
	}
	login := a.do(http.MethodGet, "/login", nil, "", map[string]string{"Accept-Language": "de-DE,de;q=0.9"})
	if !strings.Contains(login.Body.String(), "Anmeldelink senden") {
		t.Fatal("login page not German")
	}
	if rec := a.do(http.MethodGet, "/no/such/page", nil, "", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("404: %d", rec.Code)
	}
	css := a.do(http.MethodGet, "/static/app.css", nil, "", nil)
	if css.Code != http.StatusOK || !strings.HasPrefix(css.Header().Get("Cache-Control"), "public") {
		t.Fatalf("static: %d %q", css.Code, css.Header().Get("Cache-Control"))
	}
	if rec := a.do(http.MethodGet, "/", nil, "garbage", nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("garbage cookie: %d", rec.Code)
	}
}
