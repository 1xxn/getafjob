package pgstore_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/application"
)

// The queue lists runs in the order they were queued; moving swaps neighbours, a paused run is skipped by the lease
// and a run queued again goes to the end.
func TestQueueOrderAndPause(t *testing.T) {
	pool, st, svc, _ := setup(t)
	ctx := t.Context()
	runs := application.NewRuns(svc, st)
	max := user(t, pool, "max@example.org")
	a1 := queued(t, svc, max, jobAt(t, pool, "https://jobs.lever.co/acme/1", "Acme", "acme|1"))
	a2 := queued(t, svc, max, jobAt(t, pool, "https://jobs.lever.co/beispiel/2", "Beispiel", "beispiel|2"))
	a3 := queued(t, svc, max, jobAt(t, pool, "https://jobs.lever.co/muster/3", "Muster", "muster|3"))

	order := func(want ...uuid.UUID) {
		t.Helper()
		items, err := svc.QueueItems(ctx, max)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != len(want) {
			t.Fatalf("queue has %d items, want %d", len(items), len(want))
		}
		for i, it := range items {
			if it.ID != want[i] {
				t.Fatalf("position %d: %s %s, want %s", i, it.ID, it.Company, want[i])
			}
		}
	}
	order(a1, a2, a3)

	if moved, err := svc.MoveQueued(ctx, max, a3, true); err != nil || !moved {
		t.Fatalf("move up: %v %v", moved, err)
	}
	order(a1, a3, a2)
	if moved, _ := svc.MoveQueued(ctx, max, a1, true); moved {
		t.Error("the first moved further up")
	}
	if moved, _ := svc.MoveQueued(ctx, max, a1, false); !moved {
		t.Error("move down")
	}
	order(a3, a1, a2)

	// Another user cannot touch the queue.
	other := user(t, pool, "erika@example.org")
	if moved, _ := svc.MoveQueued(ctx, other, a1, true); moved {
		t.Error("another user moved the run")
	}
	if paused, _ := svc.PauseQueued(ctx, other, a3, true); paused {
		t.Error("another user paused the run")
	}

	if paused, err := svc.PauseQueued(ctx, max, a3, true); err != nil || !paused {
		t.Fatalf("pause: %v %v", paused, err)
	}
	items, _ := svc.QueueItems(ctx, max)
	if !items[0].Paused {
		t.Error("pause not shown")
	}
	l, ok, err := runs.Lease(ctx, "r1", application.RunnerServer, nil)
	if err != nil || !ok || l.AppID != a1 {
		t.Fatalf("lease takes the first run that is not paused: %+v %v %v", l, ok, err)
	}

	// Parked and queued again: the end of the queue, no longer paused.
	if _, err := svc.Fire(ctx, max, a3, application.EvPark, application.ActorUser, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Fire(ctx, max, a3, application.EvRequeue, application.ActorUser, nil); err != nil {
		t.Fatal(err)
	}
	items, _ = svc.QueueItems(ctx, max)
	if len(items) != 2 || items[0].ID != a2 || items[1].ID != a3 || items[1].Paused {
		t.Fatalf("queued again: %+v", items)
	}
}
