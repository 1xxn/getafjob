package pgstore_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/1xxn/getafjob/internal/application"
	"github.com/1xxn/getafjob/internal/platform/db"
)

func jobAt(t *testing.T, pool *pgxpool.Pool, url, company, key string) int64 {
	t.Helper()
	var id int64
	now := time.Now()
	if err := db.InSystemTx(t.Context(), pool, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `INSERT INTO jobs (fingerprint, match_key, source, external_id, company, title, url,
			content_hash, first_seen, last_seen) VALUES ($1, $2, 'greenhouse:x', $1, $3, 'Go Developer', $4, 'h', $5, $5)
			RETURNING id`, uuid.NewString(), key, company, url, now).Scan(&id)
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

func queued(t *testing.T, svc *application.Service, u uuid.UUID, jobID int64) uuid.UUID {
	t.Helper()
	id, err := svc.Create(t.Context(), u, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Fire(t.Context(), u, id, application.EvQueue, application.ActorWorker, nil); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestLeaseHonorsHostLimits(t *testing.T) {
	pool, st, svc, now := setup(t)
	ctx := t.Context()
	runs := application.NewRuns(svc, st)
	max := user(t, pool, "max@example.org")
	if _, ok, err := runs.Lease(ctx, "r1", application.RunnerServer, nil); ok || err != nil {
		t.Fatalf("empty queue: %v, %v", ok, err)
	}
	a1 := queued(t, svc, max, jobAt(t, pool, "https://boards.greenhouse.io/acme/jobs/1", "Acme", "acme|1"))
	*now = now.Add(time.Second)
	a2 := queued(t, svc, max, jobAt(t, pool, "https://BOARDS.greenhouse.io/beispiel/jobs/2", "Beispiel", "beispiel|2"))
	*now = now.Add(time.Second)
	a3 := queued(t, svc, max, jobAt(t, pool, "https://jobs.lever.co/muster/3", "Muster", "muster|3"))

	l1, ok, err := runs.Lease(ctx, "r1", application.RunnerServer, nil)
	if err != nil || !ok || l1.AppID != a1 || l1.Host != "boards.greenhouse.io" || len(l1.Token) < 40 {
		t.Fatalf("first lease: %+v, %v, %v", l1, ok, err)
	}
	l2, ok, _ := runs.Lease(ctx, "r2", application.RunnerServer, nil)
	if !ok || l2.AppID != a3 {
		t.Fatalf("second lease skips the busy host: %+v", l2)
	}
	if _, ok, _ := runs.Lease(ctx, "r3", application.RunnerServer, nil); ok {
		t.Fatal("third lease on a busy host")
	}
	ref, err := runs.Authorize(ctx, l1.Token)
	if err != nil || ref.AppID != a1 {
		t.Fatalf("authorize: %+v, %v", ref, err)
	}
	if err := runs.Park(ctx, ref, "test", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := runs.Authorize(ctx, l1.Token); !errors.Is(err, application.ErrNoRun) {
		t.Fatalf("finished run still authorized: %v", err)
	}
	if _, ok, _ := runs.Lease(ctx, "r3", application.RunnerServer, nil); ok {
		t.Fatal("lease within the host gap")
	}
	*now = now.Add(application.HostGap + time.Second)
	if l, ok, _ := runs.Lease(ctx, "r3", application.RunnerServer, nil); !ok || l.AppID != a2 {
		t.Fatalf("after the gap: %+v, %v", l, ok)
	}
	*now = now.Add(application.RunTTL)
	if _, err := runs.Authorize(ctx, l2.Token); !errors.Is(err, application.ErrNoRun) {
		t.Fatalf("expired run authorized: %v", err)
	}
	for _, bad := range []string{"", "short", l1.Token + "x"} {
		if _, err := runs.Authorize(ctx, bad); !errors.Is(err, application.ErrNoRun) {
			t.Errorf("token %q: %v", bad, err)
		}
	}
}

func TestConcurrentLeasesRespectTheHost(t *testing.T) {
	pool, st, svc, _ := setup(t)
	ctx := t.Context()
	runs := application.NewRuns(svc, st)
	max := user(t, pool, "max@example.org")
	for i := range 4 {
		queued(t, svc, max, jobAt(t, pool, "https://jobs.ashbyhq.com/acme/"+string(rune('a'+i)), "Acme", "acme|"+string(rune('a'+i))))
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	n := 0
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, ok, err := runs.Lease(ctx, "r", application.RunnerServer, nil); ok && err == nil {
				mu.Lock()
				n++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if n != 1 {
		t.Fatalf("%d leases on one host", n)
	}
}

func TestRunFlowAndGuardAtLease(t *testing.T) {
	pool, st, svc, _ := setup(t)
	ctx := t.Context()
	runs := application.NewRuns(svc, st)
	max := user(t, pool, "max@example.org")
	excluded := queued(t, svc, max, jobAt(t, pool, "https://a.example.org/1", "Beispiel", "beispiel|sales"))
	if err := db.InUserTx(ctx, pool, max, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO matches (user_id, job_id, score, decision, reasons, rules, computed_at)
			SELECT $1, job_id, 0, 'skip', '[{"code":"title.excluded","points":0,"hard":true}]', 1, now() FROM applications WHERE id = $2`, max, excluded)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	good := queued(t, svc, max, jobAt(t, pool, "https://b.example.org/2", "Muster", "muster|go"))
	l, ok, err := runs.Lease(ctx, "r1", application.RunnerServer, nil)
	if err != nil || !ok || l.AppID != good {
		t.Fatalf("lease after a failing guard: %+v, %v, %v", l, ok, err)
	}
	if a, _ := st.Get(ctx, max, excluded); a.State != application.Parked {
		t.Fatalf("excluded = %s", a.State)
	}
	ref, _ := runs.Authorize(ctx, l.Token)
	if err := runs.Checkpoint(ctx, ref, application.Checkpoint{Step: "extract", Page: 1, URL: "https://b.example.org/2",
		Data: []byte(`{"fields":[]}`), Screenshot: "runs/x/1.png"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Approve(ctx, max, good); err != nil {
		t.Fatal(err)
	}
	if err := runs.BeginSubmit(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if err := runs.Submitted(ctx, ref, true, map[string]any{"navigated": true}); err != nil {
		t.Fatal(err)
	}
	if a, _ := st.Get(ctx, max, good); a.State != application.Confirmed {
		t.Fatalf("state = %s", a.State)
	}
	var n int
	_ = db.InUserTx(ctx, pool, max, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT count(*) FROM run_checkpoints WHERE run_id = $1", ref.RunID).Scan(&n)
	})
	if n != 1 {
		t.Fatalf("checkpoints = %d", n)
	}
}
