// Package pgstore implements discovery.Store on PostgreSQL (sqlc queries in q). The job index is global and holds no
// personal data, so it runs in the system context.
package pgstore

//go:generate go tool sqlc generate -f ../../../../sqlc.yaml

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/1xxn/getafjob/internal/discovery"
	"github.com/1xxn/getafjob/internal/discovery/adapters/pgstore/q"
	"github.com/1xxn/getafjob/internal/platform/db"
)

// Store is the PostgreSQL job index.
type Store struct{ pool *pgxpool.Pool }

var (
	_ discovery.Store       = (*Store)(nil)
	_ discovery.Boards      = (*Store)(nil)
	_ discovery.BoardChecks = (*Store)(nil)
)

// New returns a store on pool.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) system(ctx context.Context, fn func(*q.Queries) error) error {
	return db.InSystemTx(ctx, s.pool, func(tx pgx.Tx) error { return fn(q.New(tx)) })
}

// StartRun records the start of a crawl.
func (s *Store) StartRun(ctx context.Context, sources []string, at time.Time) (id int64, err error) {
	err = s.system(ctx, func(qs *q.Queries) error {
		id, err = qs.StartRun(ctx, q.StartRunParams{StartedAt: at, Sources: sources})
		return err
	})
	return id, err
}

// FinishRun stores the crawl's statistics and per-source errors.
func (s *Store) FinishRun(ctx context.Context, runID int64, st discovery.Stats, errs map[string]string, at time.Time) error {
	stats, err := json.Marshal(st)
	if err != nil {
		return err
	}
	if errs == nil {
		errs = map[string]string{}
	}
	e, err := json.Marshal(errs)
	if err != nil {
		return err
	}
	return s.system(ctx, func(qs *q.Queries) error {
		return qs.FinishRun(ctx, q.FinishRunParams{ID: runID, FinishedAt: &at, Stats: stats, Errors: e})
	})
}

// Upsert inserts, updates, reopens or merges one posting and records a snapshot of every change.
func (s *Store) Upsert(ctx context.Context, runID int64, p discovery.Posting, at time.Time) (change discovery.Change, dup bool, err error) {
	err = s.system(ctx, func(qs *q.Queries) error {
		change, dup, err = upsert(ctx, qs, runID, p, at)
		return err
	})
	return change, dup, err
}

func upsert(ctx context.Context, qs *q.Queries, runID int64, p discovery.Posting, at time.Time) (discovery.Change, bool, error) {
	fp := discovery.Fingerprint(p.Source, p.ExternalID)
	mk := discovery.MatchKey(p.Company, p.Title)
	hash := discovery.ContentHash(p.Title, p.Location, p.Description)
	if err := qs.LockMatchKey(ctx, mk); err != nil {
		return "", false, err
	}

	job, err := qs.JobByFingerprint(ctx, fp)
	found := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", false, err
	}
	if found && p.Description == "" {
		hash = discovery.ContentHash(p.Title, p.Location, job.Description) // details not loaded this time
	}

	if !found {
		other, err := sameJob(ctx, qs, p, mk)
		if err != nil {
			return "", false, err
		}
		switch {
		case other != nil && discovery.IsAggregator(p.Source):
			return "", true, nil
		case other != nil && discovery.IsAggregator(other.Source):
			// The company board replaces the job-board entry (direct link, complete data).
			alt := other.AltUrls
			if other.Url != "" && other.Url != p.URL && !slices.Contains(alt, other.Url) {
				alt = append(alt, other.Url)
			}
			if err := qs.UpdateJob(ctx, updateParams(other.ID, fp, mk, hash, p, alt, true, at)); err != nil {
				return "", false, err
			}
			return discovery.ChangeMerged, false, snapshot(ctx, qs, other.ID, runID, discovery.ChangeMerged, p, hash, nil, at)
		}
		id, err := qs.InsertJob(ctx, q.InsertJobParams{
			Fingerprint: fp, MatchKey: mk, Source: p.Source, ExternalID: p.ExternalID, Company: p.Company,
			Title: p.Title, Location: p.Location, Workplace: string(p.Workplace), Url: p.URL, ApplyUrl: p.ApplyURL,
			Description: p.Description, Posted: date(p.Posted), ContentHash: hash, FirstSeen: at,
		})
		if err != nil {
			return "", false, err
		}
		return discovery.ChangeNew, false, snapshot(ctx, qs, id, runID, discovery.ChangeNew, p, hash, nil, at)
	}

	change := discovery.ChangeUnchanged
	diff := map[string]any{}
	if job.Title != p.Title {
		diff["title"] = []string{job.Title, p.Title}
	}
	if job.Location != p.Location {
		diff["location"] = []string{job.Location, p.Location}
	}
	if job.ContentHash != hash {
		diff["content"] = "changed"
	}
	if len(diff) > 0 {
		change = discovery.ChangeUpdated
	}
	if job.Status == "closed" {
		change = discovery.ChangeReopened
	}
	if err := qs.UpdateJob(ctx, updateParams(job.ID, fp, mk, hash, p, job.AltUrls, false, at)); err != nil {
		return "", false, err
	}
	if change == discovery.ChangeUnchanged {
		return change, false, nil
	}
	return change, false, snapshot(ctx, qs, job.ID, runID, change, p, hash, diff, at)
}

