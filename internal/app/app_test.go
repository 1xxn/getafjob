package app

import (
	"bytes"
	"context"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/1xxn/getafjob/internal/platform/config"
)

func testEnv(vars map[string]string) (Env, *bytes.Buffer, *bytes.Buffer) {
	var out, errb bytes.Buffer
	return Env{
		Stdout: &out,
		Stderr: &errb,
		Config: config.Source{
			LookupEnv: func(k string) (string, bool) { v, ok := vars[k]; return v, ok },
			Environ: func() []string {
				var kv []string
				for k, v := range vars {
					kv = append(kv, k+"="+v)
				}
				return kv
			},
			Open: func(string) (io.ReadCloser, error) { return nil, fs.ErrNotExist },
		},
	}, &out, &errb
}

func TestMainUsageAndVersion(t *testing.T) {
	env, out, errb := testEnv(nil)
	if code := Main(t.Context(), nil, env); code != ExitUsage || !strings.Contains(errb.String(), "Roles:") {
		t.Fatalf("no args: code %d, stderr %q", code, errb.String())
	}
	if code := Main(t.Context(), []string{"version"}, env); code != ExitOK || !strings.HasPrefix(out.String(), "getafjob ") {
		t.Fatalf("version: code %d, out %q", code, out.String())
	}
	if code := Main(t.Context(), []string{"nope"}, env); code != ExitUsage {
		t.Fatalf("unknown: code %d", code)
	}
}

func TestRolesRejectBadConfig(t *testing.T) {
	env, _, errb := testEnv(map[string]string{"GETAFJOB_LOG_FORMAT": "xml", "GETAFJOB_DATABASE_PASSWORD": "hunter2"})
	for _, role := range []string{"serve", "worker", "runner", "migrate"} {
		if code := Main(t.Context(), []string{role}, env); code != ExitUsage {
			t.Errorf("%s: code %d", role, code)
		}
	}
	if strings.Contains(errb.String(), "hunter2") {
		t.Fatalf("config error leaks secret: %s", errb.String())
	}
}

func TestRolesNeedDatabase(t *testing.T) {
	env, _, errb := testEnv(map[string]string{"GETAFJOB_LOG_FORMAT": "text"})
	for _, role := range []string{"serve", "worker", "migrate"} {
		if code := Main(t.Context(), []string{role}, env); code != ExitError {
			t.Errorf("%s without database: code %d", role, code)
		}
	}
	if !strings.Contains(errb.String(), "GETAFJOB_DATABASE_URL is not set") {
		t.Fatalf("stderr = %s", errb.String())
	}
}

func TestAdmin(t *testing.T) {
	env, out, _ := testEnv(map[string]string{"GETAFJOB_TYPO": "x"})
	if code := Main(t.Context(), []string{"admin", "check-config"}, env); code != ExitOK {
		t.Fatalf("check-config code %d", code)
	}
	if !strings.Contains(out.String(), "unknown variable GETAFJOB_TYPO") || !strings.Contains(out.String(), "configuration ok") {
		t.Fatalf("out = %q", out.String())
	}
	out.Reset()
	if code := Main(t.Context(), []string{"admin", "env"}, env); code != ExitOK || !strings.Contains(out.String(), "GETAFJOB_DATABASE_URL\n") {
		t.Fatalf("env: %d %q", code, out.String())
	}
	if code := Main(t.Context(), []string{"admin"}, env); code != ExitUsage {
		t.Fatalf("admin without command: %d", code)
	}
}

func TestHealthcheck(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer ok.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer bad.Close()
	redirect := httptest.NewServer(http.RedirectHandler(ok.URL, http.StatusFound))
	defer redirect.Close()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	cases := []struct {
		args []string
		want int
	}{
		{[]string{ok.URL + "/readyz"}, ExitOK},
		{[]string{bad.URL}, ExitError},
		{[]string{redirect.URL}, ExitError}, // redirects do not count as healthy
		{[]string{"tcp://" + ln.Addr().String()}, ExitOK},
		{[]string{"-timeout", "200ms", "tcp://127.0.0.1:1"}, ExitError},
		{[]string{"ftp://x"}, ExitUsage},
		{[]string{}, ExitUsage},
	}
	for _, tc := range cases {
		env, _, _ := testEnv(nil)
		if got := healthcheck(context.Background(), tc.args, env); got != tc.want {
			t.Errorf("healthcheck %v = %d, want %d", tc.args, got, tc.want)
		}
	}
}

func TestAdminGenSecret(t *testing.T) {
	env, out, _ := testEnv(nil)
	if code := Main(t.Context(), []string{"admin", "gen-secret"}, env); code != ExitOK {
		t.Fatalf("code %d", code)
	}
	first := strings.TrimSpace(out.String())
	if len(first) != 43 {
		t.Fatalf("secret %q has length %d, want 43 (32 bytes base64url)", first, len(first))
	}
	out.Reset()
	Main(t.Context(), []string{"admin", "gen-secret"}, env)
	if strings.TrimSpace(out.String()) == first {
		t.Fatal("two secrets are equal")
	}
}
