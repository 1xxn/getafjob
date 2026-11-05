package web_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/identity/adapters/pgstore"
	"github.com/1xxn/getafjob/internal/identity/softauthn"
	"github.com/1xxn/getafjob/internal/platform/blob"
	"github.com/1xxn/getafjob/internal/platform/config"
	"github.com/1xxn/getafjob/internal/platform/crypto"
	"github.com/1xxn/getafjob/internal/platform/db/dbtest"
	"github.com/1xxn/getafjob/internal/platform/httpx"
	"github.com/1xxn/getafjob/internal/profile"
	profilestore "github.com/1xxn/getafjob/internal/profile/adapters/pgstore"
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
	if err := ids.EnablePasskeys(base); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	ring, err := crypto.NewKeyring(config.Secret(base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32))), "")
	if err != nil {
		t.Fatal(err)
	}
	env := crypto.NewEnvelope(ring, pool)
	dir, err := blob.NewDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dir.Close() })
	pstore := profilestore.New(pool)
	profiles := profile.NewService(pstore, env)
	docs := profile.NewDocuments(pstore, blob.NewSealed(dir, env), env)
	(&web.Server{Identity: ids, Profile: profiles, Documents: docs, Items: profile.NewItems(pstore, env),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}).Register(mux)
	return &app{t: t, h: httpx.Protect(mux, 1<<20, true, httpx.Upload{Path: "/documents", Max: web.MaxUpload}), ids: ids, mail: mb}
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

func TestErrorLogHasNoToken(t *testing.T) {
	pool := dbtest.AppPool(t)
	base, _ := url.Parse("https://jobs.example.org")
	var logs strings.Builder
	mux := http.NewServeMux()
	(&web.Server{Identity: identity.NewService(pgstore.New(pool), &mailbox{}, base),
		Logger: slog.New(slog.NewTextHandler(&logs, nil))}).Register(mux)
	a := &app{t: t, h: httpx.Protect(mux, 1<<20, true)}
	pool.Close() // every query fails from here on

	tok, _ := identity.NewToken()
	token := string(tok)
	rec := a.do(http.MethodPost, "/login/link/"+token, url.Values{}, "", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d", rec.Code)
	}
	if out := logs.String(); strings.Contains(out, token) || !strings.Contains(out, "/login/link/{token}") {
		t.Fatalf("log = %q", out)
	}
}

func signedIn(t *testing.T, a *app) string {
	t.Helper()
	link, _, err := a.ids.Invite(t.Context(), identity.InviteRequest{Email: "max.mustermann@example.com", Role: identity.RoleUser})
	if err != nil {
		t.Fatal(err)
	}
	rec := a.do(http.MethodPost, strings.TrimPrefix(link, "https://jobs.example.org"), url.Values{"name": {"Max Mustermann"}, "locale": {"en"}}, "", nil)
	return sessionFrom(t, rec)
}

func TestSessionCookieRotates(t *testing.T) {
	a := newApp(t)
	now := time.Now()
	a.ids.WithClock(func() time.Time { return now })
	first := signedIn(t, a)
	if c := a.do(http.MethodGet, "/", nil, first, nil).Result().Cookies(); len(c) != 0 {
		t.Fatalf("fresh session set a cookie: %v", c)
	}
	now = now.Add(identity.SessionRotate)
	rec := a.do(http.MethodGet, "/", nil, first, nil)
	second := sessionFrom(t, rec)
	if rec.Code != http.StatusOK || second == first {
		t.Fatalf("rotation: %d, same token %v", rec.Code, second == first)
	}
	// Signing out with the new cookie ends the old token too.
	if rec := a.do(http.MethodPost, "/logout", url.Values{}, second, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("logout: %d", rec.Code)
	}
	for _, tok := range []string{first, second} {
		if rec := a.do(http.MethodGet, "/", nil, tok, nil); rec.Code != http.StatusSeeOther {
			t.Errorf("token still signed in after logout: %d", rec.Code)
		}
	}
}

