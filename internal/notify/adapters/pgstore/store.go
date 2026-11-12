// Package pgstore implements notify.Store on PostgreSQL.
package pgstore

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/1xxn/getafjob/internal/notify"
	"github.com/1xxn/getafjob/internal/notify/adapters/pgstore/q"
	"github.com/1xxn/getafjob/internal/platform/db"
	"github.com/1xxn/getafjob/internal/platform/i18n"
)

// Store is the PostgreSQL notify store.
type Store struct {
	pool *pgxpool.Pool
}

var _ notify.Store = (*Store)(nil)

// New returns a new PostgreSQL store for notify.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) user(ctx context.Context, userID uuid.UUID, fn func(*q.Queries) error) error {
	return db.InUserTx(ctx, s.pool, userID, func(tx pgx.Tx) error {
		return fn(q.New(tx))
	})
}

func (s *Store) system(ctx context.Context, fn func(*q.Queries) error) error {
	return db.InSystemTx(ctx, s.pool, func(tx pgx.Tx) error {
		return fn(q.New(tx))
	})
}

// GetPreferences returns user notification settings.
func (s *Store) GetPreferences(ctx context.Context, userID uuid.UUID) (notify.Preferences, error) {
	var prefs notify.Preferences
	err := s.user(ctx, userID, func(qs *q.Queries) error {
		r, err := qs.GetPreferences(ctx, userID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return notify.ErrNotFound
			}
			return err
		}
		prefs = notify.Preferences{
			UserID:              r.UserID,
			EmailEnabled:        r.EmailEnabled,
			EmailAddress:        r.EmailAddress,
			WebPushEnabled:      r.WebpushEnabled,
			NtfyEnabled:         r.NtfyEnabled,
			NtfyURL:             r.NtfyUrl,
			NtfyTopic:           r.NtfyTopic,
			NtfyToken:           r.NtfyToken,
			DailySummaryEnabled: r.DailySummaryEnabled,
			DailySummaryTime:    r.DailySummaryTime,
			LastSummaryAt:       r.LastSummaryAt,
			QuietHoursEnabled:   r.QuietHoursEnabled,
			QuietHoursStart:     r.QuietHoursStart,
			QuietHoursEnd:       r.QuietHoursEnd,
			CreatedAt:           r.CreatedAt,
			UpdatedAt:           r.UpdatedAt,
		}
		return nil
	})
	return prefs, err
}

// SavePreferences persists user notification settings.
func (s *Store) SavePreferences(ctx context.Context, p notify.Preferences) error {
	now := time.Now()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	p.UpdatedAt = now

	return s.user(ctx, p.UserID, func(qs *q.Queries) error {
		return qs.UpsertPreferences(ctx, q.UpsertPreferencesParams{
			UserID:              p.UserID,
			EmailEnabled:        p.EmailEnabled,
			EmailAddress:        p.EmailAddress,
			WebpushEnabled:      p.WebPushEnabled,
			NtfyEnabled:         p.NtfyEnabled,
			NtfyUrl:             p.NtfyURL,
			NtfyTopic:           p.NtfyTopic,
			NtfyToken:           p.NtfyToken,
			DailySummaryEnabled: p.DailySummaryEnabled,
			DailySummaryTime:    p.DailySummaryTime,
			QuietHoursEnabled:   p.QuietHoursEnabled,
			QuietHoursStart:     p.QuietHoursStart,
			QuietHoursEnd:       p.QuietHoursEnd,
			CreatedAt:           p.CreatedAt,
		})
	})
}

// ListDailySummaryCandidates returns all users who enabled daily summary notifications.
func (s *Store) ListDailySummaryCandidates(ctx context.Context) ([]notify.Preferences, error) {
	var candidates []notify.Preferences
	err := s.system(ctx, func(qs *q.Queries) error {
		rows, err := qs.ListEnabledDailySummaryUsers(ctx)
		if err != nil {
			return err
		}
		candidates = make([]notify.Preferences, len(rows))
		for i, r := range rows {
			candidates[i] = notify.Preferences{
				UserID:              r.UserID,
				EmailEnabled:        r.EmailEnabled,
				EmailAddress:        r.EmailAddress,
				WebPushEnabled:      r.WebpushEnabled,
				NtfyEnabled:         r.NtfyEnabled,
				NtfyURL:             r.NtfyUrl,
				NtfyTopic:           r.NtfyTopic,
				NtfyToken:           r.NtfyToken,
				DailySummaryEnabled: r.DailySummaryEnabled,
				DailySummaryTime:    r.DailySummaryTime,
				LastSummaryAt:       r.LastSummaryAt,
				QuietHoursEnabled:   r.QuietHoursEnabled,
				QuietHoursStart:     r.QuietHoursStart,
				QuietHoursEnd:       r.QuietHoursEnd,
				CreatedAt:           r.CreatedAt,
				UpdatedAt:           r.UpdatedAt,
			}
		}
		return nil
	})
	return candidates, err
}

