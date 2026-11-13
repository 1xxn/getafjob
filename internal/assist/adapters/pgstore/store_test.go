package pgstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/1xxn/getafjob/internal/assist"
	"github.com/1xxn/getafjob/internal/assist/adapters/pgstore"
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

func TestPgstoreAssist(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping postgres integration test in short mode")
	}

	pool := dbtest.AppPool(t)
	store := pgstore.New(pool)
	ctx := context.Background()

	u1 := user(t, pool, "u1@example.com")
	u2 := user(t, pool, "u2@example.com")

	now := time.Now().Truncate(time.Microsecond)

	// 1. Create and retrieve pairing code
	code, codeHash, err := assist.GenerateCode()
	if err != nil {
		t.Fatalf("GenerateCode() err = %v", err)
	}

	p := assist.PairingCode{
		ID:        uuid.New(),
		UserID:    u1,
		CodeHash:  codeHash,
		CreatedAt: now,
		ExpiresAt: now.Add(10 * time.Minute),
	}

	if err := store.CreatePairingCode(ctx, p); err != nil {
		t.Fatalf("CreatePairingCode() err = %v", err)
	}

	gotPairing, err := store.ConsumePairingCode(ctx, assist.HashCode(code), now)
	if err != nil {
		t.Fatalf("ConsumePairingCode() err = %v", err)
	}
	if gotPairing.ID != p.ID || gotPairing.UserID != u1 {
		t.Fatalf("unexpected pairing code returned: %+v", gotPairing)
	}
	// A code works once.
	if _, err := store.ConsumePairingCode(ctx, assist.HashCode(code), now); !errors.Is(err, assist.ErrNotFound) {
		t.Fatalf("second redemption: got %v, want ErrNotFound", err)
	}

	// 2. Create and retrieve extension token
	tok, err := assist.NewToken(u1, "Chrome 130", now, 24*time.Hour)
	if err != nil {
		t.Fatalf("NewToken() err = %v", err)
	}

	if err := store.CreateExtensionToken(ctx, tok); err != nil {
		t.Fatalf("CreateExtensionToken() err = %v", err)
	}

	gotTok, err := store.GetExtensionToken(ctx, assist.HashToken(tok.TokenPlain), now)
	if err != nil {
		t.Fatalf("GetExtensionToken() err = %v", err)
	}
	if gotTok.ID != tok.ID || gotTok.UserID != u1 || gotTok.ClientName != "Chrome 130" {
		t.Fatalf("unexpected token returned: %+v", gotTok)
	}

	// Touch token
	touchAt := now.Add(5 * time.Minute)
	if err := store.TouchExtensionToken(ctx, assist.HashToken(tok.TokenPlain), touchAt); err != nil {
		t.Fatalf("TouchExtensionToken() err = %v", err)
	}

	// List extension tokens for u1
	list1, err := store.ListExtensionTokens(ctx, u1)
	if err != nil {
		t.Fatalf("ListExtensionTokens() err = %v", err)
	}
	if len(list1) != 1 || list1[0].ID != tok.ID {
		t.Fatalf("expected 1 token for u1, got %v", list1)
	}

	// Tenant isolation: u2 cannot see u1's tokens
	list2, err := store.ListExtensionTokens(ctx, u2)
	if err != nil {
		t.Fatalf("ListExtensionTokens(u2) err = %v", err)
	}
	if len(list2) != 0 {
		t.Fatalf("expected 0 tokens for u2, got %v", list2)
	}

	// Revoke token
	if err := store.RevokeExtensionToken(ctx, tok.ID, u1); err != nil {
		t.Fatalf("RevokeExtensionToken() err = %v", err)
	}
	if _, err := store.GetExtensionToken(ctx, assist.HashToken(tok.TokenPlain), now); !errors.Is(err, assist.ErrNotFound) {
		t.Fatalf("expected ErrNotFound after revoke, got %v", err)
	}
}
