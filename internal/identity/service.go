package identity

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/platform/i18n"
)

// Service implements invitations, magic links and sessions.
type Service struct {
	store  Store
	mailer Mailer // nil: mail disabled (links are only printed by the CLI)
	base   *url.URL
	now    func() time.Time
	// webauthn is nil until EnablePasskeys.
	webauthn *webauthn.WebAuthn
}

// NewService wires the service; base is the public URL used in links.
func NewService(store Store, mailer Mailer, base *url.URL) *Service {
	return &Service{store: store, mailer: mailer, base: base, now: time.Now}
}

// link builds an absolute URL below the public URL.
func (s *Service) link(path string, t Token) string {
	u := *s.base
	u.Path = path + string(t)
	return u.String()
}

// InviteRequest describes a new invitation.
type InviteRequest struct {
	Email  string
	Role   Role
	By     *uuid.UUID // nil: invited from the command line
	Locale i18n.Locale
	Send   bool // also send the link by mail
}

// Invite creates a one-time invitation link.
func (s *Service) Invite(ctx context.Context, r InviteRequest) (link string, expires time.Time, err error) {
	email, err := NormalizeEmail(r.Email)
	if err != nil {
		return "", time.Time{}, err
	}
	if !r.Role.Valid() {
		return "", time.Time{}, ErrInvalidRole
	}
	if _, err := s.store.UserByEmail(ctx, email); err == nil {
		return "", time.Time{}, ErrEmailTaken
	} else if !errors.Is(err, ErrNotFound) {
		return "", time.Time{}, err
	}
	token, hash := NewToken()
	expires = s.now().Add(InvitationTTL)
	actor := "system"
	if r.By != nil {
		actor = "admin"
	}
	audit := AuditEvent{UserID: r.By, Actor: actor, Code: "identity.invitation_created", Params: map[string]any{"role": r.Role}}
	if err := s.store.CreateInvitation(ctx, NewInvitation{Email: email, Role: r.Role, Hash: hash, CreatedBy: r.By, ExpiresAt: expires}, audit); err != nil {
		return "", time.Time{}, err
	}
	link = s.link("/invite/", token)
	if r.Send && s.mailer != nil {
		l := r.Locale
		body := i18n.T(l, "mail.invitation.body", map[string]any{"link": link, "expires": expires.UTC().Format("2006-01-02 15:04 UTC")})
		if err := s.mailer.Send(ctx, Mail{To: email, Subject: i18n.T(l, "mail.invitation.subject", nil), Text: body}); err != nil {
			return "", time.Time{}, fmt.Errorf("send invitation: %w", err)
		}
	}
	return link, expires, nil
}

// PendingInvitation returns the e-mail address of a valid invitation (for the accept page).
func (s *Service) PendingInvitation(ctx context.Context, token string) (Invitation, error) {
	h, ok := ParseToken(token)
	if !ok {
		return Invitation{}, ErrInvalidToken
	}
	inv, err := s.store.InvitationByHash(ctx, h)
	if err != nil || inv.Accepted || !s.now().Before(inv.ExpiresAt) {
		return Invitation{}, ErrInvalidToken
	}
	return inv, nil
}

// AcceptInvitation creates the account and signs it in.
func (s *Service) AcceptInvitation(ctx context.Context, token, displayName string, locale i18n.Locale) (User, Token, error) {
	inv, err := s.PendingInvitation(ctx, token)
	if err != nil {
		return User{}, "", err
	}
	now := s.now()
	st, sh := NewToken()
	u := User{Email: inv.Email, DisplayName: CleanDisplayName(displayName), Role: inv.Role, Locale: i18n.Parse(string(locale))}
	created, err := s.store.AcceptInvitation(ctx, inv, u, s.newSession(sh, MethodInvitation, now), now)
	if err != nil {
		return User{}, "", err
	}
	return created, st, nil
}

func (s *Service) newSession(h Hash, m Method, now time.Time) NewSession {
	return NewSession{Hash: h, Method: m, CreatedAt: now, ExpiresAt: now.Add(SessionMaxAge)}
}

