// Package db connects to PostgreSQL (pgx pool) and applies the embedded migrations.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"github.com/1xxn/getafjob/db/migrations"
	"github.com/1xxn/getafjob/internal/platform/config"
)

// ErrNoDatabase reports a missing GETAFJOB_DATABASE_URL.
var ErrNoDatabase = errors.New("GETAFJOB_DATABASE_URL is not set")

// Open builds the connection pool. The password from GETAFJOB_DATABASE_PASSWORD wins over the one in the URL.
func Open(ctx context.Context, url, password config.Secret, appName string) (*pgxpool.Pool, error) {
	if url == "" {
		return nil, ErrNoDatabase
	}
	pc, err := pgxpool.ParseConfig(url.Reveal())
	if err != nil {
		// pgx errors may contain the connection string.
		return nil, errors.New("cannot parse GETAFJOB_DATABASE_URL")
	}
	if password != "" {
		pc.ConnConfig.Password = password.Reveal()
	}
	pc.ConnConfig.RuntimeParams["application_name"] = appName
	pc.MaxConnLifetime = time.Hour
	pc.MaxConnIdleTime = 15 * time.Minute
	pc.HealthCheckPeriod = 30 * time.Second
	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	return pool, nil
}

// Ping checks the connection (readiness).
func Ping(pool *pgxpool.Pool) func(context.Context) error {
	return pool.Ping
}

func provider(pool *pgxpool.Pool) (*goose.Provider, *sql.DB, error) {
	sqlDB := stdlib.OpenDBFromPool(pool)
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		_ = sqlDB.Close()
		return nil, nil, err
	}
	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS,
		goose.WithSessionLocker(locker), goose.WithDisableGlobalRegistry(true))
	if err != nil {
		_ = sqlDB.Close()
		return nil, nil, err
	}
	return p, sqlDB, nil
}

// Migrate applies all pending migrations. An advisory lock prevents concurrent runs.
func Migrate(ctx context.Context, pool *pgxpool.Pool) (applied []string, err error) {
	p, sqlDB, err := provider(pool)
	if err != nil {
		return nil, err
	}
	defer func() { _ = sqlDB.Close() }()
	res, err := p.Up(ctx)
	for _, r := range res {
		applied = append(applied, r.Source.Path)
	}
	return applied, err
}

// SchemaCurrent reports an error while migrations are pending (readiness of serve and worker).
func SchemaCurrent(pool *pgxpool.Pool) func(context.Context) error {
	return func(ctx context.Context) error {
		p, sqlDB, err := provider(pool)
		if err != nil {
			return err
		}
		defer func() { _ = sqlDB.Close() }()
		pending, err := p.HasPending(ctx)
		if err != nil {
			return err
		}
		if pending {
			return errors.New("database schema has pending migrations")
		}
		return nil
	}
}
