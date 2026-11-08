package notify

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// NtfyClient posts notifications to an ntfy topic.
type NtfyClient interface {
	SendNtfy(ctx context.Context, serverURL, topic, token string, n Notification) error
}

// EmailClient sends email notifications.
type EmailClient interface {
	SendEmail(ctx context.Context, to, subject, body string) error
}

// WebPushClient sends Web Push notifications.
type WebPushClient interface {
	SendWebPush(ctx context.Context, userID uuid.UUID, title, body, url string) error
}

// Store persists user notification preferences and reads daily summary statistics.
type Store interface {
	GetPreferences(ctx context.Context, userID uuid.UUID) (Preferences, error)
	SavePreferences(ctx context.Context, prefs Preferences) error
	ListDailySummaryCandidates(ctx context.Context) ([]Preferences, error)
	UpdateLastSummaryAt(ctx context.Context, userID uuid.UUID, at time.Time) error
	GetUserEmail(ctx context.Context, userID uuid.UUID) (string, error)
	DailyStats(ctx context.Context, userID uuid.UUID, since time.Time) (DailyStats, error)
}
