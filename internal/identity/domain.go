// Package identity handles accounts: invitations, magic links, passkeys, sessions, roles and the audit log.
// Nobody signs up; every account starts with an invitation (ADR 0020).
package identity

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/platform/i18n"
)

// Role of a user.
type Role string

// Roles.
const (
	RoleAdmin Role = "admin"
	RoleUser  Role = "user"
)

// Valid reports whether r is a known role.
func (r Role) Valid() bool { return r == RoleAdmin || r == RoleUser }

// Method is how a session was created.
type Method string

// Sign-in methods.
const (
	MethodInvitation Method = "invitation"
	MethodMagicLink  Method = "magic_link"
	MethodPasskey    Method = "passkey"
)

// Policy durations.
const (
	InvitationTTL   = 7 * 24 * time.Hour
	MagicLinkTTL    = 15 * time.Minute
	SessionMaxAge   = 30 * 24 * time.Hour
	SessionIdle     = 7 * 24 * time.Hour
	sessionTouchGap = 5 * time.Minute
	magicLinkWindow = 15 * time.Minute
	magicLinkLimit  = 3
)

// Errors shown to people carry an i18n code (see Msg).
var (
	ErrInvalidToken   = errors.New("identity: invalid or expired token")
	ErrSessionExpired = errors.New("identity: session expired")
	ErrUserDisabled   = errors.New("identity: user disabled")
	ErrInvalidEmail   = errors.New("identity: invalid e-mail address")
	ErrEmailTaken     = errors.New("identity: e-mail address already has an account")
	ErrInvalidRole    = errors.New("identity: invalid role")
	ErrNotFound       = errors.New("identity: not found")
)

// Msg maps an error to the text code shown to people.
func Msg(err error) i18n.Msg {
	switch {
	case errors.Is(err, ErrSessionExpired):
		return i18n.Msg{Code: "identity.session_expired"}
	case errors.Is(err, ErrUserDisabled):
		return i18n.Msg{Code: "identity.user_disabled"}
	default:
		return i18n.Msg{Code: "identity.magic_link_invalid"}
	}
}

// User is an account.
type User struct {
	ID          uuid.UUID
	Email       string
	DisplayName string
	Role        Role
	Locale      i18n.Locale
	Disabled    bool
}

// Session is a signed-in browser.
type Session struct {
	UserID    uuid.UUID
	Method    Method
	CreatedAt time.Time
	LastSeen  time.Time
	ExpiresAt time.Time
}

// Valid reports whether the session may still be used at now (absolute and idle limit).
func (s Session) Valid(now time.Time) bool {
	return now.Before(s.ExpiresAt) && now.Before(s.LastSeen.Add(SessionIdle))
}

// NeedsTouch reports whether last-seen should be written again (bounded writes per request).
func (s Session) NeedsTouch(now time.Time) bool { return now.Sub(s.LastSeen) >= sessionTouchGap }

// Token is a one-time secret handed to a person (link or cookie); only its hash is stored.
type Token string

// Hash is the stored SHA-256 of a token.
type Hash [32]byte

// NewToken returns 32 random bytes as base64url and their hash.
func NewToken() (Token, Hash) {
	b := make([]byte, 32)
	_, _ = rand.Read(b) // never fails (crypto/rand)
	t := Token(base64.RawURLEncoding.EncodeToString(b))
	return t, sha256.Sum256([]byte(t))
}

// ParseToken checks the format of a token from a link or cookie and returns its hash.
func ParseToken(s string) (Hash, bool) {
	if len(s) != 43 {
		return Hash{}, false
	}
	if _, err := base64.RawURLEncoding.DecodeString(s); err != nil {
		return Hash{}, false
	}
	return sha256.Sum256([]byte(s)), true
}

// NormalizeEmail validates a plain address (no display name) and lower-cases it.
func NormalizeEmail(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 254 || strings.ContainsAny(s, "<>\"\r\n\t ") {
		return "", ErrInvalidEmail
	}
	a, err := mail.ParseAddress(s)
	if err != nil || a.Name != "" || a.Address != s {
		return "", ErrInvalidEmail
	}
	local, domain, ok := strings.Cut(a.Address, "@")
	if !ok || local == "" || !strings.Contains(domain, ".") || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return "", ErrInvalidEmail
	}
	return strings.ToLower(a.Address), nil
}

// CleanDisplayName trims a display name and caps it at 200 characters.
func CleanDisplayName(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 200 {
		s = string(r[:200])
	}
	return s
}
