package web

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/matching"
	"github.com/1xxn/getafjob/internal/web/views"
)

// searchesPage lists the user's saved searches: open, rename, alert on or off, delete.
func (s *Server) searchesPage(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	d := views.SearchesData{Name: u.DisplayName, Notice: r.URL.Query().Get("done")}
	if s.Searches != nil {
		list, err := s.Searches.List(r.Context(), u.ID)
		if err != nil {
			s.failed(w, r, u, err)
			return
		}
		for _, ss := range list {
			d.Rows = append(d.Rows, views.SearchRow{ID: strconv.FormatInt(ss.ID, 10), Name: ss.Name, Query: ss.Query,
				Alert: ss.Alert})
		}
	}
	s.render(w, r, http.StatusOK, views.SearchesPage(d))
}

// saveSearch stores the job list's filters under a name.
func (s *Server) saveSearch(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil || s.Searches == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	_, err := s.Searches.Save(r.Context(), u.ID, r.PostFormValue("name"), r.PostFormValue("query"), r.PostFormValue("alert") != "")
	if errors.Is(err, matching.ErrInvalidSearch) {
		http.Redirect(w, r, "/searches?done=invalid", http.StatusSeeOther)
		return
	}
	if err != nil {
		s.failed(w, r, u, err)
		return
	}
	http.Redirect(w, r, "/searches?done=saved", http.StatusSeeOther)
}

// searchAction renames, turns the alert on or off, or deletes a saved search.
func (s *Server) searchAction(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil || s.Searches == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		s.notFound(w, r, u)
		return
	}
	ctx, done := r.Context(), ""
	switch r.PathValue("action") {
	case "rename":
		_, err = s.Searches.Rename(ctx, u.ID, id, r.PostFormValue("name"))
		done = "renamed"
	case "alert":
		_, err = s.Searches.SetAlert(ctx, u.ID, id, r.PostFormValue("alert") == "on")
		done = "alert"
	case "delete":
		_, err = s.Searches.Delete(ctx, u.ID, id)
		done = "deleted"
	default:
		s.notFound(w, r, u)
		return
	}
	if errors.Is(err, matching.ErrInvalidSearch) {
		done, err = "invalid", nil
	}
	if err != nil {
		s.failed(w, r, u, err)
		return
	}
	http.Redirect(w, r, "/searches?done="+done, http.StatusSeeOther)
}
