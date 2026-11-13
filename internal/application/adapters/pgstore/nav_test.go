package pgstore_test

import (
	"testing"

	"github.com/1xxn/getafjob/internal/application"
)

// The navigation counts a run parked to wait for answers as a question, like the questions page lists it.
func TestNavCountsWaitingAnswers(t *testing.T) {
	pool, st, svc, _ := setup(t)
	ctx := t.Context()
	runs := application.NewRuns(svc, st)
	max := user(t, pool, "max@example.org")
	id := queued(t, svc, max, jobAt(t, pool, "https://a.example.org/1", "Muster", "muster|go"))
	l, ok, err := runs.Lease(ctx, "r1", application.RunnerServer, nil)
	if err != nil || !ok {
		t.Fatalf("lease: %v, %v", ok, err)
	}
	ref, _ := runs.Authorize(ctx, l.Token)
	if err := runs.Ask(ctx, ref, []application.Question{{Kind: "field", Label: "Salary"}}); err != nil {
		t.Fatal(err)
	}
	if err := runs.Park(ctx, ref, "waiting for your answers; the browser was closed to free the runner", ""); err != nil {
		t.Fatal(err)
	}
	if a, _ := st.Get(ctx, max, id); a.State != application.Parked {
		t.Fatalf("state = %s, want parked", a.State)
	}
	if n, err := svc.NavCounts(ctx, max); err != nil || n.NeedsInput != 1 {
		t.Fatalf("nav counts = %+v, %v; want one question", n, err)
	}
}
