package web

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/web/views"
	"github.com/1xxn/getafjob/internal/web/views/ui"
)

func (s *Server) trackerSaveNotes(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	appID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		s.notFound(w, r, u)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	notes := strings.TrimSpace(r.PostFormValue("notes"))
	salary := strings.TrimSpace(r.PostFormValue("salary"))
	var interviewAt *time.Time
	if val := strings.TrimSpace(r.PostFormValue("interview_at")); val != "" {
		if t, err := time.Parse("2006-01-02T15:04", val); err == nil {
			interviewAt = &t
		}
	}
	var followUpAt *time.Time
	if val := strings.TrimSpace(r.PostFormValue("follow_up_at")); val != "" {
		if t, err := time.Parse("2006-01-02", val); err == nil {
			followUpAt = &t
		}
	}

	if err := s.Apps.SaveNotes(r.Context(), u.ID, appID, notes, salary, interviewAt, followUpAt); err != nil {
		s.failed(w, r, u, err)
		return
	}

	if partial(r) {
		if apps, err := s.Apps.Pipeline(r.Context(), u.ID); err == nil {
			for _, a := range apps {
				if a.ID == appID {
					s.render(w, r, http.StatusOK, views.PipelineCardDetail(s.plCard(r.Context(), u.ID, a), views.T(r.Context(), "flash.apps.notes")))
					return
				}
			}
		}
	}
	if back := r.PostFormValue("back"); strings.HasPrefix(back, "/applications/") || back == "/pipeline" {
		s.flash(w, "flash.apps.notes")
		http.Redirect(w, r, localBack(back), http.StatusSeeOther) //nolint:gosec // localBack keeps only paths on this site
		return
	}
	http.Redirect(w, r, "/pipeline", http.StatusSeeOther)
}

func (s *Server) jobBookmark(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		s.notFound(w, r, u)
		return
	}
	if s.Apps == nil {
		s.failed(w, r, u, errors.New("application service unavailable"))
		return
	}
	if _, err := s.Apps.Bookmark(r.Context(), u.ID, id); err != nil {
		s.failed(w, r, u, err)
		return
	}
	if partial(r) {
		s.render(w, r, http.StatusOK, ui.Alert(ui.AlertProps{Tone: ui.Success, Text: views.T(r.Context(), "jobs.saved_to_tracker")}))
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/jobs/%d", id), http.StatusSeeOther)
}

func (s *Server) jobApplyExternal(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		s.notFound(w, r, u)
		return
	}
	if s.Apps == nil {
		s.failed(w, r, u, errors.New("application service unavailable"))
		return
	}
	if _, err := s.Apps.ApplyExternal(r.Context(), u.ID, id); err != nil {
		s.failed(w, r, u, err)
		return
	}
	http.Redirect(w, r, "/pipeline", http.StatusSeeOther)
}

func (s *Server) trackerAddExternal(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if s.Discovery == nil || s.Apps == nil {
		s.failed(w, r, u, errors.New("service unavailable"))
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	rawURL := strings.TrimSpace(r.PostFormValue("url"))
	jobID, err := s.Discovery.ImportJob(r.Context(), u.ID, rawURL)
	if err != nil {
		s.Logger.WarnContext(r.Context(), "tracker external import failed", "err", err)
		s.flash(w, "flash.pl.import_error")
		http.Redirect(w, r, "/pipeline", http.StatusSeeOther)
		return
	}
	if _, err := s.Apps.ApplyExternal(r.Context(), u.ID, jobID); err != nil {
		s.failed(w, r, u, err)
		return
	}
	s.flash(w, "flash.pl.external")
	http.Redirect(w, r, "/pipeline", http.StatusSeeOther)
}
