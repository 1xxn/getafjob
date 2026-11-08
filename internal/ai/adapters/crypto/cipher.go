package crypto

import (
	"context"

	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/ai"
	"github.com/1xxn/getafjob/internal/platform/crypto"
)

// PurposeAIKey binds encryption to the AI personal key context.
const PurposeAIKey = "user_ai_key"

// Cipher implements ai.KeyCipher using envelope encryption (ADR 0006).
type Cipher struct {
	envelope *crypto.Envelope
}

var _ ai.KeyCipher = (*Cipher)(nil)

// NewCipher returns an envelope cipher for personal AI keys.
func NewCipher(envelope *crypto.Envelope) *Cipher {
	return &Cipher{envelope: envelope}
}

// SealKey seals the user's Anthropic API key with their envelope data key.
func (c *Cipher) SealKey(ctx context.Context, userID uuid.UUID, key string) ([]byte, error) {
	return c.envelope.Seal(ctx, userID, PurposeAIKey, []byte(key))
}

// OpenKey decrypts the user's Anthropic API key.
func (c *Cipher) OpenKey(ctx context.Context, userID uuid.UUID, ciphertext []byte) (string, error) {
	plain, err := c.envelope.Open(ctx, userID, PurposeAIKey, ciphertext)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}
