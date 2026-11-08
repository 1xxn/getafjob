// Package pgstore implements portals.Store on PostgreSQL with RLS.
package pgstore

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/1xxn/getafjob/internal/platform/db"
	"github.com/1xxn/getafjob/internal/portals"
	"github.com/1xxn/getafjob/internal/portals/adapters/pgstore/q"
)

// Store is the PostgreSQL portals store.
type Store struct {
	pool *pgxpool.Pool
}

var _ portals.Store = (*Store)(nil)

// New returns a new PostgreSQL store for portals.
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

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return portals.ErrSessionNotFound
	}
	return err
}

func toDomain(r q.PortalSession) portals.Session {
	return portals.Session{
		UserID:           r.UserID,
		Portal:           r.Portal,
		Status:           r.Status,
		EncryptedCookies: r.EncryptedCookies,
		DailyLimit:       int(r.DailyLimit),
		LastUsedAt:       r.LastUsedAt,
		CreatedAt:        r.CreatedAt,
		UpdatedAt:        r.UpdatedAt,
	}
}

// SaveSession inserts or updates a portal session record.
func (s *Store) SaveSession(ctx context.Context, sess portals.Session) error {
	return s.user(ctx, sess.UserID, func(qs *q.Queries) error {
		return qs.SavePortalSession(ctx, q.SavePortalSessionParams{
			UserID:           sess.UserID,
			Portal:           sess.Portal,
			Status:           sess.Status,
			EncryptedCookies: sess.EncryptedCookies,
			DailyLimit:       int32(sess.DailyLimit),
			LastUsedAt:       sess.LastUsedAt,
			CreatedAt:        sess.CreatedAt,
		})
	})
}

// GetSession returns the user's session for the given portal.
func (s *Store) GetSession(ctx context.Context, userID uuid.UUID, portal string) (portals.Session, error) {
	var out portals.Session
	err := s.user(ctx, userID, func(qs *q.Queries) error {
		r, err := qs.GetPortalSession(ctx, q.GetPortalSessionParams{
			UserID: userID,
			Portal: portal,
		})
		if err != nil {
			return notFound(err)
		}
		out = toDomain(r)
		return nil
	})
	return out, err
}

// ListSessions returns all portal sessions configured for the user.
func (s *Store) ListSessions(ctx context.Context, userID uuid.UUID) ([]portals.Session, error) {
	var list []portals.Session
	err := s.user(ctx, userID, func(qs *q.Queries) error {
		rows, err := qs.ListPortalSessions(ctx, userID)
		if err != nil {
			return err
		}
		list = make([]portals.Session, 0, len(rows))
		for _, r := range rows {
			list = append(list, toDomain(r))
		}
		return nil
	})
	return list, err
}

// UpdateStatus updates the active/expired/disconnected status of a portal session.
func (s *Store) UpdateStatus(ctx context.Context, userID uuid.UUID, portal, status string, at time.Time) error {
	return s.user(ctx, userID, func(qs *q.Queries) error {
		return qs.UpdatePortalStatus(ctx, q.UpdatePortalStatusParams{
			UserID:    userID,
			Portal:    portal,
			Status:    status,
			UpdatedAt: at,
		})
	})
}

// TouchLastUsed records the time when a portal run was executed.
func (s *Store) TouchLastUsed(ctx context.Context, userID uuid.UUID, portal string, at time.Time) error {
	return s.user(ctx, userID, func(qs *q.Queries) error {
		return qs.TouchPortalLastUsed(ctx, q.TouchPortalLastUsedParams{
			UserID:     userID,
			Portal:     portal,
			LastUsedAt: &at,
		})
	})
}

// DeleteSession removes a portal session.
func (s *Store) DeleteSession(ctx context.Context, userID uuid.UUID, portal string) error {
	return s.user(ctx, userID, func(qs *q.Queries) error {
		return qs.DeletePortalSession(ctx, q.DeletePortalSessionParams{
			UserID: userID,
			Portal: portal,
		})
	})
}

// SubmitsToday counts how many applications were submitted for the portal since the given timestamp.
func (s *Store) SubmitsToday(ctx context.Context, userID uuid.UUID, portal string, since time.Time) (int, error) {
	var count int64
	err := s.user(ctx, userID, func(qs *q.Queries) error {
		n, err := qs.PortalSubmitsSince(ctx, q.PortalSubmitsSinceParams{
			UserID:    userID,
			Source:    portal,
			StartedAt: since,
		})
		count = n
		return err
	})
	return int(count), err
}

// ActiveRuns counts how many applications for the portal are currently running or awaiting input.
func (s *Store) ActiveRuns(ctx context.Context, userID uuid.UUID, portal string) (int, error) {
	var count int64
	err := s.user(ctx, userID, func(qs *q.Queries) error {
		n, err := qs.ActivePortalRuns(ctx, q.ActivePortalRunsParams{
			UserID: userID,
			Source: portal,
		})
		count = n
		return err
	})
	return int(count), err
}