func TestAccountSessions(t *testing.T) {
	a := newApp(t)
	here := signedIn(t, a)
	if err := a.ids.RequestMagicLink(t.Context(), "max.mustermann@example.com"); err != nil {
		t.Fatal(err)
	}
	var link string
	for _, w := range strings.Fields(a.mail.last.Text) {
		if strings.HasPrefix(w, "https://") {
			link = w
		}
	}
	other := sessionFrom(t, a.do(http.MethodPost, strings.TrimPrefix(link, "https://jobs.example.org"), url.Values{}, "", nil))

	page := a.do(http.MethodGet, "/account", nil, here, nil)
	body := page.Body.String()
	if page.Code != http.StatusOK || strings.Count(body, "<tr>") != 3 || !strings.Contains(body, "This device") ||
		!strings.Contains(body, "Sign-in link") || !strings.Contains(body, "Invitation") {
		t.Fatalf("account %d: %s", page.Code, body)
	}
	rec := a.do(http.MethodPost, "/account/sessions/others", url.Values{}, here, nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Other sessions signed out: 1.") {
		t.Fatalf("sign out others %d: %s", rec.Code, rec.Body.String())
	}
	if rec := a.do(http.MethodGet, "/", nil, other, nil); rec.Code != http.StatusSeeOther {
		t.Errorf("other session still signed in: %d", rec.Code)
	}
	if rec := a.do(http.MethodGet, "/account", nil, "", nil); rec.Code != http.StatusSeeOther {
		t.Errorf("account without session: %d", rec.Code)
	}
}

func TestSignOutOthersAsksForFreshSignIn(t *testing.T) {
	a := newApp(t)
	now := time.Now()
	a.ids.WithClock(func() time.Time { return now })
	here := signedIn(t, a)
	now = now.Add(identity.SessionFresh + identity.SessionRotate)
	rec := a.do(http.MethodPost, "/account/sessions/others", url.Values{}, here, nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login?reauth=1" {
		t.Fatalf("stale session: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if c := sessionFrom(t, rec); c == here {
		t.Error("token not rotated on the way")
	}
	page := a.do(http.MethodGet, "/login?reauth=1", nil, here, nil)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Sign in again to confirm") {
		t.Fatalf("reauth page %d", page.Code)
	}
}

func (a *app) json(path string, body any, cookie string) *httptest.ResponseRecorder {
	a.t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		a.t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "https://jobs.example.org"+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: web.SessionCookie, Value: cookie})
	}
	rec := httptest.NewRecorder()
	a.h.ServeHTTP(rec, req)
	return rec
}

type optionsReply struct {
	Challenge string          `json:"challenge"`
	Options   json.RawMessage `json:"options"`
}

func options(t *testing.T, rec *httptest.ResponseRecorder) optionsReply {
	t.Helper()
	var o optionsReply
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &o) != nil || o.Challenge == "" {
		t.Fatalf("options %d: %s", rec.Code, rec.Body.String())
	}
	return o
}

func TestPasskeyPages(t *testing.T) {
	a := newApp(t)
	session := signedIn(t, a)
	key := softauthn.New("jobs.example.org", "https://jobs.example.org")

	o := options(t, a.json("/account/passkeys/options", map[string]any{}, session))
	cred, err := key.Create(o.Options)
	if err != nil {
		t.Fatal(err)
	}
	if rec := a.json("/account/passkeys", map[string]any{"challenge": o.Challenge, "name": "Laptop", "credential": json.RawMessage(cred)}, session); rec.Code != http.StatusCreated {
		t.Fatalf("register %d: %s", rec.Code, rec.Body.String())
	}
	page := a.do(http.MethodGet, "/account?added=1", nil, session, nil).Body.String()
	if !strings.Contains(page, "Laptop") || !strings.Contains(page, "Passkey added") || !strings.Contains(page, `src="/static/js/passkey.js"`) {
		t.Fatalf("account page: %s", page)
	}
	if rec := a.json("/account/passkeys", map[string]any{"challenge": o.Challenge, "credential": json.RawMessage(cred), "extra": 1}, session); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown field: %d", rec.Code)
	}

	login := a.do(http.MethodGet, "/login", nil, "", nil).Body.String()
	if !strings.Contains(login, `data-passkey="sign-in"`) || !strings.Contains(login, "Sign in with a passkey") {
		t.Fatalf("login page: %s", login)
	}
	o = options(t, a.json("/login/passkey/options", map[string]any{}, ""))
	assertion, err := key.Get(o.Options)
	if err != nil {
		t.Fatal(err)
	}
	rec := a.json("/login/passkey", map[string]any{"challenge": o.Challenge, "credential": json.RawMessage(assertion)}, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("passkey login %d: %s", rec.Code, rec.Body.String())
	}
	fresh := sessionFrom(t, rec)
	if home := a.do(http.MethodGet, "/", nil, fresh, nil); home.Code != http.StatusOK {
		t.Fatalf("home after passkey login: %d", home.Code)
	}
	// The same answer again: refused, with a translated message.
	rec = a.json("/login/passkey", map[string]any{"challenge": o.Challenge, "credential": json.RawMessage(assertion)}, "")
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "did not work") {
		t.Errorf("replay %d: %s", rec.Code, rec.Body.String())
	}

	js := a.do(http.MethodGet, "/static/js/passkey.js", nil, "", nil)
	if js.Code != http.StatusOK || !strings.Contains(js.Header().Get("Content-Type"), "javascript") {
		t.Errorf("script %d %q", js.Code, js.Header().Get("Content-Type"))
	}
	if rec := a.do(http.MethodPost, "/account/passkeys/"+base64.RawURLEncoding.EncodeToString(key.ID)+"/delete", url.Values{}, fresh, nil); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Passkey removed") {
		t.Errorf("remove %d", rec.Code)
	}
}

