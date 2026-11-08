package pgstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/1xxn/getafjob/internal/notify"
	"github.com/1xxn/getafjob/internal/notify/adapters/pgstore"
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

func job(t *testing.T, pool *pgxpool.Pool, company, title string) int64 {
	t.Helper()
	var id int64
	now := time.Now()
	if err := db.InSystemTx(t.Context(), pool, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `INSERT INTO jobs (fingerprint, match_key, source, external_id, company, title, url,
			content_hash, first_seen, last_seen) VALUES ($1, $2, 'test', $1, $3, $4, 'https://example.org', 'hash', $5, $5)
			RETURNING id`, uuid.NewString(), company+"|"+title, company, title, now).Scan(&id)
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

func application(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID, jobID int64, state string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	now := time.Now()
	if err := db.InSystemTx(t.Context(), pool, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `INSERT INTO applications (user_id, job_id, state, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $4) RETURNING id`, userID, jobID, state, now).Scan(&id)
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

func submitAttempt(t *testing.T, pool *pgxpool.Pool, userID, appID uuid.UUID, startedAt time.Time) {
	t.Helper()
	if err := db.InSystemTx(t.Context(), pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(t.Context(), `INSERT INTO submit_attempts (application_id, user_id, started_at)
			VALUES ($1, $2, $3)`, appID, userID, startedAt)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func suggestion(t *testing.T, pool *pgxpool.Pool, userID, appID uuid.UUID, cat string, createdAt time.Time) {
	t.Helper()
	if err := db.InSystemTx(t.Context(), pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(t.Context(), `INSERT INTO tracking_suggestions
			(id, user_id, application_id, message_uid, message_from, message_subject, message_date, category, suggested_state, status, created_at, updated_at)
			VALUES ($1, $2, $3, 100, 'hr@example.org', 'Update', $4, $5, 'confirmed', 'pending', $4, $4)`,
			uuid.New(), userID, appID, createdAt, cat)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPgstoreNotify(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.AppPool(t)
	st := pgstore.New(pool)

	u1 := user(t, pool, "user1@example.org")
	u2 := user(t, pool, "user2@example.org")

	// 1. Get user email
	email, err := st.GetUserEmail(ctx, u1)
	if err != nil || email != "user1@example.org" {
		t.Fatalf("GetUserEmail = %s, err = %v", email, err)
	}

	// 2. Save and Get Preferences
	p1 := notify.Preferences{
		UserID:              u1,
		EmailEnabled:        true,
		EmailAddress:        "notifications@user1.example.org",
		WebPushEnabled:      true,
		NtfyEnabled:         true,
		NtfyURL:             "https://ntfy.sh",
		NtfyTopic:           "alerts-user1",
		NtfyToken:           "my-token",
		DailySummaryEnabled: true,
		DailySummaryTime:    "19:00",
		QuietHoursEnabled:   true,
		QuietHoursStart:     "23:00",
		QuietHoursEnd:       "07:00",
	}

	if err := st.SavePreferences(ctx, p1); err != nil {
		t.Fatalf("SavePreferences: %v", err)
	}

	got, err := st.GetPreferences(ctx, u1)
	if err != nil {
		t.Fatalf("GetPreferences: %v", err)
	}
	if got.EmailAddress != "notifications@user1.example.org" || got.NtfyTopic != "alerts-user1" || got.DailySummaryTime != "19:00" {
		t.Errorf("GetPreferences = %+v", got)
	}

	// 3. ListDailySummaryCandidates
	candidates, err := st.ListDailySummaryCandidates(ctx)
	if err != nil {
		t.Fatalf("ListDailySummaryCandidates: %v", err)
	}
	foundU1 := false
	for _, c := range candidates {
		if c.UserID == u1 {
			foundU1 = true
			break
		}
	}
	if !foundU1 {
		t.Errorf("expected u1 in candidates")
	}

	// 4. UpdateLastSummaryAt
	now := time.Now()
	if err := st.UpdateLastSummaryAt(ctx, u1, now); err != nil {
		t.Fatalf("UpdateLastSummaryAt: %v", err)
	}
	got2, _ := st.GetPreferences(ctx, u1)
	if got2.LastSummaryAt == nil {
		t.Errorf("expected LastSummaryAt updated")
	}

	// 5. Daily Stats
	j1 := job(t, pool, "Acme Inc", "Go Developer")
	app1 := application(t, pool, u1, j1, "needs_input")
	submitAttempt(t, pool, u1, app1, now.Add(-2*time.Hour))
	suggestion(t, pool, u1, app1, "invitation", now.Add(-1*time.Hour))

	stats, err := st.DailyStats(ctx, u1, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("DailyStats: %v", err)
	}
	if stats.SubmittedCount != 1 {
		t.Errorf("stats.SubmittedCount = %d, want 1", stats.SubmittedCount)
	}
	if stats.NeedsInputCount != 1 {
		t.Errorf("stats.NeedsInputCount = %d, want 1", stats.NeedsInputCount)
	}
	if stats.Invitations != 1 {
		t.Errorf("stats.Invitations = %d, want 1", stats.Invitations)
	}

	// 6. RLS: user2 must not see user1's preferences
	_, err = st.GetPreferences(ctx, u2)
	if err == nil {
		t.Errorf("expected error or not found for u2 querying u1 without preferences")
	}
}
