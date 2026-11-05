package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/matching"
	"github.com/1xxn/getafjob/internal/web/views"
)

// decisionFilters maps the list's "decision" parameter to decisions; relevant is the default.
var decisionFilters = map[string][]matching.Decision{
	"relevant": {matching.Match, matching.Maybe},
	"match":    {matching.Match},
	"maybe":    {matching.Maybe},
	"skip":     {matching.Skip},
	"all":      nil,
}

func (s *Server) jobsPage(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	qv := r.URL.Query()
	dec := qv.Get("decision")
	if _, ok := decisionFilters[dec]; !ok {
		dec = "relevant"
	}
	page, _ := strconv.Atoi(qv.Get("page"))
	f := matching.Filter{Query: qv.Get("q"), Decisions: decisionFilters[dec], Workplace: qv.Get("workplace"),
		Page: page}.Clean()
	l, err := s.Jobs.List(r.Context(), u.ID, f)
	if err != nil {
		s.failed(w, r, u, err)
		return
	}
	now := time.Now()
	d := views.JobsData{Name: u.DisplayName, Query: f.Query, Decision: dec, Workplace: f.Workplace, Total: l.Total,
		Page: f.Page, Pages: (l.Total + matching.PageSize - 1) / matching.PageSize, Counts: map[string]int{}}
	for k, n := range l.Counts {
		d.Counts[string(k)] = n
	}
	d.Pending = len(l.Counts) == 0
	for _, it := range l.Items {
		d.Rows = append(d.Rows, jobRow(it, now))
	}
	link := func(p int) string {
		v := url.Values{}
		for k, val := range map[string]string{"q": f.Query, "workplace": f.Workplace} {
			if val != "" {
				v.Set(k, val)
			}
		}
		if dec != "relevant" {
			v.Set("decision", dec)
		}
		if p > 1 {
			v.Set("page", strconv.Itoa(p))
		}
		if len(v) == 0 {
			return "/jobs"
		}
		return "/jobs?" + v.Encode()
	}
	if f.Page > 1 {
		d.PrevURL = link(min(f.Page-1, max(d.Pages, 1)))
	}
	if f.Page < d.Pages {
		d.NextURL = link(f.Page + 1)
	}
	if partial(r) {
		s.render(w, r, http.StatusOK, views.JobResults(d))
		return
	}
	s.render(w, r, http.StatusOK, views.JobsPage(d))
}

func (s *Server) jobPage(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		s.notFound(w, r, u)
		return
	}
	j, err := s.Jobs.Detail(r.Context(), u.ID, id)
	if errors.Is(err, matching.ErrNotFound) {
		s.notFound(w, r, u)
		return
	}
	if err != nil {
		s.failed(w, r, u, err)
		return
	}
	d := views.JobData{Name: u.DisplayName, Row: jobRow(j.Item, time.Now()), Description: j.Description,
		URL: webLink(j.URL), ApplyURL: webLink(j.ApplyURL), Open: j.Open, Computed: views.When(r.Context(), j.ComputedAt),
		Back: "/jobs"}
	for _, rs := range j.Reasons {
		d.Reasons = append(d.Reasons, views.Reason{Text: reasonText(r.Context(), rs), Points: rs.Points, Hard: rs.Hard})
	}
	s.render(w, r, http.StatusOK, views.JobPage(d))
}

func jobRow(it matching.Item, now time.Time) views.JobRow {
	at := it.Posted
	if at.IsZero() || it.FirstSeen.Before(at) {
		at = it.FirstSeen
	}
	age := -1
	if !at.IsZero() {
		age = max(int(now.Sub(at).Hours()/24), 0)
	}
	return views.JobRow{ID: it.JobID, Title: it.Title, Company: it.Company, Location: it.Location,
		Workplace: it.Workplace, Age: age, Score: it.Score, Decision: string(it.Decision)}
}

// reasonText translates a reason; levels and languages in its parameters are translated too.
func reasonText(ctx context.Context, rs matching.Reason) string {
	params := make([]any, 0, 2*len(rs.Params))
	for k, v := range rs.Params {
		switch k {
		case "job", "user":
			v = views.T(ctx, "match.level."+v)
		case "lang":
			v = views.T(ctx, "match.lang."+v)
		}
		params = append(params, k, v)
	}
	return views.T(ctx, "match.reason."+rs.Code, params...)
}

// webLink keeps http(s) links of postings; anything else is dropped.
func webLink(s string) string {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return ""
	}
	return u.String()
}

// partial reports an htmx request for a fragment; history restores and plain requests get the whole page.
func partial(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-Request-Type") != "full" &&
		r.Header.Get("HX-History-Restore-Request") != "true"
}
