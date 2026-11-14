package web

import (
	"cmp"
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/1xxn/getafjob/internal/application"
	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/matching"
	"github.com/1xxn/getafjob/internal/platform/applymode"
	"github.com/1xxn/getafjob/internal/web/views"
	"github.com/1xxn/getafjob/internal/web/views/ui"
)

// bulkActions are the actions of the job list's bulk bar.
var bulkActions = map[string]string{"queue": "queue", "save": "save", "skip": "skip", "block": "block"}

// maxBulk bounds one bulk action (one page of the list).
const maxBulk = matching.PageSize

// listQuery is the job list's state in the URL: filters, sort, page and the job open beside the list.
type listQuery struct {
	Query, Decision, Workplace, Location, Source, Freshness, Sort string
	MinSalary, Page                                               int
	Job                                                           int64
}

var freshnessDays = map[string]int{"24h": 1, "3d": 3, "7d": 7, "14d": 14, "30d": 30}

func parseListQuery(v url.Values) listQuery {
	q := listQuery{Query: v.Get("q"), Decision: v.Get("decision"), Workplace: v.Get("workplace"),
		Location: v.Get("location"), Source: v.Get("source"), Freshness: v.Get("freshness"), Sort: v.Get("sort")}
	if !slices.Contains([]string{"relevant", "match", "maybe", "skip", "all", "blocked"}, q.Decision) {
		q.Decision = "relevant"
	}
	if _, ok := freshnessDays[q.Freshness]; !ok {
		q.Freshness = "all"
	}
	if !slices.Contains([]string{"match", "newest", "salary", "company"}, q.Sort) {
		q.Sort = "match"
	}
	if n, err := strconv.Atoi(v.Get("page")); err == nil && n >= 1 && n <= 1000 {
		q.Page = n
	}
	if n, err := strconv.Atoi(v.Get("min_salary")); err == nil && n >= 0 && n <= 10_000_000 {
		q.MinSalary = n
	}
	if n, err := strconv.ParseInt(v.Get("job"), 10, 64); err == nil && n > 0 {
		q.Job = n
	}
	return q
}

// filter turns the URL state into the matching filter (cleaned there).
func (q listQuery) filter() matching.Filter {
	f := matching.Filter{Query: q.Query, Workplace: q.Workplace, Location: q.Location, Source: q.Source,
		MinSalary: q.MinSalary, FreshnessDays: freshnessDays[q.Freshness], Sort: q.Sort, Page: max(q.Page, 1)}
	switch q.Decision {
	case "relevant":
		f.Decisions = []matching.Decision{matching.Match, matching.Maybe}
	case "match", "maybe", "skip":
		f.Decisions = []matching.Decision{matching.Decision(q.Decision)}
	case "blocked":
		f.Blocked = true
	}
	return f.Clean()
}

// url writes the state back; defaults are left out so URLs stay short and shareable.
func (q listQuery) url() string {
	v := url.Values{}
	set := func(k, val, def string) {
		if val != "" && val != def {
			v.Set(k, val)
		}
	}
	set("q", q.Query, "")
	set("decision", q.Decision, "relevant")
	set("workplace", q.Workplace, "")
	set("location", q.Location, "")
	set("source", q.Source, "")
	set("freshness", q.Freshness, "all")
	set("sort", q.Sort, "match")
	if q.MinSalary > 0 {
		v.Set("min_salary", strconv.Itoa(q.MinSalary))
	}
	if q.Page > 1 {
		v.Set("page", strconv.Itoa(q.Page))
	}
	if q.Job > 0 {
		v.Set("job", strconv.FormatInt(q.Job, 10))
	}
	if len(v) == 0 {
		return "/jobs"
	}
	return "/jobs?" + v.Encode()
}

// saveQuery is the filter part of the URL for a saved search: no page, no open job.
func (q listQuery) saveQuery() string {
	q.Page, q.Job = 0, 0
	_, query, _ := strings.Cut(q.url(), "?")
	return query
}

