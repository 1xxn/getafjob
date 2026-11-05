package pgstore_test

import (
	"bytes"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/1xxn/getafjob/internal/platform/blob"
	"github.com/1xxn/getafjob/internal/platform/config"
	"github.com/1xxn/getafjob/internal/platform/crypto"
	"github.com/1xxn/getafjob/internal/platform/db"
	"github.com/1xxn/getafjob/internal/platform/db/dbtest"
	"github.com/1xxn/getafjob/internal/profile"
	"github.com/1xxn/getafjob/internal/profile/adapters/pgstore"
)

func TestDocuments(t *testing.T) {
	pool := dbtest.AppPool(t)
	ctx := t.Context()
	ring, err := crypto.NewKeyring(config.Secret(base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{4}, 32))), "")
	if err != nil {
		t.Fatal(err)
	}
	env := crypto.NewEnvelope(ring, pool)
	dir, err := blob.NewDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dir.Close() })
	docs := profile.NewDocuments(pgstore.New(pool), blob.NewSealed(dir, env), env)
	max, erika := user(t, pool, "max.mustermann@example.org"), user(t, pool, "erika.mustermann@example.org")

	pdf := []byte("%PDF-1.7\nCV of Max Mustermann, Musterstrasse 1")
	d, err := docs.Upload(ctx, max, profile.KindCV, "Lebenslauf Max Mustermann.pdf", pdf)
	if err != nil || d.Mime != profile.MimePDF || d.Name != "Lebenslauf Max Mustermann.pdf" {
		t.Fatalf("upload = %+v, %v", d, err)
	}
	if _, err := docs.Upload(ctx, max, profile.KindCV, "evil.pdf", []byte("<html>")); !errors.Is(err, profile.ErrFileType) {
		t.Fatalf("html upload: %v", err)
	}
	if err := docs.SetVariant(ctx, max, d.ID, " Platform  EN ", "en", []string{"Platform", " Kubernetes "}); err != nil {
		t.Fatal(err)
	}
	list, err := docs.List(ctx, max)
	if err != nil || len(list) != 1 || list[0].Label != "Platform EN" || list[0].Focus[1] != "kubernetes" || list[0].Name != d.Name {
		t.Fatalf("list = %+v, %v", list, err)
	}
	got, data, err := docs.Read(ctx, max, d.ID)
	if err != nil || !bytes.Equal(data, pdf) || got.Size != len(pdf) {
		t.Fatalf("read = %+v, %v", got, err)
	}

	// Neither the file nor its name are stored in plaintext.
	raw, err := dir.Get(ctx, "users/"+max.String()+"/documents/"+d.ID.String())
	if err != nil || bytes.Contains(raw, []byte("Mustermann")) {
		t.Fatalf("stored file in plaintext or %v", err)
	}
	var name []byte
	if err := db.InSystemTx(ctx, pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT sealed_name FROM documents WHERE id = $1", d.ID).Scan(&name)
	}); err != nil || bytes.Contains(name, []byte("Mustermann")) {
		t.Fatalf("stored name in plaintext or %v", err)
	}

	// Another user can neither read, change nor delete it.
	if _, _, err := docs.Read(ctx, erika, d.ID); !errors.Is(err, profile.ErrNotFound) {
		t.Errorf("erika reads: %v", err)
	}
	if err := docs.SetVariant(ctx, erika, d.ID, "x", "", nil); !errors.Is(err, profile.ErrNotFound) {
		t.Errorf("erika changes: %v", err)
	}
	if err := docs.Delete(ctx, erika, d.ID); !errors.Is(err, profile.ErrNotFound) {
		t.Errorf("erika deletes: %v", err)
	}
	if err := docs.SetVariant(ctx, max, uuid.New(), "x", "fr", nil); !errors.Is(err, profile.ErrFileType) {
		t.Errorf("bad language: %v", err)
	}

	if err := docs.Delete(ctx, max, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := dir.Get(ctx, "users/"+max.String()+"/documents/"+d.ID.String()); !errors.Is(err, blob.ErrNotFound) {
		t.Errorf("file left behind: %v", err)
	}
}
