// Package pgstore implements the matching ports on PostgreSQL: jobs from the global index, results in the
// user's own transaction (row-level security).
package pgstore

//go:generate go tool sqlc generate -f ../../../../sqlc.yaml

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/1xxn/getafjob/internal/matching"
	"github.com/1xxn/getafjob/internal/matching/adapters/pgstore/q"
	"github.com/1xxn/getafjob/internal/platform/db"
)

// Store is the PostgreSQL matching store.
type Store struct{ pool *pgxpool.Pool }

var (
	_ matching.Jobs  = (*Store)(nil)
	_ matching.Store = (*Store)(nil)
)

// New returns a store on pool (application role).
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// OpenJobs returns up to limit open jobs with an ID above afterID, in ID order.
func (s *Store) OpenJobs(ctx context.Context, afterID int64, limit int) ([]matching.Job, error) {
	rows, err := q.New(s.pool).OpenJobsPage(ctx, q.OpenJobsPageParams{ID: afterID, Limit: int32(min(limit, 10_000))}) //nolint:gosec // bounded
	if err != nil {
		return nil, err
	}
	out := make([]matching.Job, len(rows))
	for i, r := range rows {
		out[i] = matching.Job{ID: r.ID, Company: r.Company, Title: r.Title, Location: r.Location,
			Workplace: r.Workplace, Description: r.Description, Posted: date(r.Posted), FirstSeen: r.FirstSeen}
	}
	return out, nil
}

type row struct {
	JobID    int64             `json:"job_id"`
	Score    int               `json:"score"`
	Decision matching.Decision `json:"decision"`
	Reasons  []matching.Reason `json:"reasons"`
}

// SaveMatches upserts results; unchanged rows are not written.
func (s *Store) SaveMatches(ctx context.Context, userID uuid.UUID, rs []matching.Result, at time.Time) error {
	if len(rs) == 0 {
		return nil
	}
	rows := make([]row, len(rs))
	for i, r := range rs {
		rows[i] = row{JobID: r.JobID, Score: r.Score, Decision: r.Decision, Reasons: r.Reasons}
		if rows[i].Reasons == nil {
			rows[i].Reasons = []matching.Reason{}
		}
	}
	data, err := json.Marshal(rows)
	if err != nil {
		return err
	}
	return db.InUserTx(ctx, s.pool, userID, func(tx pgx.Tx) error {
		return q.New(tx).SaveMatches(ctx, q.SaveMatchesParams{UserID: userID, Rules: matching.RulesVersion,
			ComputedAt: at, Results: data})
	})
}

var _ matching.Reader = (*Store)(nil)

func date(d pgtype.Date) time.Time {
	if d.Valid {
		return d.Time
	}
	return time.Time{}
}

// ListMatches returns one page of the user's open jobs and the number of hits.
func (s *Store) ListMatches(ctx context.Context, userID uuid.UUID, f matching.Filter, offset, limit int) (
	items []matching.Item, total int, err error,
) {
	ds := make([]string, len(f.Decisions))
	for i, d := range f.Decisions {
		ds[i] = string(d)
	}
	err = db.InUserTx(ctx, s.pool, userID, func(tx pgx.Tx) error {
		rows, err := q.New(tx).ListMatches(ctx, q.ListMatchesParams{UserID: userID, Decisions: ds,
			Workplace: f.Workplace, Query: f.Query, PageOffset: int32(min(offset, 1_000_000)), //nolint:gosec // bounded
			PageSize: int32(min(limit, 1000))}) //nolint:gosec // bounded
		if err != nil {
			return err
		}
		items = make([]matching.Item, len(rows))
		for i, r := range rows {
			items[i] = matching.Item{JobID: r.ID, Title: r.Title, Company: r.Company, Location: r.Location,
				Workplace: r.Workplace, Posted: date(r.Posted), FirstSeen: r.FirstSeen, Score: int(r.Score),
				Decision: matching.Decision(r.Decision)}
			total = int(r.Total)
		}
		return nil
	})
	return items, total, err
}

// MatchCounts counts the user's open jobs per decision.
func (s *Store) MatchCounts(ctx context.Context, userID uuid.UUID) (map[matching.Decision]int, error) {
	out := map[matching.Decision]int{}
	err := db.InUserTx(ctx, s.pool, userID, func(tx pgx.Tx) error {
		rows, err := q.New(tx).MatchCounts(ctx, userID)
		for _, r := range rows {
			out[matching.Decision(r.Decision)] = int(r.N)
		}
		return err
	})
	return out, err
}

// Match returns one job with the user's result.
func (s *Store) Match(ctx context.Context, userID uuid.UUID, jobID int64) (d matching.Detail, err error) {
	err = db.InUserTx(ctx, s.pool, userID, func(tx pgx.Tx) error {
		r, err := q.New(tx).GetMatch(ctx, q.GetMatchParams{UserID: userID, ID: jobID})
		if errors.Is(err, pgx.ErrNoRows) {
			return matching.ErrNotFound
		}
		if err != nil {
			return err
		}
		d = matching.Detail{Item: matching.Item{JobID: r.ID, Title: r.Title, Company: r.Company, Location: r.Location,
			Workplace: r.Workplace, Posted: date(r.Posted), FirstSeen: r.FirstSeen, Score: int(r.Score),
			Decision: matching.Decision(r.Decision)}, Description: r.Description, URL: r.Url, ApplyURL: r.ApplyUrl,
			Open: r.Status == "open", ComputedAt: r.ComputedAt}
		return json.Unmarshal(r.Reasons, &d.Reasons)
	})
	return d, err
}

// UserMatches returns all job matches for the user.
func (s *Store) UserMatches(ctx context.Context, userID uuid.UUID) (out []matching.MatchSummary, err error) {
	err = db.InUserTx(ctx, s.pool, userID, func(tx pgx.Tx) error {
		rows, err := q.New(tx).UserMatches(ctx, userID)
		if err != nil {
			return err
		}
		out = make([]matching.MatchSummary, len(rows))
		for i, r := range rows {
			var reasons []matching.Reason
			if len(r.Reasons) > 0 {
				_ = json.Unmarshal(r.Reasons, &reasons)
			}
			out[i] = matching.MatchSummary{
				JobID:      r.ID,
				Title:      r.Title,
				Company:    r.Company,
				Location:   r.Location,
				Workplace:  r.Workplace,
				Score:      int(r.Score),
				Decision:   matching.Decision(r.Decision),
				Reasons:    reasons,
				ComputedAt: r.ComputedAt,
			}
		}
		return nil
	})
	return out, err
}
