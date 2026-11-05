// Package pgstore implements identity.Store on PostgreSQL (sqlc queries in q).
//
// Sign-in happens before a user is known, so these operations run in the system context; each one is a single,
// reviewed transaction.
package pgstore

//go:generate go tool sqlc generate -f ../../../../sqlc.yaml

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/identity/adapters/pgstore/q"
	"github.com/1xxn/getafjob/internal/platform/db"
	"github.com/1xxn/getafjob/internal/platform/i18n"
)

// Store is the PostgreSQL identity store.
type Store struct{ pool *pgxpool.Pool }

var _ identity.Store = (*Store)(nil)

// New returns a store on pool (application role).
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) system(ctx context.Context, fn func(*q.Queries) error) error {
	return db.InSystemTx(ctx, s.pool, func(tx pgx.Tx) error { return fn(q.New(tx)) })
}

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.ErrNotFound
	}
	return err
}

func audit(ctx context.Context, qs *q.Queries, e identity.AuditEvent) error {
	params := e.Params
	if params == nil {
		params = map[string]any{}
	}
	b, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return qs.InsertAuditEvent(ctx, q.InsertAuditEventParams{UserID: e.UserID, Actor: e.Actor, Code: e.Code, Params: b})
}

func toUser(id uuid.UUID, email, name, role, locale string, disabled *time.Time) identity.User {
	return identity.User{ID: id, Email: email, DisplayName: name, Role: identity.Role(role),
		Locale: i18n.Parse(locale), Disabled: disabled != nil}
}

// UserByEmail looks up an account.
func (s *Store) UserByEmail(ctx context.Context, email string) (u identity.User, err error) {
	err = s.system(ctx, func(qs *q.Queries) error {
		r, err := qs.GetUserByEmail(ctx, email)
		if err != nil {
			return notFound(err)
		}
		u = toUser(r.ID, r.Email, r.DisplayName, r.Role, r.Locale, r.DisabledAt)
		return nil
	})
	return u, err
}

// CreateInvitation stores an invitation and its audit event.
func (s *Store) CreateInvitation(ctx context.Context, inv identity.NewInvitation, e identity.AuditEvent) error {
	return s.system(ctx, func(qs *q.Queries) error {
		if err := qs.CreateInvitation(ctx, q.CreateInvitationParams{Email: inv.Email, Role: string(inv.Role),
			TokenHash: inv.Hash[:], CreatedBy: inv.CreatedBy, ExpiresAt: inv.ExpiresAt}); err != nil {
			return err
		}
		return audit(ctx, qs, e)
	})
}

// InvitationByHash looks up an invitation.
func (s *Store) InvitationByHash(ctx context.Context, h identity.Hash) (inv identity.Invitation, err error) {
	err = s.system(ctx, func(qs *q.Queries) error {
		r, err := qs.GetInvitationByHash(ctx, h[:])
		if err != nil {
			return notFound(err)
		}
		inv = identity.Invitation{ID: r.ID, Email: r.Email, Role: identity.Role(r.Role), ExpiresAt: r.ExpiresAt, Accepted: r.AcceptedAt != nil}
		return nil
	})
	return inv, err
}

// AcceptInvitation creates the user, marks the invitation and starts the session in one transaction.
func (s *Store) AcceptInvitation(ctx context.Context, inv identity.Invitation, u identity.User, sess identity.NewSession, at time.Time) (created identity.User, err error) {
	err = s.system(ctx, func(qs *q.Queries) error {
		r, err := qs.CreateUser(ctx, q.CreateUserParams{Email: u.Email, DisplayName: u.DisplayName, Role: string(u.Role), Locale: string(u.Locale)})
		if err != nil {
			return fmt.Errorf("create user: %w", err)
		}
		n, err := qs.MarkInvitationAccepted(ctx, q.MarkInvitationAcceptedParams{ID: inv.ID, AcceptedAt: &at, AcceptedUser: &r.ID})
		if err != nil {
			return err
		}
		if n != 1 {
			return identity.ErrInvalidToken // accepted concurrently
		}
		if err := qs.CreateSession(ctx, q.CreateSessionParams{TokenHash: sess.Hash[:], UserID: r.ID, Method: string(sess.Method),
			CreatedAt: sess.CreatedAt, ExpiresAt: sess.ExpiresAt}); err != nil {
			return err
		}
		created = toUser(r.ID, r.Email, r.DisplayName, r.Role, r.Locale, r.DisabledAt)
		return audit(ctx, qs, identity.AuditEvent{UserID: &r.ID, Actor: "user", Code: "identity.invitation_accepted",
			Params: map[string]any{"role": r.Role}})
	})
	return created, err
}

