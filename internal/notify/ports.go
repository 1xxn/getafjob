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

// PendingEmail is a notification queued for a per-kind bundled digest email.
type PendingEmail struct {
	ID        int64
	UserID    uuid.UUID
	Kind      string
	Title     string
	Body      string
	URL       string
	CreatedAt time.Time
}

// PendingEmailBundle groups queued email items of one user and notification kind ready to send.
type PendingEmailBundle struct {
	UserID uuid.UUID
	Kind   string
	Items  []PendingEmail
}

// Store persists user notification preferences and reads daily summary statistics.
type Store interface {
	GetPreferences(ctx context.Context, userID uuid.UUID) (Preferences, error)
	SavePreferences(ctx context.Context, prefs Preferences) error
	ListDailySummaryCandidates(ctx context.Context) ([]Preferences, error)
	UpdateLastSummaryAt(ctx context.Context, userID uuid.UUID, at time.Time) error
	GetUserEmail(ctx context.Context, userID uuid.UUID) (string, error)
	UserLocale(ctx context.Context, userID uuid.UUID) (i18n.Locale, error)
	EnqueuePendingEmail(ctx context.Context, item PendingEmail) error
	ListDueEmailBundles(ctx context.Context, readyBefore time.Time) ([]PendingEmailBundle, error)
	MarkEmailBundleSent(ctx context.Context, userID uuid.UUID, kind string, upToID int64, sentAt time.Time) error
	// Activity returns the newest feed entries before a time; CountActivitySince counts the entries after one.
	Activity(ctx context.Context, userID uuid.UUID, before time.Time, limit int) ([]ActivityItem, error)
	CountActivitySince(ctx context.Context, userID uuid.UUID, since time.Time) (int, error)
	// SeenAt is when the user last marked the feed read (zero: never); MarkSeen moves it forward only.
	SeenAt(ctx context.Context, userID uuid.UUID) (time.Time, error)
	MarkSeen(ctx context.Context, userID uuid.UUID, at time.Time) error
	DailyStats(ctx context.Context, userID uuid.UUID, since time.Time) (DailyStats, error)
}
