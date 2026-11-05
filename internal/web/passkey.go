package web

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/web/views"
)

// maxPasskeyBody bounds the JSON bodies of the passkey endpoints.
const maxPasskeyBody = 64 << 10

type passkeyOptions struct {
	Challenge uuid.UUID `json:"challenge"`
	Options   any       `json:"options"`
}

type passkeyAnswer struct {
	Challenge  uuid.UUID       `json:"challenge"`
	Name       string          `json:"name,omitempty"`
	Credential json.RawMessage `json:"credential"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// jsonError answers with a translated message for the script to show.
func (s *Server) jsonError(w http.ResponseWriter, r *http.Request, status int, key string) {
	writeJSON(w, status, map[string]string{"error": views.T(r.Context(), key)})
}

func readAnswer(w http.ResponseWriter, r *http.Request) (passkeyAnswer, bool) {
	var a passkeyAnswer
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPasskeyBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil || a.Challenge == uuid.Nil || len(a.Credential) == 0 {
		return passkeyAnswer{}, false
	}
	return a, true
}

// passkeyFailed maps service errors to answers; it reports whether it answered.
func (s *Server) passkeyFailed(w http.ResponseWriter, r *http.Request, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, identity.ErrReauthRequired), errors.Is(err, identity.ErrSessionExpired):
		s.jsonError(w, r, http.StatusForbidden, "msg.identity.reauth_required")
	case errors.Is(err, identity.ErrPasskeyFailed):
		s.jsonError(w, r, http.StatusBadRequest, "passkey.failed")
	case errors.Is(err, identity.ErrTooManyPasskeys):
		s.jsonError(w, r, http.StatusConflict, "passkey.too_many")
	case errors.Is(err, identity.ErrUserDisabled):
		s.jsonError(w, r, http.StatusForbidden, "msg.identity.user_disabled")
	case errors.Is(err, identity.ErrPasskeysOff):
		s.jsonError(w, r, http.StatusNotFound, "passkey.failed")
	default:
		s.Logger.ErrorContext(r.Context(), "request failed", "route", r.Pattern, "err", err)
		s.jsonError(w, r, http.StatusInternalServerError, "error.generic.text")
	}
	return true
}

func (s *Server) passkeyLoginOptions(w http.ResponseWriter, r *http.Request, _ *identity.User) {
	opts, id, err := s.Identity.BeginPasskeyLogin(r.Context())
	if s.passkeyFailed(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, passkeyOptions{Challenge: id, Options: opts})
}

func (s *Server) passkeyLogin(w http.ResponseWriter, r *http.Request, _ *identity.User) {
	a, ok := readAnswer(w, r)
	if !ok {
		s.jsonError(w, r, http.StatusBadRequest, "passkey.failed")
		return
	}
	_, session, err := s.Identity.FinishPasskeyLogin(r.Context(), a.Challenge, a.Credential)
	if s.passkeyFailed(w, r, err) {
		return
	}
	setSession(w, session)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) passkeyAddOptions(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		s.jsonError(w, r, http.StatusForbidden, "msg.identity.session_expired")
		return
	}
	opts, id, err := s.Identity.BeginPasskeyRegistration(r.Context(), u.ID, sessionToken(r))
	if s.passkeyFailed(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, passkeyOptions{Challenge: id, Options: opts})
}

func (s *Server) passkeyAdd(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		s.jsonError(w, r, http.StatusForbidden, "msg.identity.session_expired")
		return
	}
	a, ok := readAnswer(w, r)
	if !ok {
		s.jsonError(w, r, http.StatusBadRequest, "passkey.failed")
		return
	}
	err := s.Identity.FinishPasskeyRegistration(r.Context(), u.ID, sessionToken(r), a.Challenge, a.Name, a.Credential)
	if s.passkeyFailed(w, r, err) {
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) passkeyDelete(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	id, err := base64.RawURLEncoding.DecodeString(r.PathValue("id"))
	if err != nil || len(id) == 0 || len(id) > 1023 {
		s.notFound(w, r, u)
		return
	}
	err = s.Identity.DeletePasskey(r.Context(), u.ID, sessionToken(r), id)
	switch {
	case errors.Is(err, identity.ErrReauthRequired):
		http.Redirect(w, r, "/login?reauth=1", http.StatusSeeOther)
	case errors.Is(err, identity.ErrNotFound):
		s.notFound(w, r, u)
	case err != nil:
		s.failed(w, r, u, err)
	default:
		s.renderAccount(w, r, u, views.T(r.Context(), "passkey.removed"))
	}
}
