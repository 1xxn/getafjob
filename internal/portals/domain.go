// Package portals manages portal accounts (LinkedIn, Indeed), their envelope-encrypted session state,
// low daily limits, and ban risk compliance for Easy Apply (#43).
package portals

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Supported portals for Easy Apply.
const (
	PortalLinkedIn = "linkedin"
	PortalIndeed   = "indeed"
)

// Session statuses.
const (
	StatusActive       = "active"
	StatusExpired      = "expired"
	StatusDisconnected = "disconnected"
)

// Daily limit constraints.
const (
	DefaultDailyLimit = 5
	MinDailyLimit     = 1
	MaxDailyLimit     = 20
)

// NoticeWarning explains the Terms of Service and ban risk of portal automation.
const NoticeWarning = "Automating applications on job portals (such as LinkedIn or Indeed) carries a risk of account " +
	"restriction or permanent ban under their Terms of Service. Getafjob enforces low daily limits, requires manual " +
	"login in the remote browser without storing passwords, and halts at checkpoints for your review."

// ErrInvalidPortal reports an unsupported portal name.
var ErrInvalidPortal = errors.New("unsupported portal")

// ErrInvalidDailyLimit reports a daily limit outside permitted bounds.
var ErrInvalidDailyLimit = errors.New("daily limit out of bounds")

// ErrSessionNotFound reports a missing portal session.
var ErrSessionNotFound = errors.New("portal session not found")

// ErrEasyApplyDisabled reports that the Easy Apply module or opt-in is disabled.
var ErrEasyApplyDisabled = errors.New("easy apply is disabled")

// Session holds the database record for a portal session (envelope-encrypted cookies).
type Session struct {
	UserID           uuid.UUID
	Portal           string
	Status           string
	EncryptedCookies []byte
	DailyLimit       int
	LastUsedAt       *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// AccountView presents a user-facing overview of a portal account without sensitive cookies.
type AccountView struct {
	Portal        string     `json:"portal"`
	Status        string     `json:"status"`
	DailyLimit    int        `json:"daily_limit"`
	SubmitsToday  int        `json:"submits_today"`
	ActiveRuns    int        `json:"active_runs"`
	LastUsedAt    *time.Time `json:"last_used_at,omitempty"`
	CanApply      bool       `json:"can_apply"`
	BlockedReason string     `json:"blocked_reason,omitempty"`
}

// AllPortals returns the canonical list of supported portals.
func AllPortals() []string {
	return []string{PortalLinkedIn, PortalIndeed}
}

// ValidatePortal checks that portal is one of the supported portals.
func ValidatePortal(portal string) error {
	portal = strings.ToLower(strings.TrimSpace(portal))
	switch portal {
	case PortalLinkedIn, PortalIndeed:
		return nil
	default:
		return fmt.Errorf("%w: %q (supported: linkedin, indeed)", ErrInvalidPortal, portal)
	}
}

// ValidateDailyLimit verifies that limit is within the permitted range.
func ValidateDailyLimit(limit int) error {
	if limit < MinDailyLimit || limit > MaxDailyLimit {
		return fmt.Errorf("%w: %d (must be between %d and %d)", ErrInvalidDailyLimit, limit, MinDailyLimit, MaxDailyLimit)
	}
	return nil
}

// CanApply evaluates whether an application run may proceed for this portal session.
func CanApply(s Session, submitsToday, activeRuns int) (bool, string) {
	if s.Status != StatusActive {
		return false, "session not active (expired or disconnected)"
	}
	if len(s.EncryptedCookies) == 0 {
		return false, "no session cookies stored"
	}
	if activeRuns > 0 {
		return false, "another application run for this account is already active"
	}
	limit := s.DailyLimit
	if limit <= 0 {
		limit = DefaultDailyLimit
	}
	if submitsToday >= limit {
		return false, fmt.Sprintf("daily portal limit reached (%d/%d submits today)", submitsToday, limit)
	}
	return true, ""
}

// IsSessionExpired checks whether a URL or HTML snippet indicates an unauthenticated/expired session.
func IsSessionExpired(rawURL, htmlText string) bool {
	u := strings.ToLower(rawURL)
	lower := strings.ToLower(htmlText)

	// LinkedIn checks
	if strings.Contains(u, "linkedin.com") {
		if strings.Contains(u, "/login") || strings.Contains(u, "/checkpoint/") || strings.Contains(u, "/authwall") {
			return true
		}
		if strings.Contains(lower, "join linkedin") || strings.Contains(lower, "sign in to linkedin") ||
			strings.Contains(lower, "anmelden, um fortzufahren") || strings.Contains(lower, "authwall") {
			return true
		}
	}

	// Indeed checks
	if strings.Contains(u, "indeed.com") {
		if strings.Contains(u, "secure.indeed.com/account/login") || strings.Contains(u, "secure.indeed.com/auth") {
			return true
		}
		if strings.Contains(lower, "sign in to indeed") || strings.Contains(lower, "bei indeed anmelden") ||
			strings.Contains(lower, "create an account or sign in") {
			return true
		}
	}

	return false
}