func TestPasskeyRegistrationNeedsFreshSignIn(t *testing.T) {
	a := newApp(t)
	now := time.Now()
	a.ids.WithClock(func() time.Time { return now })
	session := signedIn(t, a)
	now = now.Add(identity.SessionFresh)
	rec := a.json("/account/passkeys/options", map[string]any{}, session)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "sign in again") {
		t.Fatalf("stale session %d: %s", rec.Code, rec.Body.String())
	}
	if rec := a.json("/account/passkeys/options", map[string]any{}, ""); rec.Code != http.StatusForbidden {
		t.Errorf("signed out: %d", rec.Code)
	}
}

func TestPasskeyNudgeAndRecovery(t *testing.T) {
	a := newApp(t)
	session := signedIn(t, a)
	if home := a.do(http.MethodGet, "/", nil, session, nil).Body.String(); !strings.Contains(home, "Add a passkey now") {
		t.Fatalf("no nudge without passkey: %s", home)
	}
	key := softauthn.New("jobs.example.org", "https://jobs.example.org")
	o := options(t, a.json("/account/passkeys/options", map[string]any{}, session))
	cred, _ := key.Create(o.Options)
	if rec := a.json("/account/passkeys", map[string]any{"challenge": o.Challenge, "credential": json.RawMessage(cred)}, session); rec.Code != http.StatusCreated {
		t.Fatalf("register %d", rec.Code)
	}
	if home := a.do(http.MethodGet, "/", nil, session, nil).Body.String(); strings.Contains(home, "Add a passkey now") {
		t.Error("nudge with a passkey")
	}

	// The login form answers as for anyone, but starts a recovery instead of mailing a link.
	if rec := a.do(http.MethodPost, "/login", url.Values{"email": {"max.mustermann@example.com"}}, "", nil); rec.Code != http.StatusOK {
		t.Fatalf("login %d", rec.Code)
	}
	if strings.Contains(a.mail.last.Text, "/login/link/") {
		t.Fatal("a sign-in link was mailed to an account with a passkey")
	}
	page := a.do(http.MethodGet, "/account", nil, session, nil).Body.String()
	if !strings.Contains(page, "without your passkey") || !strings.Contains(page, "Cancel this request") {
		t.Fatalf("account page without the pending recovery: %s", page)
	}
	rec := a.do(http.MethodPost, "/account/recovery/cancel", url.Values{}, session, nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "The request was canceled") {
		t.Fatalf("cancel %d", rec.Code)
	}
}

