package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/riverqueue/river"

	"github.com/1xxn/getafjob/internal/application"
)

// RecoverArgs turns applications stuck in submitting into submit_unknown.
type RecoverArgs struct{}

// Kind implements river.JobArgs.
func (RecoverArgs) Kind() string { return "application_recover" }

type recoverWorker struct {
	river.WorkerDefaults[RecoverArgs]
	svc    *application.Service
	logger *slog.Logger
}

func (w *recoverWorker) Work(ctx context.Context, _ *river.Job[RecoverArgs]) error {
	n, err := w.svc.RecoverCrashed(ctx)
	if n > 0 {
		w.logger.WarnContext(ctx, "applications stuck in submitting marked submit_unknown", "count", n)
	}
	return err
}

// HandoffArgs warns before handoff windows close and parks applications whose window closed.
type HandoffArgs struct{}

// Kind implements river.JobArgs.
func (HandoffArgs) Kind() string { return "application_handoffs" }

type handoffWorker struct {
	river.WorkerDefaults[HandoffArgs]
	svc      *application.Service
	notifier application.Notifier
	logger   *slog.Logger
}

func (w *handoffWorker) Work(ctx context.Context, _ *river.Job[HandoffArgs]) error {
	warned, parked, err := w.svc.WatchHandoffs(ctx, w.notifier)
	if warned+parked > 0 {
		w.logger.InfoContext(ctx, "handoff windows", "warned", warned, "parked", parked)
	}
	return err
}

// logNotifier records handoff notices in the log until Web Push and mail deliver them (#31); the event row
// already shows them in the UI.
type logNotifier struct{ logger *slog.Logger }

func (n logNotifier) HandoffClosing(ctx context.Context, r application.Ref, until time.Time) error {
	n.logger.InfoContext(ctx, "handoff window closing", "application", r.ID, "until", until)
	return nil
}
