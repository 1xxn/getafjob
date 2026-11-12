package web

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/notify"
	"github.com/1xxn/getafjob/internal/web/views"
)

const (
	bellItems     = 8  // entries in the bell menu
	activityItems = 50 // entries per feed page
)

func activityRows(items []notify.ActivityItem) []views.ActivityRow {
	rows := make([]views.ActivityRow, 0, len(items))
	for _, a := range items {
		rows = append(rows, views.ActivityRow{Kind: string(a.Kind), State: a.State, Link: a.Link(), Title: a.Title,
			Company: a.Company, At: a.At, Unread: a.Unread})
	}
	return rows
}

// bell renders the notification menu of the top bar.
func (s *Server) bell(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var d views.BellData
	if s.Notify != nil {
		a, err := s.Notify.Activity(r.Context(), u.ID, time.Time{}, bellItems)
		if err != nil {
			s.failed(w, r, u, err)
			return
		}
		d = views.BellData{Unread: a.Unread, Items: activityRows(a.Items)}
	}
	w.Header().Set("Cache-Control", "no-store")
	s.render(w, r, http.StatusOK, views.Bell(d))
}

// markRead marks the feed read; the bell posts with htmx and gets itself back, the page form returns to the page.
func (s *Server) markRead(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if s.Notify != nil {
		if err := s.Notify.MarkRead(r.Context(), u.ID); err != nil {
			s.failed(w, r, u, err)
			return
		}
	}
	if partial(r) {
		s.bell(w, r, u)
		return
	}
	http.Redirect(w, r, "/activity", http.StatusSeeOther)
}

// activityPage lists the feed grouped by day, newest first, a page at a time (?before=).
func (s *Server) activityPage(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	d := views.ActivityData{Name: u.DisplayName, Self: "/activity"}
	var before time.Time
	if v := r.URL.Query().Get("before"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			s.notFound(w, r, u)
			return
		}
		before, d.Self = t, "/activity?before="+url.QueryEscape(v)
	}
	if s.Notify != nil {
		a, err := s.Notify.Activity(r.Context(), u.ID, before, activityItems)
		if err != nil {
			s.failed(w, r, u, err)
			return
		}
		d.Unread, d.Groups = a.Unread, activityGroups(r.Context(), activityRows(a.Items), time.Now())
		if a.More && len(a.Items) > 0 {
			d.Older = "/activity?before=" + url.QueryEscape(a.Items[len(a.Items)-1].At.UTC().Format(time.RFC3339Nano))
		}
	}
	if partial(r) {
		s.render(w, r, http.StatusOK, views.ActivityFeed(d))
		return
	}
	s.render(w, r, http.StatusOK, views.ActivityPage(d))
}

// activityGroups splits the rows by calendar day (UTC): today, yesterday, then the date.
func activityGroups(ctx context.Context, rows []views.ActivityRow, now time.Time) []views.ActivityGroup {
	var groups []views.ActivityGroup
	day := func(t time.Time) time.Time {
		y, m, d := t.UTC().Date()
		return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	}
	today := day(now)
	for _, a := range rows {
		label := views.DayLabel(ctx, a.At)
		switch day(a.At) {
		case today:
			label = views.T(ctx, "activity.today")
		case today.AddDate(0, 0, -1):
			label = views.T(ctx, "activity.yesterday")
		}
		if len(groups) == 0 || groups[len(groups)-1].Label != label {
			groups = append(groups, views.ActivityGroup{Label: label})
		}
		groups[len(groups)-1].Items = append(groups[len(groups)-1].Items, a)
	}
	return groups
}