func TestProfilePages(t *testing.T) {
	a := newApp(t)
	session := signedIn(t, a)
	if rec := a.do(http.MethodGet, "/profile", nil, "", nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("profile without session: %d", rec.Code)
	}
	page := a.do(http.MethodGet, "/profile", nil, session, nil)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `name="version" value="0"`) {
		t.Fatalf("empty profile page %d", page.Code)
	}
	form := url.Values{"version": {"0"}, "first_name": {"Max"}, "last_name": {"Mustermann"}, "email": {"max.mustermann@example.org"},
		"phone": {"+49 30 1234567"}, "github": {"github.com/maxmustermann"}, "skills": {"Go, PostgreSQL\nKubernetes"},
		"languages": {"German: native\nEnglish: C1"}, "work_authorized_eu": {"on"}}
	if rec := a.do(http.MethodPost, "/profile", form, session, nil); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/profile?saved=1" {
		t.Fatalf("save %d", rec.Code)
	}
	body := a.do(http.MethodGet, "/profile?saved=1", nil, session, nil).Body.String()
	for _, want := range []string{`value="Mustermann"`, `value="https://github.com/maxmustermann"`, "Go\nPostgreSQL\nKubernetes",
		"German: native\nEnglish: C1", `name="work_authorized_eu" type="checkbox" value="on" checked`, `name="version" value="1"`, "Saved."} {
		if !strings.Contains(body, want) {
			t.Errorf("profile page lacks %q", want)
		}
	}

	// Errors: the form comes back with the typed values and a message per field.
	form.Set("version", "1")
	form.Set("email", "Max <max@example.org>")
	rec := a.do(http.MethodPost, "/profile", form, session, nil)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "Enter a plain e-mail address") ||
		!strings.Contains(rec.Body.String(), `aria-invalid="true"`) {
		t.Fatalf("invalid save %d: %s", rec.Code, rec.Body.String())
	}
	// A stale version: conflict with the current data.
	form.Set("email", "max@example.org")
	form.Set("version", "0")
	if rec := a.do(http.MethodPost, "/profile", form, session, nil); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "another tab") {
		t.Fatalf("stale save %d", rec.Code)
	}

	prefs := url.Values{"titles": {"Platform Engineer\nSRE"}, "locations": {"Berlin"}, "remote": {"on"}, "min_salary": {"70000"},
		"cover_letter_lang": {"de"}, "diversity": {"decline"}}
	if rec := a.do(http.MethodPost, "/preferences", prefs, session, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("preferences %d", rec.Code)
	}
	body = a.do(http.MethodGet, "/preferences", nil, session, nil).Body.String()
	if !strings.Contains(body, "Platform Engineer\nSRE") || !strings.Contains(body, `value="70000"`) || !strings.Contains(body, `value="de" selected`) {
		t.Fatalf("preferences page: %s", body)
	}
	prefs.Set("min_salary", "a lot")
	if rec := a.do(http.MethodPost, "/preferences", prefs, session, nil); rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `value="a lot"`) {
		t.Fatalf("invalid preferences %d", rec.Code)
	}
	if body := a.do(http.MethodGet, "/preferences", nil, session, nil).Body.String(); !strings.Contains(body, `value="70000"`) {
		t.Error("invalid preferences were saved")
	}
}

func (a *app) upload(kind, name string, data []byte, cookie string) *httptest.ResponseRecorder {
	a.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("kind", kind)
	fw, err := mw.CreateFormFile("file", name)
	if err != nil {
		a.t.Fatal(err)
	}
	_, _ = fw.Write(data)
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "https://jobs.example.org/documents", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.AddCookie(&http.Cookie{Name: web.SessionCookie, Value: cookie})
	rec := httptest.NewRecorder()
	a.h.ServeHTTP(rec, req)
	return rec
}

func TestDocumentPages(t *testing.T) {
	a := newApp(t)
	session := signedIn(t, a)
	pdf := []byte("%PDF-1.7\nCV of Max Mustermann")
	if rec := a.upload("cv", "CV Max.pdf", pdf, session); rec.Code != http.StatusSeeOther {
		t.Fatalf("upload %d: %s", rec.Code, rec.Body.String())
	}
	page := a.do(http.MethodGet, "/documents?uploaded=1", nil, session, nil).Body.String()
	i := strings.Index(page, `href="/documents/`)
	if i < 0 || !strings.Contains(page, "CV Max.pdf") || !strings.Contains(page, "CV variant") {
		t.Fatalf("documents page: %s", page)
	}
	id := page[i+len(`href="/documents/`) : i+len(`href="/documents/`)+36]

	dl := a.do(http.MethodGet, "/documents/"+id, nil, session, nil)
	if dl.Code != http.StatusOK || !bytes.Equal(dl.Body.Bytes(), pdf) || dl.Header().Get("Content-Type") != "application/pdf" ||
		!strings.HasPrefix(dl.Header().Get("Content-Disposition"), "attachment") || !strings.HasPrefix(dl.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("download %d %v", dl.Code, dl.Header())
	}
	if rec := a.do(http.MethodPost, "/documents/"+id+"/variant", url.Values{"label-" + id: {"Platform"}, "language-" + id: {"en"}, "focus-" + id: {"platform, kubernetes"}}, session, nil); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `value="platform, kubernetes"`) {
		t.Fatalf("variant %d", rec.Code)
	}

	// A disguised file, an oversized one and a stranger.
	if rec := a.upload("cv", "cv.pdf", []byte("<html><script>alert(1)</script>"), session); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("html as pdf: %d", rec.Code)
	}
	if rec := a.upload("cv", "big.pdf", append([]byte("%PDF-"), make([]byte, 10<<20)...), session); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized: %d", rec.Code)
	}
	link, _, err := a.ids.Invite(t.Context(), identity.InviteRequest{Email: "erika.mustermann@example.com", Role: identity.RoleUser})
	if err != nil {
		t.Fatal(err)
	}
	erika := sessionFrom(t, a.do(http.MethodPost, strings.TrimPrefix(link, "https://jobs.example.org"), url.Values{"name": {"Erika"}, "locale": {"en"}}, "", nil))
	if rec := a.do(http.MethodGet, "/documents/"+id, nil, erika, nil); rec.Code != http.StatusNotFound {
		t.Errorf("erika downloads max's CV: %d", rec.Code)
	}
	if rec := a.do(http.MethodPost, "/documents/"+id+"/delete", url.Values{}, erika, nil); rec.Code != http.StatusNotFound {
		t.Errorf("erika deletes max's CV: %d", rec.Code)
	}
	if rec := a.do(http.MethodPost, "/documents/"+id+"/delete", url.Values{}, session, nil); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Document removed") {
		t.Errorf("delete: %d", rec.Code)
	}
}