// UpdateLastSummaryAt updates the last summary timestamp for a user.
func (s *Store) UpdateLastSummaryAt(ctx context.Context, userID uuid.UUID, at time.Time) error {
	return s.user(ctx, userID, func(qs *q.Queries) error {
		return qs.UpdateLastSummaryAt(ctx, q.UpdateLastSummaryAtParams{
			UserID:        userID,
			LastSummaryAt: &at,
		})
	})
}

// GetUserEmail retrieves the primary email address of a user.
func (s *Store) GetUserEmail(ctx context.Context, userID uuid.UUID) (string, error) {
	var email string
	err := s.system(ctx, func(qs *q.Queries) error {
		var err error
		email, err = qs.GetUserEmail(ctx, userID)
		return err
	})
	return email, err
}

// DailyStats gathers aggregated application and response numbers for the summary.
func (s *Store) DailyStats(ctx context.Context, userID uuid.UUID, since time.Time) (notify.DailyStats, error) {
	stats := notify.DailyStats{
		UserID:      userID,
		PeriodStart: since,
		PeriodEnd:   time.Now(),
	}

	err := s.user(ctx, userID, func(qs *q.Queries) error {
		subCount, err := qs.CountSubmissionsSince(ctx, q.CountSubmissionsSinceParams{
			UserID:    userID,
			StartedAt: since,
		})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		stats.SubmittedCount = subCount

		needsCount, err := qs.CountNeedsInputApplications(ctx, userID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		stats.NeedsInputCount = needsCount

		apprCount, err := qs.CountPendingApprovals(ctx, userID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		stats.PendingApprovals = apprCount

		sugCount, err := qs.CountPendingSuggestions(ctx, userID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		stats.PendingSuggestions = sugCount

		catCounts, err := qs.CategoryCountsSince(ctx, q.CategoryCountsSinceParams{
			UserID:    userID,
			CreatedAt: since,
		})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		for _, row := range catCounts {
			switch row.Category {
			case "invitation":
				stats.Invitations = row.Count
			case "received":
				stats.Confirmations = row.Count
			case "rejection":
				stats.Rejections = row.Count
			}
		}
		return nil
	})

	return stats, err
}

// UserLocale returns the language the user reads the app in.
func (s *Store) UserLocale(ctx context.Context, userID uuid.UUID) (i18n.Locale, error) {
	var l string
	err := s.system(ctx, func(qs *q.Queries) error {
		var err error
		l, err = qs.GetUserLocale(ctx, userID)
		return err
	})
	return i18n.Parse(l), err
}

// Activity returns the newest feed entries before a time.
func (s *Store) Activity(ctx context.Context, userID uuid.UUID, before time.Time, limit int) ([]notify.ActivityItem, error) {
	var out []notify.ActivityItem
	err := s.user(ctx, userID, func(qs *q.Queries) error {
		rows, err := qs.Activity(ctx, q.ActivityParams{UserID: userID, States: notify.ActivityStates, Before: before,
			Lim: int32(min(limit, 1000))}) //nolint:gosec // bounded above
		for _, r := range rows {
			out = append(out, notify.ActivityItem{ID: r.ID, Kind: notify.ActivityKind(r.Kind), State: r.State,
				ApplicationID: r.ApplicationID, Title: r.Title, Company: r.Company, At: r.At})
		}
		return err
	})
	return out, err
}

// CountActivitySince counts the feed entries after a time.
func (s *Store) CountActivitySince(ctx context.Context, userID uuid.UUID, since time.Time) (int, error) {
	var n int64
	err := s.user(ctx, userID, func(qs *q.Queries) error {
		var err error
		n, err = qs.CountActivitySince(ctx, q.CountActivitySinceParams{UserID: userID, States: notify.ActivityStates, Since: since})
		return err
	})
	return int(n), err
}

// SeenAt is when the user last marked the feed read; zero when never.
func (s *Store) SeenAt(ctx context.Context, userID uuid.UUID) (time.Time, error) {
	var at time.Time
	err := s.user(ctx, userID, func(qs *q.Queries) error {
		var err error
		at, err = qs.GetSeenAt(ctx, userID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	})
	return at, err
}

// MarkSeen moves the read mark forward to at.
func (s *Store) MarkSeen(ctx context.Context, userID uuid.UUID, at time.Time) error {
	return s.user(ctx, userID, func(qs *q.Queries) error {
		return qs.SetSeenAt(ctx, q.SetSeenAtParams{UserID: userID, SeenAt: at})
	})
}
