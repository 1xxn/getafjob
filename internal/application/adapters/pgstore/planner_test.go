package pgstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/1xxn/getafjob/internal/application"
	"github.com/1xxn/getafjob/internal/platform/db"
)

func TestPgstorePlannerAndPacing(t *testing.T) {
	ctx := context.Background()
	pool, st, svc, now := setup(t)

	u1 := user(t, pool, "planner1@example.org")
	u2 := user(t, pool, "planner2@example.org")
	u3 := user(t, pool, "planner3@example.org")

	// u1: default settings (approve, daily_limit=5) -> eligible for PlanUsers
	// u2: suggest level -> not eligible
	if err := svc.SaveSettings(ctx, u2, application.Settings{
		Level:      application.LevelSuggest,
		MinScore:   80,
		DailyLimit: 5,
	}); err != nil {
		t.Fatalf("SaveSettings(u2): %v", err)
	}

	// u3: daily_limit = 0 -> not eligible
	if err := svc.SaveSettings(ctx, u3, application.Settings{
		Level:      application.LevelAutonomous,
		MinScore:   80,
		DailyLimit: 0,
	}); err != nil {
		t.Fatalf("SaveSettings(u3): %v", err)
	}

	// 1. PlanUsers
	users, err := st.PlanUsers(ctx)
	if err != nil {
		t.Fatalf("PlanUsers() err = %v", err)
	}
	hasU1, hasU2, hasU3 := false, false, false
	for _, id := range users {
		if id == u1 {
			hasU1 = true
		}
		if id == u2 {
			hasU2 = true
		}
		if id == u3 {
			hasU3 = true
		}
	}
	if !hasU1 {
		t.Errorf("PlanUsers missing u1 (approve, limit 5)")
	}
	if hasU2 {
		t.Errorf("PlanUsers should not include u2 (suggest)")
	}
	if hasU3 {
		t.Errorf("PlanUsers should not include u3 (daily_limit 0)")
	}

	// 2. ActiveApplicationsCount
	j1 := jobAt(t, pool, "https://boards.greenhouse.io/corp/1", "Corp", "corp|1")
	j2 := jobAt(t, pool, "https://boards.greenhouse.io/corp/2", "Corp", "corp|2")
	j3 := jobAt(t, pool, "https://boards.greenhouse.io/corp/3", "Corp", "corp|3")

	app1 := queued(t, svc, u1, j1)
	queued(t, svc, u1, j2)
	_ = j3

	count, err := st.ActiveApplicationsCount(ctx, u1)
	if err != nil {
		t.Fatalf("ActiveApplicationsCount() err = %v", err)
	}
	if count != 2 {
		t.Errorf("ActiveApplicationsCount = %d, want 2", count)
	}

	// 3. UserLastPacedAt
	lastPaced, err := st.UserLastPacedAt(ctx, u1)
	if err != nil {
		t.Fatalf("UserLastPacedAt() err = %v", err)
	}
	if lastPaced.IsZero() {
		t.Errorf("UserLastPacedAt should not be zero after queueing applications")
	}

	// 4. PlannerCandidates
	// Insert match rows for u1
	if err := db.InSystemTx(ctx, pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO matches (user_id, job_id, score, decision, rules, computed_at)
			VALUES ($1, $2, 95, 'match', 0, $4),
			       ($1, $3, 70, 'match', 0, $4)
		`, u1, j3, j2, *now)
		return err
	}); err != nil {
		t.Fatalf("insert matches: %v", err)
	}

	// j2 already has an application in queued, so only j3 should be returned as candidate
	cands, err := st.PlannerCandidates(ctx, u1, 80)
	if err != nil {
		t.Fatalf("PlannerCandidates() err = %v", err)
	}
	if len(cands) != 1 || cands[0].JobID != j3 || cands[0].Score != 95 {
		t.Errorf("PlannerCandidates() = %+v, want only j3 (score 95)", cands)
	}

	// 5. LeaseCandidate enforces daily quota
	runs := application.NewRuns(svc, st)
	// u1 currently has 2 queued applications (j1, j2) and 0 submits
	// Lease j1
	l1, ok, err := runs.Lease(ctx, "r1")
	if err != nil || !ok || l1.AppID != app1 {
		t.Fatalf("Lease(r1) = %+v, ok=%v, err=%v", l1, ok, err)
	}
	ref1, err := runs.Authorize(ctx, l1.Token)
	if err != nil {
		t.Fatalf("Authorize(l1): %v", err)
	}
	if err := runs.Park(ctx, ref1, "done", ""); err != nil {
		t.Fatalf("Park(l1): %v", err)
	}

	// Fake submit attempt so u1 has a submit recorded
	if err := db.InSystemTx(ctx, pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO submit_attempts (application_id, user_id, started_at, outcome) VALUES ($1, $2, $3, 'submitted')`, app1, u1, *now)
		return err
	}); err != nil {
		t.Fatalf("insert submit_attempt: %v", err)
	}

	submits, err := st.SubmitsSince(ctx, u1, now.Add(-24*time.Hour))
	if err != nil || submits != 1 {
		t.Fatalf("SubmitsSince() = %d, err = %v, want 1", submits, err)
	}

	// If we set daily_limit = 1, then with 1 submit, the user's daily quota is exhausted!
	if err := svc.SaveSettings(ctx, u1, application.Settings{
		Level:      application.LevelApprove,
		MinScore:   80,
		DailyLimit: 1,
	}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	// Move now past HostGap so host is not busy
	*now = now.Add(application.HostGap + time.Minute)

	// Lease should now find nothing because u1's daily quota of 1 is reached!
	if _, ok, err := runs.Lease(ctx, "r2"); ok || err != nil {
		t.Fatalf("Lease when quota is exhausted: ok = %v, err = %v, want ok=false", ok, err)
	}

	// If we raise daily_limit to 5, quota is free again, and lease succeeds!
	if err := svc.SaveSettings(ctx, u1, application.Settings{
		Level:      application.LevelApprove,
		MinScore:   80,
		DailyLimit: 5,
	}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	if l2, ok, err := runs.Lease(ctx, "r2"); !ok || err != nil {
		t.Fatalf("Lease with daily_limit=5: ok=%v, err=%v", ok, err)
	} else if l2.JobID != j2 {
		t.Errorf("Lease got job %d, want j2 (%d)", l2.JobID, j2)
	}
}
