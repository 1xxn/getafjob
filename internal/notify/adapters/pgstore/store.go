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
