package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/platform/buildinfo"
	"github.com/1xxn/getafjob/internal/web/views"
	"github.com/1xxn/getafjob/internal/web/views/ui"
)

// errDeleteAdmin refuses to delete an admin: take the role away first, so the last admin can never go.
var errDeleteAdmin = errors.New("web: delete an admin only after taking the role away")

// SystemFacts describe the running installation for the admin system page.
type SystemFacts struct {
	Postgres    string
	Migration   int64
	EgressProxy bool
	Mail        bool
	PublicURL   string
}

// SystemService reports SystemFacts.
type SystemService interface {
	Facts(ctx context.Context) (SystemFacts, error)
}

// adminOnly runs h for admins; everyone else gets the page that does not exist, so the console stays hidden.
func (s *Server) adminOnly(h func(http.ResponseWriter, *http.Request, *identity.User)) http.Handler {
	return s.page(func(w http.ResponseWriter, r *http.Request, u *identity.User) {
		if u == nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if u.Role != identity.RoleAdmin {
			s.notFound(w, r, u)
			return
		}
		h(w, r, u)
	})
}

func (s *Server) adminUsers(w http.ResponseWriter, r *http.Request, u *identity.User) {
	s.renderAdminUsers(w, r, u, http.StatusOK, views.AdminUsersData{Notice: adminNotice(r)})
}

func adminNotice(r *http.Request) string {
	if k := r.URL.Query().Get("done"); k != "" && strings.Trim(k, "abcdefghijklmnopqrstuvwxyz_") == "" {
		return views.T(r.Context(), "admin.done."+k)
	}
	return ""
}

func (s *Server) renderAdminUsers(w http.ResponseWriter, r *http.Request, u *identity.User, status int, d views.AdminUsersData) {
	ctx := r.Context()
	users, err := s.Identity.Users(ctx)
	if err != nil {
		s.failed(w, r, u, err)
		return
	}
	invites, err := s.Identity.PendingInvites(ctx)
	if err != nil {
		s.failed(w, r, u, err)
		return
	}
	audit, err := s.Identity.AdminAudit(ctx, 20)
	if err != nil {
		s.failed(w, r, u, err)
		return
	}
	d.Name = u.DisplayName
	if d.Form.Values == nil {
		d.Form.Values = map[string]string{"role": "user"}
	}
	emails := map[uuid.UUID]string{}
	for _, x := range users {
		emails[x.ID] = x.Email
		name := x.DisplayName
		if name == "" {
			name = x.Email
		}
		d.Users = append(d.Users, views.AdminUserRow{ID: x.ID.String(), Email: x.Email, Name: name, Role: string(x.Role),
			Created: views.When(ctx, x.CreatedAt), LastSeen: views.When(ctx, x.LastSeen), Sessions: x.Sessions,
			Passkey: x.Passkey, Self: x.ID == u.ID})
	}
	for _, i := range invites {
		d.Invites = append(d.Invites, views.AdminInviteRow{ID: i.ID.String(), Email: i.Email, Role: string(i.Role),
			Expires: views.When(ctx, i.ExpiresAt)})
	}
	for _, a := range audit {
		if text := auditText(ctx, a, emails); text != "" {
			d.Audit = append(d.Audit, views.AdminAuditRow{At: views.When(ctx, a.At), Text: text})
		}
	}
	s.render(w, r, status, views.AdminUsersPage(d))
}

// auditText puts an admin action in words; the account an action was about is named by its address while it exists.
func auditText(ctx context.Context, a identity.AuditEntry, emails map[uuid.UUID]string) string {
	who := func() string {
		id, _ := a.Params["user"].(string)
		if uid, err := uuid.Parse(id); err == nil {
			if e, ok := emails[uid]; ok {
				return e
			}
		}
		return views.T(ctx, "admin.audit.someone")
	}
	switch a.Code {
	case "identity.invitation_created":
		return views.T(ctx, "admin.audit.invited", "role", views.T(ctx, "admin.role."+fmt.Sprint(a.Params["role"])))
	case "admin.invitation_revoked":
		return views.T(ctx, "admin.audit.revoked")
	case "admin.role_changed":
		return views.T(ctx, "admin.audit.role", "user", who(), "role", views.T(ctx, "admin.role."+fmt.Sprint(a.Params["role"])))
	case "admin.signed_out":
		return views.T(ctx, "admin.audit.signed_out", "user", who())
	case "admin.user_deleted":
		return views.T(ctx, "admin.audit.deleted")
	}
	return ""
}

// adminInvite creates an invitation and shows its link once; no mail is sent.
func (s *Server) adminInvite(w http.ResponseWriter, r *http.Request, u *identity.User) {
	email, role := strings.TrimSpace(r.PostFormValue("email")), r.PostFormValue("role")
	by := u.ID
	link, expires, err := s.Identity.CreateInvitation(r.Context(), identity.InviteRequest{Email: email, Role: identity.Role(role), By: &by})
	d := views.AdminUsersData{Form: views.Form{Values: map[string]string{"email": email, "role": role}, Errors: map[string]string{}}}
	switch {
	case err == nil:
		d.Form.Values = nil
		d.Link, d.LinkExpires = link, views.When(r.Context(), expires)
		s.renderAdminUsers(w, r, u, http.StatusOK, d)
		return
	case errors.Is(err, identity.ErrInvalidEmail):
		d.Form.Errors["email"] = views.T(r.Context(), "profile.error.email")
	case errors.Is(err, identity.ErrEmailTaken):
		d.Form.Errors["email"] = views.T(r.Context(), "admin.invite.taken")
	case errors.Is(err, identity.ErrInvalidRole):
		d.Form.Errors["email"] = views.T(r.Context(), "profile.error.invalid")
	default:
		s.failed(w, r, u, err)
		return
	}
	d.Notice, d.Failed = views.T(r.Context(), "admin.invite.failed"), true
	s.renderAdminUsers(w, r, u, http.StatusUnprocessableEntity, d)
}

