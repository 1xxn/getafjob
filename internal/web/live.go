package web

import (
	"context"
	"errors"
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

// gridRuns leaves out the queued runs: they wait in the queue panel, not in the grid.
func gridRuns(runs []views.ActiveRunItem, queue []views.QueueRow) []views.ActiveRunItem {
	if len(queue) == 0 {
		return runs
	}
	queued := make(map[string]bool, len(queue))
	for _, q := range queue {
		queued[q.AppID] = true
	}
	out := runs[:0:0]
	for _, r := range runs {
		if !queued[r.AppID] {
			out = append(out, r)
		}
	}
	return out
}

// livePage is the live center: all active runs, or the one named by ?run= in focus.
func (s *Server) livePage(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	queue := s.queueRows(r.Context(), u.ID)
	d := views.LiveData{Name: u.DisplayName, Runs: gridRuns(s.liveRuns(r.Context(), u.ID), queue), Queue: queue}
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
	s.render(w, r, http.StatusOK, views.LiveGrid(gridRuns(s.liveRuns(r.Context(), u.ID), s.queueRows(r.Context(), u.ID))))
}

// liveDock renders the run list of the dock.
func (s *Server) liveDock(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	s.render(w, r, http.StatusOK, views.LiveDockRuns(s.liveRuns(r.Context(), u.ID)))
}

// queueRows lists the user's queued runs for the queue panel.
func (s *Server) queueRows(ctx context.Context, userID uuid.UUID) []views.QueueRow {
	if s.Apps == nil {
		return nil
	}
	items, err := s.Apps.QueueItems(ctx, userID)
	if err != nil {
		s.Logger.WarnContext(ctx, "queue", "err", err)
		return nil
	}
	out := make([]views.QueueRow, 0, len(items))
	for _, it := range items {
		out = append(out, views.QueueRow{AppID: it.ID.String(), JobID: it.JobID, Title: it.Title, Company: it.Company,
			Paused: it.Paused})
	}
	return out
}

// liveQueue renders the queue panel for its refresh on live events.
func (s *Server) liveQueue(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	s.render(w, r, http.StatusOK, views.LiveQueue(s.queueRows(r.Context(), u.ID)))
}

// liveQueueAction moves, pauses, resumes or cancels (parks) a queued run, then shows the queue again.
func (s *Server) liveQueueAction(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil || s.Apps == nil {
		s.notFound(w, r, u)
		return
	}
	ctx := r.Context()
	switch r.PathValue("action") {
	case "up", "down":
		_, err = s.Apps.MoveQueued(ctx, u.ID, id, r.PathValue("action") == "up")
	case "pause", "resume":
		_, err = s.Apps.PauseQueued(ctx, u.ID, id, r.PathValue("action") == "pause")
	case "cancel":
		err = s.parkApplication(r, u, id)
		if errors.Is(err, application.ErrNotFound) || errors.Is(err, application.ErrTransition) {
			err = nil // started or gone meanwhile: the panel shows the current queue
		}
	default:
		s.notFound(w, r, u)
		return
	}
	if err != nil {
		s.failed(w, r, u, err)
		return
	}
	if partial(r) {
		s.render(w, r, http.StatusOK, views.LiveQueue(s.queueRows(ctx, u.ID)))
		return
	}
	http.Redirect(w, r, "/live", http.StatusSeeOther)
}

// liveDockLayout saves where the user's dock sits and how large it is; the next page renders it there.
func (s *Server) liveDockLayout(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	err := s.Identity.SetDockLayout(r.Context(), u.ID, r.PostFormValue("side")+":"+r.PostFormValue("size"))
	switch {
	case errors.Is(err, identity.ErrInvalidLayout):
		w.WriteHeader(http.StatusBadRequest)
	case err != nil:
		s.Logger.WarnContext(r.Context(), "dock layout", "err", err)
		w.WriteHeader(http.StatusInternalServerError)
	default:
		// 200, not 204: the proxy's compression turns an empty 204 into an HTTP/2 protocol error in Chrome.
		w.WriteHeader(http.StatusOK)
	}
}
