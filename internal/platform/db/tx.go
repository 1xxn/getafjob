package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/1xxn/getafjob/internal/platform/config"
)

// AppRole is the database role of serve and worker; it cannot bypass row-level security.
const AppRole = "getafjob_app"

// ErrNoUser reports a user transaction without a user.
var ErrNoUser = errors.New("user transaction without user id")

// InUserTx runs fn in a transaction that sees only the rows of userID (row-level security).
func InUserTx(ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID, fn func(pgx.Tx) error) error {
	if userID == uuid.Nil {
		return ErrNoUser
	}
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT set_config('app.user_id', $1, true)", userID.String()); err != nil {
			return fmt.Errorf("set tenant: %w", err)
		}
		return fn(tx)
	})
}

// InSystemTx runs fn in a transaction that sees all users' rows. Only for reviewed code paths: the global job
// index, admin tasks and sign-in lookups before a user is known.
func InSystemTx(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT set_config('app.system', 'on', true)"); err != nil {
			return fmt.Errorf("set system context: %w", err)
		}
		return fn(tx)
	})
}

// SetAppRolePassword lets the application role log in with the given password (role migrate).
func SetAppRolePassword(ctx context.Context, pool *pgxpool.Pool, password config.Secret) error {
	if password == "" {
		return nil
	}
	var stmt string
	if err := pool.QueryRow(ctx, "SELECT format('ALTER ROLE %I WITH LOGIN PASSWORD %L', $1::text, $2::text)",
		AppRole, password.Reveal()).Scan(&stmt); err != nil {
		return fmt.Errorf("prepare app role: %w", err)
	}
	if _, err := pool.Exec(ctx, stmt); err != nil {
		return errors.New("set app role password failed") // the statement contains the password
	}
	return nil
}
