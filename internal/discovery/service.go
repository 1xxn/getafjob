package discovery

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

// MaxDetails bounds the detail requests per crawl; the rest follow in the next crawl.
const MaxDetails = 300

// Service runs crawls.
type Service struct {
	store       Store
	now         func() time.Time
	parallelism int
}

// NewService returns a crawl service.
func NewService(store Store) *Service {
	return &Service{store: store, now: time.Now, parallelism: 6}
}

// WithClock replaces the clock (tests).
func (s *Service) WithClock(now func() time.Time) *Service { s.now = now; return s }

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

// Crawl fetches all sources, updates the index and closes jobs that disappeared.
func (s *Service) Crawl(ctx context.Context, sources []Source) (Report, error) {
	names := make([]string, len(sources))
	for i, src := range sources {
		names[i] = src.Name()
	}
	start := s.now()
	runID, err := s.store.StartRun(ctx, names, start)
	if err != nil {
		return Report{}, err
	}
	results := s.fetchAll(ctx, sources)

	rep := Report{RunID: runID, Stats: Stats{Changes: map[Change]int{}}, Errors: map[string]string{}}
	runs := map[string]SourceRun{}
	listed := map[string]bool{}
	var items []item
	for _, r := range results {
		name := r.src.Name()
		if r.err != nil {
			rep.Errors[name] = errorCode(r.err)
			runs[name] = SourceRun{Name: name}
			continue
		}
		runs[name] = SourceRun{Name: name, OK: true, Complete: r.batch.Complete}
		for _, p := range r.batch.Postings {
			rep.Stats.Fetched++
			if !ValidPosting(p) {
				rep.Stats.Invalid++
				continue
			}
			listed[Fingerprint(p.Source, p.ExternalID)] = true
			items = append(items, item{src: r.src, p: p})
		}
	}
	// Company boards first: an aggregator copy arriving later is recognized as a duplicate.
	sort.SliceStable(items, func(i, j int) bool {
		return !IsAggregator(items[i].p.Source) && IsAggregator(items[j].p.Source)
	})
	if err := s.details(ctx, items, &rep.Stats); err != nil {
		return rep, err
	}

	now := s.now()
	for _, it := range items {
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
	return rep, s.store.FinishRun(ctx, runID, rep.Stats, rep.Errors, s.now())
}

func (s *Service) fetchAll(ctx context.Context, sources []Source) []fetched {
	out := make([]fetched, len(sources))
	s.parallel(len(sources), func(i int) {
		b, err := sources[i].Fetch(ctx)
		out[i] = fetched{src: sources[i], batch: b, err: err}
	})
	return out
}

// details loads missing descriptions for postings the index does not describe yet, at most MaxDetails per crawl.
// A failed request leaves the description empty; the next crawl tries again.
func (s *Service) details(ctx context.Context, items []item, st *Stats) error {
	var need []int
	var fps []string
	for i, it := range items {
		if _, ok := it.src.(Detailer); ok && it.p.Description == "" {
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
	for k, i := range need {
		if !described[fps[k]] && len(todo) < MaxDetails {
			todo = append(todo, i)
		}
	}
	failed := make([]bool, len(todo))
	s.parallel(len(todo), func(k int) {
		it := &items[todo[k]]
		p := it.p
		err := it.src.(Detailer).Details(ctx, &p)
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

// parallel runs fn for 0..n-1 with at most s.parallelism at a time.
func (s *Service) parallel(n int, fn func(int)) {
	sem := make(chan struct{}, s.parallelism)
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
