package pgstore_test

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/identity/adapters/pgstore"
	"github.com/1xxn/getafjob/internal/platform/db"
	"github.com/1xxn/getafjob/internal/platform/db/dbtest"
	"github.com/1xxn/getafjob/internal/platform/i18n"
)

type fakeMailer struct {
	mu   sync.Mutex
	sent []identity.Mail
}

func (m *fakeMailer) Send(_ context.Context, mail identity.Mail) error {
	m.mu.Lock()
	defer m.mu.Unlock()
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
	got, err := e.svc.Authenticate(ctx, string(session))
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
	if _, err := e.svc.Authenticate(ctx, string(session)); !errors.Is(err, identity.ErrSessionExpired) {
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
	if m.To != "max@example.com" || !strings.HasPrefix(link, "https://jobs.example.org/login/link/") || !strings.Contains(m.Text, "15") {
		t.Fatalf("mail = %+v", m)
	}
	got, session, err := e.svc.SignInWithMagicLink(ctx, tokenFrom(link))
	if err != nil || got.ID != u.ID {
		t.Fatalf("sign in = %+v, %v", got, err)
	}
	if _, err := e.svc.Authenticate(ctx, string(session)); err != nil {
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
	if _, err := e.svc.Authenticate(ctx, string(session)); !errors.Is(err, identity.ErrUserDisabled) {
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
	// Activity every six days keeps the session alive until the absolute limit.
	for i := 0; i < 4; i++ {
		e.now = e.now.Add(6 * 24 * time.Hour)
		if _, err := e.svc.Authenticate(ctx, string(session)); err != nil {
			t.Fatalf("day %d: %v", (i+1)*6, err)
		}
	}
	e.now = e.now.Add(7 * 24 * time.Hour)
	if _, err := e.svc.Authenticate(ctx, string(session)); !errors.Is(err, identity.ErrSessionExpired) {
		t.Fatalf("after idle period: %v", err)
	}
	if _, err := e.svc.Authenticate(ctx, "garbage"); !errors.Is(err, identity.ErrSessionExpired) {
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
