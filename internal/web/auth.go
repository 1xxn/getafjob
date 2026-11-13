package web

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/platform/i18n"
	"github.com/1xxn/getafjob/internal/web/views"
)

// SessionCookie is the session cookie; the __Host- prefix pins it to this origin, path / and HTTPS.
const SessionCookie = "__Host-gaj_session"

func itoa(n int) string { return strconv.Itoa(n) }

// currentUser resolves the session cookie and sets the new cookie when the session was rotated. It also returns
// the token in use, so handlers can tell the current session from the others.
func (s *Server) currentUser(w http.ResponseWriter, r *http.Request) (*identity.User, string) {
	c, err := r.Cookie(SessionCookie)
	if err != nil {
		return nil, ""
	}
	u, next, err := s.Identity.Authenticate(r.Context(), c.Value)
	if err != nil {
		if !errors.Is(err, identity.ErrSessionExpired) && !errors.Is(err, identity.ErrUserDisabled) && !isCanceled(err) {
			s.Logger.WarnContext(r.Context(), "session lookup failed", "err", err)
		}
		return nil, ""
	}
	if next != "" {
		setSession(w, next)
		return &u, string(next)
	}
	return &u, c.Value
}

func setSession(w http.ResponseWriter, t identity.Token) {
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookie, Value: string(t), Path: "/", MaxAge: int(identity.SessionMaxAge / time.Second),
		Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
}

func clearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: "", Path: "/", MaxAge: -1, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

func (s *Server) loginForm(w http.ResponseWriter, r *http.Request, u *identity.User) {
	reauth := r.URL.Query().Get("reauth") == "1"
	if u != nil && !reauth {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	notice := ""
	switch {
	case reauth:
		notice = views.T(r.Context(), "login.reauth")
	case r.URL.Query().Get("expired") == "1":
		notice = identity.Msg(identity.ErrSessionExpired).Render(views.Locale(r.Context()))
	case r.URL.Query().Get("deleted") == "1":
		notice = views.T(r.Context(), "account.deleted.notice")
	}
	s.render(w, r, http.StatusOK, views.Login("", notice, s.Identity.PasskeysEnabled()))
}

func (s *Server) loginSubmit(w http.ResponseWriter, r *http.Request, _ *identity.User) {
	if err := s.Identity.RequestMagicLink(r.Context(), r.PostFormValue("email")); err != nil {
		s.failed(w, r, nil, err)
		return
	}
	// Same answer for every address (no account enumeration).
	s.render(w, r, http.StatusOK, views.LoginSent())
}

func (s *Server) invalidLink(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, identity.ErrUserDisabled) {
		status = http.StatusForbidden
	}
	s.problem(w, r, nil, status, "link.title", identity.Msg(err).Render(views.Locale(r.Context())))
}

func (s *Server) linkConfirm(w http.ResponseWriter, r *http.Request, _ *identity.User) {
	token := r.PathValue("token")
	if _, ok := identity.ParseToken(token); !ok {
		s.invalidLink(w, r, identity.ErrInvalidToken)
		return
	}
	s.render(w, r, http.StatusOK, views.ConfirmLink("/login/link/"+token))
}

func (s *Server) linkSubmit(w http.ResponseWriter, r *http.Request, _ *identity.User) {
	_, session, err := s.Identity.SignInWithMagicLink(r.Context(), r.PathValue("token"))
	if errors.Is(err, identity.ErrInvalidToken) || errors.Is(err, identity.ErrUserDisabled) || errors.Is(err, identity.ErrRecoveryWait) {
		s.invalidLink(w, r, err)
		return
	}
	if err != nil {
		s.failed(w, r, nil, err)
		return
	}
	setSession(w, session)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) inviteForm(w http.ResponseWriter, r *http.Request, _ *identity.User) {
	token := r.PathValue("token")
	inv, err := s.Identity.PendingInvitation(r.Context(), token)
	if err != nil {
		s.problem(w, r, nil, http.StatusBadRequest, "invite.title", views.T(r.Context(), "msg.identity.invitation_invalid"))
		return
	}
	s.render(w, r, http.StatusOK, views.Invite("/invite/"+token, inv.Email, "", ""))
}