// CreateMagicLink stores a link unless the rate limit is reached.
func (s *Store) CreateMagicLink(ctx context.Context, userID uuid.UUID, h identity.Hash, at, expires, since time.Time, limit int) (ok bool, err error) {
	err = s.system(ctx, func(qs *q.Queries) error {
		n, err := qs.CountMagicLinksSince(ctx, q.CountMagicLinksSinceParams{UserID: userID, CreatedAt: since})
		if err != nil {
			return err
		}
		if n >= int64(limit) {
			return audit(ctx, qs, identity.AuditEvent{UserID: &userID, Actor: "system", Code: "identity.magic_link_throttled"})
		}
		if err := qs.CreateMagicLink(ctx, q.CreateMagicLinkParams{TokenHash: h[:], UserID: userID, CreatedAt: at, ExpiresAt: expires}); err != nil {
			return err
		}
		ok = true
		return audit(ctx, qs, identity.AuditEvent{UserID: &userID, Actor: "user", Code: "identity.magic_link_requested"})
	})
	return ok, err
}

// ConsumeMagicLink marks the link used and starts a session, unless the account is disabled.
func (s *Store) ConsumeMagicLink(ctx context.Context, h identity.Hash, sess identity.NewSession, at time.Time) (u identity.User, err error) {
	err = s.system(ctx, func(qs *q.Queries) error {
		userID, err := qs.UseMagicLink(ctx, q.UseMagicLinkParams{TokenHash: h[:], UsedAt: &at})
		if err != nil {
			return notFound(err)
		}
		r, err := qs.GetUser(ctx, userID)
		if err != nil {
			return err
		}
		u = toUser(r.ID, r.Email, r.DisplayName, r.Role, r.Locale, r.DisabledAt)
		if u.Disabled {
			return nil
		}
		if err := qs.CreateSession(ctx, q.CreateSessionParams{TokenHash: sess.Hash[:], UserID: u.ID, Method: string(sess.Method),
			CreatedAt: sess.CreatedAt, ExpiresAt: sess.ExpiresAt}); err != nil {
			return err
		}
		return audit(ctx, qs, identity.AuditEvent{UserID: &u.ID, Actor: "user", Code: "identity.signed_in",
			Params: map[string]any{"method": sess.Method}})
	})
	return u, err
}

// SessionByHash resolves a session cookie.
func (s *Store) SessionByHash(ctx context.Context, h identity.Hash) (u identity.User, sess identity.Session, err error) {
	err = s.system(ctx, func(qs *q.Queries) error {
		r, err := qs.GetSession(ctx, h[:])
		if err != nil {
			return notFound(err)
		}
		u = toUser(r.UserID, r.Email, r.DisplayName, r.Role, r.Locale, r.DisabledAt)
		sess = identity.Session{UserID: r.UserID, Method: identity.Method(r.Method), CreatedAt: r.CreatedAt, LastSeen: r.LastSeenAt, ExpiresAt: r.ExpiresAt}
		return nil
	})
	return u, sess, err
}

// TouchSession records activity.
func (s *Store) TouchSession(ctx context.Context, h identity.Hash, at time.Time) error {
	return s.system(ctx, func(qs *q.Queries) error {
		return qs.TouchSession(ctx, q.TouchSessionParams{TokenHash: h[:], LastSeenAt: at})
	})
}

// DeleteSession ends a session.
func (s *Store) DeleteSession(ctx context.Context, h identity.Hash, e identity.AuditEvent) error {
	return s.system(ctx, func(qs *q.Queries) error {
		if err := qs.DeleteSession(ctx, h[:]); err != nil {
			return err
		}
		return audit(ctx, qs, e)
	})
}

// DeleteExpired removes expired sessions, links and challenges.
func (s *Store) DeleteExpired(ctx context.Context, before time.Time) error {
	return s.system(ctx, func(qs *q.Queries) error { return qs.DeleteExpired(ctx, before) })
}
