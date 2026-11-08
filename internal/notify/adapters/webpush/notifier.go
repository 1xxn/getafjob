// Package webpush adapts platform/webpush to notify.WebPushClient.
package webpush

import (
	"context"

	"github.com/google/uuid"

	platformpush "github.com/1xxn/getafjob/internal/platform/webpush"
)

// Notifier delivers Web Push notifications using platform/webpush.Notifier.
type Notifier struct {
	notifier *platformpush.Notifier
}

// NewNotifier wraps a platform webpush notifier.
func NewNotifier(n *platformpush.Notifier) *Notifier {
	return &Notifier{notifier: n}
}

// SendWebPush sends a push notification with a title, body and click URL.
func (n *Notifier) SendWebPush(ctx context.Context, userID uuid.UUID, title, body, url string) error {
	if n.notifier == nil {
		return nil
	}
	return n.notifier.Notify(ctx, userID, platformpush.NoticePayload{
		Title: title,
		Body:  body,
		URL:   url,
	})
}
