package app

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/1xxn/getafjob/internal/platform/db"
	"github.com/1xxn/getafjob/internal/platform/db/dbtest"
)

func TestAdminDemo(t *testing.T) {
	url, pw := dbtest.AppURL(t)
	vars := map[string]string{
		"GETAFJOB_DATABASE_URL":      url.Reveal(),
		"GETAFJOB_DATABASE_PASSWORD": pw.Reveal(),
		"GETAFJOB_MASTER_KEY":        "AwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwM",
	}
	for run, want := range []string{"postings: 300 (300 new)", "postings: 300 (0 new)"} {
		env, out, errb := testEnv(vars)
		if code := Main(t.Context(), []string{"admin", "demo", "-jobs", "300", "-seed", "5", "-autonomy", "suggest"}, env); code != ExitOK ||
			!strings.Contains(out.String(), want) || !strings.Contains(out.String(), "matched 300 jobs for 3 users") ||
			!strings.Contains(out.String(), "user max.mustermann@example.org") {
			t.Fatalf("run %d: code %d, out %q, err %q", run+1, code, out.String(), errb.String())
		}
	}

	// The demo users only get suggestions: the planner leaves them alone.
	pool, err := db.Open(t.Context(), url, pw, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var levels []string
	err = db.InSystemTx(t.Context(), pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(t.Context(), `SELECT level FROM autonomy_settings ORDER BY level`)
		if err != nil {
			return err
		}
		levels, err = pgx.CollectRows(rows, pgx.RowTo[string])
		return err
	})
	if len(levels) != 3 || levels[0] != "suggest" || levels[2] != "suggest" {
		t.Errorf("autonomy levels %v, %v", levels, err)
	}
	env, _, errb := testEnv(vars)
	if code := Main(t.Context(), []string{"admin", "demo", "-autonomy", "bold"}, env); code != ExitUsage {
		t.Fatalf("unknown level: code %d, err %q", code, errb.String())
	}

	vars["GETAFJOB_PUBLIC_URL"] = "https://jobs.example.org"
	env, _, errb = testEnv(vars)
	if code := Main(t.Context(), []string{"admin", "demo"}, env); code != ExitUsage || !strings.Contains(errb.String(), "not local") {
		t.Fatalf("public URL: code %d, err %q", code, errb.String())
	}
	if code := Main(t.Context(), []string{"admin", "demo", "-jobs", "60000"}, env); code != ExitUsage {
		t.Fatalf("too many jobs: code %d", code)
	}
}
