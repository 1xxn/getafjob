// Package queue sets up River, the job queue in PostgreSQL (ADR 0005): its schema, the queues and the client.
package queue

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

// Queues.
const (
	Crawl = "crawl" // one crawl at a time
	Apply = "apply"
	Mail  = "mail"
)

// Client is the River client used by the worker (and, for inserts only, by serve).
type Client = river.Client[pgx.Tx]

// Migrate brings River's tables up to date (role migrate, database owner). It returns the applied versions.
func Migrate(ctx context.Context, pool *pgxpool.Pool) ([]int, error) {
	m, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		return nil, err
	}
	res, err := m.Migrate(ctx, rivermigrate.DirectionUp, nil)
	if err != nil {
		return nil, fmt.Errorf("river migrations: %w", err)
	}
	out := make([]int, 0, len(res.Versions))
	for _, v := range res.Versions {
		out = append(out, v.Version)
	}
	return out, nil
}

// Options configure a working client.
type Options struct {
	Workers  *river.Workers
	Periodic []*river.PeriodicJob
	Logger   *slog.Logger
}

// NewWorker returns a client that works the queues; start it with Start and stop it with Stop.
func NewWorker(pool *pgxpool.Pool, o Options) (*Client, error) {
	return river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: 4},
			Crawl:              {MaxWorkers: 1},
			Apply:              {MaxWorkers: 2},
			Mail:               {MaxWorkers: 2},
		},
		Workers:      o.Workers,
		PeriodicJobs: o.Periodic,
		Logger:       o.Logger,
	})
}

// NewInserter returns a client that only inserts jobs (serve, admin).
func NewInserter(pool *pgxpool.Pool, logger *slog.Logger) (*Client, error) {
	return river.NewClient(riverpgxv5.New(pool), &river.Config{Logger: logger})
}
