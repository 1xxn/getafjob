package app

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/1xxn/getafjob/internal/discovery"
	"github.com/1xxn/getafjob/internal/matching"
	"github.com/1xxn/getafjob/internal/platform/queue"
)

// RiverCrawlQueue connects the URL import service to River crawl worker jobs.
type RiverCrawlQueue struct {
	inserter *queue.Client
	pool     *pgxpool.Pool
}

// NewCrawlQueue returns a CrawlQueue backed by River.
func NewCrawlQueue(inserter *queue.Client, pool *pgxpool.Pool) *RiverCrawlQueue {
	return &RiverCrawlQueue{inserter: inserter, pool: pool}
}

// QueueCrawl queues an immediate manual crawl.
func (q *RiverCrawlQueue) QueueCrawl(ctx context.Context) (bool, error) {
	if q.inserter == nil {
		return false, nil
	}
	res, err := q.inserter.Insert(ctx, CrawlArgs{Manual: true}, nil)
	if err != nil {
		return false, err
	}
	return !res.UniqueSkippedAsDuplicate, nil
}

// IsQueued reports whether a crawl job is currently waiting in the River queue.
func (q *RiverCrawlQueue) IsQueued(ctx context.Context) (bool, error) {
	if q.pool == nil {
		return false, nil
	}
	var queued bool
	err := q.pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM river_job WHERE kind = 'crawl' AND state IN ('available', 'pending', 'scheduled'))").Scan(&queued)
	if err != nil {
		return false, err
	}
	return queued, nil
}

// AppMatchEvaluator adapts matching profile evaluation to the urlimport MatchEvaluator interface.
type AppMatchEvaluator struct {
	profiles matching.Profiles
	matching matching.Store
}

// NewMatchEvaluator returns a MatchEvaluator backed by the matching module.
func NewMatchEvaluator(profiles matching.Profiles, matching matching.Store) *AppMatchEvaluator {
	return &AppMatchEvaluator{profiles: profiles, matching: matching}
}

// EvaluateAndSave evaluates a posting against the user's active profile and saves the match.
func (a *AppMatchEvaluator) EvaluateAndSave(ctx context.Context, userID uuid.UUID, jobID int64, p discovery.Posting, at time.Time) error {
	var criteria matching.Criteria
	if a.profiles != nil {
		if c, err := a.profiles.Criteria(ctx, userID); err == nil {
			criteria = c
		}
	}
	// Rules only here (serve role, no model in memory); the worker's next match run adds the similarity.
	m := matching.NewMatcher(criteria, nil)
	posted, _ := time.Parse(time.DateOnly, p.Posted)
	mj := matching.Job{
		ID:          jobID,
		Company:     p.Company,
		Title:       p.Title,
		Location:    p.Location,
		Workplace:   string(p.Workplace),
		Description: p.Description,
		Posted:      posted,
		FirstSeen:   at,
	}
	res := m.Evaluate(mj, at)
	return a.matching.SaveMatches(ctx, userID, []matching.Result{res}, at)
}
