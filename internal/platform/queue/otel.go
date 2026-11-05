package queue

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	gotel "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

const scope = "github.com/1xxn/getafjob/internal/platform/queue"

// telemetry is a worker middleware: one span and one duration sample per job, by kind and outcome.
type telemetry struct {
	river.MiddlewareDefaults
	tracer   trace.Tracer
	duration metric.Float64Histogram
}

func newTelemetry() (*telemetry, error) {
	d, err := gotel.Meter(scope).Float64Histogram("getafjob.queue.job.duration", metric.WithUnit("s"),
		metric.WithDescription("Duration of worked jobs by kind and outcome."))
	if err != nil {
		return nil, err
	}
	return &telemetry{tracer: gotel.Tracer(scope), duration: d}, nil
}

var _ rivertype.WorkerMiddleware = (*telemetry)(nil)

// Work implements rivertype.WorkerMiddleware.
func (t *telemetry) Work(ctx context.Context, job *rivertype.JobRow, doInner func(context.Context) error) error {
	ctx, span := t.tracer.Start(ctx, "job "+job.Kind, trace.WithAttributes(
		attribute.String("job.kind", job.Kind), attribute.String("job.queue", job.Queue), attribute.Int("job.attempt", job.Attempt)))
	defer span.End()
	start := time.Now()
	err := doInner(ctx)
	outcome := "ok"
	if err != nil {
		outcome = "error"
		span.SetStatus(codes.Error, "job failed") // the error text may carry details that do not belong in traces
	}
	t.duration.Record(ctx, time.Since(start).Seconds(), metric.WithAttributes(
		attribute.String("job.kind", job.Kind), attribute.String("outcome", outcome)))
	return err
}

// RegisterDepth reports the number of waiting and running jobs per queue and state (getafjob.queue.jobs).
func RegisterDepth(pool *pgxpool.Pool) error {
	m := gotel.Meter(scope)
	g, err := m.Int64ObservableGauge("getafjob.queue.jobs", metric.WithDescription("Jobs per queue and state."))
	if err != nil {
		return err
	}
	_, err = m.RegisterCallback(func(ctx context.Context, o metric.Observer) error {
		rows, err := pool.Query(ctx, `SELECT queue, state::text, count(*) FROM river_job
			WHERE state IN ('available', 'running', 'retryable', 'scheduled') GROUP BY 1, 2`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var q, state string
			var n int64
			if err := rows.Scan(&q, &state, &n); err != nil {
				return err
			}
			o.ObserveInt64(g, n, metric.WithAttributes(attribute.String("queue", q), attribute.String("state", state)))
		}
		return rows.Err()
	}, g)
	return err
}
