package crypto_test

import (
	"bytes"
	"crypto/rand"
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
)

func newKey(t *testing.T) config.Secret {
	t.Helper()
	b := make([]byte, crypto.KeySize)
	_, _ = rand.Read(b)
	return config.Secret(base64.RawURLEncoding.EncodeToString(b))
}

func TestKeyring(t *testing.T) {
	a, b := newKey(t), newKey(t)
	ra, err := crypto.NewKeyring(a, "")
	if err != nil {
		t.Fatal(err)
	}
	rb, err := crypto.NewKeyring(b, a)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := crypto.NewKeyring(a+"\n", "")
	if ra.CurrentID() == rb.CurrentID() || ra.CurrentID() != again.CurrentID() || len(ra.CurrentID()) != 16 {
		t.Errorf("ids %q %q %q", ra.CurrentID(), rb.CurrentID(), again.CurrentID())
	}
	for _, bad := range []config.Secret{"", "short", config.Secret(base64.RawURLEncoding.EncodeToString(make([]byte, 16)))} {
		if _, err := crypto.NewKeyring(bad, ""); !errors.Is(err, crypto.ErrBadKey) {
			t.Errorf("key %q accepted", bad)
		}
		if crypto.ValidMasterKey(bad.Reveal()) {
			t.Errorf("ValidMasterKey(%q)", bad)
		}
	}
	if _, err := crypto.NewKeyring(a, "bad"); err == nil {
		t.Error("bad old key accepted")
	}
}

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

func TestEnvelope(t *testing.T) {
	pool := dbtest.AppPool(t)
	ctx := t.Context()
	old := newKey(t)
	ring, err := crypto.NewKeyring(old, "")
	if err != nil {
		t.Fatal(err)
	}
	env := crypto.NewEnvelope(ring, pool)
	max, erika := user(t, pool, "max.mustermann@example.org"), user(t, pool, "erika.mustermann@example.org")
	secret := []byte("app password of Max Mustermann")

	ct, err := env.Seal(ctx, max, "mail_password", secret)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ct, secret) {
		t.Fatal("ciphertext contains the plaintext")
	}
	ct2, _ := env.Seal(ctx, max, "mail_password", secret)
	if bytes.Equal(ct, ct2) {
		t.Error("same ciphertext twice (nonce reuse)")
	}
	if got, err := env.Open(ctx, max, "mail_password", ct); err != nil || !bytes.Equal(got, secret) {
		t.Fatalf("open = %q, %v", got, err)
	}
	if _, err := env.Open(ctx, max, "document:1", ct); !errors.Is(err, crypto.ErrDecrypt) {
		t.Errorf("other purpose: %v", err)
	}
	if _, err := env.Open(ctx, erika, "mail_password", ct); !errors.Is(err, crypto.ErrNoKey) {
		t.Errorf("other user without key: %v", err)
	}
	if _, err := env.Seal(ctx, erika, "mail_password", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Open(ctx, erika, "mail_password", ct); !errors.Is(err, crypto.ErrDecrypt) {
		t.Errorf("other user's key: %v", err)
	}
	tampered := bytes.Clone(ct)
	tampered[len(tampered)-1] ^= 1
	if _, err := env.Open(ctx, max, "mail_password", tampered); !errors.Is(err, crypto.ErrDecrypt) {
		t.Errorf("tampered: %v", err)
	}

	// The database holds only wrapped keys, and each user sees only their own row.
	var n int
	if err := db.InUserTx(ctx, pool, max, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT count(*) FROM user_keys").Scan(&n)
	}); err != nil || n != 1 {
		t.Fatalf("rows visible to max = %d, %v", n, err)
	}

	// Rotation: a new master key without the old one cannot read; with it, all keys are re-wrapped.
	cur := newKey(t)
	without, _ := crypto.NewKeyring(cur, "")
	if _, err := crypto.NewEnvelope(without, pool).Open(ctx, max, "mail_password", ct); !errors.Is(err, crypto.ErrUnknownMasterKey) {
		t.Errorf("without old key: %v", err)
	}
	rotating, _ := crypto.NewKeyring(cur, old)
	if n, err := crypto.NewEnvelope(rotating, pool).Rotate(ctx); err != nil || n != 2 {
		t.Fatalf("rotated %d, %v", n, err)
	}
	if n, err := crypto.NewEnvelope(rotating, pool).Rotate(ctx); err != nil || n != 0 {
		t.Fatalf("second rotation %d, %v", n, err)
	}
	if got, err := crypto.NewEnvelope(without, pool).Open(ctx, max, "mail_password", ct); err != nil || !bytes.Equal(got, secret) {
		t.Fatalf("after rotation: %q, %v (data untouched, old key no longer needed)", got, err)
	}

	// Forget: the data of Max is gone for good.
	if ok, err := env.Forget(ctx, max); err != nil || !ok {
		t.Fatalf("forget = %v, %v", ok, err)
	}
	if _, err := crypto.NewEnvelope(without, pool).Open(ctx, max, "mail_password", ct); !errors.Is(err, crypto.ErrNoKey) {
		t.Errorf("after forget: %v", err)
	}
}

func FuzzOpen(f *testing.F) {
	f.Add([]byte{1, 2, 3})
	f.Add(append([]byte{1}, make([]byte, 40)...))
	ring, err := crypto.NewKeyring(config.Secret(base64.RawURLEncoding.EncodeToString(make([]byte, 32))), "")
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		if _, err := crypto.OpenWrapped(ring, ring.CurrentID(), b); err == nil {
			t.Fatal("random bytes opened")
		}
	})
}
