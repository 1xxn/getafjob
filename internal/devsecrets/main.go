// Command devsecrets writes the missing development secrets of deploy/secrets: 32 random bytes in base64url, the
// format of "getafjob admin gen-secret", and the Mailpit address. A small program of its own, so a fresh checkout
// (and CI) does not compile the whole application just to make a password.
package main

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

func main() {
	if err := run("deploy/secrets"); err != nil {
		fmt.Fprintln(os.Stderr, "devsecrets:", err)
		os.Exit(1)
	}
}

func run(dir string) error {
	files := map[string]func() string{
		"db_password": random, "db_app_password": random, "master_key": random, "runner_secret": random,
		"backup_password": random, "smtp_url": func() string { return "smtp+insecure://mailpit:1025" },
	}
	for name, value := range files {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			continue
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		// Compose mounts file secrets with the host's mode; the services run as other users and must read them.
		if err := os.WriteFile(path, []byte(value()), 0o644); err != nil { //nolint:gosec // development secrets only
			return err
		}
	}
	return nil
}

// random is what "getafjob admin gen-secret" prints: 32 random bytes, base64url, and a line break.
func random() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b) // crypto/rand.Read never returns an error (it crashes instead)
	return base64.RawURLEncoding.EncodeToString(b) + "\n"
}
