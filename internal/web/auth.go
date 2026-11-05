package web

import (
	"errors"
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

func (s *Server) currentUser(r *http.Request) *identity.User {
	c, err := r.Cookie(SessionCookie)
	if err != nil {
		return nil
	}
	u, err := s.Identity.Authenticate(r.Context(), c.Value)
	if err != nil {
		if !errors.Is(err, identity.ErrSessionExpired) && !errors.Is(err, identity.ErrUserDisabled) && !isCanceled(err) {
			s.Logger.WarnContext(r.Context(), "session lookup failed", "err", err)
		}
		return nil
	}
	return &u
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
	if u != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	notice := ""
	if r.URL.Query().Get("expired") == "1" {
		notice = identity.Msg(identity.ErrSessionExpired).Render(views.Locale(r.Context()))
	}
	s.render(w, r, http.StatusOK, views.Login("", notice))
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
	if errors.Is(err, identity.ErrInvalidToken) || errors.Is(err, identity.ErrUserDisabled) {
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
		s.render(w, r, http.StatusUnprocessableEntity, views.Invite("/invite/"+token, inv.Email, "", views.T(ctx, "invite.name_label")))
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
	if c, err := r.Cookie(SessionCookie); err == nil && u != nil {
		if err := s.Identity.SignOut(r.Context(), c.Value, u.ID); err != nil {
			s.Logger.WarnContext(r.Context(), "sign out failed", "err", err)
		}
	}
	clearSession(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
