package pgstore

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/identity/adapters/pgstore/q"
)

var _ identity.AdminStore = (*Store)(nil)

// Users lists every account with its activity.
func (s *Store) Users(ctx context.Context, now time.Time) ([]identity.UserSummary, error) {
	var out []identity.UserSummary
	err := s.system(ctx, func(qs *q.Queries) error {
		rows, err := qs.ListUsers(ctx, now)
		for _, r := range rows {
			out = append(out, identity.UserSummary{User: toUser(r.ID, r.Email, r.DisplayName, r.Role, r.Locale, r.DisabledAt),
				CreatedAt: r.CreatedAt, LastSeen: r.LastSeen, Sessions: int(r.Sessions), Passkey: r.HasPasskey})
		}
		return err
	})
	return out, err
}

// PendingInvites lists the invitations that can still be accepted.
func (s *Store) PendingInvites(ctx context.Context, now time.Time) ([]identity.PendingInvite, error) {
	var out []identity.PendingInvite
	err := s.system(ctx, func(qs *q.Queries) error {
		rows, err := qs.PendingInvitations(ctx, now)
		for _, r := range rows {
			out = append(out, identity.PendingInvite{ID: r.ID, Email: r.Email, Role: identity.Role(r.Role), CreatedAt: r.CreatedAt,
				ExpiresAt: r.ExpiresAt})
		}
		return err
	})
	return out, err
}

// RevokeInvite deletes an open invitation.
func (s *Store) RevokeInvite(ctx context.Context, id uuid.UUID, e identity.AuditEvent) (ok bool, err error) {
	err = s.system(ctx, func(qs *q.Queries) error {
		n, err := qs.RevokeInvitation(ctx, id)
		if err != nil || n == 0 {
			return err
		}
		ok = true
		return audit(ctx, qs, e)
	})
	return ok, err
}

// SetRole changes a role; the active admins are locked first so two demotions cannot both pass the check.
func (s *Store) SetRole(ctx context.Context, userID uuid.UUID, role identity.Role, e identity.AuditEvent) error {
	return s.system(ctx, func(qs *q.Queries) error {
		admins, err := qs.LockAdmins(ctx)
		if err != nil {
			return err
		}
		if role != identity.RoleAdmin && len(admins) == 1 && slices.Contains(admins, userID) {
			return identity.ErrLastAdmin
		}
		n, err := qs.SetUserRole(ctx, q.SetUserRoleParams{ID: userID, Role: string(role)})
		if err != nil {
			return err
		}
		if n == 0 {
			return identity.ErrNotFound
		}
		return audit(ctx, qs, e)
	})
}

// LastAdmin reports whether userID is the only active admin.
func (s *Store) LastAdmin(ctx context.Context, userID uuid.UUID) (last bool, err error) {
	err = s.system(ctx, func(qs *q.Queries) error {
		admins, err := qs.LockAdmins(ctx)
		last = len(admins) == 1 && admins[0] == userID
		return err
	})
	return last, err
}

// AdminAudit returns the newest admin actions.
func (s *Store) AdminAudit(ctx context.Context, limit int) ([]identity.AuditEntry, error) {
	var out []identity.AuditEntry
	err := s.system(ctx, func(qs *q.Queries) error {
		rows, err := qs.AdminAuditEvents(ctx, int32(min(limit, 1000))) //nolint:gosec // bounded above
		for _, r := range rows {
			var params map[string]any
			if len(r.Params) > 0 {
				_ = json.Unmarshal(r.Params, &params)
			}
			out = append(out, identity.AuditEntry{AuditEvent: identity.AuditEvent{UserID: r.UserID, Actor: r.Actor, Code: r.Code,
				Params: params}, At: r.CreatedAt})
		}
		return err
	})
	return out, err
}

// Audit records one event.
func (s *Store) Audit(ctx context.Context, e identity.AuditEvent) error {
	return s.system(ctx, func(qs *q.Queries) error { return audit(ctx, qs, e) })
}
