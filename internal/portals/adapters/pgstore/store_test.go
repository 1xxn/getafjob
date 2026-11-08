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
	"github.com/1xxn/getafjob/internal/portals"
	"github.com/1xxn/getafjob/internal/portals/adapters/pgstore"
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

func TestPgstorePortals(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping postgres integration test in short mode")
	}

	pool := dbtest.AppPool(t)
	store := pgstore.New(pool)
	ctx := context.Background()

	u1 := user(t, pool, "portal1@example.com")
	u2 := user(t, pool, "portal2@example.com")

	now := time.Now().Truncate(time.Microsecond)

	s1 := portals.Session{
		UserID:           u1,
		Portal:           portals.PortalLinkedIn,
		Status:           portals.StatusActive,
		EncryptedCookies: []byte("encrypted_li"),
		DailyLimit:       5,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	// 1. Save and Get
	if err := store.SaveSession(ctx, s1); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	got, err := store.GetSession(ctx, u1, portals.PortalLinkedIn)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.Portal != portals.PortalLinkedIn || string(got.EncryptedCookies) != "encrypted_li" {
		t.Fatalf("got unexpected session: %+v", got)
	}

	// 2. Tenant isolation (u2 cannot read u1's session)
	_, err = store.GetSession(ctx, u2, portals.PortalLinkedIn)
	if !errors.Is(err, portals.ErrSessionNotFound) {
		t.Fatalf("expected ErrSessionNotFound for u2, got %v", err)
	}

	// 3. Update status
	if err := store.UpdateStatus(ctx, u1, portals.PortalLinkedIn, portals.StatusExpired, now); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	got, _ = store.GetSession(ctx, u1, portals.PortalLinkedIn)
	if got.Status != portals.StatusExpired {
		t.Fatalf("status = %s, want expired", got.Status)
	}

	// 4. Touch last used
	used := now.Add(time.Hour)
	if err := store.TouchLastUsed(ctx, u1, portals.PortalLinkedIn, used); err != nil {
		t.Fatalf("TouchLastUsed: %v", err)
	}
	got, _ = store.GetSession(ctx, u1, portals.PortalLinkedIn)
	if got.LastUsedAt == nil || !got.LastUsedAt.Equal(used) {
		t.Fatalf("LastUsedAt = %v, want %v", got.LastUsedAt, used)
	}

	// 5. List sessions
	list, err := store.ListSessions(ctx, u1)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListSessions: len=%d, err=%v", len(list), err)
	}

	// 6. Delete
	if err := store.DeleteSession(ctx, u1, portals.PortalLinkedIn); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	_, err = store.GetSession(ctx, u1, portals.PortalLinkedIn)
	if !errors.Is(err, portals.ErrSessionNotFound) {
		t.Fatalf("expected ErrSessionNotFound after delete, got %v", err)
	}
}
