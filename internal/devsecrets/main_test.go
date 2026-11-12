package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Missing secrets are written in the format of "admin gen-secret"; existing ones stay as they are.
func TestRunWritesMissingSecretsOnly(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "master_key"), []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(dir); err != nil {
		t.Fatal(err)
	}
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if got := read("master_key"); got != "keep\n" {
		t.Errorf("existing secret replaced: %q", got)
	}
	if got := read("smtp_url"); got != "smtp+insecure://mailpit:1025" {
		t.Errorf("smtp_url = %q", got)
	}
	for _, name := range []string{"db_password", "db_app_password", "runner_secret", "backup_password"} {
		v := read(name)
		b, err := base64.RawURLEncoding.DecodeString(strings.TrimSuffix(v, "\n"))
		if err != nil || len(b) != 32 || !strings.HasSuffix(v, "\n") {
			t.Errorf("%s = %q (%v)", name, v, err)
		}
	}
	if read("db_password") == read("db_app_password") {
		t.Error("two secrets are equal")
	}
}
