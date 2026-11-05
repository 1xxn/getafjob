package app

import (
	"strings"
	"testing"

	"github.com/1xxn/getafjob/internal/platform/db/dbtest"
)

func TestAdminInvite(t *testing.T) {
	url, pw := dbtest.AppURL(t)
	vars := map[string]string{
		"GETAFJOB_DATABASE_URL":      url.Reveal(),
		"GETAFJOB_DATABASE_PASSWORD": pw.Reveal(),
		"GETAFJOB_PUBLIC_URL":        "https://jobs.example.org",
	}
	env, out, errb := testEnv(vars)
	if code := Main(t.Context(), []string{"admin", "invite", "-email", "max.mustermann@example.com", "-role", "admin"}, env); code != ExitOK {
		t.Fatalf("code %d: %s", code, errb.String())
	}
	if !strings.HasPrefix(out.String(), "https://jobs.example.org/invite/") || !strings.Contains(out.String(), "valid until") {
		t.Fatalf("out = %q", out.String())
	}
	cases := []struct {
		args []string
		want int
	}{
		{[]string{"admin", "invite"}, ExitUsage},
		{[]string{"admin", "invite", "-email", "not-an-address"}, ExitUsage},
		{[]string{"admin", "invite", "-email", "max@example.com", "-role", "root"}, ExitUsage},
		{[]string{"admin", "invite", "-email", "max@example.com", "-send"}, ExitUsage}, // no SMTP configured
	}
	for _, c := range cases {
		env, _, _ := testEnv(vars)
		if got := Main(t.Context(), c.args, env); got != c.want {
			t.Errorf("%v = %d, want %d", c.args, got, c.want)
		}
	}
}
