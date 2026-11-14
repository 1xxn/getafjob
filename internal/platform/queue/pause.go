package queue

import (
	"context"
	"errors"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// ErrPaused is why a job was canceled: the instance was in maintenance when it was due.
var ErrPaused = errors.New("canceled: the instance is in maintenance")

// pause is a worker middleware that cancels jobs while paused reports true; canceled jobs are not retried.
type pause struct {
	river.MiddlewareDefaults
	paused func(ctx context.Context) bool
}

var _ rivertype.WorkerMiddleware = (*pause)(nil)

// Work implements rivertype.WorkerMiddleware.
func (p *pause) Work(ctx context.Context, _ *rivertype.JobRow, doInner func(context.Context) error) error {
	if p.paused(ctx) {
		return river.JobCancel(ErrPaused)
	}
	return doInner(ctx)
}
