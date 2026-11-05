package db_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/1xxn/getafjob/internal/platform/db"
	"github.com/1xxn/getafjob/internal/platform/db/dbtest"
)

func createUser(t *testing.T, pool *pgxpool.Pool, email string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := db.InSystemTx(t.Context(), pool, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), "INSERT INTO users (email) VALUES ($1) RETURNING id", email).Scan(&id)
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return id
}

// TestTenantSeparation reads, changes and inserts another user's data as the application role; all of it must fail.
func TestTenantSeparation(t *testing.T) {
	pool := dbtest.AppPool(t)
	ctx := t.Context()
	alice := createUser(t, pool, "alice@example.com")
	bob := createUser(t, pool, "bob@example.com")
	for _, u := range []uuid.UUID{alice, bob} {
		err := db.InUserTx(ctx, pool, u, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, "INSERT INTO audit_events (user_id, actor, code) VALUES ($1, 'user', 'test.event')", u)
			return err
		})
		if err != nil {
			t.Fatalf("own insert: %v", err)
		}
	}

	err := db.InUserTx(ctx, pool, alice, func(tx pgx.Tx) error {
		var users, events int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM users").Scan(&users); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM audit_events").Scan(&events); err != nil {
			return err
		}
		if users != 1 || events != 1 {
			t.Errorf("alice sees %d users and %d events, want 1 and 1", users, events)
		}
		// Even a query that forgets the filter or names bob explicitly returns nothing.
		var email string
		err := tx.QueryRow(ctx, "SELECT email FROM users WHERE id = $1", bob).Scan(&email)
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Errorf("alice read bob's row: %q, %v", email, err)
		}
		tag, err := tx.Exec(ctx, "UPDATE users SET display_name = 'x' WHERE id = $1", bob)
		if err != nil || tag.RowsAffected() != 0 {
			t.Errorf("alice updated bob: %v rows, %v", tag.RowsAffected(), err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Writing a row for another user is rejected by the policy's WITH CHECK.
	err = db.InUserTx(ctx, pool, alice, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "INSERT INTO audit_events (user_id, actor, code) VALUES ($1, 'user', 'test.forged')", bob)
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "row-level security") {
		t.Fatalf("forged insert: %v", err)
	}

	// Without a tenant the application role sees nothing at all.
	var n int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM users").Scan(&n); err != nil || n != 0 {
		t.Fatalf("no tenant: %d users, %v", n, err)
	}
	if err := db.InUserTx(ctx, pool, uuid.Nil, func(pgx.Tx) error { return nil }); !errors.Is(err, db.ErrNoUser) {
		t.Fatalf("nil user: %v", err)
	}
}

func TestAuditEventsAreAppendOnly(t *testing.T) {
	pool := dbtest.AppPool(t)
	ctx := t.Context()
	alice := createUser(t, pool, "alice@example.com")
	err := db.InUserTx(ctx, pool, alice, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "INSERT INTO audit_events (user_id, actor, code) VALUES ($1, 'user', 'test.event')", alice); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, "DELETE FROM audit_events")
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("delete audit events: %v", err)
	}
}

func TestAppRoleCannotBypassRLS(t *testing.T) {
	pool := dbtest.AppPool(t)
	if err := db.SchemaCurrent(pool)(t.Context()); err != nil {
		t.Fatalf("readiness as app role: %v", err)
	}
	var bypass, super bool
	err := pool.QueryRow(t.Context(), "SELECT rolbypassrls, rolsuper FROM pg_roles WHERE rolname = current_user").Scan(&bypass, &super)
	if err != nil || bypass || super {
		t.Fatalf("app role: bypassrls=%v superuser=%v err=%v", bypass, super, err)
	}
}