// SearchFilter turns a saved search's URL query into the list filter (the alerts count new jobs with it).
func SearchFilter(query string) matching.Filter {
	v, err := url.ParseQuery(query)
	if err != nil {
		v = url.Values{}
	}
	return parseListQuery(v).filter()
}

func (q listQuery) with(change func(*listQuery)) string {
	change(&q)
	return q.url()
}

// jobsPage is the job list with filters, bulk actions and the job open beside it (?job=).
func (s *Server) jobsPage(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	lq := parseListQuery(r.URL.Query())
	f := lq.filter()
	l, err := s.Jobs.List(r.Context(), u.ID, f)
	if err != nil {
		s.failed(w, r, u, err)
		return
	}
	d := views.JobsData{
		Name: u.DisplayName, Query: lq.Query, Decision: lq.Decision, Workplace: f.Workplace, Location: f.Location,
		Source: f.Source, MinSalary: f.MinSalary, Freshness: lq.Freshness, Sort: f.Sort,
		Total: l.Total, Page: f.Page, Pages: (l.Total + matching.PageSize - 1) / matching.PageSize,
		Counts: map[string]int{}, BlockedCount: l.BlockedCount, Back: lq.url(), SaveQuery: lq.saveQuery(),
	}
	for k, n := range l.Counts {
		d.Counts[string(k)] = n
	}
	d.Pending = len(l.Counts) == 0 && l.BlockedCount == 0

	ids := make([]int64, 0, len(l.Items))
	for _, it := range l.Items {
		ids = append(ids, it.JobID)
	}
	states := map[int64]application.State{}
	if s.Apps != nil {
		if states, err = s.Apps.StatesByJob(r.Context(), u.ID, ids); err != nil {
			s.failed(w, r, u, err)
			return
		}
	}
	now := time.Now()
	for _, it := range l.Items {
		row := jobRow(r.Context(), it, now)
		row.State = stateKey(states[it.JobID], row.Age)
		row.PanelURL = lq.with(func(q *listQuery) { q.Job = it.JobID })
		row.PanelGet = "/jobs/" + strconv.FormatInt(it.JobID, 10) + "/panel" + query(lq.with(func(q *listQuery) { q.Job = it.JobID }))
		row.Current = it.JobID == lq.Job
		d.Rows = append(d.Rows, row)
	}
	d.Chips = chips(r.Context(), lq)
	d.PageLinks, d.PrevURL, d.NextURL = pageLinks(lq, d.Pages)

	if blocklist, err := s.Jobs.Blocklist(r.Context(), u.ID); err == nil {
		for _, b := range blocklist {
			d.Blocklist = append(d.Blocklist, views.BlocklistItemData{ID: b.ID, Kind: b.Kind, Pattern: b.Pattern,
				CreatedAt: views.When(r.Context(), b.CreatedAt)})
		}
	}
	if s.Discovery != nil {
		if st, err := s.Discovery.CrawlStatus(r.Context()); err == nil {
			d.Crawl = toCrawlStatusData(r.Context(), st)
		}
	}
	if lq.Job > 0 {
		if p, err := s.jobPanelData(r.Context(), u, lq.Job, lq); err == nil {
			d.Panel = &p
		}
	}
	if partial(r) {
		s.render(w, r, http.StatusOK, views.JobResultsSwap(d))
		return
	}
	s.render(w, r, http.StatusOK, views.JobsPage(d))
}

// jobPanel renders the job beside the list (desktop) or as a sheet over it (phone).
func (s *Server) jobPanel(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		s.notFound(w, r, u)
		return
	}
	lq := parseListQuery(r.URL.Query())
	p, err := s.jobPanelData(r.Context(), u, id, lq)
	if errors.Is(err, matching.ErrNotFound) {
		s.notFound(w, r, u)
		return
	}
	if err != nil {
		s.failed(w, r, u, err)
		return
	}
	s.render(w, r, http.StatusOK, views.JobPanel(p))
}

