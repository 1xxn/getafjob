package web

import (
	"context"
	"net/http"

	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/platform/maintenance"
	"github.com/1xxn/getafjob/internal/web/views"
)

// MaintenanceService reads and sets the maintenance mode (#518); turning it on also stops the running runs.
type MaintenanceService interface {
	State(ctx context.Context) (maintenance.State, error)
	Set(ctx context.Context, on bool, message string) error
}

// maintenanceRetry is the Retry-After of the maintenance page, in seconds.
const maintenanceRetry = "300"

// maintenanceOpen are the routes everyone reaches during maintenance: signing in and out, so admins get in, and
// the privacy notice.
var maintenanceOpen = map[string]bool{
	"GET /login": true, "POST /login": true, "GET /login/link/{token}": true, "POST /login/link/{token}": true,
	"POST /login/passkey/options": true, "POST /login/passkey": true, "POST /logout": true, "GET /privacy": true,
}

// maintenanceState returns the maintenance state; without a service, or when it cannot be read and was never
// read before, the instance counts as not in maintenance.
func (s *Server) maintenanceState(ctx context.Context) maintenance.State {
	if s.Maintenance == nil {
		return maintenance.State{}
	}
	st, err := s.Maintenance.State(ctx)
	if err != nil && !isCanceled(err) {
		s.Logger.WarnContext(ctx, "maintenance state", "err", err)
	}
	return st
}

// blockedForMaintenance answers with the maintenance page while the instance is in maintenance, except for admins
// and the open routes, and reports whether it answered.
func (s *Server) blockedForMaintenance(w http.ResponseWriter, r *http.Request, u *identity.User, st maintenance.State) bool {
	if !st.On || maintenanceOpen[r.Pattern] || (u != nil && u.Role == identity.RoleAdmin) {
		return false
	}
	w.Header().Set("Retry-After", maintenanceRetry)
	w.Header().Set("X-Robots-Tag", "noindex")
	s.render(w, r, http.StatusServiceUnavailable, views.Maintenance(st.Message, u == nil))
	return true
}

// adminMaintenance turns the maintenance mode on (with an optional note for visitors) or off.
func (s *Server) adminMaintenance(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if s.Maintenance == nil {
		s.notFound(w, r, u)
		return
	}
	on := r.PostFormValue("on") == "1"
	message := maintenance.CleanMessage(r.PostFormValue("message"))
	if err := s.Maintenance.Set(r.Context(), on, message); err != nil {
		s.failed(w, r, u, err)
		return
	}
	code := "admin.maintenance_off"
	if on {
		code = "admin.maintenance_on"
	}
	if err := s.Identity.RecordAdmin(r.Context(), u.ID, code, nil); err != nil {
		s.failed(w, r, u, err)
		return
	}
	http.Redirect(w, r, localBack(r.PostFormValue("back")), http.StatusSeeOther) //nolint:gosec // localBack keeps only paths on this site
}
