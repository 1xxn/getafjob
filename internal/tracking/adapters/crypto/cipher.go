package crypto

import (
	"context"

	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/platform/crypto"
	"github.com/1xxn/getafjob/internal/tracking"
)

const purposeMailPassword = "mail_password"

// Cipher implements tracking.PasswordCipher using envelope encryption.
type Cipher struct {
	envelope *crypto.Envelope
}

var _ tracking.PasswordCipher = (*Cipher)(nil)

// NewCipher returns a new envelope-backed password cipher.
func NewCipher(envelope *crypto.Envelope) *Cipher {
	return &Cipher{envelope: envelope}
}

// SealPassword seals the plaintext password bound to the user.
func (c *Cipher) SealPassword(ctx context.Context, userID uuid.UUID, password string) ([]byte, error) {
	return c.envelope.Seal(ctx, userID, purposeMailPassword, []byte(password))
}

// OpenPassword opens the ciphertext password bound to the user.
func (c *Cipher) OpenPassword(ctx context.Context, userID uuid.UUID, ciphertext []byte) (string, error) {
	b, err := c.envelope.Open(ctx, userID, purposeMailPassword, ciphertext)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