func (s *Server) jobPanelData(ctx context.Context, u *identity.User, id int64, lq listQuery) (views.JobPanelData, error) {
	j, err := s.Jobs.Detail(ctx, u.ID, id)
	if err != nil {
		return views.JobPanelData{}, err
	}
	p := views.JobPanelData{
		Row:       jobRow(ctx, j.Item, time.Now()),
		ApplyMode: string(applymode.Of(j.URL, j.ApplyURL)),
		ApplyHost: applymode.Host(cmp.Or(j.ApplyURL, j.URL)),
		URL:       webLink(j.URL),
		CloseURL:  lq.with(func(q *listQuery) { q.Job = 0 }),
		Back:      lq.with(func(q *listQuery) { q.Job = id }),
	}
	for _, rs := range j.Reasons {
		p.Reasons = append(p.Reasons, views.Reason{Text: reasonText(ctx, rs), Points: rs.Points, Hard: rs.Hard})
		// The same rule as the queue guard (HardSkipped): a skip with a hard reason is never applied to.
		p.Excluded = p.Excluded || (rs.Hard && j.Decision == matching.Skip)
	}
	// The panel shows five reasons: the hard ones first (they decide on their own and carry no points, so sorting by
	// points alone would cut them), then the weightiest.
	slices.SortStableFunc(p.Reasons, func(a, b views.Reason) int {
		if a.Hard != b.Hard {
			if a.Hard {
				return -1
			}
			return 1
		}
		return cmp.Compare(abs(b.Points), abs(a.Points))
	})
	p.Reasons = p.Reasons[:min(len(p.Reasons), 5)]
	p.Excerpt = excerpt(j.Description, 420)
	if s.Apps != nil {
		if app, err := s.Apps.ByJob(ctx, u.ID, id); err == nil {
			p.Row.State = stateKey(app.State, p.Row.Age)
			p.ApplicationID = app.ID.String()
			p.Applying = app.State != application.Matched && app.State != application.Skipped
		}
	}
	return p, nil
}

// query returns the query part of a list URL, with its "?".
func query(u string) string {
	if i := strings.IndexByte(u, '?'); i >= 0 {
		return u[i:]
	}
	return ""
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// excerpt cuts a description at a word boundary.
func excerpt(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) <= n {
		return s
	}
	r := []rune(s)[:n]
	if i := strings.LastIndexByte(string(r), ' '); i > n/2 {
		return string(r)[:i] + " …"
	}
	return string(r) + " …"
}

// stateKey names a job's application state for the list; fresh jobs without one are "new".
func stateKey(st application.State, age int) string {
	switch st {
	case "":
		if age >= 0 && age <= 2 {
			return "new"
		}
		return ""
	case application.Matched:
		return "saved"
	case application.Skipped:
		return "skipped"
	case application.Queued, application.Running, application.NeedsInput, application.Submitting:
		return "applying"
	case application.Submitted, application.Confirmed, application.Interview, application.Offer:
		return "applied"
	case application.Parked, application.Failed, application.SubmitUnknown:
		return "stopped"
	case application.Rejected, application.Withdrawn:
		return "closed"
	default:
		return ""
	}
}

// chips lists the active filters, each with the URL that removes it.
func chips(ctx context.Context, q listQuery) []views.FilterChip {
	var out []views.FilterChip
	add := func(label string, drop func(*listQuery)) {
		out = append(out, views.FilterChip{Label: label, RemoveURL: q.with(func(c *listQuery) { drop(c); c.Page = 0; c.Job = 0 })})
	}
	t := func(key string, params ...any) string { return views.T(ctx, key, params...) }
	if q.Query != "" {
		add(t("jobs.chip.query", "q", q.Query), func(c *listQuery) { c.Query = "" })
	}
	if q.Decision != "relevant" {
		add(t("jobs.chip.decision."+q.Decision), func(c *listQuery) { c.Decision = "relevant" })
	}
	if q.Location != "" {
		add(t("jobs.chip.location", "where", q.Location), func(c *listQuery) { c.Location = "" })
	}
	if q.Workplace != "" {
		add(t("jobs.workplace."+q.Workplace), func(c *listQuery) { c.Workplace = "" })
	}
	if q.Source != "" {
		add(q.Source, func(c *listQuery) { c.Source = "" })
	}
	if q.Freshness != "all" {
		add(t("jobs.freshness."+q.Freshness), func(c *listQuery) { c.Freshness = "all" })
	}
	if q.MinSalary > 0 {
		add(t("jobs.chip.salary", "val", views.Amount(ctx, strconv.Itoa(q.MinSalary))), func(c *listQuery) { c.MinSalary = 0 })
	}
	return out
}

