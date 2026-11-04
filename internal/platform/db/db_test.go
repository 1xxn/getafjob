package db

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/1xxn/getafjob/internal/platform/config"
)

// postgresImage must match the Compose stack (deploy/compose.yaml); Renovate keeps both current.
const postgresImage = "postgres:18.6-alpine@sha256:10a5c69a83035ab5239f5620d708520e8b28f5aa1f2b20143e6024532edadfb5"

// startPostgres starts a throwaway PostgreSQL. Integration tests do not run with -short.
func startPostgres(t *testing.T) (url, password config.Secret) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test (needs Docker); runs without -short")
	}
	if os.Getenv("GETAFJOB_SKIP_DOCKER") == "1" {
		t.Skip("GETAFJOB_SKIP_DOCKER=1")
	}
	ctx := t.Context()
	c, err := tcpostgres.Run(ctx, postgresImage,
		tcpostgres.WithDatabase("getafjob"),
		tcpostgres.WithUsername("getafjob"),
		tcpostgres.WithPassword("test-only-password"),
		tcpostgres.BasicWaitStrategies(),
	)
	testcontainers.CleanupContainer(t, c)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	return config.Secret(dsn), ""
}

func TestMigrateAndReadiness(t *testing.T) {
	url, pw := startPostgres(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	pool, err := Open(ctx, url, pw, "getafjob-test")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := Ping(pool)(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
	if err := SchemaCurrent(pool)(ctx); err == nil {
		t.Fatal("fresh database must report pending migrations")
	}

	applied, err := Migrate(ctx, pool)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if len(applied) == 0 {
		t.Fatal("no migration applied")
	}
	if err := SchemaCurrent(pool)(ctx); err != nil {
		t.Fatalf("schema not current after migrate: %v", err)
	}
	again, err := Migrate(ctx, pool)
	if err != nil || len(again) != 0 {
		t.Fatalf("second migrate: %v, %v", again, err)
	}

	var ext string
	if err := pool.QueryRow(ctx, "SELECT extname FROM pg_extension WHERE extname = 'pg_trgm'").Scan(&ext); err != nil {
		t.Fatalf("pg_trgm missing: %v", err)
	}
}

func TestPasswordOverridesURL(t *testing.T) {
	url, _ := startPostgres(t)
	ctx := t.Context()
	// A wrong password in GETAFJOB_DATABASE_PASSWORD overrides the right one in the URL → ping fails.
	pool, err := Open(ctx, url, "wrong", "getafjob-test")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err == nil {
		t.Fatal("ping with wrong password succeeded")
	}
}

func TestOpenRejectsMissingAndBrokenURL(t *testing.T) {
	if _, err := Open(t.Context(), "", "", "x"); !errors.Is(err, ErrNoDatabase) {
		t.Fatalf("err = %v", err)
	}
	if _, err := Open(t.Context(), "postgres://u:hunter2@h:notaport/db", "", "x"); err == nil ||
		err.Error() != "cannot parse GETAFJOB_DATABASE_URL" {
		t.Fatalf("err = %v", err)
	}
}
