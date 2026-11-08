package pgstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/1xxn/getafjob/internal/platform/db"
	"github.com/1xxn/getafjob/internal/platform/db/dbtest"
	"github.com/1xxn/getafjob/internal/tracking"
	"github.com/1xxn/getafjob/internal/tracking/adapters/pgstore"
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

func job(t *testing.T, pool *pgxpool.Pool, company, title, url string) int64 {
	t.Helper()
	var id int64
	now := time.Now()
	if err := db.InSystemTx(t.Context(), pool, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `INSERT INTO jobs (fingerprint, match_key, source, external_id, company, title, url,
			content_hash, first_seen, last_seen) VALUES ($1, $2, 'test', $1, $3, $4, $5, 'hash', $6, $6)
			RETURNING id`, uuid.NewString(), company+"|"+title, company, title, url, now).Scan(&id)
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

func applicationForJob(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID, jobID int64, state string) uuid.UUID {
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

func TestPgstoreTracking(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.AppPool(t)
	st := pgstore.New(pool)

	u1 := user(t, pool, "tracking1@example.org")
	u2 := user(t, pool, "tracking2@example.org")

	// 1. Mailbox not found initially
	if _, err := st.GetMailbox(ctx, u1); !errors.Is(err, tracking.ErrNotFound) {
		t.Fatalf("GetMailbox before save: got %v, want ErrNotFound", err)
	}

	// 2. Save and Get Mailbox
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	acc1 := tracking.MailboxAccount{
		UserID:            u1,
		Host:              "imap.example.com",
		Port:              993,
		TLS:               true,
		Username:          "u1@example.com",
		EncryptedPassword: []byte("enc-pass-1"),
		Enabled:           true,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := st.SaveMailbox(ctx, acc1); err != nil {
		t.Fatalf("SaveMailbox: %v", err)
	}

	got1, err := st.GetMailbox(ctx, u1)
	if err != nil {
		t.Fatalf("GetMailbox(u1): %v", err)
	}
	if got1.Host != "imap.example.com" || got1.Username != "u1@example.com" || string(got1.EncryptedPassword) != "enc-pass-1" {
		t.Errorf("GetMailbox(u1) = %+v, want %+v", got1, acc1)
	}

	// 3. Update Cursor
	checkTime := now.Add(time.Hour)
	if err := st.UpdateMailboxCursor(ctx, u1, 150, 443322, checkTime, ""); err != nil {
		t.Fatalf("UpdateMailboxCursor: %v", err)
	}
	got1, err = st.GetMailbox(ctx, u1)
	if err != nil {
		t.Fatalf("GetMailbox after cursor: %v", err)
	}
	if got1.LastUID != 150 || got1.UIDValidity != 443322 || got1.LastCheckedAt == nil || !got1.LastCheckedAt.Equal(checkTime) {
		t.Errorf("cursor after update: lastUID=%d, val=%d, checkedAt=%v", got1.LastUID, got1.UIDValidity, got1.LastCheckedAt)
	}

	// 4. ListEnabledMailboxes (system query)
	mailboxes, err := st.ListEnabledMailboxes(ctx)
	if err != nil {
		t.Fatalf("ListEnabledMailboxes: %v", err)
	}
	foundU1 := false
	for _, m := range mailboxes {
		if m.UserID == u1 {
			foundU1 = true
			break
		}
	}
	if !foundU1 {
		t.Errorf("ListEnabledMailboxes missing u1")
	}

	// 5. Active Applications for Tracking
	j1 := job(t, pool, "Acme Inc", "Software Engineer", "https://jobs.acme.example.com/1")
	app1 := applicationForJob(t, pool, u1, j1, "submitted")

	apps, err := st.ListActiveApplications(ctx, u1)
	if err != nil {
		t.Fatalf("ListActiveApplications: %v", err)
	}
	if len(apps) != 1 || apps[0].ID != app1 || apps[0].Company != "Acme Inc" {
		t.Errorf("ListActiveApplications = %+v, want app1", apps)
	}

	// 6. Suggestions
	sugID := uuid.New()
	sug := tracking.StatusSuggestion{
		ID:             sugID,
		UserID:         u1,
		ApplicationID:  app1,
		MessageUID:     151,
		MessageID:      "msg-123@acme.example.com",
		MessageFrom:    "hr@acme.example.com",
		MessageSubject: "Invitation to interview",
		MessageDate:    now,
		Category:       tracking.CategoryInvitation,
		SuggestedState: tracking.StateInterview,
		Reasons:        []string{"Interview invitation keyword", "Company name matched"},
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := st.CreateSuggestion(ctx, sug); err != nil {
		t.Fatalf("CreateSuggestion: %v", err)
	}

	// Suggestions for user
	sugs, err := st.Suggestions(ctx, u1, tracking.SuggestionPending)
	if err != nil {
		t.Fatalf("Suggestions: %v", err)
	}
	if len(sugs) != 1 || sugs[0].ID != sugID || sugs[0].Company != "Acme Inc" || sugs[0].JobTitle != "Software Engineer" {
		t.Errorf("Suggestions = %+v", sugs)
	}

	// Suggestions for application
	appSugs, err := st.SuggestionsForApplication(ctx, u1, app1)
	if err != nil {
		t.Fatalf("SuggestionsForApplication: %v", err)
	}
	if len(appSugs) != 1 || appSugs[0].ID != sugID {
		t.Errorf("SuggestionsForApplication = %+v", appSugs)
	}

	// Single suggestion
	single, err := st.Suggestion(ctx, u1, sugID)
	if err != nil {
		t.Fatalf("Suggestion: %v", err)
	}
	if single.ID != sugID || single.Category != tracking.CategoryInvitation {
		t.Errorf("single suggestion = %+v", single)
	}

	// Update status
	if err := st.SetSuggestionStatus(ctx, u1, sugID, tracking.SuggestionAccepted); err != nil {
		t.Fatalf("SetSuggestionStatus: %v", err)
	}
	single, err = st.Suggestion(ctx, u1, sugID)
	if err != nil {
		t.Fatalf("Suggestion after accept: %v", err)
	}
	if single.Status != tracking.SuggestionAccepted {
		t.Errorf("single.Status = %v, want accepted", single.Status)
	}

	// 7. RLS Isolation: u2 cannot see u1's suggestions or mailbox
	if _, err := st.GetMailbox(ctx, u2); !errors.Is(err, tracking.ErrNotFound) {
		t.Errorf("u2 GetMailbox of u1: got %v, want ErrNotFound", err)
	}
	u2Sugs, err := st.Suggestions(ctx, u2, "")
	if err != nil {
		t.Fatalf("u2 Suggestions: %v", err)
	}
	if len(u2Sugs) != 0 {
		t.Errorf("u2 should see 0 suggestions, got %d", len(u2Sugs))
	}
}