// pageLinks builds a pager with the first, the last and two pages around the current one.
func pageLinks(q listQuery, pages int) (links []ui.PageLink, prev, next string) {
	if pages <= 1 {
		return nil, "", ""
	}
	cur := max(q.Page, 1)
	at := func(p int) string { return q.with(func(c *listQuery) { c.Page = p; c.Job = 0 }) }
	last := 0
	for p := 1; p <= pages; p++ {
		if p != 1 && p != pages && (p < cur-2 || p > cur+2) {
			continue
		}
		if last > 0 && p > last+1 {
			links = append(links, ui.PageLink{Label: "…"})
		}
		links = append(links, ui.PageLink{Label: strconv.Itoa(p), Href: at(p), Current: p == cur})
		last = p
	}
	if cur > 1 {
		prev = at(cur - 1)
	}
	if cur < pages {
		next = at(cur + 1)
	}
	return links, prev, next
}

// jobsBulk applies one action to the selected jobs: queue, save, skip or block their employers.
func (s *Server) jobsBulk(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	back := localBack(cmp.Or(r.PostFormValue("back"), "/jobs"))
	if !strings.HasPrefix(back, "/jobs") {
		back = "/jobs"
	}
	action, known := bulkActions[r.PostFormValue("action")] // the constant, never the request's text
	var ids []int64
	for _, v := range r.PostForm["ids"] {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil && id > 0 && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 || len(ids) > maxBulk || !known ||
		s.Apps == nil {
		http.Redirect(w, r, back, http.StatusSeeOther) //nolint:gosec // localBack keeps only paths on this site
		return
	}
	done, why := 0, ""
	for _, id := range ids {
		j, err := s.Jobs.Detail(r.Context(), u.ID, id)
		if err != nil {
			continue // not the user's job or gone
		}
		switch action {
		case "queue":
			if applymode.Of(j.URL, j.ApplyURL) == applymode.Email {
				why = "flash.jobs.not_queued.mail"
				continue
			}
			if gerr := s.Apps.CheckGuard(r.Context(), u.ID, id); gerr != nil {
				why = guardFlash(gerr)
				continue
			}
			_, _, err = s.Apps.Queue(r.Context(), u.ID, id)
		case "save":
			_, err = s.Apps.Bookmark(r.Context(), u.ID, id)
		case "skip":
			err = s.Apps.SkipJob(r.Context(), u.ID, id)
		case "block":
			err = s.Jobs.AddBlocklist(r.Context(), u.ID, "company", j.Company)
		}
		if err != nil {
			s.Logger.WarnContext(r.Context(), "bulk action", "action", action, "job_id", id, "err", err)
			continue
		}
		done++
	}
	key := "flash.jobs." + action
	switch {
	case len(ids) == 1 && done == 0 && why != "":
		key = why // one job: say why it was not queued
	case done < len(ids):
		key += "_partial"
	}
	s.flash(w, key)
	http.Redirect(w, r, back, http.StatusSeeOther) //nolint:gosec // localBack keeps only paths on this site
}

// guardFlash names the guard that kept a job from the queue.
func guardFlash(err error) string {
	switch {
	case errors.Is(err, application.ErrDuplicate):
		return "flash.jobs.not_queued.duplicate"
	case errors.Is(err, application.ErrCompanyWindow):
		return "flash.jobs.not_queued.company"
	case errors.Is(err, application.ErrExcluded):
		return "flash.jobs.not_queued.excluded"
	case errors.Is(err, application.ErrAlreadySubmitted):
		return "flash.apps.retry_attempted"
	default:
		return "flash.jobs.not_queued.other"
	}
}
