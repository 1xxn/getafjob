// Package pgstore implements ai.Store on PostgreSQL with RLS.
package pgstore

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/1xxn/getafjob/internal/ai"
	"github.com/1xxn/getafjob/internal/ai/adapters/pgstore/q"
	"github.com/1xxn/getafjob/internal/platform/db"
)

// Store is the PostgreSQL AI settings and usage store.
type Store struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

var _ ai.Store = (*Store)(nil)

// New returns a new PostgreSQL store for AI.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, now: time.Now}
}

// SetNow overrides time for testing.
func (s *Store) SetNow(now func() time.Time) {
	s.now = now
}

func (s *Store) user(ctx context.Context, userID uuid.UUID, fn func(*q.Queries) error) error {
	return db.InUserTx(ctx, s.pool, userID, func(tx pgx.Tx) error {
		return fn(q.New(tx))
	})
}

// GetSettings retrieves the user's AI settings and encrypted custom key.
func (s *Store) GetSettings(ctx context.Context, userID uuid.UUID) (ai.UserSettings, []byte, error) {
	var out ai.UserSettings
	var encKey []byte
	err := s.user(ctx, userID, func(qs *q.Queries) error {
		r, err := qs.GetAISettings(ctx, userID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				out = ai.UserSettings{Enabled: false}
				return nil
			}
			return err
		}
		out = ai.UserSettings{
			Enabled:    r.Enabled,
			SpentCents: int(r.SpentCents),
		}
		encKey = r.EncryptedApiKey
		return nil
	})
	return out, encKey, err
}

// SaveSettings updates or creates the user's AI configuration.
func (s *Store) SaveSettings(ctx context.Context, userID uuid.UUID, enabled bool, encryptedKey []byte) error {
	return s.user(ctx, userID, func(qs *q.Queries) error {
		return qs.SaveAISettings(ctx, q.SaveAISettingsParams{
			UserID:          userID,
			Enabled:         enabled,
			EncryptedApiKey: encryptedKey,
			SpentCents:      0,
			CreatedAt:       s.now(),
		})
	})
}

// IncrementSpent adds cost in cents to the user's spent total.
func (s *Store) IncrementSpent(ctx context.Context, userID uuid.UUID, costCents int) error {
	return s.user(ctx, userID, func(qs *q.Queries) error {
		return qs.IncrementSpent(ctx, q.IncrementSpentParams{
			UserID:     userID,
			SpentCents: int32(costCents), //nolint:gosec // bounded integer conversion
			UpdatedAt:  s.now(),
		})
	})
}

// RecordUsage appends an audit event tracking model, tokens, and cost.
func (s *Store) RecordUsage(ctx context.Context, userID uuid.UUID, task ai.TaskType, model string, tokensIn, tokensOut, costCents int) error {
	return s.user(ctx, userID, func(qs *q.Queries) error {
		return qs.RecordUsageEvent(ctx, q.RecordUsageEventParams{
			UserID:    userID,
			Task:      string(task),
			Model:     model,
			TokensIn:  int32(tokensIn),  //nolint:gosec // bounded
			TokensOut: int32(tokensOut), //nolint:gosec // bounded
			CostCents: int32(costCents), //nolint:gosec // bounded
			CreatedAt: s.now(),
		})
	})
}
