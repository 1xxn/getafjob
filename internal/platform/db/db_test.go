package db_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/1xxn/getafjob/internal/platform/config"
	"github.com/1xxn/getafjob/internal/platform/db"
	"github.com/1xxn/getafjob/internal/platform/db/dbtest"
)

func TestMigrateAndReadiness(t *testing.T) {
	url := dbtest.Start(t)
	var pw config.Secret
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	pool, err := db.Open(ctx, url, pw, "getafjob-test")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := db.Ping(pool)(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
	if err := db.SchemaCurrent(pool)(ctx); err == nil {
		t.Fatal("fresh database must report pending migrations")
	}

	applied, err := db.Migrate(ctx, pool)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if len(applied) == 0 {
		t.Fatal("no migration applied")
	}
	if err := db.SchemaCurrent(pool)(ctx); err != nil {
		t.Fatalf("schema not current after migrate: %v", err)
	}
	again, err := db.Migrate(ctx, pool)
	if err != nil || len(again) != 0 {
		t.Fatalf("second migrate: %v, %v", again, err)
	}

	var ext string
	if err := pool.QueryRow(ctx, "SELECT extname FROM pg_extension WHERE extname = 'pg_trgm'").Scan(&ext); err != nil {
		t.Fatalf("pg_trgm missing: %v", err)
	}
}

func TestPasswordOverridesURL(t *testing.T) {
	url := dbtest.Start(t)
	ctx := t.Context()
	// A wrong password in GETAFJOB_DATABASE_PASSWORD overrides the right one in the URL → ping fails.
	pool, err := db.Open(ctx, url, "wrong", "getafjob-test")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err == nil {
		t.Fatal("ping with wrong password succeeded")
	}
}

func TestOpenRejectsMissingAndBrokenURL(t *testing.T) {
	if _, err := db.Open(t.Context(), "", "", "x"); !errors.Is(err, db.ErrNoDatabase) {
		t.Fatalf("err = %v", err)
	}
	if _, err := db.Open(t.Context(), "postgres://u:hunter2@h:notaport/db", "", "x"); err == nil ||
		err.Error() != "cannot parse GETAFJOB_DATABASE_URL" {
		t.Fatalf("err = %v", err)
	}
}
