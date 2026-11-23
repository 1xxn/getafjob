package crypto

import (
	"context"
	"encoding/json"

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

func mailPurpose(suggestionID uuid.UUID) string { return "tracking_mail:" + suggestionID.String() }

// SealMail seals the mail data of a suggestion bound to the user and the suggestion.
func (c *Cipher) SealMail(ctx context.Context, userID, suggestionID uuid.UUID, m tracking.MailMeta) ([]byte, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	return c.envelope.Seal(ctx, userID, mailPurpose(suggestionID), b)
}

// OpenMail opens the sealed mail data of a suggestion.
func (c *Cipher) OpenMail(ctx context.Context, userID, suggestionID uuid.UUID, sealed []byte) (tracking.MailMeta, error) {
	var m tracking.MailMeta
	b, err := c.envelope.Open(ctx, userID, mailPurpose(suggestionID), sealed)
	if err != nil {
		return m, err
	}
	return m, json.Unmarshal(b, &m)
}
