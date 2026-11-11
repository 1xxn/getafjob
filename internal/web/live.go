package web

import (
	"context"
	"net/http"
	"sort"

	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/application"
	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/web/views"
)

// liveRuns lists the user's active runs, the ones waiting for the user first.
func (s *Server) liveRuns(ctx context.Context, userID uuid.UUID) []views.ActiveRunItem {
	runs := s.activeRuns(ctx, userID, uuid.Nil)
	sort.SliceStable(runs, func(i, j int) bool { return runs[i].Waiting && !runs[j].Waiting })
	return runs
}

// livePage is the live center: all active runs, or the one named by ?run= in focus.
func (s *Server) livePage(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	d := views.LiveData{Name: u.DisplayName, Runs: s.liveRuns(r.Context(), u.ID)}
	if id := r.URL.Query().Get("run"); id != "" {
		for i := range d.Runs {
			if d.Runs[i].AppID == id {
				d.Focus = &d.Runs[i]
			}
		}
		if d.Focus == nil {
			appID, err := uuid.Parse(id)
			if err != nil || s.Apps == nil {
				http.Redirect(w, r, "/live", http.StatusSeeOther)
				return
			}
			app, err := s.Apps.Get(r.Context(), u.ID, appID)
			if err != nil {
				http.Redirect(w, r, "/live", http.StatusSeeOther)
				return
			}
			if !liveStates[app.State] {
				// The run ended: its result is on the application page.
				http.Redirect(w, r, "/applications/"+appID.String(), http.StatusSeeOther) //nolint:gosec // a parsed UUID, always a local path
				return
			}
			// Live but between two browser sessions for a moment: keep the focus, the picture connects when it can.
			item := views.ActiveRunItem{AppID: appID.String(), JobID: app.JobID, Waiting: app.State == application.NeedsInput}
			if s.Jobs != nil {
				if j, err := s.Jobs.Detail(r.Context(), u.ID, app.JobID); err == nil {
					item.JobTitle, item.Company = j.Title, j.Company
				}
			}
			d.Focus = &item
		}
	}
	s.render(w, r, http.StatusOK, views.LivePage(d))
}

// liveGrid renders the run grid of the live center for its refresh on live events.
func (s *Server) liveGrid(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	s.render(w, r, http.StatusOK, views.LiveGrid(s.liveRuns(r.Context(), u.ID)))
}

// liveDock renders the run list of the dock.
func (s *Server) liveDock(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	s.render(w, r, http.StatusOK, views.LiveDockRuns(s.liveRuns(r.Context(), u.ID)))
}