// RequestMagicLink mails a sign-in link if the address belongs to an active account. It answers the same way
// for unknown addresses (no account enumeration) and stays silent above the rate limit.
func (s *Service) RequestMagicLink(ctx context.Context, email string) error {
	email, err := NormalizeEmail(email)
	if err != nil {
		return nil //nolint:nilerr // same answer as for unknown addresses
	}
	u, err := s.store.UserByEmail(ctx, email)
	if errors.Is(err, ErrNotFound) || (err == nil && u.Disabled) {
		return nil
	}
	if err != nil {
		return err
	}
	now := s.now()
	hasKeys, rec, err := s.store.RecoveryState(ctx, u.ID)
	if err != nil {
		return err
	}
	if hasKeys && (rec == nil || !rec.Ready(now)) {
		return s.startRecovery(ctx, u, now)
	}
	token, hash := NewToken()
	created, err := s.store.CreateMagicLink(ctx, u.ID, hash, now, now.Add(MagicLinkTTL), now.Add(-magicLinkWindow), magicLinkLimit)
	if err != nil || !created || s.mailer == nil {
		return err
	}
	l := u.Locale
	body := i18n.T(l, "mail.magic_link.body", map[string]any{
		"name": u.DisplayName, "link": s.link("/login/link/", token), "minutes": int(MagicLinkTTL.Minutes()),
	})
	return s.mailer.Send(ctx, Mail{To: u.Email, Subject: i18n.T(l, "mail.magic_link.subject", nil), Text: body})
}

// SignInWithMagicLink consumes a magic link and starts a session.
func (s *Service) SignInWithMagicLink(ctx context.Context, token string) (User, Token, error) {
	h, ok := ParseToken(token)
	if !ok {
		return User{}, "", ErrInvalidToken
	}
	now := s.now()
	st, sh := NewToken()
	u, recovered, err := s.store.ConsumeMagicLink(ctx, h, s.newSession(sh, MethodMagicLink, now), now)
	if errors.Is(err, ErrNotFound) {
		return User{}, "", ErrInvalidToken
	}
	if err != nil {
		return User{}, "", err
	}
	if u.Disabled {
		return User{}, "", ErrUserDisabled // the store created no session
	}
	if recovered && s.mailer != nil {
		l := u.Locale
		body := i18n.T(l, "mail.recovered.body", map[string]any{"name": u.DisplayName, "time": stamp(now), "account": s.link("/account", "")})
		if err := s.mailer.Send(ctx, Mail{To: u.Email, Subject: i18n.T(l, "mail.recovered.subject", nil), Text: body}); err != nil {
			return User{}, "", fmt.Errorf("send recovery notice: %w", err)
		}
	}
	return u, st, nil
}

// startRecovery answers a mail link request of an account with a passkey: it starts the waiting period and tells
// the owner by mail. A recovery that is already waiting is not restarted and not mailed again.
func (s *Service) startRecovery(ctx context.Context, u User, now time.Time) error {
	r, started, err := s.store.StartRecovery(ctx, u.ID, NewRecovery(now),
		AuditEvent{UserID: &u.ID, Actor: "user", Code: "identity.recovery_requested"})
	if err != nil || !started || s.mailer == nil {
		return err
	}
	l := u.Locale
	body := i18n.T(l, "mail.recovery.body", map[string]any{
		"name": u.DisplayName, "ready": stamp(r.ReadyAt), "until": stamp(r.ExpiresAt), "login": s.link("/login", ""),
	})
	return s.mailer.Send(ctx, Mail{To: u.Email, Subject: i18n.T(l, "mail.recovery.subject", nil), Text: body})
}

func stamp(t time.Time) string { return t.UTC().Format("2006-01-02 15:04 UTC") }

// Recovery returns the user's pending recovery, if any.
func (s *Service) Recovery(ctx context.Context, userID uuid.UUID) (*Recovery, error) {
	_, r, err := s.store.RecoveryState(ctx, userID)
	if r != nil && !s.now().Before(r.ExpiresAt) {
		r = nil
	}
	return r, err
}

// CancelRecovery ends a pending recovery (account page).
func (s *Service) CancelRecovery(ctx context.Context, userID uuid.UUID) (bool, error) {
	return s.store.CancelRecovery(ctx, userID, AuditEvent{UserID: &userID, Actor: "user", Code: "identity.recovery_canceled"})
}