// sameJob finds the open job another source already lists under the same match key. A job board that lists a job
// twice (reposted under a new ID) counts as well when the location is the same; a company board may list one title
// in several places.
func sameJob(ctx context.Context, qs *q.Queries, p discovery.Posting, mk string) (*q.OpenJobsByMatchKeyRow, error) {
	if discovery.CompanyKey(p.Company) == "" {
		return nil, nil // without a company every "Software Engineer" would match
	}
	rows, err := qs.OpenJobsByMatchKey(ctx, mk)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		if rows[i].Source != p.Source {
			return &rows[i], nil
		}
	}
	if discovery.IsAggregator(p.Source) {
		for i := range rows {
			if rows[i].Location == p.Location {
				return &rows[i], nil
			}
		}
	}
	return nil, nil
}

func updateParams(id int64, fp, mk, hash string, p discovery.Posting, alt []string, merge bool, at time.Time) q.UpdateJobParams {
	if alt == nil {
		alt = []string{}
	}
	return q.UpdateJobParams{
		ID: id, Fingerprint: fp, MatchKey: mk, Source: p.Source, ExternalID: p.ExternalID, Company: p.Company,
		Title: p.Title, Location: p.Location, Workplace: string(p.Workplace), Url: p.URL, Merge: merge,
		ApplyUrl: p.ApplyURL, AltUrls: alt, Description: p.Description, Posted: date(p.Posted), ContentHash: hash,
		LastSeen: at,
	}
}

func snapshot(ctx context.Context, qs *q.Queries, jobID, runID int64, c discovery.Change, p discovery.Posting, hash string, diff map[string]any, at time.Time) error {
	if diff == nil {
		diff = map[string]any{}
	}
	b, err := json.Marshal(diff)
	if err != nil {
		return err
	}
	return qs.InsertSnapshot(ctx, q.InsertSnapshotParams{
		JobID: jobID, CrawlRun: &runID, Change: string(c), Title: p.Title, Location: p.Location,
		ContentHash: hash, Diff: b, CreatedAt: at,
	})
}

func date(s string) pgtype.Date {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return pgtype.Date{}
	}
	return pgtype.Date{Time: t, Valid: true}
}

// OpenJobs lists the open jobs of the given sources.
func (s *Store) OpenJobs(ctx context.Context, sources []string) ([]discovery.OpenJob, error) {
	var out []discovery.OpenJob
	err := s.system(ctx, func(qs *q.Queries) error {
		rows, err := qs.OpenJobsOfSources(ctx, sources)
		if err != nil {
			return err
		}
		out = make([]discovery.OpenJob, len(rows))
		for i, r := range rows {
			out[i] = discovery.OpenJob{ID: r.ID, Fingerprint: r.Fingerprint, Source: r.Source, LastSeen: r.LastSeen}
		}
		return nil
	})
	return out, err
}

// Seen marks open jobs as seen and returns the fingerprints it found.
func (s *Store) Seen(ctx context.Context, fingerprints []string, at time.Time) (map[string]bool, error) {
	out := map[string]bool{}
	if len(fingerprints) == 0 {
		return out, nil
	}
	err := s.system(ctx, func(qs *q.Queries) error {
		fps, err := qs.TouchOpenJobs(ctx, q.TouchOpenJobsParams{At: at, Fingerprints: fingerprints})
		for _, fp := range fps {
			out[fp] = true
		}
		return err
	})
	return out, err
}

// Described returns the fingerprints of stored jobs that have a description.
func (s *Store) Described(ctx context.Context, fingerprints []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(fingerprints) == 0 {
		return out, nil
	}
	err := s.system(ctx, func(qs *q.Queries) error {
		fps, err := qs.DescribedJobs(ctx, fingerprints)
		for _, fp := range fps {
			out[fp] = true
		}
		return err
	})
	return out, err
}

