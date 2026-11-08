package pgstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/1xxn/getafjob/internal/ai"
	"github.com/1xxn/getafjob/internal/ai/adapters/pgstore"
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

func TestPgstoreAI(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping postgres integration test in short mode")
	}

	pool := dbtest.AppPool(t)
	store := pgstore.New(pool)
	ctx := context.Background()

	u1 := user(t, pool, "ai1@example.com")
	u2 := user(t, pool, "ai2@example.com")

	now := time.Now().Truncate(time.Microsecond)
	store.SetNow(func() time.Time { return now })

	// 1. Initial settings when none exist
	st, encKey, err := store.GetSettings(ctx, u1)
	if err != nil {
		t.Fatalf("GetSettings empty: %v", err)
	}
	if st.Enabled || len(encKey) > 0 {
		t.Errorf("expected default disabled, got %+v", st)
	}

	// 2. Save settings
	if err := store.SaveSettings(ctx, u1, true, []byte("encrypted_api_key")); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	st, encKey, err = store.GetSettings(ctx, u1)
	if err != nil {
		t.Fatalf("GetSettings after save: %v", err)
	}
	if !st.Enabled || string(encKey) != "encrypted_api_key" {
		t.Errorf("saved settings mismatch: enabled=%v, key=%s", st.Enabled, string(encKey))
	}

	// 3. Increment spent
	if err := store.IncrementSpent(ctx, u1, 45); err != nil {
		t.Fatalf("IncrementSpent: %v", err)
	}
	st, _, _ = store.GetSettings(ctx, u1)
	if st.SpentCents != 45 {
		t.Errorf("spent cents = %d, want 45", st.SpentCents)
	}

	// 4. Record usage event
	if err := store.RecordUsage(ctx, u1, ai.TaskDraft, ai.ModelClaude35Haiku, 250, 40, 2); err != nil {
		t.Fatalf("RecordUsage: %v", err)
	}

	// 5. Tenant isolation: u2 cannot see u1 settings
	st2, encKey2, err := store.GetSettings(ctx, u2)
	if err != nil {
		t.Fatalf("GetSettings u2: %v", err)
	}
	if st2.Enabled || len(encKey2) > 0 {
		t.Errorf("tenant leak: u2 saw u1 data: %+v", st2)
	}
}
