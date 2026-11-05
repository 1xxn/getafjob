package identity

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Invitation is a pending invitation.
type Invitation struct {
	ID        uuid.UUID
	Email     string
	Role      Role
	ExpiresAt time.Time
	Accepted  bool
}

// NewInvitation is stored by Invite.
type NewInvitation struct {
	Email     string
	Role      Role
	Hash      Hash
	CreatedBy *uuid.UUID
	ExpiresAt time.Time
}

// NewSession is stored when someone signs in.
type NewSession struct {
	Hash      Hash
	Method    Method
	CreatedAt time.Time
	ExpiresAt time.Time
}

// StoredSession is a session row as Sessions returns it.
type StoredSession struct {
	Hash Hash
	SessionInfo
}

// AuditEvent is an entry of the audit log: a code plus params, never personal data.
type AuditEvent struct {
	UserID *uuid.UUID
	Actor  string // user, admin, system
	Code   string
	Params map[string]any
}

// Store persists identity data. Methods that change several rows run in one transaction.
type Store interface {
	UserByEmail(ctx context.Context, email string) (User, error)
	CreateInvitation(ctx context.Context, inv NewInvitation, audit AuditEvent) error
	InvitationByHash(ctx context.Context, h Hash) (Invitation, error)
	// AcceptInvitation creates the user, marks the invitation used and creates the session atomically.
	AcceptInvitation(ctx context.Context, inv Invitation, u User, s NewSession, at time.Time) (User, error)
	// CreateMagicLink stores a link unless the user already got limit links since 'since'; returns false then.
	CreateMagicLink(ctx context.Context, userID uuid.UUID, h Hash, at, expires, since time.Time, limit int) (bool, error)
	// ConsumeMagicLink marks the link used and creates the session atomically.
	ConsumeMagicLink(ctx context.Context, h Hash, s NewSession, at time.Time) (User, error)
	SessionByHash(ctx context.Context, h Hash) (User, Session, error)
	TouchSession(ctx context.Context, h Hash, at time.Time) error
	// RotateSession gives the session old the new token next and marks old replaced; false when another request
	// rotated it first.
	RotateSession(ctx context.Context, old, next Hash, at time.Time) (bool, error)
	// Sessions lists the user's current sessions with their token hashes.
	Sessions(ctx context.Context, userID uuid.UUID) ([]StoredSession, error)
	// DeleteOtherSessions ends every session of the user except keep and returns how many ended.
	DeleteOtherSessions(ctx context.Context, userID uuid.UUID, keep Hash, audit AuditEvent) (int, error)
	// DeleteUserSessions ends every session of the user.
	DeleteUserSessions(ctx context.Context, userID uuid.UUID, audit AuditEvent) (int, error)
	DeleteSession(ctx context.Context, h Hash, audit AuditEvent) error
	DeleteExpired(ctx context.Context, before time.Time) error
}

// Mail is a plain-text mail.
type Mail struct {
	To      string
	Subject string
	Text    string
}

// Mailer sends mail.
type Mailer interface {
	Send(ctx context.Context, m Mail) error
}
