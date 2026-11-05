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
	"github.com/jackc/pgx/v5/pgconn"
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

// ConsumeMagicLink marks the link used and starts a session, unless the account is disabled. An account with a
// passkey needs a ready recovery, which is used up; without one the link is spent and no session starts.
func (s *Store) ConsumeMagicLink(ctx context.Context, h identity.Hash, sess identity.NewSession, at time.Time) (u identity.User, recovered bool, err error) {
	wait := false
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
		hasKeys, err := qs.HasPasskeys(ctx, userID)
		if err != nil {
			return err
		}
		if hasKeys {
			if _, err := qs.UseRecovery(ctx, q.UseRecoveryParams{UserID: userID, At: at}); errors.Is(err, pgx.ErrNoRows) {
				wait = true
				return audit(ctx, qs, identity.AuditEvent{UserID: &u.ID, Actor: "system", Code: "identity.magic_link_refused"})
			} else if err != nil {
				return err
			}
			recovered = true
		}
		if err := qs.CreateSession(ctx, q.CreateSessionParams{TokenHash: sess.Hash[:], UserID: u.ID, Method: string(sess.Method),
			CreatedAt: sess.CreatedAt, ExpiresAt: sess.ExpiresAt}); err != nil {
			return err
		}
		code := "identity.signed_in"
		if recovered {
			code = "identity.recovered"
		}
		return audit(ctx, qs, identity.AuditEvent{UserID: &u.ID, Actor: "user", Code: code,
			Params: map[string]any{"method": sess.Method}})
	})
	if err == nil && wait {
		err = identity.ErrRecoveryWait
	}
	return u, recovered, err
}

