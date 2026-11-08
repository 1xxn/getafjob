// Package pgstore implements assist.Store on PostgreSQL.
package pgstore

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/1xxn/getafjob/internal/assist"
	"github.com/1xxn/getafjob/internal/assist/adapters/pgstore/q"
	"github.com/1xxn/getafjob/internal/platform/db"
)

// Store is the PostgreSQL assist store.
type Store struct {
	pool *pgxpool.Pool
}

var _ assist.Store = (*Store)(nil)

// New returns a new PostgreSQL store for assist.
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
		return assist.ErrNotFound
	}
	return err
}

// CreatePairingCode stores a new pairing code.
func (s *Store) CreatePairingCode(ctx context.Context, p assist.PairingCode) error {
	return s.user(ctx, p.UserID, func(qs *q.Queries) error {
		return qs.CreatePairingCode(ctx, q.CreatePairingCodeParams{
			ID:        p.ID,
			UserID:    p.UserID,
			CodeHash:  p.CodeHash,
			CreatedAt: p.CreatedAt,
			ExpiresAt: p.ExpiresAt,
		})
	})
}

// GetPairingCode retrieves a valid pairing code by its hash.
func (s *Store) GetPairingCode(ctx context.Context, codeHash []byte, now time.Time) (assist.PairingCode, error) {
	var p assist.PairingCode
	err := s.system(ctx, func(qs *q.Queries) error {
		r, err := qs.GetPairingCode(ctx, q.GetPairingCodeParams{
			CodeHash:  codeHash,
			ExpiresAt: now,
		})
		if err != nil {
			return notFound(err)
		}
		p = assist.PairingCode{
			ID:        r.ID,
			UserID:    r.UserID,
			CodeHash:  r.CodeHash,
			CreatedAt: r.CreatedAt,
			ExpiresAt: r.ExpiresAt,
		}
		return nil
	})
	return p, err
}

// DeletePairingCode removes a pairing code.
func (s *Store) DeletePairingCode(ctx context.Context, id uuid.UUID) error {
	return s.system(ctx, func(qs *q.Queries) error {
		return qs.DeletePairingCode(ctx, id)
	})
}

// CreateExtensionToken stores a new extension token.
func (s *Store) CreateExtensionToken(ctx context.Context, t assist.Token) error {
	return s.user(ctx, t.UserID, func(qs *q.Queries) error {
		return qs.CreateExtensionToken(ctx, q.CreateExtensionTokenParams{
			ID:         t.ID,
			UserID:     t.UserID,
			TokenHash:  t.TokenHash,
			ClientName: t.ClientName,
			CreatedAt:  t.CreatedAt,
			ExpiresAt:  t.ExpiresAt,
			LastUsedAt: t.LastUsedAt,
		})
	})
}

// GetExtensionToken retrieves a token by its hash.
func (s *Store) GetExtensionToken(ctx context.Context, tokenHash []byte, now time.Time) (assist.Token, error) {
	var tok assist.Token
	err := s.system(ctx, func(qs *q.Queries) error {
		r, err := qs.GetExtensionToken(ctx, q.GetExtensionTokenParams{
			TokenHash: tokenHash,
			ExpiresAt: now,
		})
		if err != nil {
			return notFound(err)
		}
		tok = assist.Token{
			ID:         r.ID,
			UserID:     r.UserID,
			TokenHash:  r.TokenHash,
			ClientName: r.ClientName,
			CreatedAt:  r.CreatedAt,
			ExpiresAt:  r.ExpiresAt,
			LastUsedAt: r.LastUsedAt,
		}
		return nil
	})
	return tok, err
}

// TouchExtensionToken updates the last used timestamp of a token.
func (s *Store) TouchExtensionToken(ctx context.Context, tokenHash []byte, now time.Time) error {
	return s.system(ctx, func(qs *q.Queries) error {
		return qs.TouchExtensionToken(ctx, q.TouchExtensionTokenParams{
			TokenHash:  tokenHash,
			LastUsedAt: &now,
		})
	})
}

// ListExtensionTokens returns all extension tokens for a user.
func (s *Store) ListExtensionTokens(ctx context.Context, userID uuid.UUID) ([]assist.Token, error) {
	var list []assist.Token
	err := s.user(ctx, userID, func(qs *q.Queries) error {
		rows, err := qs.ListExtensionTokens(ctx, userID)
		if err != nil {
			return err
		}
		list = make([]assist.Token, len(rows))
		for i, r := range rows {
			list[i] = assist.Token{
				ID:         r.ID,
				UserID:     r.UserID,
				ClientName: r.ClientName,
				CreatedAt:  r.CreatedAt,
				ExpiresAt:  r.ExpiresAt,
				LastUsedAt: r.LastUsedAt,
			}
		}
		return nil
	})
	return list, err
}

// RevokeExtensionToken deletes an extension token.
func (s *Store) RevokeExtensionToken(ctx context.Context, id, userID uuid.UUID) error {
	return s.user(ctx, userID, func(qs *q.Queries) error {
		return qs.RevokeExtensionToken(ctx, q.RevokeExtensionTokenParams{
			ID:     id,
			UserID: userID,
		})
	})
}