func pathID(r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	return id, err == nil
}

// adminDone answers an admin action: back to the list with a message, or the list with what went wrong.
func (s *Server) adminDone(w http.ResponseWriter, r *http.Request, u *identity.User, done string, err error) {
	var key string
	switch {
	case err == nil:
		http.Redirect(w, r, "/admin/users?done="+done, http.StatusSeeOther)
		return
	case errors.Is(err, identity.ErrNotFound):
		key = "admin.error.gone"
	case errors.Is(err, identity.ErrLastAdmin):
		key = "admin.error.last_admin"
	case errors.Is(err, identity.ErrSelf):
		key = "admin.error.self"
	case errors.Is(err, errDeleteAdmin):
		key = "admin.error.delete_admin"
	default:
		s.failed(w, r, u, err)
		return
	}
	s.renderAdminUsers(w, r, u, http.StatusConflict, views.AdminUsersData{Notice: views.T(r.Context(), key), Failed: true})
}

func (s *Server) adminRevoke(w http.ResponseWriter, r *http.Request, u *identity.User) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r, u)
		return
	}
	s.adminDone(w, r, u, "revoked", s.Identity.RevokeInvite(r.Context(), u.ID, id))
}

func (s *Server) adminRole(w http.ResponseWriter, r *http.Request, u *identity.User) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r, u)
		return
	}
	s.adminDone(w, r, u, "role", s.Identity.SetRole(r.Context(), u.ID, id, identity.Role(r.PostFormValue("role"))))
}

func (s *Server) adminSignOut(w http.ResponseWriter, r *http.Request, u *identity.User) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r, u)
		return
	}
	_, err := s.Identity.SignOutAccount(r.Context(), u.ID, id)
	s.adminDone(w, r, u, "signed_out", err)
}

// adminDelete removes an account with all its data, the same way the account owner can.
func (s *Server) adminDelete(w http.ResponseWriter, r *http.Request, u *identity.User) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r, u)
		return
	}
	if id == u.ID {
		s.adminDone(w, r, u, "", identity.ErrSelf)
		return
	}
	target, err := s.Identity.UserByID(r.Context(), id)
	if err != nil {
		s.adminDone(w, r, u, "", err)
		return
	}
	if target.Role == identity.RoleAdmin {
		s.adminDone(w, r, u, "", errDeleteAdmin)
		return
	}
	if s.Privacy == nil {
		s.failed(w, r, u, errors.New("privacy service unavailable"))
		return
	}
	if err := s.Privacy.DeleteAccount(r.Context(), id); err != nil {
		s.failed(w, r, u, err)
		return
	}
	s.adminDone(w, r, u, "deleted", s.Identity.RecordDeletion(r.Context(), u.ID, id))
}

func (s *Server) adminSystem(w http.ResponseWriter, r *http.Request, u *identity.User) {
	ctx := r.Context()
	bi := buildinfo.Get()
	yes := func(b bool) (string, ui.Tone) {
		if b {
			return views.T(ctx, "admin.system.on"), ui.Success
		}
		return views.T(ctx, "admin.system.off"), ui.Warning
	}
	rows := []views.AdminSystemRow{
		{Key: "admin.system.version", Value: bi.Version},
		{Key: "admin.system.commit", Value: bi.Commit},
		{Key: "admin.system.built", Value: bi.Date},
		{Key: "admin.system.go", Value: bi.GoVersion},
	}
	if s.System != nil {
		f, err := s.System.Facts(ctx)
		if err != nil {
			s.failed(w, r, u, err)
			return
		}
		proxy, proxyTone := yes(f.EgressProxy)
		mail, mailTone := yes(f.Mail)
		rows = append(rows,
			views.AdminSystemRow{Key: "admin.system.postgres", Value: f.Postgres},
			views.AdminSystemRow{Key: "admin.system.migration", Value: strconv.FormatInt(f.Migration, 10)},
			views.AdminSystemRow{Key: "admin.system.url", Value: f.PublicURL},
			views.AdminSystemRow{Key: "admin.system.egress", Value: proxy, Tone: proxyTone},
			views.AdminSystemRow{Key: "admin.system.mail", Value: mail, Tone: mailTone})
	}
	passkeys, tone := yes(s.Identity.PasskeysEnabled())
	rows = append(rows, views.AdminSystemRow{Key: "admin.system.passkeys", Value: passkeys, Tone: tone})
	rows = slices.DeleteFunc(rows, func(r views.AdminSystemRow) bool { return r.Value == "" })
	s.render(w, r, http.StatusOK, views.AdminSystemPage(views.AdminSystemData{Name: u.DisplayName, Rows: rows}))
}
