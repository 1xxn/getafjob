package notify

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/platform/i18n"
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
	UserLocale(ctx context.Context, userID uuid.UUID) (i18n.Locale, error)
	// Activity returns the newest feed entries before a time; CountActivitySince counts the entries after one.
	Activity(ctx context.Context, userID uuid.UUID, before time.Time, limit int) ([]ActivityItem, error)
	CountActivitySince(ctx context.Context, userID uuid.UUID, since time.Time) (int, error)
	// SeenAt is when the user last marked the feed read (zero: never); MarkSeen moves it forward only.
	SeenAt(ctx context.Context, userID uuid.UUID) (time.Time, error)
	MarkSeen(ctx context.Context, userID uuid.UUID, at time.Time) error
	DailyStats(ctx context.Context, userID uuid.UUID, since time.Time) (DailyStats, error)
}
