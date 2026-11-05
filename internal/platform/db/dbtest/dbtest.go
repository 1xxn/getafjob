// Package dbtest starts throwaway PostgreSQL databases for integration tests (testcontainers, needs Docker).
package dbtest

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/1xxn/getafjob/internal/platform/config"
	"github.com/1xxn/getafjob/internal/platform/db"
	"github.com/1xxn/getafjob/internal/platform/queue"
)

// Image must match the Compose stack (deploy/compose.yaml); Renovate keeps both current.
const Image = "postgres:18.6-alpine@sha256:10a5c69a83035ab5239f5620d708520e8b28f5aa1f2b20143e6024532edadfb5"

const (
	ownerPassword = "test-only-password"
	appPassword   = "app-test-password"
)

// Start runs a fresh PostgreSQL and returns the owner's connection URL. Skipped with -short.
func Start(t *testing.T) config.Secret {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test (needs Docker); runs without -short")
	}
	if os.Getenv("GETAFJOB_SKIP_DOCKER") == "1" {
		t.Skip("GETAFJOB_SKIP_DOCKER=1")
	}
	ctx := t.Context()
	c, err := tcpostgres.Run(ctx, Image,
		tcpostgres.WithDatabase("getafjob"),
		tcpostgres.WithUsername("getafjob"),
		tcpostgres.WithPassword(ownerPassword),
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
	return config.Secret(dsn)
}

// AppURL migrates a fresh database (schema and queue) as owner and returns the application role's URL and password.
func AppURL(t *testing.T) (config.Secret, config.Secret) {
	t.Helper()
	url := Start(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	owner, err := db.Open(ctx, url, "", "getafjob-test-owner")
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if _, err := db.Migrate(ctx, owner); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := queue.Migrate(ctx, owner); err != nil {
		t.Fatalf("migrate queue: %v", err)
	}
	if err := db.SetAppRolePassword(ctx, owner, appPassword); err != nil {
		t.Fatal(err)
	}
	return config.Secret(strings.Replace(url.Reveal(), "getafjob:"+ownerPassword+"@", db.AppRole+"@", 1)), appPassword
}

// AppPool returns a pool of the application role on a fresh, migrated database (RLS applies).
func AppPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	appURL, pw := AppURL(t)
	pool, err := db.Open(t.Context(), appURL, pw, "getafjob-test-app")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}
