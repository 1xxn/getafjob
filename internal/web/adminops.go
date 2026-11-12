package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/web/views"
)

// RunnerStat is one runner as its runs show it.
type RunnerStat struct {
	ID                                        string
	LastLease                                 time.Time
	Active, Runs24h, Submitted24h, Stopped24h int
}

// SourceStat is one board of the crawl.
type SourceStat struct {
	ID                 int64
	ATS, Slug, Company string
	Enabled            bool
	LastCrawled        *time.Time
	LastError          string
}

// QueueCount is how many jobs of a kind are in a state.
type QueueCount struct {
	Kind, State string
	Count       int
}

// QueueFailure is a job that failed and may run again.
type QueueFailure struct {
	ID          int64
	Kind, State string
	Attempt     int
	Error       string
	At          time.Time
}

// AdminOps reads and changes what the admin console shows about runners, sources and the job queue.
type AdminOps interface {
	Runners(ctx context.Context, now time.Time) ([]RunnerStat, error)
	Sources(ctx context.Context, q, state string, limit int) ([]SourceStat, int, error)
	SetSourceEnabled(ctx context.Context, id int64, enabled bool) (bool, error)
	Queue(ctx context.Context, now time.Time) ([]QueueCount, []QueueFailure, error)
	Retry(ctx context.Context, id int64) error
}

// runnerIdle is how long a runner may go without a run before it counts as quiet.
const runnerIdle = time.Hour

func (s *Server) adminRunners(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if s.Ops == nil {
		s.notFound(w, r, u)
		return
	}
	now := time.Now()
	list, err := s.Ops.Runners(r.Context(), now)
	if err != nil {
		s.failed(w, r, u, err)
		return
	}
	d := views.AdminRunnersData{Name: u.DisplayName}
	for _, x := range list {
		d.Runners = append(d.Runners, views.AdminRunnerRow{ID: x.ID, LastLease: views.When(r.Context(), x.LastLease),
			Active: x.Active, Runs: x.Runs24h, Submitted: x.Submitted24h, Stopped: x.Stopped24h,
			Quiet: now.Sub(x.LastLease) > runnerIdle && x.Active == 0})
	}
	s.render(w, r, http.StatusOK, views.AdminRunnersPage(d))
}

func (s *Server) adminSources(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if s.Ops == nil {
		s.notFound(w, r, u)
		return
	}
	q, state := r.URL.Query().Get("q"), r.URL.Query().Get("state")
	if state != "failing" && state != "disabled" {
		state = ""
	}
	list, total, err := s.Ops.Sources(r.Context(), q, state, 200)
	if err != nil {
		s.failed(w, r, u, err)
		return
	}
	d := views.AdminSourcesData{Name: u.DisplayName, Query: q, State: state, Total: total, Notice: adminNotice(r),
		Back: "/admin/sources?" + url.Values{"q": {q}, "state": {state}}.Encode()}
	for _, x := range list {
		row := views.AdminSourceRow{ID: strconv.FormatInt(x.ID, 10), ATS: x.ATS, Slug: x.Slug, Company: x.Company,
			Enabled: x.Enabled, Error: x.LastError}
		if x.LastCrawled != nil {
			row.LastCrawled = views.When(r.Context(), *x.LastCrawled)
		}
		d.Sources = append(d.Sources, row)
	}
	if s.Discovery != nil {
		if st, err := s.Discovery.CrawlStatus(r.Context()); err == nil {
			d.Crawl = toCrawlStatusData(r.Context(), st)
		}
	}
	s.render(w, r, http.StatusOK, views.AdminSourcesPage(d))
}

// adminSourceSwitch switches a board on or off and goes back to the list as it was.
func (s *Server) adminSourceSwitch(enabled bool) func(http.ResponseWriter, *http.Request, *identity.User) {
	return func(w http.ResponseWriter, r *http.Request, u *identity.User) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || s.Ops == nil {
			s.notFound(w, r, u)
			return
		}
		ok, err := s.Ops.SetSourceEnabled(r.Context(), id, enabled)
		if err != nil {
			s.failed(w, r, u, err)
			return
		}
		if !ok {
			s.notFound(w, r, u)
			return
		}
		code := "admin.source_disabled"
		if enabled {
			code = "admin.source_enabled"
		}
		if err := s.Identity.RecordAdmin(r.Context(), u.ID, code, map[string]any{"source": id}); err != nil {
			s.failed(w, r, u, err)
			return
		}
		http.Redirect(w, r, localBack(r.PostFormValue("back")), http.StatusSeeOther) //nolint:gosec // localBack keeps only paths on this site
	}
}

// adminCrawl starts a crawl of every enabled source now.
func (s *Server) adminCrawl(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if s.Discovery == nil {
		s.notFound(w, r, u)
		return
	}
	if _, err := s.Discovery.TriggerCrawl(r.Context()); err != nil {
		s.failed(w, r, u, err)
		return
	}
	if err := s.Identity.RecordAdmin(r.Context(), u.ID, "admin.crawl_started", nil); err != nil {
		s.failed(w, r, u, err)
		return
	}
	http.Redirect(w, r, "/admin/sources?done=crawl", http.StatusSeeOther)
}

func (s *Server) adminQueue(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if s.Ops == nil {
		s.notFound(w, r, u)
		return
	}
	counts, failures, err := s.Ops.Queue(r.Context(), time.Now())
	if err != nil {
		s.failed(w, r, u, err)
		return
	}
	d := views.AdminQueueData{Name: u.DisplayName, Notice: adminNotice(r)}
	byKind := map[string]*views.AdminQueueRow{}
	for _, c := range counts {
		row, ok := byKind[c.Kind]
		if !ok {
			d.Kinds = append(d.Kinds, views.AdminQueueRow{Kind: c.Kind, States: map[string]int{}})
			row = &d.Kinds[len(d.Kinds)-1]
			byKind[c.Kind] = row
		}
		row.States[c.State] = c.Count
	}
	for _, f := range failures {
		d.Failures = append(d.Failures, views.AdminQueueFailure{ID: strconv.FormatInt(f.ID, 10), Kind: f.Kind, State: f.State,
			Attempt: f.Attempt, Error: f.Error, At: views.When(r.Context(), f.At)})
	}
	s.render(w, r, http.StatusOK, views.AdminQueuePage(d))
}

// adminRetry puts a failed job back in the queue.
func (s *Server) adminRetry(w http.ResponseWriter, r *http.Request, u *identity.User) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || s.Ops == nil {
		s.notFound(w, r, u)
		return
	}
	if err := s.Ops.Retry(r.Context(), id); err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		http.Redirect(w, r, "/admin/queue?done=retry_failed", http.StatusSeeOther)
		return
	}
	if err := s.Identity.RecordAdmin(r.Context(), u.ID, "admin.job_retried", map[string]any{"job": id}); err != nil {
		s.failed(w, r, u, err)
		return
	}
	http.Redirect(w, r, "/admin/queue?done=retried", http.StatusSeeOther)
}
