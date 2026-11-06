package pgstore_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/1xxn/getafjob/internal/application"
	"github.com/1xxn/getafjob/internal/application/adapters/pgstore"
	"github.com/1xxn/getafjob/internal/platform/db"
	"github.com/1xxn/getafjob/internal/platform/db/dbtest"
)

func user(t *testing.T, pool *pgxpool.Pool, email string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := db.InSystemTx(t.Context(), pool, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), "INSERT INTO users (email) VALUES ($1) RETURNING id", email).Scan(&id)
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

// job inserts a job directly; jobs sharing a match key are the same job from different sources.
func job(t *testing.T, pool *pgxpool.Pool, source, company, matchKey string) int64 {
	t.Helper()
	var id int64
	now := time.Now()
	if err := db.InSystemTx(t.Context(), pool, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `INSERT INTO jobs (fingerprint, match_key, source, external_id, company, title, url,
			content_hash, first_seen, last_seen) VALUES ($1, $2, $3, $1, $4, 'Go Developer', 'https://jobs.example.org/x', 'h', $5, $5)
			RETURNING id`, uuid.NewString(), matchKey, source, company, now).Scan(&id)
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

func setup(t *testing.T) (*pgxpool.Pool, *pgstore.Store, *application.Service, *time.Time) {
	pool := dbtest.AppPool(t)
	st := pgstore.New(pool)
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	svc := application.NewService(st).WithClock(func() time.Time { return now })
	return pool, st, svc, &now
}

func running(ctx context.Context, t *testing.T, svc *application.Service, u uuid.UUID, jobID int64) uuid.UUID {
	t.Helper()
	id, err := svc.Create(ctx, u, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Fire(ctx, u, id, application.EvQueue, application.ActorWorker, nil); err != nil {
		t.Fatal(err)
	}
	if err := svc.Start(ctx, u, id, application.ActorRunner); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestSubmitRegister(t *testing.T) {
	pool, st, svc, _ := setup(t)
	ctx := t.Context()
	max, erika := user(t, pool, "max@example.org"), user(t, pool, "erika@example.org")
	id := running(ctx, t, svc, max, job(t, pool, "greenhouse:acme", "Acme GmbH", "acme|go"))

	// Two runners race to submit: exactly one gets through.
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func() { defer wg.Done(); errs[i] = svc.BeginSubmit(ctx, max, id) }()
	}
	wg.Wait()
	if (errs[0] == nil) == (errs[1] == nil) {
		t.Fatalf("both or neither submitted: %v", errs)
	}
	if err := svc.BeginSubmit(ctx, max, id); !errors.Is(err, application.ErrTransition) {
		t.Fatalf("third submit: %v", err)
	}
	if s, err := svc.Fire(ctx, max, id, application.EvSubmitted, application.ActorRunner, map[string]any{"effect": "navigated"}); err != nil || s != application.Submitted {
		t.Fatalf("submitted: %s, %v", s, err)
	}
	if _, err := st.Get(ctx, erika, id); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("erika sees max's application: %v", err)
	}
	var outcome string
	_ = db.InUserTx(ctx, pool, max, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT outcome FROM submit_attempts WHERE application_id = $1", id).Scan(&outcome)
	})
	if outcome != "submitted" {
		t.Fatalf("outcome = %q", outcome)
	}
	if err := db.InUserTx(ctx, pool, max, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, "DELETE FROM submit_attempts WHERE application_id = $1", id)
		if err == nil && tag.RowsAffected() != 0 {
			return errors.New("submit attempt deleted")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestParkedAfterSubmitIsNeverSentAgain(t *testing.T) {
	pool, _, svc, _ := setup(t)
	ctx := t.Context()
	max := user(t, pool, "max@example.org")
	id := running(ctx, t, svc, max, job(t, pool, "lever:acme", "Acme GmbH", "acme|sre"))
	if err := svc.BeginSubmit(ctx, max, id); err != nil {
		t.Fatal(err)
	}
	// No effect after the retry: parked; the user requeues it; the register still blocks the submit.
	for _, ev := range []application.Event{application.EvPark, application.EvRequeue} {
		if _, err := svc.Fire(ctx, max, id, ev, application.ActorUser, nil); err != nil {
			t.Fatalf("%s: %v", ev, err)
		}
	}
	if err := svc.Start(ctx, max, id, application.ActorRunner); err != nil {
		t.Fatal(err)
	}
	if err := svc.BeginSubmit(ctx, max, id); !errors.Is(err, application.ErrAlreadySubmitted) {
		t.Fatalf("second submit: %v", err)
	}
}

func TestCrashWhileSubmittingBecomesUnknown(t *testing.T) {
	pool, st, svc, now := setup(t)
	ctx := t.Context()
	max := user(t, pool, "max@example.org")
	id := running(ctx, t, svc, max, job(t, pool, "ashby:acme", "Acme GmbH", "acme|platform"))
	if err := svc.BeginSubmit(ctx, max, id); err != nil {
		t.Fatal(err)
	}
	if n, err := svc.RecoverCrashed(ctx); err != nil || n != 0 {
		t.Fatalf("fresh submit recovered: %d, %v", n, err)
	}
	*now = now.Add(application.StuckAfter + time.Minute)
	if n, err := svc.RecoverCrashed(ctx); err != nil || n != 1 {
		t.Fatalf("recovered %d, %v", n, err)
	}
	if a, _ := st.Get(ctx, max, id); a.State != application.SubmitUnknown {
		t.Fatalf("state = %s", a.State)
	}
	for _, ev := range []application.Event{application.EvRequeue, application.EvStart, application.EvBeginSubmit} {
		if _, err := svc.Fire(ctx, max, id, ev, application.ActorUser, nil); !errors.Is(err, application.ErrTransition) {
			t.Fatalf("%s on submit_unknown: %v", ev, err)
		}
	}
	if s, err := svc.Fire(ctx, max, id, application.EvConfirm, application.ActorWorker, map[string]any{"via": "inbox"}); err != nil || s != application.Confirmed {
		t.Fatalf("reconciled: %s, %v", s, err)
	}
}

func TestGuardsBeforeStart(t *testing.T) {
	pool, st, svc, _ := setup(t)
	ctx := t.Context()
	max := user(t, pool, "max@example.org")
	start := func(jobID int64) (uuid.UUID, error) {
		id, err := svc.Create(ctx, max, jobID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Fire(ctx, max, id, application.EvQueue, application.ActorWorker, nil); err != nil {
			t.Fatal(err)
		}
		return id, svc.Start(ctx, max, id, application.ActorRunner)
	}
	submit := func(jobID int64) {
		id := running(ctx, t, svc, max, jobID)
		if err := svc.BeginSubmit(ctx, max, id); err != nil {
			t.Fatal(err)
		}
	}

	// The same job from a job board after the company board's application went out.
	submit(job(t, pool, "greenhouse:muster", "Muster AG", "muster|backend"))
	id, err := start(job(t, pool, "arbeitnow", "Muster AG", "muster|backend"))
	if !errors.Is(err, application.ErrDuplicate) {
		t.Fatalf("duplicate: %v", err)
	}
	if a, _ := st.Get(ctx, max, id); a.State != application.Parked {
		t.Fatalf("duplicate not parked: %s", a.State)
	}
	// A second job at the same company is fine, a third within 30 days is not.
	submit(job(t, pool, "greenhouse:muster", "Muster AG", "muster|frontend"))
	if _, err := start(job(t, pool, "greenhouse:muster", "MUSTER AG", "muster|data")); !errors.Is(err, application.ErrCompanyWindow) {
		t.Fatalf("company window: %v", err)
	}
	// Matching filtered the job out.
	excluded := job(t, pool, "lever:beispiel", "Beispiel GmbH", "beispiel|sales")
	if err := db.InUserTx(ctx, pool, max, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO matches (user_id, job_id, score, decision, reasons, rules, computed_at)
			VALUES ($1, $2, 0, 'skip', '[{"code":"title.excluded","points":0,"hard":true}]', 1, now())`, max, excluded)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := start(excluded); !errors.Is(err, application.ErrExcluded) {
		t.Fatalf("excluded: %v", err)
	}
}

type notices struct{ refs []application.Ref }

func (n *notices) HandoffClosing(_ context.Context, r application.Ref, _ time.Time) error {
	n.refs = append(n.refs, r)
	return nil
}

func TestHandoffWindow(t *testing.T) {
	pool, st, svc, now := setup(t)
	ctx := t.Context()
	max := user(t, pool, "max@example.org")
	answered := running(ctx, t, svc, max, job(t, pool, "greenhouse:acme", "Acme GmbH", "acme|go"))
	waiting := running(ctx, t, svc, max, job(t, pool, "greenhouse:beispiel", "Beispiel AG", "beispiel|go"))
	for _, id := range []uuid.UUID{answered, waiting} {
		if _, err := svc.Fire(ctx, max, id, application.EvAsk, application.ActorRunner, map[string]any{"kind": "captcha"}); err != nil {
			t.Fatal(err)
		}
	}
	n := &notices{}
	if w, p, err := svc.WatchHandoffs(ctx, n); err != nil || w+p != 0 {
		t.Fatalf("fresh handoffs: %d, %d, %v", w, p, err)
	}
	if _, err := svc.Fire(ctx, max, answered, application.EvAnswer, application.ActorUser, nil); err != nil {
		t.Fatal(err)
	}

	*now = now.Add(application.HandoffWindow - application.HandoffNotice + time.Minute)
	for range 2 { // the notice goes out once
		if w, p, err := svc.WatchHandoffs(ctx, n); err != nil || p != 0 {
			t.Fatalf("notice: %d, %d, %v", w, p, err)
		}
	}
	if len(n.refs) != 1 || n.refs[0].ID != waiting {
		t.Fatalf("notices = %+v", n.refs)
	}

	*now = now.Add(application.HandoffNotice)
	if w, p, err := svc.WatchHandoffs(ctx, n); err != nil || p != 1 || w != 0 {
		t.Fatalf("expiry: %d, %d, %v", w, p, err)
	}
	if a, _ := st.Get(ctx, max, waiting); a.State != application.Parked {
		t.Fatalf("waiting = %s", a.State)
	}
	if a, _ := st.Get(ctx, max, answered); a.State != application.Running {
		t.Fatalf("answered = %s", a.State)
	}
}