// Close marks jobs as closed and records a snapshot for each.
func (s *Store) Close(ctx context.Context, runID int64, ids []int64, at time.Time) error {
	if len(ids) == 0 {
		return nil
	}
	return s.system(ctx, func(qs *q.Queries) error {
		for _, id := range ids {
			r, err := qs.CloseJob(ctx, q.CloseJobParams{ID: id, ClosedAt: &at})
			if errors.Is(err, pgx.ErrNoRows) {
				continue // closed meanwhile
			}
			if err != nil {
				return err
			}
			if err := qs.InsertSnapshot(ctx, q.InsertSnapshotParams{
				JobID: id, CrawlRun: &runID, Change: string(discovery.ChangeClosed), Title: r.Title,
				Location: r.Location, ContentHash: r.ContentHash, Diff: []byte("{}"), CreatedAt: at,
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// ImportBoards upserts boards in one transaction.
func (s *Store) ImportBoards(ctx context.Context, boards []discovery.Board) (inserted, updated int, err error) {
	err = s.system(ctx, func(qs *q.Queries) error {
		inserted, updated = 0, 0
		for _, b := range boards {
			cfg := b.Config
			if len(cfg) == 0 {
				cfg = []byte("{}")
			}
			isNew, err := qs.UpsertBoard(ctx, q.UpsertBoardParams{Ats: b.ATS, Slug: b.Slug, Company: b.Company, Config: cfg})
			if err != nil {
				return err
			}
			if isNew {
				inserted++
			} else {
				updated++
			}
		}
		return nil
	})
	return inserted, updated, err
}

// EnabledBoards lists the boards to crawl.
func (s *Store) EnabledBoards(ctx context.Context) ([]discovery.Board, error) {
	var out []discovery.Board
	err := s.system(ctx, func(qs *q.Queries) error {
		rows, err := qs.EnabledBoards(ctx)
		for _, r := range rows {
			out = append(out, discovery.Board{ID: r.ID, ATS: r.Ats, Slug: r.Slug, Company: r.Company, Config: r.Config})
		}
		return err
	})
	return out, err
}

// BoardCandidates returns open job-board hits of companies without a check that still holds.
func (s *Store) BoardCandidates(ctx context.Context, now time.Time, maxRows int) ([]discovery.Hit, error) {
	var out []discovery.Hit
	err := s.system(ctx, func(qs *q.Queries) error {
		rows, err := qs.BoardCandidates(ctx, q.BoardCandidatesParams{
			Aggregators: discovery.Aggregators(), Now: now, MaxRows: int32(min(maxRows, 1<<20)), //nolint:gosec // bounded
		})
		for _, r := range rows {
			out = append(out, discovery.Hit{Company: r.Company, Title: r.Title, URL: r.Url, ApplyURL: r.ApplyUrl, MatchKey: r.MatchKey})
		}
		return err
	})
	return out, err
}

// RecordBoardChecks stores the outcomes and adds confirmed boards that are not known yet (slugs compared without
// case).
func (s *Store) RecordBoardChecks(ctx context.Context, checks []discovery.BoardCheck, at time.Time) (added int, err error) {
	err = s.system(ctx, func(qs *q.Queries) error {
		added = 0
		for _, c := range checks {
			var boardID *int64
			if b := c.Board; b != nil {
				id, isNew, err := discoveredBoard(ctx, qs, *b)
				if err != nil {
					return err
				}
				boardID = &id
				if isNew {
					added++
				}
			}
			if err := qs.RecordBoardCheck(ctx, q.RecordBoardCheckParams{
				CompanyKey: c.CompanyKey, CheckedAt: at, RecheckAt: at.Add(c.Result.RecheckAfter()),
				Result: string(c.Result), BoardID: boardID,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	return added, err
}

func discoveredBoard(ctx context.Context, qs *q.Queries, b discovery.Board) (id int64, isNew bool, err error) {
	id, err = qs.BoardBySlug(ctx, q.BoardBySlugParams{Ats: b.ATS, Slug: b.Slug})
	if err == nil {
		return id, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, false, err
	}
	cfg := b.Config
	if len(cfg) == 0 {
		cfg = []byte("{}")
	}
	id, err = qs.InsertDiscoveredBoard(ctx, q.InsertDiscoveredBoardParams{Ats: b.ATS, Slug: b.Slug, Company: b.Company, Config: cfg})
	return id, err == nil, err
}
