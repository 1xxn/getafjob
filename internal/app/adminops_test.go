package app

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/1xxn/getafjob/internal/platform/db"
	"github.com/1xxn/getafjob/internal/platform/db/dbtest"
	"github.com/1xxn/getafjob/internal/platform/queue"
)

// The admin pages read runners from runs, boards with their state and the queue by kind and state; a failed job
// goes back to the queue.
func TestAdminOps(t *testing.T) {
	pool := dbtest.AppPool(t)
	ctx := t.Context()
	ins, err := queue.NewInserter(pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ops := adminOps{pool: pool, queue: ins}
	now := time.Now()
	var failed int64
	if err := db.InSystemTx(ctx, pool, func(tx pgx.Tx) error {
		var user uuid.UUID
		if err := tx.QueryRow(ctx, "INSERT INTO users (email) VALUES ('ops@example.org') RETURNING id").Scan(&user); err != nil {
			return err
		}
		var job int64
		if err := tx.QueryRow(ctx, `INSERT INTO jobs (fingerprint, match_key, source, external_id, company, title, url, content_hash,
			first_seen, last_seen) VALUES ('fp-ops', 'k', 'demo', 'fp-ops', 'Muster GmbH', 'Go Developer', 'https://example.org', 'h', $1, $1)
			RETURNING id`, now).Scan(&job); err != nil {
			return err
		}
		var app uuid.UUID
		if err := tx.QueryRow(ctx, `INSERT INTO applications (user_id, job_id, state, created_at, updated_at)
			VALUES ($1, $2, 'running', $3, $3) RETURNING id`, user, job, now).Scan(&app); err != nil {
			return err
		}
		for i, outcome := range []any{nil, "submitted", "parked"} {
			if _, err := tx.Exec(ctx, `INSERT INTO runs (application_id, user_id, runner_id, host, token_hash, leased_at, expires_at,
				finished_at, outcome) VALUES ($1, $2, 'runner-a', 'example.org', $3, $4, $5, $6, $7)`, app, user,
				token(), now.Add(-time.Duration(i)*time.Minute), now.Add(time.Hour),
				map[bool]any{true: nil, false: now}[outcome == nil], outcome); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO boards (ats, slug, company, last_error) VALUES
			('greenhouse', 'muster', 'Muster GmbH', NULL), ('lever', 'beispiel', 'Beispiel AG', 'http 404')`); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `INSERT INTO river_job (args, kind, max_attempts, state, finalized_at, errors, attempt)
			VALUES ('{}', 'admin_ops_test', 1, 'discarded', now(), ARRAY['{"error":"boom","attempt":1}'::jsonb], 1) RETURNING id`).Scan(&failed)
	}); err != nil {
		t.Fatal(err)
	}

	runners, err := ops.Runners(ctx, now)
	if err != nil || len(runners) != 1 || runners[0].ID != "runner-a" || runners[0].Active != 1 || runners[0].Runs24h != 3 ||
		runners[0].Submitted24h != 1 || runners[0].Stopped24h != 1 {
		t.Fatalf("runners = %+v, %v", runners, err)
	}

	list, total, err := ops.Sources(ctx, "", "failing", 10)
	if err != nil || total != 1 || len(list) != 1 || list[0].Slug != "beispiel" || list[0].LastError != "http 404" {
		t.Fatalf("failing = %+v %d %v", list, total, err)
	}
	if list, _, _ := ops.Sources(ctx, "mus%", "", 10); len(list) != 0 {
		t.Errorf("a %% in the search matched as a wildcard: %+v", list)
	}
	if ok, err := ops.SetSourceEnabled(ctx, list0ID(t, ops, "muster"), false); !ok || err != nil {
		t.Fatalf("disable: %v %v", ok, err)
	}
	if list, total, _ := ops.Sources(ctx, "muster", "disabled", 10); total != 1 || len(list) != 1 || list[0].Enabled {
		t.Errorf("disabled = %+v", list)
	}

	counts, failures, err := ops.Queue(ctx, now)
	if err != nil || len(failures) != 1 || failures[0].ID != failed || failures[0].Error != "boom" {
		t.Fatalf("queue = %+v %+v %v", counts, failures, err)
	}
	if err := ops.Retry(ctx, failed); err != nil {
		t.Fatal(err)
	}
	if row, err := ins.JobGet(context.WithoutCancel(ctx), failed); err != nil || row.State == "discarded" {
		t.Errorf("after retry: %v %v", row, err)
	}
}

// token is a random 32 byte hash for a run.
func token() []byte {
	a, b := uuid.New(), uuid.New()
	return append(a[:], b[:]...)
}

func list0ID(t *testing.T, ops adminOps, slug string) int64 {
	t.Helper()
	list, _, err := ops.Sources(t.Context(), slug, "", 1)
	if err != nil || len(list) != 1 {
		t.Fatalf("source %s: %v %v", slug, list, err)
	}
	return list[0].ID
}
