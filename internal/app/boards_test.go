package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/1xxn/getafjob/internal/discovery/adapters/sources"
	"github.com/1xxn/getafjob/internal/platform/db/dbtest"
)

func TestAdminBoards(t *testing.T) {
	url, pw := dbtest.AppURL(t)
	vars := map[string]string{
		"GETAFJOB_DATABASE_URL":      url.Reveal(),
		"GETAFJOB_DATABASE_PASSWORD": pw.Reveal(),
		"GETAFJOB_PUBLIC_URL":        "https://jobs.example.org",
	}
	catalog, err := sources.Catalog()
	if err != nil || len(catalog) == 0 {
		t.Fatalf("catalog: %d boards, %v", len(catalog), err)
	}
	for i, want := range []string{
		fmt.Sprintf("%d boards added, 0 updated", len(catalog)),
		fmt.Sprintf("0 boards added, %d updated", len(catalog)), // a second import changes nothing
	} {
		env, out, errb := testEnv(vars)
		if code := Main(t.Context(), []string{"admin", "boards", "import"}, env); code != ExitOK || strings.TrimSpace(out.String()) != want {
			t.Fatalf("import %d: code %d, out %q, err %q", i+1, code, out.String(), errb.String())
		}
	}
	env, out, _ := testEnv(vars)
	if code := Main(t.Context(), []string{"admin", "boards", "list"}, env); code != ExitOK ||
		strings.Count(out.String(), "\n") != len(catalog) || !strings.Contains(out.String(), catalog[0].Slug) {
		t.Fatalf("list: code %d, out %q", code, out.String())
	}
	env, _, _ = testEnv(vars)
	if code := Main(t.Context(), []string{"admin", "boards", "delete"}, env); code != ExitUsage {
		t.Fatalf("unknown subcommand: code %d", code)
	}
}
