package web_test

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// keyCall finds catalog lookups with a literal key: T(ctx, "a.b"), views.T(r.Context(), "a.b"), i18n.T(l, "a.b").
// A literal followed by + is a prefix ("apps.state." + state): some key must start with it.
var keyCall = regexp.MustCompile(`\bT\([^,"()]*(?:\(\))?,\s*"([a-z][a-z0-9_]*(?:\.[a-z0-9_]*)+)"(\s*\+)?`)

// TestCatalogHasEveryKeyUsed fails when templates or Go code look up a key the English catalog lacks (the other
// locales are held to English by TestCatalogsAreComplete).
func TestCatalogHasEveryKeyUsed(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "platform", "i18n", "catalog", "en.json"))
	if err != nil {
		t.Fatal(err)
	}
	var en map[string]string
	if err := json.Unmarshal(raw, &en); err != nil {
		t.Fatal(err)
	}
	hasPrefix := func(p string) bool {
		for k := range en {
			if strings.HasPrefix(k, p) {
				return true
			}
		}
		return false
	}
	checked := 0
	err = filepath.WalkDir("..", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !strings.HasSuffix(path, ".templ") && (!strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_templ.go") ||
			strings.HasSuffix(path, "_test.go")) {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range keyCall.FindAllStringSubmatch(string(src), -1) {
			checked++
			key, prefix := m[1], m[2] != ""
			if prefix && !hasPrefix(key) || !prefix && en[key] == "" {
				t.Errorf("%s: key %q not in the catalog", path, key)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 500 {
		t.Fatalf("only %d lookups found: the pattern no longer matches the code", checked)
	}
}

func TestKeyCallPattern(t *testing.T) {
	for src, want := range map[string]string{
		`T(ctx, "setup.next")`:                    "setup.next",
		`views.T(r.Context(), "admin.done.role")`: "admin.done.role",
		`i18n.T(l, "notify.summary.title", nil)`:  "notify.summary.title",
		`T(ctx, "apps.state." + a.State)`:         "apps.state.+",
		`fmt.Sprintf("x %s", "not.a.lookup")`:     "",
		`T(ctx, title)`:                           "",
	} {
		m := keyCall.FindStringSubmatch(src)
		got := ""
		if m != nil {
			got = m[1]
			if m[2] != "" {
				got += "+"
			}
		}
		if got != want {
			t.Errorf("%s: got %q, want %q", src, got, want)
		}
	}
}
