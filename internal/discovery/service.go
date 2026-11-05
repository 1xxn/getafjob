package discovery

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

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
	var postings []Posting
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
			postings = append(postings, p)
		}
	}
	// Company boards first: an aggregator copy arriving later is recognized as a duplicate.
	sort.SliceStable(postings, func(i, j int) bool { return !IsAggregator(postings[i].Source) && IsAggregator(postings[j].Source) })

	now := s.now()
	for _, p := range postings {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		change, dup, err := s.store.Upsert(ctx, runID, p, now)
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
	sem := make(chan struct{}, s.parallelism)
	var wg sync.WaitGroup
	for i, src := range sources {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			b, err := src.Fetch(ctx)
			out[i] = fetched{src: src, batch: b, err: err}
		})
	}
	wg.Wait()
	return out
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
