package app

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/1xxn/getafjob/internal/platform/db"
	"github.com/1xxn/getafjob/internal/platform/queue"
	"github.com/1xxn/getafjob/internal/web"
)

// adminOps reads runners, sources and the job queue for the admin console; all in system context.
type adminOps struct {
	pool  *pgxpool.Pool
	queue *queue.Client
}

var _ web.AdminOps = adminOps{}

// Runners summarizes the runs per runner: when it last took a run, what runs now, what the last day brought.
func (o adminOps) Runners(ctx context.Context, now time.Time) ([]web.RunnerStat, error) {
	var out []web.RunnerStat
	err := db.InSystemTx(ctx, o.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT runner_id, max(leased_at),
			       count(*) FILTER (WHERE finished_at IS NULL AND expires_at > $1),
			       count(*) FILTER (WHERE leased_at > $2),
			       count(*) FILTER (WHERE leased_at > $2 AND outcome = 'submitted'),
			       count(*) FILTER (WHERE leased_at > $2 AND outcome IN ('parked', 'expired'))
			FROM runs GROUP BY runner_id ORDER BY max(leased_at) DESC LIMIT 100`, now, now.Add(-24*time.Hour))
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (web.RunnerStat, error) {
			var s web.RunnerStat
			err := r.Scan(&s.ID, &s.LastLease, &s.Active, &s.Runs24h, &s.Submitted24h, &s.Stopped24h)
			return s, err
		})
		return err
	})
	return out, err
}

// Sources lists boards matching q (company or slug); state "failing" keeps boards with an error, "disabled" the
// switched off ones. total counts all matches, the list holds at most limit.
func (o adminOps) Sources(ctx context.Context, q, state string, limit int) (list []web.SourceStat, total int, err error) {
	where := []string{"true"}
	args := []any{}
	if q = strings.TrimSpace(q); q != "" {
		args = append(args, "%"+strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(q)+"%")
		where = append(where, "(company ILIKE $1 OR slug ILIKE $1)")
	}
	switch state {
	case "failing":
		where = append(where, "enabled AND last_error IS NOT NULL AND last_error <> ''")
	case "disabled":
		where = append(where, "NOT enabled")
	}
	cond := strings.Join(where, " AND ") // fixed fragments only; values travel as parameters
	args = append(args, min(max(limit, 1), 500))
	lim := "$" + strconv.Itoa(len(args))
	err = db.InSystemTx(ctx, o.pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM boards WHERE "+cond, args[:len(args)-1]...).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id, ats, slug, company, enabled, last_crawled_at, coalesce(last_error, '')
			FROM boards WHERE `+cond+` ORDER BY (last_error IS NOT NULL AND last_error <> '') DESC, company, slug LIMIT `+lim, args...)
		if err != nil {
			return err
		}
		list, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (web.SourceStat, error) {
			var s web.SourceStat
			err := r.Scan(&s.ID, &s.ATS, &s.Slug, &s.Company, &s.Enabled, &s.LastCrawled, &s.LastError)
			return s, err
		})
		return err
	})
	return list, total, err
}

// SetSourceEnabled switches a board on or off; false when there is none with the ID.
func (o adminOps) SetSourceEnabled(ctx context.Context, id int64, enabled bool) (bool, error) {
	var n int64
	err := db.InSystemTx(ctx, o.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, "UPDATE boards SET enabled = $2 WHERE id = $1", id, enabled)
		n = tag.RowsAffected()
		return err
	})
	return n > 0, err
}

// Queue counts the jobs per kind and state (finished ones of the last day only) and lists the newest failures.
func (o adminOps) Queue(ctx context.Context, now time.Time) (counts []web.QueueCount, failures []web.QueueFailure, err error) {
	err = db.InSystemTx(ctx, o.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT kind, state::text, count(*) FROM river_job
			WHERE finalized_at IS NULL OR finalized_at > $1 GROUP BY 1, 2 ORDER BY 1, 2`, now.Add(-24*time.Hour))
		if err != nil {
			return err
		}
		counts, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (web.QueueCount, error) {
			var c web.QueueCount
			err := r.Scan(&c.Kind, &c.State, &c.Count)
			return c, err
		})
		if err != nil {
			return err
		}
		rows, err = tx.Query(ctx, `SELECT id, kind, state::text, attempt, coalesce(errors[array_length(errors, 1)]->>'error', ''),
			coalesce(finalized_at, attempted_at, created_at) FROM river_job
			WHERE state IN ('discarded', 'retryable') ORDER BY id DESC LIMIT 20`)
		if err != nil {
			return err
		}
		failures, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (web.QueueFailure, error) {
			var f web.QueueFailure
			err := r.Scan(&f.ID, &f.Kind, &f.State, &f.Attempt, &f.Error, &f.At)
			return f, err
		})
		return err
	})
	return counts, failures, err
}

// Retry puts a failed job back in the queue now.
func (o adminOps) Retry(ctx context.Context, id int64) error {
	_, err := o.queue.JobRetry(ctx, id)
	return err
}
