package profile

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Stored is the raw state of a user's profile: the sealed profile and the readable preferences.
type Stored struct {
	Sealed      []byte // nil: no profile yet
	Preferences []byte // JSON, nil: none yet
	Version     int64  // 0: nothing stored
	UpdatedAt   time.Time
}

// Store persists profiles per user (row-level security).
type Store interface {
	Load(ctx context.Context, userID uuid.UUID) (Stored, error)
	// SaveProfile stores sealed if the stored version is still version; false when someone saved in between.
	SaveProfile(ctx context.Context, userID uuid.UUID, sealed []byte, version int64, at time.Time) (bool, error)
	SavePreferences(ctx context.Context, userID uuid.UUID, prefs []byte, at time.Time) error
}

// Sealer encrypts per user (crypto.Envelope).
type Sealer interface {
	Seal(ctx context.Context, userID uuid.UUID, purpose string, plaintext []byte) ([]byte, error)
	Open(ctx context.Context, userID uuid.UUID, purpose string, ciphertext []byte) ([]byte, error)
}
