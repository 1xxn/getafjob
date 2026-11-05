package queue_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/riverqueue/river"

	"github.com/1xxn/getafjob/internal/platform/db/dbtest"
	"github.com/1xxn/getafjob/internal/platform/queue"
)

type pingArgs struct{ N int }

func (pingArgs) Kind() string { return "ping" }

type pingWorker struct {
	river.WorkerDefaults[pingArgs]
	done chan int
}

func (w *pingWorker) Work(_ context.Context, j *river.Job[pingArgs]) error {
	w.done <- j.Args.N
	return nil
}

// The application role inserts and works jobs on the tables migrate created as owner.
func TestWorkerRunsJobsAsAppRole(t *testing.T) {
	pool := dbtest.AppPool(t)
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	w := &pingWorker{done: make(chan int, 1)}
	workers := river.NewWorkers()
	river.AddWorker(workers, w)
	client, err := queue.NewWorker(pool, queue.Options{Workers: workers, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Stop(context.WithoutCancel(ctx)) }()

	ins, err := queue.NewInserter(pool, logger)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ins.Insert(ctx, pingArgs{N: 42}, &river.InsertOpts{Queue: queue.Mail}); err != nil {
		t.Fatal(err)
	}
	select {
	case n := <-w.done:
		if n != 42 {
			t.Fatalf("got %d", n)
		}
	case <-ctx.Done():
		t.Fatal("job did not run")
	}
}
