package app

import (
	"context"
	"log/slog"

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
