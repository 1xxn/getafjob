package web

import (
	"cmp"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/application"
	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/web/views"
	"github.com/1xxn/getafjob/internal/web/views/ui"
)

// appsPageSize bounds one page of the application list and one bulk action.
const appsPageSize = 50

// appBulkActions are the actions of the application list's bulk bar.
var appBulkActions = map[string]string{"retry": "retry", "park": "park"}

// appGroupOrder is the order of the state filters; "attention" is what waits for the user.
var appGroupOrder = []string{"all", "attention", "active", "sent", "saved", "closed"}

// appGroup sorts a state into one filter group.
func appGroup(st application.State) string {
	switch st {
	case application.NeedsInput, application.Parked, application.Failed, application.SubmitUnknown:
		return "attention"
	case application.Queued, application.Running, application.Submitting:
		return "active"
	case application.Submitted, application.Confirmed, application.Interview, application.Offer:
		return "sent"
	case application.Discovered, application.Matched:
		return "saved"
	default: // rejected, withdrawn, skipped
		return "closed"
	}
}

// appTone colors a state: what waits for the user stands out, what is done is calm.
func appTone(st application.State) ui.Tone {
	switch appGroup(st) {
	case "attention":
		if st == application.Failed || st == application.SubmitUnknown {
			return ui.Danger
		}
		return ui.Warning
	case "active":
		return ui.Info
	case "sent":
		return ui.Success
	default:
		return ui.Neutral
	}
}

// appStepKey is the catalog key of the last step: the park reason for parked applications, else the state's hint.
func appStepKey(st application.State, parkReason string) string {
	if st == application.Parked {
		return parkReasonKey(parkReason)
	}
	return "apps.step." + string(st)
}

type appsQuery struct {
	Query, Group string
	Page         int
}

func parseAppsQuery(v url.Values) appsQuery {
	q := appsQuery{Query: strings.TrimSpace(v.Get("q")), Group: v.Get("group")}
	if len(q.Query) > 200 {
		q.Query = q.Query[:200]
	}
	if !slices.Contains(appGroupOrder, q.Group) {
		q.Group = "all"
	}
	if n, err := strconv.Atoi(v.Get("page")); err == nil && n > 1 && n <= 1000 {
		q.Page = n
	}
	return q
}

func (q appsQuery) url() string {
	v := url.Values{}
	if q.Query != "" {
		v.Set("q", q.Query)
	}
	if q.Group != "all" {
		v.Set("group", q.Group)
	}
	if q.Page > 1 {
		v.Set("page", strconv.Itoa(q.Page))
	}
	if len(v) == 0 {
		return "/applications"
	}
	return "/applications?" + v.Encode()
}

func (s *Server) applicationsPage(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	q := parseAppsQuery(r.URL.Query())
	d := views.AppsData{Name: u.DisplayName, Query: q.Query, Group: q.Group, Self: q.url()}
	if s.Apps != nil {
		apps, reasons, err := s.Apps.List(r.Context(), u.ID)
		if err != nil {
			s.failed(w, r, u, err)
			return
		}
		buildAppsData(&d, q, apps, reasons)
	}
	if partial(r) {
		s.render(w, r, http.StatusOK, views.AppsResults(d))
		return
	}
	s.render(w, r, http.StatusOK, views.ApplicationsPage(d))
}

// buildAppsData filters by search and group, counts the groups (after the search) and cuts one page.
func buildAppsData(d *views.AppsData, q appsQuery, apps []application.PipelineApplication, reasons map[uuid.UUID]string) {
	d.Any = len(apps) > 0
	needle := strings.ToLower(q.Query)
	counts := map[string]int{}
	var rows []views.AppRow
	for _, a := range apps {
		if needle != "" && !strings.Contains(strings.ToLower(a.Title+" "+a.Company), needle) {
			continue
		}
		g := appGroup(a.State)
		counts["all"]++
		counts[g]++
		if q.Group != "all" && q.Group != g {
			continue
		}
		rows = append(rows, views.AppRow{ID: a.ID.String(), JobID: a.JobID, Title: a.Title, Company: a.Company,
			State: string(a.State), StateKey: "apps.state." + string(a.State), Tone: appTone(a.State),
			Live: liveStates[a.State], StepKey: appStepKey(a.State, reasons[a.ID]), Updated: a.UpdatedAt})
	}
	// Newest change first; what waits for the user comes before the rest in the "all" view.
	slices.SortStableFunc(rows, func(x, y views.AppRow) int {
		if q.Group == "all" {
			ax, ay := appGroup(application.State(x.State)) == "attention", appGroup(application.State(y.State)) == "attention"
			if ax != ay {
				if ax {
					return -1
				}
				return 1
			}
		}
		return y.Updated.Compare(x.Updated)
	})
	for _, g := range appGroupOrder {
		gq := appsQuery{Query: q.Query, Group: g}
		d.Groups = append(d.Groups, views.AppGroup{Key: g, Href: gq.url(), Count: counts[g], Active: g == q.Group})
	}
	d.Total = len(rows)
	page := max(q.Page, 1)
	start := min((page-1)*appsPageSize, len(rows))
	end := min(start+appsPageSize, len(rows))
	d.Rows = rows[start:end]
	if page > 1 {
		pq := q
		pq.Page = page - 1
		d.PrevURL = pq.url()
	}
	if end < len(rows) {
		nq := q
		nq.Page = page + 1
		d.NextURL = nq.url()
	}
}

