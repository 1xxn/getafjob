package pgstore_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/identity/adapters/pgstore"
	"github.com/1xxn/getafjob/internal/identity/softauthn"
	"github.com/1xxn/getafjob/internal/platform/db"
	"github.com/1xxn/getafjob/internal/platform/db/dbtest"
	"github.com/1xxn/getafjob/internal/platform/i18n"
)

type fakeMailer struct {
	mu   sync.Mutex
	sent []identity.Mail
	fail error // returned by Send when set
}

func (m *fakeMailer) Send(_ context.Context, mail identity.Mail) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail != nil {
		return m.fail
	}
	m.sent = append(m.sent, mail)
	return nil
}

func (m *fakeMailer) last(t *testing.T) identity.Mail {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.sent) == 0 {
		t.Fatal("no mail sent")
	}
	return m.sent[len(m.sent)-1]
}

func (m *fakeMailer) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sent)
}

type env struct {
	pool *pgxpool.Pool
	svc  *identity.Service
	mail *fakeMailer
	now  time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{pool: dbtest.AppPool(t), mail: &fakeMailer{}, now: time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)}
	base, _ := url.Parse("https://jobs.example.org")
	e.svc = identity.NewService(pgstore.New(e.pool), e.mail, base).WithClock(func() time.Time { return e.now })
	return e
}

// tokenFrom returns the last path segment of a link.
func tokenFrom(link string) string { return link[strings.LastIndex(link, "/")+1:] }

func linkIn(t *testing.T, text string) string {
	t.Helper()
	for _, f := range strings.Fields(text) {
		if strings.HasPrefix(f, "https://jobs.example.org/") {
			return f
		}
	}
	t.Fatalf("no link in %q", text)
	return ""
}

