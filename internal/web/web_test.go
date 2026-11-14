package web_test

import (
	"archive/zip"
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
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/1xxn/getafjob/internal/application"
	applicationstore "github.com/1xxn/getafjob/internal/application/adapters/pgstore"
	"github.com/1xxn/getafjob/internal/discovery"
	discoverystore "github.com/1xxn/getafjob/internal/discovery/adapters/pgstore"
	"github.com/1xxn/getafjob/internal/discovery/urlimport"
	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/identity/adapters/pgstore"
	"github.com/1xxn/getafjob/internal/identity/softauthn"
	"github.com/1xxn/getafjob/internal/matching"
	matchingstore "github.com/1xxn/getafjob/internal/matching/adapters/pgstore"
	"github.com/1xxn/getafjob/internal/notify"
	notifystore "github.com/1xxn/getafjob/internal/notify/adapters/pgstore"
	"github.com/1xxn/getafjob/internal/platform/blob"
	"github.com/1xxn/getafjob/internal/platform/config"
	"github.com/1xxn/getafjob/internal/platform/crypto"
	"github.com/1xxn/getafjob/internal/platform/db/dbtest"
	"github.com/1xxn/getafjob/internal/platform/httpx"
	"github.com/1xxn/getafjob/internal/privacy"
	"github.com/1xxn/getafjob/internal/profile"
	profilestore "github.com/1xxn/getafjob/internal/profile/adapters/pgstore"
	"github.com/1xxn/getafjob/internal/profile/cvimport"
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

type testMatchEvaluator struct {
	matching matching.Store
}

func (e *testMatchEvaluator) EvaluateAndSave(ctx context.Context, userID uuid.UUID, jobID int64, p discovery.Posting, at time.Time) error {
	m := matching.NewMatcher(matching.Criteria{Titles: []string{p.Title}})
	posted, _ := time.Parse(time.DateOnly, p.Posted)
	mj := matching.Job{
		ID:          jobID,
		Company:     p.Company,
		Title:       p.Title,
		Location:    p.Location,
		Workplace:   string(p.Workplace),
		Description: p.Description,
		Posted:      posted,
		FirstSeen:   at,
	}
	res := m.Evaluate(mj, at)
	return e.matching.SaveMatches(ctx, userID, []matching.Result{res}, at)
}

type app struct {
	t    *testing.T
	h    http.Handler
	ids  *identity.Service
	mail *mailbox
	pool *pgxpool.Pool
}

func newApp(t *testing.T, opts ...func(*web.Server)) *app {
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
	sealedFiles := blob.NewSealed(dir, env)
	docs := profile.NewDocuments(pstore, sealedFiles, env)
	items := profile.NewItems(pstore, env)
	stations := profile.NewStations(pstore, env)
	listings := matching.NewListings(matchingstore.New(pool))
	apps := application.NewService(applicationstore.New(pool))
	privacySvc := privacy.NewService(ids, profiles, items, docs, apps, listings, env, sealedFiles)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	importer := cvimport.NewImporter(docs, profiles, stations, cvimport.NewExtractorFor(exe, 20*time.Second))
	dstore := discoverystore.New(pool)
	mstore := matchingstore.New(pool)
	discoverySvc := urlimport.NewService(urlimport.Options{
		Discovery:    dstore,
		Boards:       dstore,
		Matcher:      &testMatchEvaluator{matching: mstore},
		Queue:        testCrawlQueue{},
		AllowPrivate: true,
	})
	srv := &web.Server{Identity: ids, Profile: profiles, Documents: docs, Items: items, Stations: stations, Importer: importer,
		Jobs: listings, Discovery: discoverySvc, Apps: apps, Privacy: privacySvc, StyleGuide: true,
		Notify: notify.NewService(notifystore.New(pool), nil, nil, nil, "", nil),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	for _, o := range opts {
		o(srv)
	}
	srv.Register(mux)
	return &app{t: t, h: httpx.Protect(mux, 1<<20, true, httpx.Upload{Path: "/documents", Max: web.MaxUpload},
		httpx.Upload{Path: "/setup/cv", Max: web.MaxUpload}), ids: ids, mail: mb, pool: pool}
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
	// Equal apart from the per-response nonce, which has a fixed length.
	if known.Code != http.StatusOK || withoutNonces(known.Body.String()) != withoutNonces(unknown.Body.String()) ||
		known.Body.Len() != unknown.Body.Len() {
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

	page := a.do(http.MethodGet, "/settings/account", nil, here, nil)
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
	if rec := a.do(http.MethodGet, "/settings/account", nil, "", nil); rec.Code != http.StatusSeeOther {
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
	page := a.do(http.MethodGet, "/settings/account?added=1", nil, session, nil).Body.String()
	if !strings.Contains(page, "Laptop") || !strings.Contains(page, "Passkey added") || !strings.Contains(page, `src="/static/js/passkey.js?v=`) {
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
	page := a.do(http.MethodGet, "/settings/account", nil, session, nil).Body.String()
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
		"German: native\nEnglish: C1", `type="checkbox" id="f-work_authorized_eu" name="work_authorized_eu" value="on" checked`, `name="version" value="1"`, "Saved."} {
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
	body = a.do(http.MethodGet, "/settings/search", nil, session, nil).Body.String()
	if !strings.Contains(body, "Platform Engineer\nSRE") || !strings.Contains(body, `value="70000"`) || !strings.Contains(body, `value="de" selected`) {
		t.Fatalf("preferences page: %s", body)
	}
	prefs.Set("min_salary", "a lot")
	if rec := a.do(http.MethodPost, "/preferences", prefs, session, nil); rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `value="a lot"`) {
		t.Fatalf("invalid preferences %d", rec.Code)
	}
	if body := a.do(http.MethodGet, "/settings/search", nil, session, nil).Body.String(); !strings.Contains(body, `value="70000"`) {
		t.Error("invalid preferences were saved")
	}
	// Invalid notification settings come back as field errors; nothing is saved.
	bad := url.Values{"section": {"notifications"}, "notify_ntfy_enabled": {"on"}, "notify_daily_summary_time": {"25:00"}}
	if rec := a.do(http.MethodPost, "/preferences", bad, session, nil); rec.Code != http.StatusUnprocessableEntity ||
		strings.Count(rec.Body.String(), "This value is not valid.") != 2 || rec.Header().Get("HX-Replace-Url") != "/settings/notifications" {
		t.Fatalf("invalid notifications %d, address %q: %s", rec.Code, rec.Header().Get("HX-Replace-Url"), rec.Body.String())
	}
	body = a.do(http.MethodGet, "/settings/notifications", nil, session, nil).Body.String()
	if strings.Contains(body, "25:00") {
		t.Error("invalid notification settings were saved")
	}
	// Without a VAPID key the web push switch is off and says why.
	if !strings.Contains(body, "Web push is not set up on this instance") ||
		!regexp.MustCompile(`<input[^>]*name="notify_webpush_enabled"[^>]*disabled`).MatchString(body) {
		t.Error("web push switch is offered without a VAPID key")
	}
	port := url.Values{"section": {"integrations"}, "mailbox_host": {"imap.example.org"}, "mailbox_username": {"max"}, "mailbox_port": {"99999"}}
	if rec := a.do(http.MethodPost, "/preferences", port, session, nil); rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `value="99999"`) {
		t.Fatalf("invalid mailbox port %d", rec.Code)
	}

	// Every field on /profile has its own id, so each label names its field.
	ids := map[string]int{}
	for _, m := range regexp.MustCompile(` id="([^"]+)"`).FindAllStringSubmatch(a.do(http.MethodGet, "/profile", nil, session, nil).Body.String(), -1) {
		if ids[m[1]]++; ids[m[1]] == 2 {
			t.Errorf("duplicate id %q on /profile", m[1])
		}
	}

	// Station management on /profile
	expForm := url.Values{
		"title":          {"Staff Platform Engineer"},
		"company":        {"Acme Corp"},
		"location":       {"Munich"},
		"workplace_type": {"hybrid"},
		"start_date":     {"2022-04"},
		"is_current":     {"on"},
		"bullets":        {"Built scalable infra\nImproved uptime to 99.99%"},
	}
	rec = a.do(http.MethodPost, "/profile/experiences", expForm, session, nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("add experience failed: %d", rec.Code)
	}
	// The page after adding names the new entry's first field, which the shell focuses.
	loc := rec.Header().Get("Location")
	body = a.do(http.MethodGet, loc, nil, session, nil).Body.String()
	m := regexp.MustCompile(`data-focus-field="(f-title-[0-9a-f-]{36})"`).FindStringSubmatch(body)
	if loc != "/profile?saved=1&added=experience" || m == nil || !strings.Contains(body, `id="`+m[1]+`"`) {
		t.Errorf("add experience: Location %q, focus %v", loc, m)
	}
	// An empty entry comes back on the profile page instead of the error page.
	if rec := a.do(http.MethodPost, "/profile/experiences", url.Values{"title": {""}}, session, nil); rec.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(rec.Body.String(), "was not saved") {
		t.Errorf("empty experience %d", rec.Code)
	}
	for _, want := range []string{"Staff Platform Engineer", "Acme Corp", "Munich", "Built scalable infra"} {
		if !strings.Contains(body, want) {
			t.Errorf("profile page lacks experience field %q", want)
		}
	}
	// Existing stations come first, named by title and company; the add form follows them.
	head, add := strings.Index(body, "<h3>Staff Platform Engineer · Acme Corp</h3>"), strings.Index(body, `action="/profile/experiences" class="stack"`)
	if head < 0 || add < head {
		t.Errorf("station heading at %d, add form at %d", head, add)
	}

	eduForm := url.Values{
		"degree":         {"M.Sc. Computer Science"},
		"institution":    {"TU Berlin"},
		"field_of_study": {"Software Systems"},
		"start_date":     {"2018"},
		"end_date":       {"2020"},
		"grade":          {"1.2"},
	}
	if rec := a.do(http.MethodPost, "/profile/educations", eduForm, session, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("add education failed: %d", rec.Code)
	}
	body = a.do(http.MethodGet, "/profile", nil, session, nil).Body.String()
	for _, want := range []string{"M.Sc. Computer Science", "TU Berlin", "Software Systems", "1.2"} {
		if !strings.Contains(body, want) {
			t.Errorf("profile page lacks education field %q", want)
		}
	}
}

func TestProfileStations(t *testing.T) {
	a := newApp(t)
	session := signedIn(t, a)

	// Add two work experiences
	exp1 := url.Values{
		"title":          {"Junior Dev"},
		"company":        {"Company 1"},
		"start_date":     {"2019"},
		"end_date":       {"2021"},
		"workplace_type": {"onsite"},
	}
	if rec := a.do(http.MethodPost, "/profile/experiences", exp1, session, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("add exp1: %d", rec.Code)
	}

	exp2 := url.Values{
		"title":          {"Senior Dev"},
		"company":        {"Company 2"},
		"start_date":     {"2021"},
		"is_current":     {"on"},
		"workplace_type": {"remote"},
	}
	if rec := a.do(http.MethodPost, "/profile/experiences", exp2, session, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("add exp2: %d", rec.Code)
	}

	body := a.do(http.MethodGet, "/profile", nil, session, nil).Body.String()
	m := regexp.MustCompile(`/profile/experiences/([a-f0-9-]+)`).FindAllStringSubmatch(body, -1)
	if len(m) < 2 {
		t.Fatalf("expected at least 2 experience actions in body, found %d", len(m))
	}
	id1, id2 := m[0][1], m[1][1]

	// Move second experience up
	if rec := a.do(http.MethodPost, "/profile/experiences/"+id2+"/up", nil, session, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("move up: %d", rec.Code)
	}

	// Update first experience
	exp1Update := url.Values{
		"title":          {"Lead Dev"},
		"company":        {"Company 1 Updated"},
		"start_date":     {"2019"},
		"end_date":       {"2021"},
		"workplace_type": {"hybrid"},
	}
	if rec := a.do(http.MethodPost, "/profile/experiences/"+id1, exp1Update, session, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("update exp1: %d", rec.Code)
	}

	// Delete first experience
	if rec := a.do(http.MethodPost, "/profile/experiences/"+id1+"/delete", nil, session, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("delete exp1: %d", rec.Code)
	}

	body = a.do(http.MethodGet, "/profile", nil, session, nil).Body.String()
	if strings.Contains(body, "Company 1 Updated") {
		t.Error("deleted experience should not be in body")
	}
	if !strings.Contains(body, "Company 2") {
		t.Error("exp2 should still be in body")
	}

	// Add, move, update, delete education
	edu1 := url.Values{
		"degree":      {"B.Sc."},
		"institution": {"Uni 1"},
		"start_date":  {"2015"},
		"end_date":    {"2018"},
	}
	if rec := a.do(http.MethodPost, "/profile/educations", edu1, session, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("add edu1: %d", rec.Code)
	}
	edu2 := url.Values{
		"degree":      {"M.Sc."},
		"institution": {"Uni 2"},
		"start_date":  {"2018"},
		"end_date":    {"2020"},
	}
	if rec := a.do(http.MethodPost, "/profile/educations", edu2, session, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("add edu2: %d", rec.Code)
	}

	body = a.do(http.MethodGet, "/profile", nil, session, nil).Body.String()
	eduMatches := regexp.MustCompile(`/profile/educations/([a-f0-9-]+)`).FindAllStringSubmatch(body, -1)
	if len(eduMatches) < 2 {
		t.Fatalf("expected at least 2 education actions, got %d", len(eduMatches))
	}
	eduID1, eduID2 := eduMatches[0][1], eduMatches[1][1]

	// Move edu2 up
	if rec := a.do(http.MethodPost, "/profile/educations/"+eduID2+"/up", nil, session, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("move edu2 up: %d", rec.Code)
	}

	// Delete edu1
	if rec := a.do(http.MethodPost, "/profile/educations/"+eduID1+"/delete", nil, session, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("delete edu1: %d", rec.Code)
	}

	body = a.do(http.MethodGet, "/profile", nil, session, nil).Body.String()
	if strings.Contains(body, "Uni 1") {
		t.Error("deleted education should not be in body")
	}
	if !strings.Contains(body, "Uni 2") {
		t.Error("edu2 should still be in body")
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

// TestMain lets the test binary act as the CV text child process.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == cvimport.ChildCommand {
		os.Exit(cvimport.RunChild(os.Args[2:], os.Stdin, os.Stdout))
	}
	os.Exit(m.Run())
}

func cvDocx(t *testing.T, lines ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("[Content_Types].xml")
	_, _ = w.Write([]byte("<Types/>"))
	w, _ = zw.Create("word/document.xml")
	_, _ = w.Write([]byte(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>`))
	for _, l := range lines {
		_, _ = w.Write([]byte("<w:p><w:r><w:t>" + l + "</w:t></w:r></w:p>"))
	}
	_, _ = w.Write([]byte("</w:body></w:document>"))
	_ = zw.Close()
	return buf.Bytes()
}

func TestCVImport(t *testing.T) {
	a := newApp(t)
	session := signedIn(t, a)
	if rec := a.do(http.MethodPost, "/profile", url.Values{"version": {"0"}, "first_name": {"Maximilian"}}, session, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("profile %d", rec.Code)
	}
	cv := cvDocx(t, "Max Mustermann", "10115 Berlin", "max.mustermann@example.org | +49 30 1234567",
		"Summary", "Platform engineer with eight years of experience in Go, Kubernetes and PostgreSQL in production.",
		"Experience", "Senior Platform Engineer at Muster GmbH 01/2021 - present", "Skills", "Go, Kubernetes, PostgreSQL",
		"Languages", "German: native", "English: C1")
	if rec := a.upload("cv", "cv.docx", cv, session); rec.Code != http.StatusSeeOther {
		t.Fatalf("upload %d", rec.Code)
	}
	page := a.do(http.MethodGet, "/documents", nil, session, nil).Body.String()
	i := strings.Index(page, `formaction="/documents/`)
	if i < 0 {
		t.Fatalf("no import button: %s", page)
	}
	path := page[i+len(`formaction="`):i+len(`formaction="`)+len("/documents/")+36] + "/import"
	rec := a.do(http.MethodPost, path, url.Values{}, session, nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/profile?imported=1" {
		t.Fatalf("import %d: %s", rec.Code, rec.Body.String())
	}
	body := a.do(http.MethodGet, "/profile?imported=1", nil, session, nil).Body.String()
	for _, want := range []string{`value="Maximilian"`, `value="Mustermann"`, `value="max.mustermann@example.org"`, `value="Berlin"`,
		"Go\nKubernetes\nPostgreSQL", "German: native", `value="Senior Platform Engineer"`, "nothing you entered was changed"} {
		if !strings.Contains(body, want) {
			t.Errorf("profile lacks %q", want)
		}
	}
	// A second import has nothing to add.
	if rec := a.do(http.MethodPost, path, url.Values{}, session, nil); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "nothing to add") {
		t.Errorf("second import %d", rec.Code)
	}
}

func TestCVReviewAndImport(t *testing.T) {
	a := newApp(t)
	session := signedIn(t, a)
	cv := cvDocx(t, "Max Mustermann", "10115 Berlin", "max.mustermann@example.org | +49 30 1234567",
		"Summary", "Staff Platform Engineer with extensive Go and Kubernetes experience.",
		"Experience", "Lead Platform Engineer at Muster Cloud GmbH | Berlin, Germany 02/2021 - present",
		"• Built cloud developer platform",
		"Education", "M.Sc. Computer Science, TU Berlin 2016 - 2018",
		"Skills", "Go, Kubernetes, PostgreSQL")
	if rec := a.upload("cv", "cv.docx", cv, session); rec.Code != http.StatusSeeOther {
		t.Fatalf("upload %d", rec.Code)
	}
	page := a.do(http.MethodGet, "/documents", nil, session, nil).Body.String()
	idx := strings.Index(page, `href="/documents/`)
	if idx < 0 {
		t.Fatalf("no document in page: %s", page)
	}
	raw := page[idx+len(`href="/documents/`):]
	if len(raw) < 36 {
		t.Fatalf("short raw: %s", raw)
	}
	docID := raw[:36]
	if _, err := uuid.Parse(docID); err != nil {
		t.Fatalf("invalid doc id %q: %v", docID, err)
	}

	// 1. GET /documents/{id}/review renders review page
	rev := a.do(http.MethodGet, "/documents/"+docID+"/review", nil, session, nil)
	if rev.Code != http.StatusOK {
		t.Fatalf("review get %d: %s", rev.Code, rev.Body.String())
	}
	revBody := rev.Body.String()
	for _, want := range []string{"Lead Platform Engineer", "Muster Cloud GmbH", "TU Berlin", "M.Sc.", "Computer Science", "Built cloud developer platform"} {
		if !strings.Contains(revBody, want) {
			t.Errorf("review page lacks %q", want)
		}
	}

	// 2. POST /documents/{id}/review submits confirmed entries
	form := url.Values{
		"exp_count":         {"1"},
		"include_exp_0":     {"1"},
		"exp_0_title":       {"Lead Platform Engineer"},
		"exp_0_company":     {"Muster Cloud GmbH"},
		"exp_0_location":    {"Berlin, Germany"},
		"exp_0_workplace":   {"remote"},
		"exp_0_start":       {"2021-02"},
		"exp_0_end":         {""},
		"exp_0_current":     {"1"},
		"exp_0_bullets":     {"Built cloud developer platform\nCut infrastructure cost"},
		"edu_count":         {"1"},
		"include_edu_0":     {"1"},
		"edu_0_institution": {"TU Berlin"},
		"edu_0_degree":      {"M.Sc."},
		"edu_0_field":       {"Computer Science"},
		"edu_0_grade":       {"1.3"},
		"edu_0_start":       {"2016"},
		"edu_0_end":         {"2018"},
		"include_profile":   {"1"},
	}
	saveRec := a.do(http.MethodPost, "/documents/"+docID+"/review", form, session, nil)
	if saveRec.Code != http.StatusSeeOther || saveRec.Header().Get("Location") != "/profile?imported=1" {
		t.Fatalf("review post %d: %s", saveRec.Code, saveRec.Body.String())
	}

	// 3. GET /profile verifies profile has stations and imported details
	profBody := a.do(http.MethodGet, "/profile?imported=1", nil, session, nil).Body.String()
	for _, want := range []string{"Lead Platform Engineer", "Muster Cloud GmbH", "TU Berlin", "Computer Science", "Built cloud developer platform"} {
		if !strings.Contains(profBody, want) {
			t.Errorf("profile lacks %q", want)
		}
	}

	// 4. An entry the profile refuses is reported, not dropped without a word.
	refused := url.Values{"exp_count": {"1"}, "include_exp_0": {"1"}, "exp_0_title": {strings.Repeat("x", 1000)},
		"exp_0_bullets": {strings.Repeat("bullet\n", 51)}}
	if rec := a.do(http.MethodPost, "/documents/"+docID+"/review", refused, session, nil); rec.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(rec.Body.String(), "were not saved") {
		t.Errorf("refused entry %d: %s", rec.Code, rec.Body.String())
	}
}

func (a *app) uploadTo(path, name string, data []byte, cookie string) *httptest.ResponseRecorder {
	a.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("kind", "cv")
	fw, _ := mw.CreateFormFile("file", name)
	_, _ = fw.Write(data)
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "https://jobs.example.org"+path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.AddCookie(&http.Cookie{Name: web.SessionCookie, Value: cookie})
	rec := httptest.NewRecorder()
	a.h.ServeHTTP(rec, req)
	return rec
}

func TestSetupAssistant(t *testing.T) {
	a := newApp(t)
	session := signedIn(t, a)
	if home := a.do(http.MethodGet, "/", nil, session, nil).Body.String(); !strings.Contains(home, `href="/setup"`) {
		t.Fatal("no setup link on an empty profile")
	}
	resume := func(want string) {
		t.Helper()
		if rec := a.do(http.MethodGet, "/setup", nil, session, nil); rec.Header().Get("Location") != want {
			t.Fatalf("resume at %s, want %s", rec.Header().Get("Location"), want)
		}
	}
	resume("/setup/cv")
	cv := cvDocx(t, "Max Mustermann", "10115 Berlin", "max.mustermann@example.org | +49 30 1234567",
		"Summary", "Platform engineer with eight years of experience in Go, Kubernetes and PostgreSQL in production.",
		"Experience", "Senior Platform Engineer at Muster GmbH 01/2021 - present", "Backend Developer, Beispiel AG 2017 - 2020",
		"Skills", "Go, Kubernetes")
	rec := a.uploadTo("/setup/cv", "cv.docx", cv, session)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/setup/profile?imported=1" {
		t.Fatalf("cv step %d %s: %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	resume("/setup/profile")

	// The profile step shows what the import found, positions included, and the progress.
	page := a.do(http.MethodGet, "/setup/profile?imported=1", nil, session, nil).Body.String()
	for _, want := range []string{`value="Mustermann"`, `aria-current="step"`, "Step 2 of 5", "Senior Platform Engineer, Muster GmbH"} {
		if !strings.Contains(page, want) {
			t.Fatalf("profile step lacks %q: %s", want, page)
		}
	}
	version := func(body string) string {
		_, rest, ok := strings.Cut(body, `name="version" value="`)
		v, _, _ := strings.Cut(rest, `"`)
		if !ok {
			t.Fatalf("no version field: %s", body)
		}
		return v
	}
	form := url.Values{"version": {version(page)}, "first_name": {"Max"}, "last_name": {"Mustermann"}, "email": {"bad"}, "phone": {"+49 30 1234567"}}
	if rec := a.do(http.MethodPost, "/setup/profile", form, session, nil); rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `value="bad"`) {
		t.Fatalf("invalid profile %d", rec.Code)
	}
	form.Set("email", "max.mustermann@example.org")
	for k, v := range map[string]string{"city": "Berlin", "current_title": "Platform Engineer", "years_total": "8", "notice_period": "3 months",
		"earliest_start": "2027-01-01", "salary_expectation": "80000", "skills": "Go\nKubernetes"} {
		form.Set(k, v)
	}
	if rec := a.do(http.MethodPost, "/setup/profile", form, session, nil); rec.Header().Get("Location") != "/setup/search" {
		t.Fatalf("profile save %d: %s", rec.Code, rec.Body.String())
	}
	resume("/setup/search")
	// The first search starts from the profile's current title and city.
	if body := a.do(http.MethodGet, "/setup/search", nil, session, nil).Body.String(); !strings.Contains(body, "Platform Engineer</textarea>") || !strings.Contains(body, "Berlin</textarea>") {
		t.Fatalf("search step not prefilled from the profile: %s", body)
	}

	if rec := a.do(http.MethodPost, "/setup/search", url.Values{"titles": {"Platform Engineer"}, "remote": {"on"}, "min_salary": {"x"}}, session, nil); rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `value="x"`) {
		t.Fatalf("invalid salary %d", rec.Code)
	}
	if rec := a.do(http.MethodPost, "/setup/search", url.Values{"titles": {"Platform Engineer"}, "remote": {"on"}, "min_salary": {"70000"}}, session, nil); rec.Header().Get("Location") != "/setup/automation" {
		t.Fatalf("search save %d", rec.Code)
	}
	resume("/setup/matches")

	// Automation stores the level only.
	if rec := a.do(http.MethodPost, "/setup/automation", url.Values{"autonomy_level": {"bogus"}}, session, nil); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid level %d", rec.Code)
	}
	if rec := a.do(http.MethodPost, "/setup/automation", url.Values{"autonomy_level": {"suggest"}}, session, nil); rec.Header().Get("Location") != "/setup/matches" {
		t.Fatalf("automation save %d", rec.Code)
	}
	if page := a.do(http.MethodGet, "/setup/automation", nil, session, nil).Body.String(); !strings.Contains(page, `value="suggest" checked`) {
		t.Fatal("level not stored")
	}

	matches := a.do(http.MethodGet, "/setup/matches", nil, session, nil).Body.String()
	for _, want := range []string{`id="setup-matches"`, "Step 5 of 5", `href="/jobs"`, "Add a passkey"} {
		if !strings.Contains(matches, want) {
			t.Errorf("matches step lacks %q", want)
		}
	}
	if rec := a.do(http.MethodGet, "/setup/history", nil, session, nil); rec.Code != http.StatusPermanentRedirect || rec.Header().Get("Location") != "/setup/profile" {
		t.Errorf("old step %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if rec := a.do(http.MethodGet, "/setup/nonsense", nil, session, nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown step %d", rec.Code)
	}
}

func TestPrivacyExportAndDelete(t *testing.T) {
	a := newApp(t)

	// 1. GET /privacy is public and rendered in German and English
	enPage := a.do(http.MethodGet, "/privacy", nil, "", map[string]string{"Accept-Language": "en"}).Body.String()
	if !strings.Contains(enPage, "Privacy notice") || !strings.Contains(enPage, "envelope encryption") {
		t.Errorf("privacy page EN missing expected headings: %s", enPage)
	}
	dePage := a.do(http.MethodGet, "/privacy", nil, "", map[string]string{"Accept-Language": "de"}).Body.String()
	if !strings.Contains(dePage, "Datenschutzerklärung") || !strings.Contains(dePage, "Envelope-Verschlüsselung") {
		t.Errorf("privacy page DE missing expected headings: %s", dePage)
	}

	// 2. Unauthenticated access to /account/export and /account/delete redirects to login
	if rec := a.do(http.MethodGet, "/account/export", nil, "", nil); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Fatalf("export unauthenticated = %d, want 303 to /login", rec.Code)
	}
	if rec := a.do(http.MethodPost, "/account/delete", nil, "", nil); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Fatalf("delete unauthenticated = %d, want 303 to /login", rec.Code)
	}

	// 3. Sign in a user via invitation
	link, _, err := a.ids.Invite(t.Context(), identity.InviteRequest{Email: "privacy-user@example.com", Role: identity.RoleUser})
	if err != nil {
		t.Fatal(err)
	}
	path := strings.TrimPrefix(link, "https://jobs.example.org")
	rec := a.do(http.MethodPost, path, url.Values{"name": {"Privacy Tester"}, "locale": {"en"}}, "", nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("invite accept: %d", rec.Code)
	}
	session := sessionFrom(t, rec)

	// 4. View account page and verify privacy section is present
	accPage := a.do(http.MethodGet, "/settings/account", nil, session, nil).Body.String()
	if !strings.Contains(accPage, "Privacy and your data") {
		t.Errorf("account page lacks privacy section: %s", accPage)
	}

	// 5. GET /account/export returns a valid ZIP archive
	exportRec := a.do(http.MethodGet, "/account/export", nil, session, nil)
	if exportRec.Code != http.StatusOK {
		t.Fatalf("export = %d, want 200", exportRec.Code)
	}
	if ct := exportRec.Header().Get("Content-Type"); ct != "application/zip" {
		t.Errorf("export Content-Type = %q, want application/zip", ct)
	}
	bodyBytes := exportRec.Body.Bytes()
	zr, err := zip.NewReader(bytes.NewReader(bodyBytes), int64(len(bodyBytes)))
	if err != nil {
		t.Fatalf("zip reader failed: %v", err)
	}
	foundFiles := map[string]bool{}
	for _, f := range zr.File {
		foundFiles[f.Name] = true
	}
	for _, expected := range []string{"profile.json", "applications.json", "matches.json", "settings.json", "audit_events.json"} {
		if !foundFiles[expected] {
			t.Errorf("export archive missing expected file: %s", expected)
		}
	}

	// 6. A session older than SessionFresh must sign in again first
	a.ids.WithClock(func() time.Time { return time.Now().Add(identity.SessionFresh) })
	if rec := a.do(http.MethodPost, "/account/delete", nil, session, nil); rec.Header().Get("Location") != "/login?reauth=1" {
		t.Fatalf("stale delete = %d, loc = %q", rec.Code, rec.Header().Get("Location"))
	}
	a.ids.WithClock(time.Now)

	// 7. POST /account/delete removes user, clears cookie, redirects to /login?deleted=1
	delRec := a.do(http.MethodPost, "/account/delete", nil, session, nil)
	if delRec.Code != http.StatusSeeOther || delRec.Header().Get("Location") != "/login?deleted=1" {
		t.Fatalf("delete account = %d, loc = %q", delRec.Code, delRec.Header().Get("Location"))
	}
	var cleared bool
	for _, c := range delRec.Result().Cookies() {
		if c.Name == web.SessionCookie && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Errorf("session cookie was not cleared on deletion")
	}

	// 8. Login page with ?deleted=1 shows deletion notice
	loginPage := a.do(http.MethodGet, "/login?deleted=1", nil, "", nil).Body.String()
	if !strings.Contains(loginPage, "permanently deleted") {
		t.Errorf("login page lacks deletion notice: %s", loginPage)
	}
}

// testCrawlQueue accepts every manual crawl.
type testCrawlQueue struct{}

func (testCrawlQueue) QueueCrawl(context.Context) (bool, error) { return true, nil }
func (testCrawlQueue) IsQueued(context.Context) (bool, error)   { return false, nil }
