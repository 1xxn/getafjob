package portals

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Store persists portal sessions and queries submission rate metrics.
type Store interface {
	SaveSession(ctx context.Context, s Session) error
	GetSession(ctx context.Context, userID uuid.UUID, portal string) (Session, error)
	ListSessions(ctx context.Context, userID uuid.UUID) ([]Session, error)
	UpdateStatus(ctx context.Context, userID uuid.UUID, portal, status string, at time.Time) error
	TouchLastUsed(ctx context.Context, userID uuid.UUID, portal string, at time.Time) error
	DeleteSession(ctx context.Context, userID uuid.UUID, portal string) error
	SubmitsToday(ctx context.Context, userID uuid.UUID, portal string, since time.Time) (int, error)
	ActiveRuns(ctx context.Context, userID uuid.UUID, portal string) (int, error)
}

// SessionCipher seals and opens portal cookies using envelope encryption.
type SessionCipher interface {
	SealCookies(ctx context.Context, userID uuid.UUID, portal string, cookiesJSON []byte) ([]byte, error)
	OpenCookies(ctx context.Context, userID uuid.UUID, portal string, ciphertext []byte) ([]byte, error)
}

// AutonomySettingsReader reads the user's opt-in status and terms acceptance.
type AutonomySettingsReader interface {
	IsEasyApplyEnabled(ctx context.Context, userID uuid.UUID) (enabled bool, termsAccepted bool, err error)
}