// applicationsBulk retries or parks the selected applications; the state machine refuses what does not fit.
func (s *Server) applicationsBulk(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	back := appsBack(r.PostFormValue("back"))
	action, known := appBulkActions[r.PostFormValue("action")] // the constant, never the request's text
	var ids []uuid.UUID
	for _, v := range r.PostForm["ids"] {
		if id, err := uuid.Parse(v); err == nil && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 || len(ids) > appsPageSize || !known || s.Apps == nil {
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	done := 0
	for _, id := range ids {
		var err error
		if action == "retry" {
			_, err = s.Apps.Retry(r.Context(), u.ID, id)
		} else {
			err = s.parkApplication(r, u, id)
		}
		if err != nil {
			s.Logger.DebugContext(r.Context(), "bulk action refused", "action", action, "app", id, "err", err)
			continue
		}
		done++
	}
	key := "flash.apps." + action
	switch {
	case done == 0:
		key += "_none"
	case done < len(ids):
		key += "_partial"
	}
	s.flash(w, key)
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// appsBack keeps a return target on the application pages.
func appsBack(back string) string {
	back = localBack(cmp.Or(back, "/applications"))
	if !strings.HasPrefix(back, "/applications") {
		return "/applications"
	}
	return back
}

// applicationRetry queues a parked or failed application again.
func (s *Server) applicationRetry(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	appID, err := uuid.Parse(r.PathValue("id"))
	if err != nil || s.Apps == nil {
		s.notFound(w, r, u)
		return
	}
	key := "flash.apps.retry"
	if _, err := s.Apps.Retry(r.Context(), u.ID, appID); err != nil {
		key = "flash.apps.retry_none"
	}
	s.flash(w, key)
	http.Redirect(w, r, "/applications/"+appID.String(), http.StatusSeeOther) //nolint:gosec // G710: appID is a parsed UUID
}

// applicationReconcile records the user's answer for a run that crashed while sending: it was sent or it was not.
// Nothing is retried automatically; a "not sent" application is parked and the user decides about a retry.
func (s *Server) applicationReconcile(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	appID, err := uuid.Parse(r.PathValue("id"))
	if err != nil || s.Apps == nil {
		s.notFound(w, r, u)
		return
	}
	ev, key := application.EvReconciled, "flash.apps.sent"
	if r.PostFormValue("decision") == "not_sent" {
		ev, key = application.EvNotSent, "flash.apps.not_sent"
	}
	if _, err := s.Apps.Fire(r.Context(), u.ID, appID, ev, application.ActorUser, map[string]any{"reason": "decided by user"}); err != nil {
		key = "flash.apps.unchanged"
	}
	s.flash(w, key)
	http.Redirect(w, r, "/applications/"+appID.String(), http.StatusSeeOther) //nolint:gosec // G710: appID is a parsed UUID
}

// appActions are the next steps for a state; every button says what it does.
func appActions(d views.AppDetailData, st application.State) []views.AppAction {
	base := "/applications/" + d.AppID
	var out []views.AppAction
	switch {
	case d.ApprovalAsked:
		out = append(out, views.AppAction{Key: "apps.action.review", Icon: "check-circle", Href: "/approvals", Variant: "primary"})
	case len(d.Questions) > 0:
		out = append(out, views.AppAction{Key: "apps.action.answer", Icon: "message", Href: base + "#questions", Variant: "primary"})
	}
	switch st {
	case application.NeedsInput:
		out = append(out, views.AppAction{Key: "apps.action.watch", Icon: "live", Href: base + "?tab=live"},
			views.AppAction{Key: "apps.action.park", Icon: "pause", Post: base + "/park"})
	case application.Queued, application.Running, application.Submitting:
		out = append(out, views.AppAction{Key: "apps.action.watch", Icon: "live", Href: base + "?tab=live", Variant: "primary"})
		if st != application.Submitting {
			out = append(out, views.AppAction{Key: "apps.action.park", Icon: "pause", Post: base + "/park"})
		}
	case application.Parked, application.Failed:
		v := "primary"
		if len(out) > 0 {
			v = ""
		}
		if d.ApplyMode == "form" {
			out = append(out, views.AppAction{Key: "apps.action.retry", Icon: "refresh", Post: base + "/retry", Variant: v})
		}
		if d.ExternalURL != "" {
			out = append(out, views.AppAction{Key: "apps.action.open_external", Icon: "external", Href: d.ExternalURL, New: true})
		}
	case application.SubmitUnknown:
		out = append(out,
			views.AppAction{Key: "apps.action.sent", Icon: "check", Post: base + "/reconcile", Value: "sent", Variant: "primary"},
			views.AppAction{Key: "apps.action.not_sent", Icon: "x", Post: base + "/reconcile", Value: "not_sent"})
		if d.ConfirmationURL != "" || d.BeforeSubmitURL != "" {
			out = append(out, views.AppAction{Key: "apps.action.evidence", Icon: "eye", Href: base + "?tab=evidence", Variant: "ghost"})
		}
	case application.Matched, application.Discovered:
		if d.JobID > 0 {
			out = append(out, views.AppAction{Key: "apps.action.apply", Icon: "send", Href: "/jobs/" + strconv.FormatInt(d.JobID, 10) + "/apply", Variant: "primary"})
		}
	case application.Submitted, application.Confirmed, application.Interview, application.Offer:
		out = append(out, views.AppAction{Key: "apps.action.notes", Icon: "edit", Href: base + "?tab=notes"})
	default: // closed: nothing to do
	}
	return out
}
