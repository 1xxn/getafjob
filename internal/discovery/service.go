package discovery

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

// Detail requests per crawl, in total and per source (one large sitemap must not starve the others); the rest
// follow in the next crawls.
const (
	MaxDetails          = 300
	MaxDetailsPerSource = 100
)

// Service runs crawls.
type Service struct {
	store       Store
	imports     ImportChecker // nil: jobs added by URL are not rechecked
	now         func() time.Time
	parallelism int
}

// NewService returns a crawl service.
func NewService(store Store) *Service {
	return &Service{store: store, now: time.Now, parallelism: 6}
}

// WithClock replaces the clock (tests).
func (s *Service) WithClock(now func() time.Time) *Service { s.now = now; return s }

// WithImportChecker lets each crawl recheck jobs added by URL with c.
func (s *Service) WithImportChecker(c ImportChecker) *Service { s.imports = c; return s }

// SourceError carries a short, stable reason per source (no response bodies, no URLs with secrets).
type SourceError struct {
	Source string
	Code   string // e.g. "http_404", "timeout", "decode"
}

func (e *SourceError) Error() string { return e.Source + ": " + e.Code }

// Report is the result of a crawl.
type Report struct {
	RunID  int64
	Stats  Stats
	Errors map[string]string
	// Validators per source that answered; save them only after the crawl succeeded, or the next crawl may skip
	// changes it never stored.
	Validators map[string]Validators
}

type fetched struct {
	src   Source
	batch Batch
	err   error
}

// item is a valid posting and the source that delivered it.
type item struct {
	src Source
	p   Posting
}

// crawlFailed is the error key of a crawl that stopped before its end; source names never contain a space.
const crawlFailed = "crawl failed"

// Crawl fetches all sources, updates the index and closes jobs that disappeared. The run is finished on every exit;
// a crawl that stops early records why under crawlFailed, so the pages do not keep showing the last good run.
func (s *Service) Crawl(ctx context.Context, sources []Source) (rep Report, err error) {
	names := make([]string, len(sources))
	for i, src := range sources {
		names[i] = src.Name()
	}
	start := s.now()
	runID, err := s.store.StartRun(ctx, names, start)
	if err != nil {
		return Report{}, err
	}
	rep = Report{RunID: runID, Stats: Stats{Changes: map[Change]int{}}, Errors: map[string]string{},
		Validators: map[string]Validators{}}
	defer func() {
		if err == nil {
			return
		}
		rep.Errors[crawlFailed] = errorCode(err)
		err = errors.Join(err, s.store.FinishRun(context.WithoutCancel(ctx), runID, rep.Stats, rep.Errors, s.now()))
	}()
	results := s.fetchAll(ctx, sources)

	runs := map[string]SourceRun{}
	listed := map[string]bool{}
	var items, stubs []item
	var unchanged []string
	for _, r := range results {
		name := r.src.Name()
		if r.err != nil {
			rep.Errors[name] = errorCode(r.err)
			runs[name] = SourceRun{Name: name}
			continue
		}
		_, detailer := r.src.(Detailer)
		if !detailer { // a detailer may leave jobs without details for later; a 304 would hide them
			rep.Validators[name] = r.batch.Validators
		}
		if r.batch.NotModified && !detailer {
			runs[name] = SourceRun{Name: name, OK: true} // not complete: closes nothing
			unchanged = append(unchanged, name)
			rep.Stats.NotModified++
			continue
		}
		runs[name] = SourceRun{Name: name, OK: true, Complete: r.batch.Complete}
		for _, p := range r.batch.Postings {
			rep.Stats.Fetched++
			valid := ValidPosting(p) || (detailer && ValidStub(p))
			if !valid {
				rep.Stats.Invalid++
				continue
			}
			listed[Fingerprint(p.Source, p.ExternalID)] = true
			if p.Stub {
				stubs = append(stubs, item{src: r.src, p: p})
			} else {
				items = append(items, item{src: r.src, p: p})
			}
		}
	}
	now := s.now()
	if err := s.unchanged(ctx, unchanged, listed, now, &rep.Stats); err != nil {
		return rep, err
	}
	newStubs, err := s.seen(ctx, stubs, now, &rep.Stats)
	if err != nil {
		return rep, err
	}
	items = append(items, newStubs...)
	// Company boards first: an aggregator copy arriving later is recognized as a duplicate.
	sort.SliceStable(items, func(i, j int) bool {
		return !IsAggregator(items[i].p.Source) && IsAggregator(items[j].p.Source)
	})
	if err := s.details(ctx, items, &rep.Stats); err != nil {
		return rep, err
	}

	for _, it := range items {
		if it.p.Stub {
			continue // no details this time (cap or failure); the next crawl tries again
		}
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		change, dup, err := s.store.Upsert(ctx, runID, it.p, now)
		if err != nil {
			return rep, err
		}
		if dup {
			rep.Stats.Duplicates++
			continue
		}
		rep.Stats.Changes[change]++
	}

	open, err := s.store.OpenJobs(ctx, names)
	if err != nil {
		return rep, err
	}
	var closing []int64
	for _, j := range open {
		if ShouldClose(j, runs, listed, now) {
			closing = append(closing, j.ID)
		}
	}
	if err := s.store.Close(ctx, runID, closing, now); err != nil {
		return rep, err
	}
	rep.Stats.Closed = len(closing)
	if err := s.recheckImports(ctx, runID, now, &rep.Stats); err != nil {
		return rep, err
	}
	return rep, s.store.FinishRun(ctx, runID, rep.Stats, rep.Errors, s.now())
}

