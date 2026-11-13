package web

import (
	"context"
	"net/http"
	"sort"
	"strings"

	"github.com/1xxn/getafjob/internal/application"
	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/matching"
	"github.com/1xxn/getafjob/internal/web/views"
)

// maxParked is how many parked applications the action list shows; the rest are in the tracker.
const maxParked = 5

// home renders the dashboard; it is the start page of signed-in users.
func (s *Server) home(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	d, err := s.dashboard(r.Context(), u)
	if err != nil {
		s.failed(w, r, u, err)
		return
	}
	s.render(w, r, http.StatusOK, views.Dashboard(d))
}

// dashboardBody renders the dashboard content for its refresh on live events.
func (s *Server) dashboardBody(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	d, err := s.dashboard(r.Context(), u)
	if err != nil {
		s.failed(w, r, u, err)
		return
	}
	s.render(w, r, http.StatusOK, views.DashboardBody(d))
}

// dashboard shapes the start page: what needs the user, what runs, what is new, how the search goes.
func (s *Server) dashboard(ctx context.Context, u *identity.User) (views.DashboardData, error) {
	d := views.DashboardData{Name: u.DisplayName}
	if s.Identity.PasskeysEnabled() {
		keys, err := s.Identity.Passkeys(ctx, u.ID)
		if err != nil {
			return d, err
		}
		d.PasskeyNudge = len(keys) == 0
	}
	snap, err := s.Profile.Get(ctx, u.ID)
	if err != nil {
		return d, err
	}
	d.ProfileMissing = len(snap.Profile.Missing())

	if s.Apps != nil {
		ov, err := s.Apps.Overview(ctx, u.ID)
		if err != nil {
			return d, err
		}
		fillApplications(&d, ov)
		d.Running = s.liveRuns(ctx, u.ID)
	}
	if s.Jobs != nil {
		list, err := s.Jobs.List(ctx, u.ID, matching.Filter{Decisions: []matching.Decision{matching.Match, matching.Maybe},
			FreshnessDays: 7, Sort: "newest", Page: 1})
		if err != nil {
			return d, err
		}
		d.NewMatches = list.Total
		for i, it := range list.Items {
			if i == 3 {
				break
			}
			d.TopMatches = append(d.TopMatches, views.DashJob{ID: it.JobID, Title: it.Title, Company: it.Company, Score: it.Score})
		}
	}
	if s.Discovery != nil {
		if st, err := s.Discovery.CrawlStatus(ctx); err == nil {
			d.Crawl = toCrawlStatusData(ctx, st)
		}
	}
	if d.ProfileMissing > 0 {
		d.Actions = append(d.Actions, views.DashAction{Kind: "profile", Href: "/setup", Count: d.ProfileMissing})
	}
	d.NeedsYou = d.Answers + d.Approvals
	return d, nil
}

// fillApplications counts the pipeline, lists what needs the user and builds the 30 day trend.
func fillApplications(d *views.DashboardData, ov application.Overview) {
	var parked []views.DashAction
	for _, a := range ov.Apps {
		item := views.DashAction{Title: a.Title, Company: a.Company, Href: "/applications/" + a.ID.String(),
			At: a.UpdatedAt}
		switch {
		case a.ApprovalAskedAt != nil && a.ApprovedAt == nil &&
			(a.State == application.NeedsInput || a.State == application.Parked):
			d.Approvals++
			item.Kind, item.Href = "approve", "/approvals"
			d.Actions = append(d.Actions, item)
		case a.State == application.NeedsInput:
			d.Answers++
			item.Kind = "answer"
			d.Actions = append(d.Actions, item)
		case a.State == application.Parked:
			item.Kind, item.ReasonKey = "parked", parkReasonKey(ov.ParkReasons[a.ID])
			parked = append(parked, item)
		case a.State == application.Queued:
			d.Queued++
		}
		switch a.State {
		case application.Submitted, application.Confirmed:
			d.Pipeline.Applied++
		case application.Interview:
			d.Pipeline.Interview++
		case application.Offer:
			d.Pipeline.Offer++
		case application.Rejected, application.Withdrawn:
			d.Pipeline.Closed++
		default: // not in the tracker yet, or stopped before sending
		}
	}
	sort.SliceStable(d.Actions, func(i, j int) bool { return d.Actions[i].At.After(d.Actions[j].At) })
	sort.SliceStable(parked, func(i, j int) bool { return parked[i].At.After(parked[j].At) })
	d.Parked = len(parked)
	d.Actions = append(d.Actions, parked[:min(len(parked), maxParked)]...)
	d.HasApplications = len(ov.Apps) > 0

	for i := range 30 {
		day := ov.Since.AddDate(0, 0, i)
		n := ov.SubmitsByDay[day]
		d.Trend = append(d.Trend, views.DashDay{Day: day, Count: n})
		d.Sent30 += n
		d.TrendMax = max(d.TrendMax, n)
	}
}

// parkReasonKey turns the runner's park reason into a catalog key; raw reasons are internal and never shown.
func parkReasonKey(reason string) string {
	r := strings.ToLower(reason)
	switch {
	case strings.Contains(r, "parked by user"):
		return "dash.park.user"
	case strings.Contains(r, "waiting for your answers"):
		return "dash.park.answers"
	case strings.Contains(r, "handoff window closed"):
		return "dash.park.timeout"
	case strings.Contains(r, "captcha"), strings.Contains(r, "verification"), strings.Contains(r, "sign-in"):
		return "dash.park.check"
	case strings.Contains(r, "navigation failed"), strings.Contains(r, "open the posting"):
		return "dash.park.unreachable"
	case strings.Contains(r, "submit not registered"), strings.Contains(r, "no click"), strings.Contains(r, "no effect"):
		return "dash.park.no_confirm"
	case strings.Contains(r, "form rejected"):
		return "dash.park.form_rejected"
	case r == strings.ToLower(application.ErrExcluded.Error()):
		return "dash.park.excluded"
	case r == strings.ToLower(application.ErrDuplicate.Error()):
		return "dash.park.duplicate"
	case r == strings.ToLower(application.ErrCompanyWindow.Error()):
		return "dash.park.company"
	default:
		return "dash.park.other"
	}
}
