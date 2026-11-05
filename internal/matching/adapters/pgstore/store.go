// Package pgstore implements the matching ports on PostgreSQL: jobs from the global index, results in the
// user's own transaction (row-level security).
package pgstore

//go:generate go tool sqlc generate -f ../../../../sqlc.yaml

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
			Workplace: r.Workplace, Description: r.Description, FirstSeen: r.FirstSeen}
		if r.Posted.Valid {
			out[i].Posted = r.Posted.Time
		}
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
