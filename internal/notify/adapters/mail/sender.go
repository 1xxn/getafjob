// Package mail adapts platform/mail to notify.EmailClient.
package mail

import (
	"context"

	platformmail "github.com/1xxn/getafjob/internal/platform/mail"
)

// Sender delivers email notifications using platform/mail.Sender.
type Sender struct {
	sender *platformmail.Sender
}

// NewSender wraps a platform mail sender.
func NewSender(s *platformmail.Sender) *Sender {
	return &Sender{sender: s}
}

// SendEmail delivers a plain-text email message.
func (s *Sender) SendEmail(ctx context.Context, to, subject, body string) error {
	if s.sender == nil {
		return nil
	}
	return s.sender.Send(ctx, platformmail.Message{
		To:      to,
		Subject: subject,
		Text:    body,
	})
}
