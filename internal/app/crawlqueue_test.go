package app

import (
	"io"
	"log/slog"
	"testing"

	"github.com/1xxn/getafjob/internal/platform/db/dbtest"
	"github.com/1xxn/getafjob/internal/platform/queue"
)

// A crawl counts as on its way from the click until it ends: waiting, taken by the worker before its run row
// exists, or waiting for a retry. Only a finished job does not.
func TestCrawlQueueCountsRunningJobs(t *testing.T) {
	pool := dbtest.AppPool(t)
	ctx := t.Context()
	ins, err := queue.NewInserter(pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	q := NewCrawlQueue(ins, pool)
	if on, err := q.IsQueued(ctx); err != nil || on {
		t.Fatalf("before any crawl: %v, %v", on, err)
	}
	if _, err := q.QueueCrawl(ctx); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"available", "running", "retryable", "completed"} {
		if _, err := pool.Exec(ctx, "UPDATE river_job SET state = $1::river_job_state, finalized_at = CASE WHEN $1::text = 'completed' THEN now() END WHERE kind = 'crawl'", state); err != nil {
			t.Fatal(err)
		}
		on, err := q.IsQueued(ctx)
		if err != nil || on != (state != "completed") {
			t.Errorf("state %s: on its way = %v, %v", state, on, err)
		}
	}
}
