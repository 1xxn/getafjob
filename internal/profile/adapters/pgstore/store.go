// Package pgstore implements profile.Store on PostgreSQL. Every call runs in the user's own transaction, so
// row-level security keeps users apart.
package pgstore

//go:generate go tool sqlc generate -f ../../../../sqlc.yaml

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/1xxn/getafjob/internal/platform/db"
	"github.com/1xxn/getafjob/internal/profile"
	"github.com/1xxn/getafjob/internal/profile/adapters/pgstore/q"
)

// Store is the PostgreSQL profile store.
type Store struct{ pool *pgxpool.Pool }

var _ profile.Store = (*Store)(nil)

// New returns a store on pool (application role).
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) user(ctx context.Context, userID uuid.UUID, fn func(*q.Queries) error) error {
	return db.InUserTx(ctx, s.pool, userID, func(tx pgx.Tx) error { return fn(q.New(tx)) })
}

// Load returns the stored profile; an empty Stored if there is none.
func (s *Store) Load(ctx context.Context, userID uuid.UUID) (st profile.Stored, err error) {
	err = s.user(ctx, userID, func(qs *q.Queries) error {
		r, err := qs.LoadProfile(ctx, userID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		st = profile.Stored{Sealed: r.Sealed, Preferences: r.Preferences, Version: r.Version, UpdatedAt: r.UpdatedAt}
		return nil
	})
	return st, err
}

// SaveProfile stores the sealed profile if the version still matches.
func (s *Store) SaveProfile(ctx context.Context, userID uuid.UUID, sealed []byte, version int64, at time.Time) (ok bool, err error) {
	err = s.user(ctx, userID, func(qs *q.Queries) error {
		n, err := qs.SaveProfile(ctx, q.SaveProfileParams{UserID: userID, Sealed: sealed, UpdatedAt: at, Version: version})
		ok = n == 1
		return err
	})
	return ok, err
}

// SavePreferences stores the preferences.
func (s *Store) SavePreferences(ctx context.Context, userID uuid.UUID, prefs []byte, at time.Time) error {
	return s.user(ctx, userID, func(qs *q.Queries) error {
		return qs.SavePreferences(ctx, q.SavePreferencesParams{UserID: userID, Preferences: prefs, UpdatedAt: at})
	})
}

// ActiveUsers lists the enabled users with a profile or preferences (system role, for matching).
func (s *Store) ActiveUsers(ctx context.Context) (ids []uuid.UUID, err error) {
	err = db.InSystemTx(ctx, s.pool, func(tx pgx.Tx) error {
		ids, err = q.New(tx).ActiveProfileUsers(ctx)
		return err
	})
	return ids, err
}

// ActivePreferences returns the preferences JSON of enabled users (system role, for the crawl's search terms).
func (s *Store) ActivePreferences(ctx context.Context) (prefs [][]byte, err error) {
	err = db.InSystemTx(ctx, s.pool, func(tx pgx.Tx) error {
		prefs, err = q.New(tx).ActivePreferences(ctx)
		return err
	})
	return prefs, err
}