// recheckImports looks at the pages of jobs added by URL that were not seen for ImportRecheck: a job still there is
// seen again, a gone one is closed, an unknown answer changes nothing.
func (s *Service) recheckImports(ctx context.Context, runID int64, now time.Time, st *Stats) error {
	if s.imports == nil {
		return nil
	}
	jobs, err := s.store.StaleImports(ctx, now.Add(-ImportRecheck), MaxImportChecks)
	if err != nil || len(jobs) == 0 {
		return err
	}
	states := make([]ImportState, len(jobs))
	s.parallel(len(jobs), func(i int) { states[i] = s.imports.CheckImport(ctx, jobs[i].URL) })
	if err := ctx.Err(); err != nil {
		return err
	}
	var seen []string
	var gone []int64
	for i, j := range jobs {
		switch states[i] {
		case ImportOpen:
			seen = append(seen, j.Fingerprint)
		case ImportGone:
			gone = append(gone, j.ID)
		case ImportUnknown:
		}
	}
	st.Rechecked += len(jobs)
	if _, err := s.store.Seen(ctx, seen, now); err != nil {
		return err
	}
	if err := s.store.Close(ctx, runID, gone, now); err != nil {
		return err
	}
	st.Closed += len(gone)
	return nil
}

// unchanged marks the open jobs of sources that answered 304 as seen and listed.
func (s *Service) unchanged(ctx context.Context, sources []string, listed map[string]bool, now time.Time, st *Stats) error {
	if len(sources) == 0 {
		return nil
	}
	open, err := s.store.OpenJobs(ctx, sources)
	if err != nil {
		return err
	}
	fps := make([]string, len(open))
	for i, j := range open {
		fps[i] = j.Fingerprint
		listed[j.Fingerprint] = true
	}
	known, err := s.store.Seen(ctx, fps, now)
	st.Seen += len(known)
	return err
}

// seen marks the known open jobs among stubs as seen and returns the stubs of jobs the index does not have yet.
func (s *Service) seen(ctx context.Context, stubs []item, now time.Time, st *Stats) ([]item, error) {
	if len(stubs) == 0 {
		return nil, nil
	}
	fps := make([]string, len(stubs))
	for i, it := range stubs {
		fps[i] = Fingerprint(it.p.Source, it.p.ExternalID)
	}
	known, err := s.store.Seen(ctx, fps, now)
	if err != nil {
		return nil, err
	}
	st.Seen += len(known)
	var out []item
	for i, it := range stubs {
		if !known[fps[i]] {
			out = append(out, it)
		}
	}
	return out, nil
}

func (s *Service) fetchAll(ctx context.Context, sources []Source) []fetched {
	out := make([]fetched, len(sources))
	s.parallel(len(sources), func(i int) {
		b, err := sources[i].Fetch(ctx)
		out[i] = fetched{src: sources[i], batch: b, err: err}
	})
	return out
}

// details loads missing descriptions, and whole postings for new stubs, at most MaxDetails per crawl. A failed
// request leaves the posting as it was (a stub is then skipped); the next crawl tries again.
func (s *Service) details(ctx context.Context, items []item, st *Stats) error {
	var need []int
	var fps []string
	for i, it := range items {
		if _, ok := it.src.(Detailer); ok && (it.p.Description == "" || it.p.Stub) {
			need = append(need, i)
			fps = append(fps, Fingerprint(it.p.Source, it.p.ExternalID))
		}
	}
	if len(need) == 0 {
		return nil
	}
	described, err := s.store.Described(ctx, fps)
	if err != nil {
		return err
	}
	var todo []int
	perSource := map[string]int{}
	for k, i := range need {
		name := items[i].src.Name()
		if !described[fps[k]] && len(todo) < MaxDetails && perSource[name] < MaxDetailsPerSource {
			perSource[name]++
			todo = append(todo, i)
		}
	}
	failed := make([]bool, len(todo))
	s.parallel(len(todo), func(k int) {
		it := &items[todo[k]]
		p := it.p
		err := it.src.(Detailer).Details(ctx, &p)
		p.Stub = false // filled now, or dropped below
		if err != nil || !ValidPosting(p) || p.Source != it.p.Source || p.ExternalID != it.p.ExternalID {
			failed[k] = true
			return
		}
		it.p = p
	})
	for _, f := range failed {
		if f {
			st.DetailsFailed++
		} else {
			st.Details++
		}
	}
	return ctx.Err()
}

func (s *Service) parallel(n int, fn func(int)) { parallel(n, s.parallelism, fn) }

// parallel runs fn for 0..n-1 with at most k at a time.
func parallel(n, k int, fn func(int)) {
	sem := make(chan struct{}, k)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			fn(i)
		})
	}
	wg.Wait()
}

func errorCode(err error) string {
	var se *SourceError
	switch {
	case errors.As(err, &se):
		return se.Code
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	default:
		return "error"
	}
}