func TestInvitationFlow(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	link, expires, err := e.svc.Invite(ctx, identity.InviteRequest{Email: "Max.Mustermann@Example.com", Role: identity.RoleAdmin, Send: true, Locale: i18n.DE})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(link, "https://jobs.example.org/invite/") || !expires.Equal(e.now.Add(identity.InvitationTTL)) {
		t.Fatalf("link %q expires %v", link, expires)
	}
	if m := e.mail.last(t); m.To != "max.mustermann@example.com" || !strings.Contains(m.Subject, "Einladung") || linkIn(t, m.Text) != link {
		t.Fatalf("mail = %+v", m)
	}
	inv, err := e.svc.PendingInvitation(ctx, tokenFrom(link))
	if err != nil || inv.Email != "max.mustermann@example.com" {
		t.Fatalf("pending = %+v, %v", inv, err)
	}

	u, session, err := e.svc.AcceptInvitation(ctx, tokenFrom(link), "  Max  Mustermann ", i18n.DE)
	if err != nil {
		t.Fatal(err)
	}
	if u.Role != identity.RoleAdmin || u.DisplayName != "Max Mustermann" || u.Locale != i18n.DE {
		t.Fatalf("user = %+v", u)
	}
	got, _, err := e.svc.Authenticate(ctx, string(session))
	if err != nil || got.ID != u.ID {
		t.Fatalf("authenticate = %+v, %v", got, err)
	}

	// One-time: a second accept and a new invitation for the same address fail.
	if _, _, err := e.svc.AcceptInvitation(ctx, tokenFrom(link), "x", i18n.EN); !errors.Is(err, identity.ErrInvalidToken) {
		t.Fatalf("second accept: %v", err)
	}
	if _, _, err := e.svc.Invite(ctx, identity.InviteRequest{Email: "max.mustermann@example.com", Role: identity.RoleUser}); !errors.Is(err, identity.ErrEmailTaken) {
		t.Fatalf("invite existing: %v", err)
	}

	if err := e.svc.SignOut(ctx, string(session), u.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.Authenticate(ctx, string(session)); !errors.Is(err, identity.ErrSessionExpired) {
		t.Fatalf("after sign-out: %v", err)
	}
}

func TestExpiredInvitation(t *testing.T) {
	e := newEnv(t)
	link, _, err := e.svc.Invite(t.Context(), identity.InviteRequest{Email: "max@example.com", Role: identity.RoleUser})
	if err != nil {
		t.Fatal(err)
	}
	e.now = e.now.Add(identity.InvitationTTL)
	if _, _, err := e.svc.AcceptInvitation(t.Context(), tokenFrom(link), "Max", i18n.EN); !errors.Is(err, identity.ErrInvalidToken) {
		t.Fatalf("expired accept: %v", err)
	}
}

func signedUp(t *testing.T, e *env, email string) identity.User {
	t.Helper()
	link, _, err := e.svc.Invite(t.Context(), identity.InviteRequest{Email: email, Role: identity.RoleUser})
	if err != nil {
		t.Fatal(err)
	}
	u, _, err := e.svc.AcceptInvitation(t.Context(), tokenFrom(link), "Max Mustermann", i18n.EN)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestMagicLinkFlow(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	u := signedUp(t, e, "max@example.com")

	// Unknown and malformed addresses get the same answer and no mail.
	for _, addr := range []string{"nobody@example.com", "not an address"} {
		if err := e.svc.RequestMagicLink(ctx, addr); err != nil {
			t.Fatalf("request for %q: %v", addr, err)
		}
	}
	if e.mail.count() != 0 {
		t.Fatalf("mails for unknown addresses: %d", e.mail.count())
	}

	if err := e.svc.RequestMagicLink(ctx, "MAX@example.com"); err != nil {
		t.Fatal(err)
	}
	m := e.mail.last(t)
	link := linkIn(t, m.Text)
	if m.To != "max@example.com" || !strings.HasPrefix(link, "https://jobs.example.org/login/link/") || !strings.Contains(m.Text, "10 minutes") {
		t.Fatalf("mail = %+v", m)
	}
	got, session, err := e.svc.SignInWithMagicLink(ctx, tokenFrom(link))
	if err != nil || got.ID != u.ID {
		t.Fatalf("sign in = %+v, %v", got, err)
	}
	if _, _, err := e.svc.Authenticate(ctx, string(session)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.SignInWithMagicLink(ctx, tokenFrom(link)); !errors.Is(err, identity.ErrInvalidToken) {
		t.Fatalf("reused link: %v", err)
	}

	// Expired link.
	if err := e.svc.RequestMagicLink(ctx, "max@example.com"); err != nil {
		t.Fatal(err)
	}
	expired := tokenFrom(linkIn(t, e.mail.last(t).Text))
	e.now = e.now.Add(identity.MagicLinkTTL + time.Second)
	if _, _, err := e.svc.SignInWithMagicLink(ctx, expired); !errors.Is(err, identity.ErrInvalidToken) {
		t.Fatalf("expired link: %v", err)
	}
}

func TestMagicLinksAreRateLimited(t *testing.T) {
	e := newEnv(t)
	signedUp(t, e, "max@example.com")
	for range 5 {
		if err := e.svc.RequestMagicLink(t.Context(), "max@example.com"); err != nil {
			t.Fatal(err)
		}
	}
	if e.mail.count() != 3 {
		t.Fatalf("sent %d mails, want 3", e.mail.count())
	}
	e.now = e.now.Add(16 * time.Minute)
	if err := e.svc.RequestMagicLink(t.Context(), "max@example.com"); err != nil || e.mail.count() != 4 {
		t.Fatalf("after window: %d mails, %v", e.mail.count(), err)
	}
}

func TestDisabledUser(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	u := signedUp(t, e, "max@example.com")
	if err := e.svc.RequestMagicLink(ctx, "max@example.com"); err != nil {
		t.Fatal(err)
	}
	link := tokenFrom(linkIn(t, e.mail.last(t).Text))
	_, session, err := e.svc.SignInWithMagicLink(ctx, link)
	if err != nil {
		t.Fatal(err)
	}
	err = db.InSystemTx(ctx, e.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "UPDATE users SET disabled_at = now() WHERE id = $1", u.ID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.Authenticate(ctx, string(session)); !errors.Is(err, identity.ErrUserDisabled) {
		t.Fatalf("disabled session: %v", err)
	}
	before := e.mail.count()
	if err := e.svc.RequestMagicLink(ctx, "max@example.com"); err != nil || e.mail.count() != before {
		t.Fatalf("disabled user got a link: %v", err)
	}
}

func TestSessionIdleTimeoutAndTouch(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	link, _, _ := e.svc.Invite(ctx, identity.InviteRequest{Email: "max@example.com", Role: identity.RoleUser})
	_, session, err := e.svc.AcceptInvitation(ctx, tokenFrom(link), "Max", i18n.EN)
	if err != nil {
		t.Fatal(err)
	}
	// Activity every six days keeps the session alive until the absolute limit (following its rotated token).
	for i := 0; i < 4; i++ {
		e.now = e.now.Add(6 * 24 * time.Hour)
		_, next, err := e.svc.Authenticate(ctx, string(session))
		if next != "" {
			session = next
		}
		if err != nil {
			t.Fatalf("day %d: %v", (i+1)*6, err)
		}
	}
	e.now = e.now.Add(7 * 24 * time.Hour)
	if _, _, err := e.svc.Authenticate(ctx, string(session)); !errors.Is(err, identity.ErrSessionExpired) {
		t.Fatalf("after idle period: %v", err)
	}
	if _, _, err := e.svc.Authenticate(ctx, "garbage"); !errors.Is(err, identity.ErrSessionExpired) {
		t.Fatalf("garbage cookie: %v", err)
	}
}

// TestAuditLogHasNoPersonalData: audit params never contain addresses or names.
func TestAuditLogHasNoPersonalData(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	signedUp(t, e, "max.mustermann@example.com")
	_ = e.svc.RequestMagicLink(ctx, "max.mustermann@example.com")
	var codes []string
	err := db.InSystemTx(ctx, e.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, "SELECT code, params::text FROM audit_events ORDER BY id")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var code, params string
			if err := rows.Scan(&code, &params); err != nil {
				return err
			}
			if strings.Contains(params, "example.com") || strings.Contains(params, "Mustermann") {
				t.Errorf("audit %s contains personal data: %s", code, params)
			}
			codes = append(codes, code)
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"identity.invitation_created", "identity.invitation_accepted", "identity.magic_link_requested"}
	if strings.Join(codes, ",") != strings.Join(want, ",") {
		t.Fatalf("audit codes = %v", codes)
	}
}

func TestCleanupRemovesExpired(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	link, _, _ := e.svc.Invite(ctx, identity.InviteRequest{Email: "max@example.com", Role: identity.RoleUser})
	_, session, _ := e.svc.AcceptInvitation(ctx, tokenFrom(link), "Max", i18n.EN)
	e.now = e.now.Add(identity.SessionMaxAge + time.Hour)
	if err := e.svc.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	var n int
	err := db.InSystemTx(ctx, e.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT count(*) FROM sessions").Scan(&n)
	})
	if err != nil || n != 0 {
		t.Fatalf("sessions left: %d, %v", n, err)
	}
	_ = session
}

// signIn creates Max Mustermann and returns a fresh session token.
func signIn(t *testing.T, e *env) (identity.User, identity.Token) {
	t.Helper()
	link, _, err := e.svc.Invite(t.Context(), identity.InviteRequest{Email: "max.mustermann@example.com", Role: identity.RoleUser})
	if err != nil {
		t.Fatal(err)
	}
	u, session, err := e.svc.AcceptInvitation(t.Context(), tokenFrom(link), "Max Mustermann", i18n.EN)
	if err != nil {
		t.Fatal(err)
	}
	return u, session
}

func TestSessionRotation(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	u, first := signIn(t, e)
	start := e.now

	if _, next, err := e.svc.Authenticate(ctx, string(first)); err != nil || next != "" {
		t.Fatalf("fresh session rotated: %q, %v", next, err)
	}
	e.now = start.Add(identity.SessionRotate)
	_, second, err := e.svc.Authenticate(ctx, string(first))
	if err != nil || second == "" || second == first {
		t.Fatalf("rotation: %q, %v", second, err)
	}
	// A request in flight with the old token still works, and does not rotate again.
	if _, next, err := e.svc.Authenticate(ctx, string(first)); err != nil || next != "" {
		t.Fatalf("old token in grace: %q, %v", next, err)
	}
	e.now = e.now.Add(2 * time.Minute)
	if _, _, err := e.svc.Authenticate(ctx, string(first)); !errors.Is(err, identity.ErrSessionExpired) {
		t.Fatalf("old token after grace: %v", err)
	}
	if _, next, err := e.svc.Authenticate(ctx, string(second)); err != nil || next != "" {
		t.Fatalf("new token: %q, %v", next, err)
	}
	list, err := e.svc.Sessions(ctx, u.ID, string(second))
	if err != nil || len(list) != 1 || !list[0].Current || !list[0].CreatedAt.Equal(start) || list[0].Method != identity.MethodInvitation {
		t.Fatalf("sessions = %+v, %v (one session, sign-in time kept)", list, err)
	}

	// The absolute limit stays that of the sign-in, however often the token rotates.
	tok := second
	for e.now = e.now.Add(identity.SessionRotate); e.now.Before(start.Add(identity.SessionMaxAge)); e.now = e.now.Add(6 * 24 * time.Hour) {
		_, next, err := e.svc.Authenticate(ctx, string(tok))
		if err != nil {
			t.Fatalf("at %v: %v", e.now, err)
		}
		if next != "" {
			tok = next
		}
	}
	e.now = start.Add(identity.SessionMaxAge)
	if _, _, err := e.svc.Authenticate(ctx, string(tok)); !errors.Is(err, identity.ErrSessionExpired) {
		t.Fatalf("after the absolute limit: %v", err)
	}
}

func TestSignOutEndsRotatedTokens(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	u, first := signIn(t, e)
	e.now = e.now.Add(identity.SessionRotate)
	_, second, err := e.svc.Authenticate(ctx, string(first))
	if err != nil || second == "" {
		t.Fatal(err)
	}
	// Signing out with the old token (another tab) also ends the new one.
	if err := e.svc.SignOut(ctx, string(first), u.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.Authenticate(ctx, string(second)); !errors.Is(err, identity.ErrSessionExpired) {
		t.Fatalf("rotated token after sign-out: %v", err)
	}
}

func TestSignOutOthers(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	u, here := signIn(t, e)
	var others []identity.Token
	for range 2 {
		if err := e.svc.RequestMagicLink(ctx, "max.mustermann@example.com"); err != nil {
			t.Fatal(err)
		}
		_, s, err := e.svc.SignInWithMagicLink(ctx, tokenFrom(linkIn(t, e.mail.last(t).Text)))
		if err != nil {
			t.Fatal(err)
		}
		others = append(others, s)
		e.now = e.now.Add(time.Second)
	}
	if list, err := e.svc.Sessions(ctx, u.ID, string(here)); err != nil || len(list) != 3 {
		t.Fatalf("sessions = %+v, %v", list, err)
	}
	n, err := e.svc.SignOutOthers(ctx, u.ID, string(here))
	if err != nil || n != 2 {
		t.Fatalf("signed out %d, %v", n, err)
	}
	for _, o := range others {
		if _, _, err := e.svc.Authenticate(ctx, string(o)); !errors.Is(err, identity.ErrSessionExpired) {
			t.Errorf("other session still valid: %v", err)
		}
	}
	if _, _, err := e.svc.Authenticate(ctx, string(here)); err != nil {
		t.Errorf("own session ended: %v", err)
	}
	var code string
	if err := db.InSystemTx(ctx, e.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT code FROM audit_events ORDER BY id DESC LIMIT 1").Scan(&code)
	}); err != nil || code != "identity.signed_out_others" {
		t.Errorf("audit = %q, %v", code, err)
	}
}

func TestSignOutOthersNeedsFreshSignIn(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	u, here := signIn(t, e)
	e.now = e.now.Add(identity.SessionFresh)
	_, next, err := e.svc.Authenticate(ctx, string(here))
	if err != nil {
		t.Fatal(err)
	}
	if next != "" {
		here = next
	}
	if _, err := e.svc.SignOutOthers(ctx, u.ID, string(here)); !errors.Is(err, identity.ErrReauthRequired) {
		t.Fatalf("stale session: %v", err)
	}
	other, _ := signInAgain(t, e)
	if _, err := e.svc.SignOutOthers(ctx, uuid.New(), string(other)); !errors.Is(err, identity.ErrSessionExpired) {
		t.Fatalf("someone else's session: %v", err)
	}
	if n, err := e.svc.SignOutOthers(ctx, u.ID, string(other)); err != nil || n != 1 {
		t.Fatalf("after signing in again: %d, %v", n, err)
	}
}

func TestCheckDeletion(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	u, here := signIn(t, e)
	if err := e.svc.CheckDeletion(ctx, u.ID, string(here)); err != nil {
		t.Fatalf("fresh user session: %v", err)
	}
	link, _, err := e.svc.Invite(ctx, identity.InviteRequest{Email: "erika.mustermann@example.com", Role: identity.RoleAdmin})
	if err != nil {
		t.Fatal(err)
	}
	erika, _, err := e.svc.AcceptInvitation(ctx, tokenFrom(link), "Erika", i18n.EN)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetRole(ctx, erika.ID, u.ID, identity.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.CheckDeletion(ctx, u.ID, string(here)); err != nil {
		t.Fatalf("second admin exists: %v", err)
	}
	if err := e.svc.SetRole(ctx, u.ID, erika.ID, identity.RoleUser); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.CheckDeletion(ctx, u.ID, string(here)); !errors.Is(err, identity.ErrLastAdmin) {
		t.Fatalf("last admin: %v", err)
	}
	e.now = e.now.Add(identity.SessionFresh)
	if err := e.svc.CheckDeletion(ctx, u.ID, string(here)); !errors.Is(err, identity.ErrReauthRequired) {
		t.Fatalf("stale session: %v", err)
	}
}

// signInAgain signs Max in with a magic link and returns the session.
func signInAgain(t *testing.T, e *env) (identity.Token, identity.User) {
	t.Helper()
	if err := e.svc.RequestMagicLink(t.Context(), "max.mustermann@example.com"); err != nil {
		t.Fatal(err)
	}
	u, s, err := e.svc.SignInWithMagicLink(t.Context(), tokenFrom(linkIn(t, e.mail.last(t).Text)))
	if err != nil {
		t.Fatal(err)
	}
	return s, u
}

func TestSignOutUser(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	_, a := signIn(t, e)
	b, _ := signInAgain(t, e)
	if n, err := e.svc.SignOutUser(ctx, "Max.Mustermann@example.com"); err != nil || n != 2 {
		t.Fatalf("signed out %d, %v", n, err)
	}
	for _, tok := range []identity.Token{a, b} {
		if _, _, err := e.svc.Authenticate(ctx, string(tok)); !errors.Is(err, identity.ErrSessionExpired) {
			t.Errorf("session still valid: %v", err)
		}
	}
	if _, err := e.svc.SignOutUser(ctx, "erika@example.com"); !errors.Is(err, identity.ErrNotFound) {
		t.Errorf("unknown account: %v", err)
	}
}

func passkeyEnv(t *testing.T) *env {
	t.Helper()
	e := newEnv(t)
	base, _ := url.Parse("https://jobs.example.org")
	if err := e.svc.EnablePasskeys(base); err != nil {
		t.Fatal(err)
	}
	return e
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func register(t *testing.T, e *env, u identity.User, session identity.Token, key *softauthn.Key) error {
	t.Helper()
	opts, ch, err := e.svc.BeginPasskeyRegistration(t.Context(), u.ID, string(session))
	if err != nil {
		return err
	}
	resp, err := key.Create(mustJSON(t, opts))
	if err != nil {
		t.Fatal(err)
	}
	return e.svc.FinishPasskeyRegistration(t.Context(), u.ID, string(session), ch, " Laptop  of Max ", resp)
}

func login(t *testing.T, e *env, key *softauthn.Key) (identity.User, identity.Token, error) {
	t.Helper()
	opts, ch, err := e.svc.BeginPasskeyLogin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	resp, err := key.Get(mustJSON(t, opts))
	if err != nil {
		t.Fatal(err)
	}
	return e.svc.FinishPasskeyLogin(t.Context(), ch, resp)
}

func TestPasskeys(t *testing.T) {
	e := passkeyEnv(t)
	ctx := t.Context()
	u, session := signIn(t, e)
	key := softauthn.New("jobs.example.org", "https://jobs.example.org")
	if err := register(t, e, u, session, key); err != nil {
		t.Fatal(err)
	}
	keys, err := e.svc.Passkeys(ctx, u.ID)
	if err != nil || len(keys) != 1 || keys[0].Name != "Laptop of Max" || !bytes.Equal(keys[0].ID, key.ID) {
		t.Fatalf("passkeys = %+v, %v", keys, err)
	}
	// The same authenticator cannot register twice.
	if err := register(t, e, u, session, key); !errors.Is(err, identity.ErrPasskeyFailed) {
		t.Fatalf("second registration: %v", err)
	}

	got, s, err := login(t, e, key)
	if err != nil || got.ID != u.ID || s == "" {
		t.Fatalf("login = %+v, %q, %v", got, s, err)
	}
	list, _ := e.svc.Sessions(ctx, u.ID, string(s))
	if len(list) != 2 || !slices.ContainsFunc(list, func(x identity.SessionInfo) bool { return x.Current && x.Method == identity.MethodPasskey }) {
		t.Fatalf("sessions after passkey login = %+v", list)
	}
	if keys, _ := e.svc.Passkeys(ctx, u.ID); keys[0].LastUsed.IsZero() {
		t.Error("last use not recorded")
	}

	// A replayed challenge, a counter that went backwards, no user verification and a foreign origin are refused.
	opts, ch, _ := e.svc.BeginPasskeyLogin(ctx)
	resp, _ := key.Get(mustJSON(t, opts))
	if _, _, err := e.svc.FinishPasskeyLogin(ctx, ch, resp); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.FinishPasskeyLogin(ctx, ch, resp); !errors.Is(err, identity.ErrPasskeyFailed) {
		t.Errorf("replayed challenge: %v", err)
	}
	key.Count = 0
	if _, _, err := login(t, e, key); !errors.Is(err, identity.ErrPasskeyFailed) {
		t.Errorf("cloned authenticator: %v", err)
	}
	key.Count = 100
	key.NoUV = true
	if _, _, err := login(t, e, key); !errors.Is(err, identity.ErrPasskeyFailed) {
		t.Errorf("without user verification: %v", err)
	}
	key.NoUV = false
	key.Origin = "https://evil.example.org"
	if _, _, err := login(t, e, key); !errors.Is(err, identity.ErrPasskeyFailed) {
		t.Errorf("foreign origin: %v", err)
	}
	key.Origin = "https://jobs.example.org"
	stranger := softauthn.New("jobs.example.org", "https://jobs.example.org")
	stranger.UserHandle = u.ID[:]
	if _, _, err := login(t, e, stranger); !errors.Is(err, identity.ErrPasskeyFailed) {
		t.Errorf("unknown credential: %v", err)
	}
	// An expired challenge is refused.
	opts, ch, _ = e.svc.BeginPasskeyLogin(ctx)
	resp, _ = key.Get(mustJSON(t, opts))
	e.now = e.now.Add(6 * time.Minute)
	if _, _, err := e.svc.FinishPasskeyLogin(ctx, ch, resp); !errors.Is(err, identity.ErrPasskeyFailed) {
		t.Errorf("expired challenge: %v", err)
	}
}

func TestPasskeyChangesNeedFreshSignIn(t *testing.T) {
	e := passkeyEnv(t)
	ctx := t.Context()
	u, session := signIn(t, e)
	key := softauthn.New("jobs.example.org", "https://jobs.example.org")
	if err := register(t, e, u, session, key); err != nil {
		t.Fatal(err)
	}
	e.now = e.now.Add(identity.SessionFresh)
	_, next, err := e.svc.Authenticate(ctx, string(session))
	if err != nil {
		t.Fatal(err)
	}
	if next != "" {
		session = next
	}
	if err := register(t, e, u, session, softauthn.New("jobs.example.org", "https://jobs.example.org")); !errors.Is(err, identity.ErrReauthRequired) {
		t.Errorf("stale registration: %v", err)
	}
	if err := e.svc.DeletePasskey(ctx, u.ID, string(session), key.ID); !errors.Is(err, identity.ErrReauthRequired) {
		t.Errorf("stale removal: %v", err)
	}
	// Signing in with the passkey makes a fresh session that may remove it.
	_, fresh, err := login(t, e, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.DeletePasskey(ctx, u.ID, string(fresh), key.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.DeletePasskey(ctx, u.ID, string(fresh), key.ID); !errors.Is(err, identity.ErrNotFound) {
		t.Errorf("second removal: %v", err)
	}
	if _, _, err := login(t, e, key); !errors.Is(err, identity.ErrPasskeyFailed) {
		t.Errorf("removed passkey still signs in: %v", err)
	}
}

func TestRecoveryInsteadOfMagicLink(t *testing.T) {
	e := passkeyEnv(t)
	ctx := t.Context()
	u, session := signIn(t, e)
	start := e.now

	// A link requested before the passkey existed no longer signs in once there is one.
	if err := e.svc.RequestMagicLink(ctx, "max.mustermann@example.com"); err != nil {
		t.Fatal(err)
	}
	early := tokenFrom(linkIn(t, e.mail.last(t).Text))
	key := softauthn.New("jobs.example.org", "https://jobs.example.org")
	if err := register(t, e, u, session, key); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.SignInWithMagicLink(ctx, early); !errors.Is(err, identity.ErrRecoveryWait) {
		t.Fatalf("old link: %v", err)
	}
	if _, _, err := e.svc.SignInWithMagicLink(ctx, early); !errors.Is(err, identity.ErrInvalidToken) {
		t.Fatalf("old link is not spent: %v", err)
	}

	// A request starts the waiting period: a notice by mail, no link, and no second notice.
	mails := e.mail.count()
	if err := e.svc.RequestMagicLink(ctx, "max.mustermann@example.com"); err != nil {
		t.Fatal(err)
	}
	if m := e.mail.last(t); e.mail.count() != mails+1 || m.Subject != "Sign-in without your passkey requested" || strings.Contains(m.Text, "/login/link/") {
		t.Fatalf("recovery mail = %+v", m)
	}
	if err := e.svc.RequestMagicLink(ctx, "max.mustermann@example.com"); err != nil || e.mail.count() != mails+1 {
		t.Fatalf("second request mailed again: %v", err)
	}
	rec, err := e.svc.Recovery(ctx, u.ID)
	if err != nil || rec == nil || !rec.ReadyAt.Equal(start.Add(identity.RecoveryDelay)) {
		t.Fatalf("recovery = %+v, %v", rec, err)
	}

	// Signing in with the passkey cancels it.
	if _, _, err := login(t, e, key); err != nil {
		t.Fatal(err)
	}
	if rec, _ := e.svc.Recovery(ctx, u.ID); rec != nil {
		t.Fatalf("recovery after passkey sign-in: %+v", rec)
	}

	// After the waiting period a link works once, and the owner is told.
	if err := e.svc.RequestMagicLink(ctx, "max.mustermann@example.com"); err != nil {
		t.Fatal(err)
	}
	e.now = e.now.Add(identity.RecoveryDelay - time.Minute)
	mails = e.mail.count()
	if err := e.svc.RequestMagicLink(ctx, "max.mustermann@example.com"); err != nil || e.mail.count() != mails {
		t.Fatalf("request before the delay: mails %d, %v", e.mail.count()-mails, err)
	}
	e.now = e.now.Add(2 * time.Minute)
	if err := e.svc.RequestMagicLink(ctx, "max.mustermann@example.com"); err != nil {
		t.Fatal(err)
	}
	link := tokenFrom(linkIn(t, e.mail.last(t).Text))
	// Without the notice there is no recovery sign-in, and nothing is used up.
	e.mail.fail = errors.New("smtp down")
	if _, s, err := e.svc.SignInWithMagicLink(ctx, link); err == nil || s != "" {
		t.Fatalf("recovery sign-in without notice: %q, %v", s, err)
	}
	e.mail.fail = nil
	if rec, _ := e.svc.Recovery(ctx, u.ID); rec == nil {
		t.Fatal("failed notice used up the recovery")
	}
	if _, s, err := e.svc.SignInWithMagicLink(ctx, link); err != nil || s == "" {
		t.Fatalf("recovery sign-in: %v", err)
	}
	if m := e.mail.last(t); m.Subject != "Signed in without a passkey" {
		t.Fatalf("notice = %+v", m)
	}
	if rec, _ := e.svc.Recovery(ctx, u.ID); rec != nil {
		t.Fatalf("recovery not used up: %+v", rec)
	}

	// An expired recovery is replaced by a new one, with a new waiting period.
	if err := e.svc.RequestMagicLink(ctx, "max.mustermann@example.com"); err != nil {
		t.Fatal(err)
	}
	e.now = e.now.Add(identity.RecoveryDelay + identity.RecoveryWindow)
	if rec, _ := e.svc.Recovery(ctx, u.ID); rec != nil {
		t.Fatalf("expired recovery still shown: %+v", rec)
	}
	if err := e.svc.RequestMagicLink(ctx, "max.mustermann@example.com"); err != nil {
		t.Fatal(err)
	}
	if rec, _ := e.svc.Recovery(ctx, u.ID); rec == nil || !rec.ReadyAt.Equal(e.now.Add(identity.RecoveryDelay)) {
		t.Fatalf("new recovery = %+v", rec)
	}
	if ok, err := e.svc.CancelRecovery(ctx, u.ID); err != nil || !ok {
		t.Fatalf("cancel = %v, %v", ok, err)
	}
}