// RecoveryState tells whether the user has a passkey and returns a pending recovery.
func (s *Store) RecoveryState(ctx context.Context, userID uuid.UUID) (has bool, r *identity.Recovery, err error) {
	err = s.system(ctx, func(qs *q.Queries) error {
		if has, err = qs.HasPasskeys(ctx, userID); err != nil {
			return err
		}
		row, err := qs.GetRecovery(ctx, userID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		r = &identity.Recovery{RequestedAt: row.RequestedAt, ReadyAt: row.ReadyAt, ExpiresAt: row.ExpiresAt}
		return nil
	})
	return has, r, err
}

// StartRecovery stores a new recovery unless one is pending or ready.
func (s *Store) StartRecovery(ctx context.Context, userID uuid.UUID, r identity.Recovery, e identity.AuditEvent) (cur identity.Recovery, started bool, err error) {
	err = s.system(ctx, func(qs *q.Queries) error {
		row, err := qs.StartRecovery(ctx, q.StartRecoveryParams{UserID: userID, RequestedAt: r.RequestedAt, ReadyAt: r.ReadyAt, ExpiresAt: r.ExpiresAt})
		if errors.Is(err, pgx.ErrNoRows) {
			old, err := qs.GetRecovery(ctx, userID)
			cur = identity.Recovery{RequestedAt: old.RequestedAt, ReadyAt: old.ReadyAt, ExpiresAt: old.ExpiresAt}
			return err
		}
		if err != nil {
			return err
		}
		cur, started = identity.Recovery{RequestedAt: r.RequestedAt, ReadyAt: row.ReadyAt, ExpiresAt: row.ExpiresAt}, true
		return audit(ctx, qs, e)
	})
	return cur, started, err
}

// CancelRecovery deletes a pending recovery.
func (s *Store) CancelRecovery(ctx context.Context, userID uuid.UUID, e identity.AuditEvent) (ok bool, err error) {
	err = s.system(ctx, func(qs *q.Queries) error {
		n, err := qs.CancelRecovery(ctx, userID)
		if err != nil || n == 0 {
			return err
		}
		ok = true
		return audit(ctx, qs, e)
	})
	return ok, err
}

// SessionByHash resolves a session cookie.
func (s *Store) SessionByHash(ctx context.Context, h identity.Hash) (u identity.User, sess identity.Session, err error) {
	err = s.system(ctx, func(qs *q.Queries) error {
		r, err := qs.GetSession(ctx, h[:])
		if err != nil {
			return notFound(err)
		}
		u = toUser(r.UserID, r.Email, r.DisplayName, r.Role, r.Locale, r.DisabledAt)
		sess = identity.Session{UserID: r.UserID, Method: identity.Method(r.Method), CreatedAt: r.CreatedAt, LastSeen: r.LastSeenAt,
			ExpiresAt: r.ExpiresAt, RotatedAt: deref(r.RotatedAt), ReplacedAt: deref(r.ReplacedAt)}
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

func deref(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

func (s *Store) user(ctx context.Context, userID uuid.UUID, fn func(*q.Queries) error) error {
	return db.InUserTx(ctx, s.pool, userID, func(tx pgx.Tx) error { return fn(q.New(tx)) })
}

// RotateSession replaces the token old by next in one transaction; the absolute expiry stays.
func (s *Store) RotateSession(ctx context.Context, old, next identity.Hash, at time.Time) (rotated bool, err error) {
	err = s.system(ctx, func(qs *q.Queries) error {
		r, err := qs.ReplaceSession(ctx, q.ReplaceSessionParams{TokenHash: old[:], At: &at})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		rotated = true
		return qs.CreateRotatedSession(ctx, q.CreateRotatedSessionParams{TokenHash: next[:], UserID: r.UserID, Method: r.Method,
			CreatedAt: r.CreatedAt, LastSeenAt: at, ExpiresAt: r.ExpiresAt, Family: r.Family})
	})
	return rotated, err
}

// Sessions lists the user's current sessions (user context, row-level security).
func (s *Store) Sessions(ctx context.Context, userID uuid.UUID) (out []identity.StoredSession, err error) {
	err = s.user(ctx, userID, func(qs *q.Queries) error {
		rows, err := qs.UserSessions(ctx, userID)
		for _, r := range rows {
			var h identity.Hash
			copy(h[:], r.TokenHash)
			out = append(out, identity.StoredSession{Hash: h, SessionInfo: identity.SessionInfo{
				Method: identity.Method(r.Method), CreatedAt: r.CreatedAt, LastSeen: r.LastSeenAt}})
		}
		return err
	})
	return out, err
}

// DeleteOtherSessions ends the user's other sessions and records it.
func (s *Store) DeleteOtherSessions(ctx context.Context, userID uuid.UUID, keep identity.Hash, e identity.AuditEvent) (n int, err error) {
	err = s.user(ctx, userID, func(qs *q.Queries) error {
		rows, err := qs.DeleteOtherSessions(ctx, q.DeleteOtherSessionsParams{UserID: userID, Keep: keep[:]})
		if err != nil {
			return err
		}
		n = int(rows)
		if e.Params == nil {
			e.Params = map[string]any{}
		}
		e.Params["count"] = n
		return audit(ctx, qs, e)
	})
	return n, err
}

// DeleteUserSessions ends every session of the user and records it.
func (s *Store) DeleteUserSessions(ctx context.Context, userID uuid.UUID, e identity.AuditEvent) (n int, err error) {
	err = s.system(ctx, func(qs *q.Queries) error {
		rows, err := qs.DeleteUserSessions(ctx, userID)
		if err != nil {
			return err
		}
		n = int(rows)
		if e.Params == nil {
			e.Params = map[string]any{}
		}
		e.Params["count"] = n
		return audit(ctx, qs, e)
	})
	return n, err
}

// SaveChallenge stores a WebAuthn ceremony state.
func (s *Store) SaveChallenge(ctx context.Context, c identity.Challenge) (id uuid.UUID, err error) {
	err = s.system(ctx, func(qs *q.Queries) error {
		id, err = qs.CreateChallenge(ctx, q.CreateChallengeParams{UserID: c.UserID, Kind: c.Kind, Data: c.Data, ExpiresAt: c.ExpiresAt})
		return err
	})
	return id, err
}

// TakeChallenge returns and deletes a valid challenge.
func (s *Store) TakeChallenge(ctx context.Context, id uuid.UUID, kind string, now time.Time) (c identity.Challenge, err error) {
	err = s.system(ctx, func(qs *q.Queries) error {
		r, err := qs.TakeChallenge(ctx, q.TakeChallengeParams{ID: id, Kind: kind, Now: now})
		if err != nil {
			return notFound(err)
		}
		c = identity.Challenge{UserID: r.UserID, Kind: kind, Data: r.Data}
		return nil
	})
	return c, err
}

// UserByID returns an account and its passkeys.
func (s *Store) UserByID(ctx context.Context, id uuid.UUID) (u identity.User, keys []identity.Passkey, err error) {
	err = s.system(ctx, func(qs *q.Queries) error {
		r, err := qs.GetUser(ctx, id)
		if err != nil {
			return notFound(err)
		}
		u = toUser(r.ID, r.Email, r.DisplayName, r.Role, r.Locale, r.DisabledAt)
		rows, err := qs.UserPasskeys(ctx, id)
		for _, p := range rows {
			keys = append(keys, identity.Passkey{ID: p.ID, Name: p.Name, Credential: p.Credential, CreatedAt: p.CreatedAt,
				LastUsed: deref(p.LastUsedAt)})
		}
		return err
	})
	return u, keys, err
}

// AddPasskey stores a new credential.
func (s *Store) AddPasskey(ctx context.Context, userID uuid.UUID, p identity.Passkey, e identity.AuditEvent) error {
	return s.user(ctx, userID, func(qs *q.Queries) error {
		if err := qs.InsertPasskey(ctx, q.InsertPasskeyParams{ID: p.ID, UserID: userID, Name: p.Name, Credential: p.Credential,
			CreatedAt: p.CreatedAt}); err != nil {
			if pgErr := (*pgconn.PgError)(nil); errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return identity.ErrPasskeyFailed // the credential is registered already (by anyone)
			}
			return err
		}
		return audit(ctx, qs, e)
	})
}

// SignInWithPasskey updates the credential (sign count) and creates the session.
func (s *Store) SignInWithPasskey(ctx context.Context, userID uuid.UUID, id, credential []byte, sess identity.NewSession, at time.Time) (u identity.User, err error) {
	err = s.system(ctx, func(qs *q.Queries) error {
		if _, err := qs.UsePasskey(ctx, q.UsePasskeyParams{ID: id, UserID: userID, Credential: credential, LastUsedAt: &at}); err != nil {
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
		// The owner is back with a passkey: a recovery that someone may have started is void.
		if n, err := qs.CancelRecovery(ctx, u.ID); err != nil {
			return err
		} else if n > 0 {
			if err := audit(ctx, qs, identity.AuditEvent{UserID: &u.ID, Actor: "system", Code: "identity.recovery_canceled"}); err != nil {
				return err
			}
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

// DeletePasskey removes a credential of the user.
func (s *Store) DeletePasskey(ctx context.Context, userID uuid.UUID, id []byte, e identity.AuditEvent) (ok bool, err error) {
	err = s.user(ctx, userID, func(qs *q.Queries) error {
		n, err := qs.DeletePasskey(ctx, q.DeletePasskeyParams{ID: id, UserID: userID})
		if err != nil || n == 0 {
			return err
		}
		ok = true
		return audit(ctx, qs, e)
	})
	return ok, err
}