func (s *Server) inviteSubmit(w http.ResponseWriter, r *http.Request, _ *identity.User) {
	token := r.PathValue("token")
	name := identity.CleanDisplayName(r.PostFormValue("name"))
	locale := i18n.Parse(r.PostFormValue("locale"))
	ctx := views.WithLocale(r.Context(), locale)
	r = r.WithContext(ctx)
	if name == "" {
		inv, err := s.Identity.PendingInvitation(ctx, token)
		if err != nil {
			s.problem(w, r, nil, http.StatusBadRequest, "invite.title", views.T(ctx, "msg.identity.invitation_invalid"))
			return
		}
		s.render(w, r, http.StatusUnprocessableEntity, views.Invite("/invite/"+token, inv.Email, "", views.T(ctx, "invite.name_required")))
		return
	}
	_, session, err := s.Identity.AcceptInvitation(ctx, token, name, locale)
	if errors.Is(err, identity.ErrInvalidToken) {
		s.problem(w, r, nil, http.StatusBadRequest, "invite.title", views.T(ctx, "msg.identity.invitation_invalid"))
		return
	}
	if err != nil {
		s.failed(w, r, nil, err)
		return
	}
	setSession(w, session)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u != nil {
		if err := s.Identity.SignOut(r.Context(), sessionToken(r), u.ID); err != nil {
			s.Logger.WarnContext(r.Context(), "sign out failed", "err", err)
		}
	}
	clearSession(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

type tokenKey struct{}

// sessionToken is the session token of the request (after a rotation the new one).
func sessionToken(r *http.Request) string {
	t, _ := r.Context().Value(tokenKey{}).(string)
	return t
}

func (s *Server) account(w http.ResponseWriter, r *http.Request, u *identity.User) {
	notice := ""
	if r.URL.Query().Get("added") == "1" {
		notice = views.T(r.Context(), "passkey.added")
	}
	s.renderAccount(w, r, u, notice)
}

func (s *Server) renderAccount(w http.ResponseWriter, r *http.Request, u *identity.User, notice string) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	list, err := s.Identity.Sessions(r.Context(), u.ID, sessionToken(r))
	if err != nil {
		s.failed(w, r, u, err)
		return
	}
	rows := make([]views.SessionRow, len(list))
	for i, si := range list {
		rows[i] = views.SessionRow{Method: string(si.Method), Created: views.When(r.Context(), si.CreatedAt),
			LastSeen: views.When(r.Context(), si.LastSeen), Current: si.Current}
	}
	d := views.AccountData{Name: u.DisplayName, Notice: notice, Sessions: rows, Enabled: s.Identity.PasskeysEnabled()}
	if d.Enabled {
		keys, err := s.Identity.Passkeys(r.Context(), u.ID)
		if err != nil {
			s.failed(w, r, u, err)
			return
		}
		for _, k := range keys {
			p := views.PasskeyRow{ID: base64.RawURLEncoding.EncodeToString(k.ID), Name: k.Name, Created: views.When(r.Context(), k.CreatedAt)}
			if !k.LastUsed.IsZero() {
				p.LastUsed = views.When(r.Context(), k.LastUsed)
			}
			d.Passkeys = append(d.Passkeys, p)
		}
	}
	rec, err := s.Identity.Recovery(r.Context(), u.ID)
	if err != nil {
		s.failed(w, r, u, err)
		return
	}
	if rec != nil {
		d.Recovery = views.T(r.Context(), "account.recovery.pending", "requested", views.When(r.Context(), rec.RequestedAt),
			"ready", views.When(r.Context(), rec.ReadyAt), "until", views.When(r.Context(), rec.ExpiresAt))
	}
	s.render(w, r, http.StatusOK, views.Account(d))
}

func (s *Server) signOutOthers(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	n, err := s.Identity.SignOutOthers(r.Context(), u.ID, sessionToken(r))
	if errors.Is(err, identity.ErrReauthRequired) {
		http.Redirect(w, r, "/login?reauth=1", http.StatusSeeOther)
		return
	}
	if err != nil {
		s.failed(w, r, u, err)
		return
	}
	s.renderAccount(w, r, u, views.T(r.Context(), "account.sessions.signed_out_others", "count", n))
}

// cancelRecovery stops a pending sign-in without passkey. Any signed-in session may do it: it only removes access.
func (s *Server) cancelRecovery(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if _, err := s.Identity.CancelRecovery(r.Context(), u.ID); err != nil {
		s.failed(w, r, u, err)
		return
	}
	s.renderAccount(w, r, u, views.T(r.Context(), "account.recovery.canceled"))
}

func (s *Server) privacyPage(w http.ResponseWriter, r *http.Request, u *identity.User) {
	name := ""
	passkeys := false
	if u != nil {
		name = u.DisplayName
		passkeys = s.Identity.PasskeysEnabled()
	}
	s.render(w, r, http.StatusOK, views.Privacy(views.PrivacyData{UserName: name, Passkeys: passkeys}))
}

func (s *Server) accountExport(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if s.Privacy == nil {
		http.Error(w, "export not configured", http.StatusInternalServerError)
		return
	}
	filename := fmt.Sprintf("getafjob-export-%s-%s.zip", u.ID.String()[:8], time.Now().UTC().Format("20060102150405"))
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	if err := s.Privacy.Export(r.Context(), u.ID, w); err != nil {
		s.Logger.ErrorContext(r.Context(), "data export failed", "err", err, "user", u.ID)
	}
}

func (s *Server) accountDelete(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if s.Privacy == nil {
		http.Error(w, "deletion not configured", http.StatusInternalServerError)
		return
	}
	switch err := s.Identity.CheckDeletion(r.Context(), u.ID, sessionToken(r)); {
	case errors.Is(err, identity.ErrReauthRequired):
		http.Redirect(w, r, "/login?reauth=1", http.StatusSeeOther)
		return
	case errors.Is(err, identity.ErrLastAdmin):
		s.renderAccount(w, r, u, views.T(r.Context(), "admin.error.last_admin"))
		return
	case err != nil:
		s.failed(w, r, u, err)
		return
	}
	if err := s.Privacy.DeleteAccount(r.Context(), u.ID); err != nil {
		s.Logger.ErrorContext(r.Context(), "account deletion failed", "err", err, "user", u.ID)
		s.failed(w, r, u, err)
		return
	}
	clearSession(w)
	http.Redirect(w, r, "/login?deleted=1", http.StatusSeeOther)
}
