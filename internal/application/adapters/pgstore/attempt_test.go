package pgstore_test

import (
	"errors"
	"testing"

	"github.com/1xxn/getafjob/internal/application"
)

// A submit parked without effect keeps its register row: retry, approval and a new queue are refused, marking it
// as applied by hand still works (#336).
func TestNoRequeueAfterSubmitAttempt(t *testing.T) {
	pool, st, svc, _ := setup(t)
	ctx := t.Context()
	max := user(t, pool, "max@example.org")
	jobID := job(t, pool, "greenhouse:muster", "Muster AG", "muster|go")
	id := running(ctx, t, svc, max, jobID)
	if err := svc.BeginSubmit(ctx, max, id); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Fire(ctx, max, id, application.EvPark, application.ActorRunner,
		map[string]any{"reason": "submit had no effect after one retry"}); err != nil {
		t.Fatal(err)
	}
	if a, _ := st.Get(ctx, max, id); a.State != application.Parked || !a.Attempted {
		t.Fatalf("app = %s attempted=%v, want parked with attempt", a.State, a.Attempted)
	}
	if _, err := svc.Retry(ctx, max, id); !errors.Is(err, application.ErrAlreadySubmitted) {
		t.Fatalf("retry = %v, want ErrAlreadySubmitted", err)
	}
	if _, err := svc.Approve(ctx, max, id); !errors.Is(err, application.ErrAlreadySubmitted) {
		t.Fatalf("approve = %v, want ErrAlreadySubmitted", err)
	}
	if _, _, err := svc.Queue(ctx, max, jobID); !errors.Is(err, application.ErrAlreadySubmitted) {
		t.Fatalf("queue = %v, want ErrAlreadySubmitted", err)
	}
	if err := svc.CheckGuard(ctx, max, jobID); !errors.Is(err, application.ErrAlreadySubmitted) {
		t.Fatalf("guard = %v, want ErrAlreadySubmitted", err)
	}
	if _, err := svc.ApplyExternal(ctx, max, jobID); err != nil {
		t.Fatal(err)
	}
	if a, _ := st.Get(ctx, max, id); a.State != application.Submitted {
		t.Fatalf("state = %s, want submitted", a.State)
	}
}
