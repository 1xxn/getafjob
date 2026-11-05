package pgstore_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/1xxn/getafjob/internal/platform/config"
	"github.com/1xxn/getafjob/internal/platform/crypto"
	"github.com/1xxn/getafjob/internal/platform/db"
	"github.com/1xxn/getafjob/internal/platform/db/dbtest"
	"github.com/1xxn/getafjob/internal/profile"
	"github.com/1xxn/getafjob/internal/profile/adapters/pgstore"
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

func sealer(t *testing.T, pool *pgxpool.Pool) *crypto.Envelope {
	t.Helper()
	ring, err := crypto.NewKeyring(config.Secret(base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32))), "")
	if err != nil {
		t.Fatal(err)
	}
	return crypto.NewEnvelope(ring, pool)
}

func service(t *testing.T, pool *pgxpool.Pool) *profile.Service {
	t.Helper()
	return profile.NewService(pgstore.New(pool), sealer(t, pool))
}

func TestProfileRoundTrip(t *testing.T) {
	pool := dbtest.AppPool(t)
	ctx := t.Context()
	svc := service(t, pool)
	max, erika := user(t, pool, "max.mustermann@example.org"), user(t, pool, "erika.mustermann@example.org")

	snap, err := svc.Get(ctx, max)
	if err != nil || snap.Version != 0 || !snap.Preferences.Remote || snap.Preferences.Diversity != "decline" {
		t.Fatalf("empty profile = %+v, %v", snap, err)
	}
	p := profile.Profile{Personal: profile.Personal{FirstName: "Max", LastName: "Mustermann", Phone: "+49 30 1234567",
		GitHub: "github.com/maxmustermann"}, Skills: []string{"Go"}}
	if err := svc.SaveProfile(ctx, max, p, 0); err != nil {
		t.Fatal(err)
	}
	if err := svc.SavePreferences(ctx, max, profile.Preferences{Titles: []string{"Platform Engineer"}, Remote: true}); err != nil {
		t.Fatal(err)
	}
	snap, err = svc.Get(ctx, max)
	if err != nil || snap.Version != 1 || snap.Profile.Personal.LastName != "Mustermann" ||
		snap.Profile.Personal.GitHub != "https://github.com/maxmustermann" || snap.Preferences.Titles[0] != "Platform Engineer" {
		t.Fatalf("profile = %+v, %v", snap, err)
	}

	// A save based on an old version is refused (another tab saved meanwhile).
	if err := svc.SaveProfile(ctx, max, p, 0); !errors.Is(err, profile.ErrConflict) {
		t.Fatalf("stale save: %v", err)
	}
	if err := svc.SaveProfile(ctx, max, p, 1); err != nil {
		t.Fatal(err)
	}

	// Invalid data is refused with field errors.
	bad := p
	bad.Personal.Email = "not an address"
	var ve *profile.ValidationError
	if err := svc.SaveProfile(ctx, max, bad, 2); !errors.As(err, &ve) || ve.Fields[0].Field != "email" {
		t.Fatalf("invalid save: %v", err)
	}

	// The database holds no plaintext, and another user sees nothing.
	var raw []byte
	if err := db.InSystemTx(ctx, pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT sealed FROM profiles WHERE user_id = $1", max).Scan(&raw)
	}); err != nil || bytes.Contains(raw, []byte("Mustermann")) || bytes.Contains(raw, []byte("+49")) {
		t.Fatalf("stored plaintext or %v", err)
	}
	var n int
	if err := db.InUserTx(ctx, pool, erika, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT count(*) FROM profiles").Scan(&n)
	}); err != nil || n != 0 {
		t.Fatalf("erika sees %d profiles, %v", n, err)
	}
	if snap, err := svc.Get(ctx, erika); err != nil || snap.Version != 0 || snap.Profile.Personal.LastName != "" {
		t.Fatalf("erika's profile = %+v, %v", snap, err)
	}
}

func TestActiveUsers(t *testing.T) {
	pool := dbtest.AppPool(t)
	ctx := t.Context()
	st := pgstore.New(pool)
	changed := map[uuid.UUID]int{}
	svc := service(t, pool).WithOnChange(func(_ context.Context, id uuid.UUID) { changed[id]++ })
	max, off := user(t, pool, "max@example.org"), user(t, pool, "off@example.org")
	user(t, pool, "erika@example.org") // no profile
	for _, u := range []uuid.UUID{max, off} {
		if err := svc.SavePreferences(ctx, u, profile.Preferences{Titles: []string{"SRE"}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.InSystemTx(ctx, pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "UPDATE users SET disabled_at = now() WHERE id = $1", off)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if changed[max] != 1 || changed[off] != 1 {
		t.Fatalf("changes = %v", changed)
	}
	ids, err := st.ActiveUsers(ctx)
	if err != nil || len(ids) != 1 || ids[0] != max {
		t.Fatalf("ActiveUsers = %v, %v", ids, err)
	}
	prefs, err := st.ActivePreferences(ctx)
	if err != nil || len(prefs) != 1 || !bytes.Contains(prefs[0], []byte("SRE")) {
		t.Fatalf("ActivePreferences = %q, %v", prefs, err)
	}
}
