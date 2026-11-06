package app

import (
	"strings"
	"testing"

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
		if code := Main(t.Context(), []string{"admin", "demo", "-jobs", "300", "-seed", "5"}, env); code != ExitOK ||
			!strings.Contains(out.String(), want) || !strings.Contains(out.String(), "matched 300 jobs for 3 users") ||
			!strings.Contains(out.String(), "user max.mustermann@example.org") {
			t.Fatalf("run %d: code %d, out %q, err %q", run+1, code, out.String(), errb.String())
		}
	}

	vars["GETAFJOB_PUBLIC_URL"] = "https://jobs.example.org"
	env, _, errb := testEnv(vars)
	if code := Main(t.Context(), []string{"admin", "demo"}, env); code != ExitUsage || !strings.Contains(errb.String(), "not local") {
		t.Fatalf("public URL: code %d, err %q", code, errb.String())
	}
	if code := Main(t.Context(), []string{"admin", "demo", "-jobs", "60000"}, env); code != ExitUsage {
		t.Fatalf("too many jobs: code %d", code)
	}
}
