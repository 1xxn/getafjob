package webpush

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
)

// Notifier delivers notices via Web Push (RFC 8292).
type Notifier struct {
	Store  Store
	Client *Client
	Logger *slog.Logger
}

// NoticePayload is serialized into the push message.
type NoticePayload struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url"`
}

// Notify sends a Web Push notification to all active subscriptions of the user.
func (n *Notifier) Notify(ctx context.Context, userID uuid.UUID, p NoticePayload) error {
	if n.Client == nil || n.Client.VAPIDKey == nil {
		if n.Logger != nil {
			n.Logger.InfoContext(ctx, "web push notice (no VAPID key configured)", "user", userID, "url", p.URL)
		}
		return nil
	}

	subs, err := n.Store.ByUser(ctx, userID)
	if err != nil {
		return fmt.Errorf("list push subscriptions: %w", err)
	}
	if len(subs) == 0 {
		if n.Logger != nil {
			n.Logger.InfoContext(ctx, "web push notice (no subscriptions)", "user", userID, "url", p.URL)
		}
		return nil
	}

	payload, err := json.Marshal(p)
	if err != nil {
		return err
	}

	var errs []error
	for _, sub := range subs {
		if err := n.Client.Send(ctx, sub, payload); err != nil {
			if errors.Is(err, ErrSubscriptionExpired) {
				_ = n.Store.DeleteEndpoint(ctx, sub.Endpoint)
				continue
			}
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
