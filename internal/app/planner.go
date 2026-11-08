package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/1xxn/getafjob/internal/application"
	"github.com/1xxn/getafjob/internal/platform/config"
	"github.com/1xxn/getafjob/internal/platform/db"
	"github.com/1xxn/getafjob/internal/platform/queue"
)

// PlanArgs runs the planner to queue matching applications according to user quotas and pacing.
type PlanArgs struct{}

// Kind implements river.JobArgs.
func (PlanArgs) Kind() string { return "application_plan" }

// InsertOpts: at most one planner run waits or runs.
func (PlanArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 3, UniqueOpts: river.UniqueOpts{ByArgs: true,
		ByState: []rivertype.JobState{rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateRunning,
			rivertype.JobStateRetryable, rivertype.JobStateScheduled}}}
}

type planWorker struct {
	river.WorkerDefaults[PlanArgs]
	svc    *application.Service
	logger *slog.Logger
}

func (w *planWorker) Timeout(*river.Job[PlanArgs]) time.Duration { return 15 * time.Minute }

func (w *planWorker) Work(ctx context.Context, _ *river.Job[PlanArgs]) error {
	st, err := w.svc.Plan(ctx)
	if err != nil {
		return err
	}
	if st.JobsPlanned > 0 {
		w.logger.InfoContext(ctx, "planner finished", "users_eligible", st.UsersEligible, "jobs_planned", st.JobsPlanned)
	}
	return nil
}

// adminPlan queues a plan run now; the worker runs it.
func adminPlan(ctx context.Context, args []string, env Env) int {
	if len(args) != 0 {
		_, _ = fmt.Fprintln(env.Stderr, "usage: getafjob admin plan")
		return ExitUsage
	}
	cfg, err := config.Load(env.Config)
	if err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "configuration error:\n%v\n", err)
		return ExitUsage
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL, cfg.DatabasePassword, "getafjob-admin")
	if err != nil {
		_, _ = fmt.Fprintln(env.Stderr, err)
		return ExitError
	}
	defer pool.Close()
	ins, err := queue.NewInserter(pool, nil)
	if err != nil {
		_, _ = fmt.Fprintln(env.Stderr, err)
		return ExitError
	}
	if _, err := ins.Insert(ctx, PlanArgs{}, nil); err != nil {
		_, _ = fmt.Fprintln(env.Stderr, err)
		return ExitError
	}
	_, _ = fmt.Fprintln(env.Stdout, "planning queued")
	return ExitOK
}
