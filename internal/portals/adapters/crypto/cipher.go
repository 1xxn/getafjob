package crypto

import (
	"context"

	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/platform/crypto"
	"github.com/1xxn/getafjob/internal/portals"
)

// Cipher implements portals.SessionCipher using envelope encryption.
type Cipher struct {
	envelope *crypto.Envelope
}

var _ portals.SessionCipher = (*Cipher)(nil)

// NewCipher returns a new envelope-backed session cipher.
func NewCipher(envelope *crypto.Envelope) *Cipher {
	return &Cipher{envelope: envelope}
}

func purpose(portal string) string {
	return "portal_session:" + portal
}

// SealCookies seals the plaintext cookies JSON bound to the user and portal.
func (c *Cipher) SealCookies(ctx context.Context, userID uuid.UUID, portal string, cookiesJSON []byte) ([]byte, error) {
	return c.envelope.Seal(ctx, userID, purpose(portal), cookiesJSON)
}

// OpenCookies opens the ciphertext cookies bound to the user and portal.
func (c *Cipher) OpenCookies(ctx context.Context, userID uuid.UUID, portal string, ciphertext []byte) ([]byte, error) {
	return c.envelope.Open(ctx, userID, purpose(portal), ciphertext)
}
