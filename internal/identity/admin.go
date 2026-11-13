package identity

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrLastAdmin refuses a change that would leave no active admin.
var ErrLastAdmin = errors.New("identity: the last admin must stay")

// ErrSelf refuses an admin action on the admin's own account (use the account page instead).
var ErrSelf = errors.New("identity: not on the own account")

// UserSummary is one row of the admin user list.
type UserSummary struct {
	User
	CreatedAt time.Time
	LastSeen  time.Time
	Sessions  int
	Passkey   bool
}

// PendingInvite is an invitation not yet accepted and not expired.
type PendingInvite struct {
	ID        uuid.UUID
	Email     string
	Role      Role
	CreatedAt time.Time
	ExpiresAt time.Time
}

// AuditEntry is an audit event with its time.
type AuditEntry struct {
	AuditEvent
	At time.Time
}

// AdminStore holds what the admin console reads and changes; all in system context.
type AdminStore interface {
	Users(ctx context.Context, now time.Time) ([]UserSummary, error)
	PendingInvites(ctx context.Context, now time.Time) ([]PendingInvite, error)
	RevokeInvite(ctx context.Context, id uuid.UUID, audit AuditEvent) (bool, error)
	// SetRole changes the role unless it would leave no active admin (ErrLastAdmin).
	SetRole(ctx context.Context, userID uuid.UUID, role Role, audit AuditEvent) error
	AdminAudit(ctx context.Context, limit int) ([]AuditEntry, error)
	Audit(ctx context.Context, e AuditEvent) error
	// SetDisabled disables (at set) or enables (at nil) an account; disabling ends its sessions and refuses the last
	// active admin (ErrLastAdmin).
	SetDisabled(ctx context.Context, userID uuid.UUID, at *time.Time, audit AuditEvent) error
	// LastAdmin reports whether userID is the only active admin.
	LastAdmin(ctx context.Context, userID uuid.UUID) (bool, error)
}

func (s *Service) admin() AdminStore { return s.store.(AdminStore) }

func adminAudit(by uuid.UUID, code string, params map[string]any) AuditEvent {
	return AuditEvent{UserID: &by, Actor: "admin", Code: code, Params: params}
}

// Users lists every account for the admin console.
func (s *Service) Users(ctx context.Context) ([]UserSummary, error) {
	return s.admin().Users(ctx, s.now())
}

// PendingInvites lists the open invitations.
func (s *Service) PendingInvites(ctx context.Context) ([]PendingInvite, error) {
	return s.admin().PendingInvites(ctx, s.now())
}

// RevokeInvite deletes an open invitation; the link stops working.
func (s *Service) RevokeInvite(ctx context.Context, by, id uuid.UUID) error {
	ok, err := s.admin().RevokeInvite(ctx, id, adminAudit(by, "admin.invitation_revoked", map[string]any{"invitation": id}))
	if err == nil && !ok {
		return ErrNotFound
	}
	return err
}

// SetRole gives an account another role; an admin cannot change their own role.
func (s *Service) SetRole(ctx context.Context, by, userID uuid.UUID, role Role) error {
	if by == userID {
		return ErrSelf
	}
	if !role.Valid() {
		return ErrInvalidRole
	}
	return s.admin().SetRole(ctx, userID, role, adminAudit(by, "admin.role_changed", map[string]any{"user": userID, "role": role}))
}

// SignOutAccount ends every session of an account and records who did it on both sides.
func (s *Service) SignOutAccount(ctx context.Context, by, userID uuid.UUID) (int, error) {
	if by == userID {
		return 0, ErrSelf
	}
	n, err := s.store.DeleteUserSessions(ctx, userID, AuditEvent{UserID: &userID, Actor: "admin", Code: "identity.signed_out_by_admin"})
	if err != nil {
		return 0, err
	}
	return n, s.admin().Audit(ctx, adminAudit(by, "admin.signed_out", map[string]any{"user": userID, "count": n}))
}

// SetDisabled disables or enables an account from the admin console; a disabled account cannot sign in and its
// sessions end. An admin cannot disable their own account.
func (s *Service) SetDisabled(ctx context.Context, by, userID uuid.UUID, disabled bool) error {
	if by == userID {
		return ErrSelf
	}
	code := "admin.user_enabled"
	if disabled {
		code = "admin.user_disabled"
	}
	return s.setDisabled(ctx, userID, disabled, adminAudit(by, code, map[string]any{"user": userID}))
}

// SetDisabledByEmail disables or enables the account with email (admin command).
func (s *Service) SetDisabledByEmail(ctx context.Context, email string, disabled bool) error {
	u, err := s.UserByEmail(ctx, email)
	if err != nil {
		return err
	}
	code := "identity.enabled_by_admin"
	if disabled {
		code = "identity.disabled_by_admin"
	}
	return s.setDisabled(ctx, u.ID, disabled, AuditEvent{UserID: &u.ID, Actor: "admin", Code: code})
}

func (s *Service) setDisabled(ctx context.Context, userID uuid.UUID, disabled bool, audit AuditEvent) error {
	var at *time.Time
	if disabled {
		now := s.now()
		at = &now
	}
	return s.admin().SetDisabled(ctx, userID, at, audit)
}

// CheckDeletion allows users to delete their own account only from a fresh session (ErrReauthRequired), so a
// stolen session cannot wipe it, and never as the last admin (ErrLastAdmin).
func (s *Service) CheckDeletion(ctx context.Context, userID uuid.UUID, token string) error {
	if _, err := s.freshSession(ctx, userID, token); err != nil {
		return err
	}
	last, err := s.admin().LastAdmin(ctx, userID)
	if err != nil {
		return err
	}
	if last {
		return ErrLastAdmin
	}
	return nil
}

// RecordDeletion notes on the admin's own log that an account was deleted (the account's log goes with it).
func (s *Service) RecordDeletion(ctx context.Context, by, userID uuid.UUID) error {
	return s.admin().Audit(ctx, adminAudit(by, "admin.user_deleted", map[string]any{"user": userID}))
}

// RecordAdmin notes an admin action that is not about an account (sources, the job queue) on the admin's log.
func (s *Service) RecordAdmin(ctx context.Context, by uuid.UUID, code string, params map[string]any) error {
	return s.admin().Audit(ctx, adminAudit(by, code, params))
}

// AdminAudit returns the newest admin actions.
func (s *Service) AdminAudit(ctx context.Context, limit int) ([]AuditEntry, error) {
	return s.admin().AdminAudit(ctx, min(max(limit, 1), 200))
}