// Authenticate resolves a session cookie to its user. When the token is due for rotation it returns the new
// token, which the caller must set as the cookie; otherwise next is empty.
func (s *Service) Authenticate(ctx context.Context, token string) (u User, next Token, err error) {
	h, ok := ParseToken(token)
	if !ok {
		return User{}, "", ErrSessionExpired
	}
	u, sess, err := s.store.SessionByHash(ctx, h)
	if errors.Is(err, ErrNotFound) {
		return User{}, "", ErrSessionExpired
	}
	if err != nil {
		return User{}, "", err
	}
	now := s.now()
	if !sess.Valid(now) {
		if sess.ReplacedAt.IsZero() { // a replaced token just ran out of grace; cleanup removes it
			_ = s.store.DeleteSession(ctx, h, AuditEvent{UserID: &u.ID, Actor: "system", Code: "identity.session_expired"})
		}
		return User{}, "", ErrSessionExpired
	}
	if u.Disabled {
		return User{}, "", ErrUserDisabled
	}
	if sess.NeedsRotation(now) {
		t, nh := NewToken()
		rotated, err := s.store.RotateSession(ctx, h, nh, now)
		if err != nil {
			return User{}, "", err
		}
		if rotated {
			return u, t, nil
		}
	}
	if sess.NeedsTouch(now) {
		if err := s.store.TouchSession(ctx, h, now); err != nil {
			return User{}, "", err
		}
	}
	return u, "", nil
}

// Sessions lists the user's sessions; the one of token is marked current.
func (s *Service) Sessions(ctx context.Context, userID uuid.UUID, token string) ([]SessionInfo, error) {
	h, _ := ParseToken(token)
	rows, err := s.store.Sessions(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]SessionInfo, len(rows))
	for i, r := range rows {
		out[i] = r.SessionInfo
		out[i].Current = r.Hash == h
	}
	return out, nil
}

// SignOutOthers ends all sessions of the user except the one of token. The session must be fresh (signed in
// within SessionFresh), otherwise ErrReauthRequired: whoever stole a session cannot lock the owner out.
func (s *Service) SignOutOthers(ctx context.Context, userID uuid.UUID, token string) (int, error) {
	h, err := s.freshSession(ctx, userID, token)
	if err != nil {
		return 0, err
	}
	return s.store.DeleteOtherSessions(ctx, userID, h, AuditEvent{UserID: &userID, Actor: "user", Code: "identity.signed_out_others"})
}

// freshSession checks that token is a fresh session of userID.
func (s *Service) freshSession(ctx context.Context, userID uuid.UUID, token string) (Hash, error) {
	h, ok := ParseToken(token)
	if !ok {
		return Hash{}, ErrSessionExpired
	}
	u, sess, err := s.store.SessionByHash(ctx, h)
	if errors.Is(err, ErrNotFound) || (err == nil && (u.ID != userID || !sess.Valid(s.now()))) {
		return Hash{}, ErrSessionExpired
	}
	if err != nil {
		return Hash{}, err
	}
	if !sess.Fresh(s.now()) {
		return Hash{}, ErrReauthRequired
	}
	return h, nil
}

// SignOutUser ends every session of the account with email (admin command).
func (s *Service) SignOutUser(ctx context.Context, email string) (int, error) {
	email, err := NormalizeEmail(email)
	if err != nil {
		return 0, err
	}
	u, err := s.store.UserByEmail(ctx, email)
	if err != nil {
		return 0, err
	}
	return s.store.DeleteUserSessions(ctx, u.ID, AuditEvent{UserID: &u.ID, Actor: "admin", Code: "identity.signed_out_by_admin"})
}

// SignOut ends a session.
func (s *Service) SignOut(ctx context.Context, token string, userID uuid.UUID) error {
	h, ok := ParseToken(token)
	if !ok {
		return nil
	}
	return s.store.DeleteSession(ctx, h, AuditEvent{UserID: &userID, Actor: "user", Code: "identity.signed_out"})
}

// Cleanup removes expired sessions, links and challenges (worker job).
func (s *Service) Cleanup(ctx context.Context) error { return s.store.DeleteExpired(ctx, s.now()) }

// WithClock replaces the clock (tests).
func (s *Service) WithClock(now func() time.Time) *Service { s.now = now; return s }

// UserByEmail returns the account of an address; ErrNotFound if there is none.
func (s *Service) UserByEmail(ctx context.Context, email string) (User, error) {
	e, err := NormalizeEmail(email)
	if err != nil {
		return User{}, err
	}
	return s.store.UserByEmail(ctx, e)
}

// UserByID returns the account of an ID; ErrNotFound if there is none.
func (s *Service) UserByID(ctx context.Context, id uuid.UUID) (User, error) {
	u, _, err := s.store.UserByID(ctx, id)
	return u, err
}

// DeleteUser permanently deletes an account and all its personal data.
func (s *Service) DeleteUser(ctx context.Context, userID uuid.UUID) error {
	return s.store.DeleteUser(ctx, userID)
}

// AuditEvents returns all audit log events recorded for a user.
func (s *Service) AuditEvents(ctx context.Context, userID uuid.UUID) ([]AuditEvent, error) {
	return s.store.AuditEvents(ctx, userID)
}
