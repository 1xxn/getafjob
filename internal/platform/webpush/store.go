package webpush

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/1xxn/getafjob/internal/platform/db"
	"github.com/1xxn/getafjob/internal/platform/webpush/q"
)

// Store persists push subscriptions.
type Store interface {
	Upsert(ctx context.Context, userID uuid.UUID, sub Subscription) error
	Delete(ctx context.Context, userID uuid.UUID, endpoint string) error
	DeleteEndpoint(ctx context.Context, endpoint string) error
	ByUser(ctx context.Context, userID uuid.UUID) ([]Subscription, error)
}

// PGStore implements Store on PostgreSQL with row-level security.
type PGStore struct {
	pool *pgxpool.Pool
}

var _ Store = (*PGStore)(nil)

// NewStore returns a PostgreSQL-backed subscription store.
func NewStore(pool *pgxpool.Pool) *PGStore {
	return &PGStore{pool: pool}
}

// Upsert registers or updates a subscription for the user.
func (s *PGStore) Upsert(ctx context.Context, userID uuid.UUID, sub Subscription) error {
	return db.InUserTx(ctx, s.pool, userID, func(tx pgx.Tx) error {
		_, err := q.New(tx).UpsertSubscription(ctx, q.UpsertSubscriptionParams{
			UserID:   userID,
			Endpoint: sub.Endpoint,
			P256dh:   sub.P256dh,
			Auth:     sub.Auth,
		})
		return err
	})
}

// Delete removes a subscription of the user.
func (s *PGStore) Delete(ctx context.Context, userID uuid.UUID, endpoint string) error {
	return db.InUserTx(ctx, s.pool, userID, func(tx pgx.Tx) error {
		return q.New(tx).DeleteSubscription(ctx, q.DeleteSubscriptionParams{
			UserID:   userID,
			Endpoint: endpoint,
		})
	})
}

// DeleteEndpoint removes an expired endpoint (system context).
func (s *PGStore) DeleteEndpoint(ctx context.Context, endpoint string) error {
	return db.InSystemTx(ctx, s.pool, func(tx pgx.Tx) error {
		return q.New(tx).DeleteSubscriptionByEndpoint(ctx, endpoint)
	})
}

// ByUser lists all push subscriptions of a user (system context for delivery).
func (s *PGStore) ByUser(ctx context.Context, userID uuid.UUID) ([]Subscription, error) {
	var out []Subscription
	err := db.InSystemTx(ctx, s.pool, func(tx pgx.Tx) error {
		rows, err := q.New(tx).ListSubscriptionsByUser(ctx, userID)
		if err != nil {
			return err
		}
		out = make([]Subscription, len(rows))
		for i, r := range rows {
			out[i] = Subscription{
				Endpoint: r.Endpoint,
				P256dh:   r.P256dh,
				Auth:     r.Auth,
			}
		}
		return nil
	})
	return out, err
}