func TestAnswersAndTexts(t *testing.T) {
	a := newApp(t)
	session := signedIn(t, a)
	if rec := a.do(http.MethodPost, "/answers", url.Values{"question": {"Why us? *"}, "answer": {"Because of the product."}}, session, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("add answer %d", rec.Code)
	}
	page := a.do(http.MethodGet, "/answers?saved=1", nil, session, nil).Body.String()
	i := strings.Index(page, `action="/answers/`)
	if i < 0 || !strings.Contains(page, "Because of the product.") {
		t.Fatalf("answers page: %s", page)
	}
	id := page[i+len(`action="/answers/`) : i+len(`action="/answers/`)+36]
	if rec := a.do(http.MethodPost, "/answers", url.Values{"question": {"Why us?"}, "answer": {""}}, session, nil); rec.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(rec.Body.String(), `value="Why us?"`) {
		t.Fatalf("empty answer %d", rec.Code)
	}
	if rec := a.do(http.MethodPost, "/answers/"+id, url.Values{"question": {"Why us?"}, "answer": {"Changed."}}, session, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("edit %d", rec.Code)
	}
	if body := a.do(http.MethodGet, "/answers", nil, session, nil).Body.String(); !strings.Contains(body, "Changed.") || strings.Count(body, `action="/answers/`) != 2 {
		t.Fatalf("after edit: %s", body)
	}

	if rec := a.do(http.MethodPost, "/texts", url.Values{"name": {"Intro"}, "language": {"en"}, "body": {"Dear {company} team"}}, session, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("add text %d", rec.Code)
	}
	rec := a.do(http.MethodPost, "/texts", url.Values{"name": {"Typo"}, "body": {"Hi {employer}"}}, session, nil)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "Unknown placeholder") {
		t.Fatalf("typo %d", rec.Code)
	}
	if body := a.do(http.MethodGet, "/texts", nil, session, nil).Body.String(); !strings.Contains(body, "Dear {company} team") || !strings.Contains(body, "{job_title}") {
		t.Fatalf("texts page: %s", body)
	}

	link, _, _ := a.ids.Invite(t.Context(), identity.InviteRequest{Email: "erika.mustermann@example.com", Role: identity.RoleUser})
	erika := sessionFrom(t, a.do(http.MethodPost, strings.TrimPrefix(link, "https://jobs.example.org"), url.Values{"name": {"Erika"}, "locale": {"en"}}, "", nil))
	if rec := a.do(http.MethodPost, "/answers/"+id+"/delete", url.Values{}, erika, nil); rec.Code != http.StatusNotFound {
		t.Errorf("erika deletes max's answer: %d", rec.Code)
	}
	if body := a.do(http.MethodGet, "/answers", nil, erika, nil).Body.String(); strings.Contains(body, "Changed.") {
		t.Error("erika sees max's answers")
	}
	if rec := a.do(http.MethodPost, "/answers/"+id+"/delete", url.Values{}, session, nil); rec.Code != http.StatusSeeOther {
		t.Errorf("delete %d", rec.Code)
	}
}
