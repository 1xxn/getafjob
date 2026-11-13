package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/1xxn/getafjob/internal/application"
	"github.com/1xxn/getafjob/internal/platform/config"
	"github.com/1xxn/getafjob/internal/platform/db"
	"github.com/1xxn/getafjob/internal/platform/queue"
	"github.com/1xxn/getafjob/internal/tracking"
)

type appTransitionAdapter struct {
	apps *application.Service
}

// Allowed reports whether a suggestion's state is reachable from the application's state.
func (a *appTransitionAdapter) Allowed(from, to string) bool {
	ev, ok := suggestionEvent(to)
	if !ok {
		return false
	}
	_, err := application.Next(application.State(from), ev)
	return err == nil
}

func suggestionEvent(state string) (application.Event, bool) {
	switch application.State(state) {
	case application.Confirmed:
		return application.EvConfirm, true
	case application.Rejected:
		return application.EvReject, true
	case application.Interview:
		return application.EvInterview, true
	default:
		return "", false
	}
}

func (a *appTransitionAdapter) Transition(ctx context.Context, userID, appID uuid.UUID, state string, detail map[string]any) error {
	ev, ok := suggestionEvent(state)
	if !ok {
		return fmt.Errorf("unsupported suggestion state transition: %s", state)
	}

	_, err := a.apps.Fire(ctx, userID, appID, ev, application.ActorUser, detail)
	return err
}

// newAppTransitionAdapter adapts application.Service to tracking.ApplicationUpdater.
func newAppTransitionAdapter(apps *application.Service) tracking.ApplicationUpdater {
	return &appTransitionAdapter{apps: apps}
}

// InboxArgs triggers an inbox check for a single user or all enabled mailboxes.
type InboxArgs struct {
	UserID uuid.UUID `json:"user_id,omitzero"`
}

// Kind implements river.JobArgs.
func (InboxArgs) Kind() string { return "inbox" }

// InsertOpts: at most one inbox check runs or waits.
func (a InboxArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		MaxAttempts: 3,
		UniqueOpts: river.UniqueOpts{
			ByArgs: true,
			ByState: []rivertype.JobState{
				rivertype.JobStateAvailable,
				rivertype.JobStatePending,
				rivertype.JobStateRunning,
				rivertype.JobStateRetryable,
				rivertype.JobStateScheduled,
			},
		},
	}
}

type inboxWorker struct {
	river.WorkerDefaults[InboxArgs]
	svc    *tracking.Service
	logger *slog.Logger
}

// Timeout bounds an inbox checking run.
func (w *inboxWorker) Timeout(*river.Job[InboxArgs]) time.Duration { return 15 * time.Minute }

func (w *inboxWorker) Work(ctx context.Context, job *river.Job[InboxArgs]) error {
	if job.Args.UserID != uuid.Nil {
		sugs, err := w.svc.CheckMailbox(ctx, job.Args.UserID)
		if err != nil {
			return err
		}
		w.logger.InfoContext(ctx, "inbox checked for user", "user_id", job.Args.UserID, "suggestions", len(sugs))
		return nil
	}

	n, err := w.svc.CheckAllMailboxes(ctx)
	if err != nil {
		return err
	}
	w.logger.InfoContext(ctx, "inbox check completed for all mailboxes", "checked", n)
	return nil
}

// adminInbox queues an inbox check run now; the worker runs it.
func adminInbox(ctx context.Context, args []string, env Env) int {
	if len(args) != 0 {
		_, _ = fmt.Fprintln(env.Stderr, "usage: getafjob admin inbox")
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
	if _, err := ins.Insert(ctx, InboxArgs{}, nil); err != nil {
		_, _ = fmt.Fprintln(env.Stderr, err)
		return ExitError
	}
	_, _ = fmt.Fprintln(env.Stdout, "inbox check queued")
	return ExitOK
}
