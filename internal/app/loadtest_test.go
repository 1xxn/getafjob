package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAdminLoadTestUsage(t *testing.T) {
	env, _, errb := testEnv(nil)
	if code := Main(t.Context(), []string{"admin", "load-test", "-unknown-flag"}, env); code != ExitUsage {
		t.Fatalf("expected ExitUsage on unknown flag, got %d (err: %s)", code, errb.String())
	}
}

func TestAdminLoadTestSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	env, out, errb := testEnv(nil)
	code := Main(t.Context(), []string{
		"admin", "load-test",
		"-url", srv.URL,
		"-c", "2",
		"-duration", "300ms",
		"-slo", "200ms",
	}, env)

	if code != ExitOK {
		t.Fatalf("expected ExitOK, got %d (err: %s)", code, errb.String())
	}
	summary := out.String()
	if !strings.Contains(summary, "GETAFJOB LOAD TEST REPORT") || !strings.Contains(summary, "[ PASS ]") {
		t.Fatalf("expected passing load test summary, got:\n%s", summary)
	}
}

func TestAdminLoadTestSLOFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(30 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	env, out, errb := testEnv(nil)
	code := Main(t.Context(), []string{
		"admin", "load-test",
		"-url", srv.URL,
		"-c", "2",
		"-requests", "6",
		"-slo", "5ms", // intentionally low to trigger SLO failure
	}, env)

	if code != ExitError {
		t.Fatalf("expected ExitError on SLO failure, got %d (out: %s)", code, out.String())
	}
	if !strings.Contains(errb.String(), "SLO check failed") {
		t.Fatalf("expected stderr to report SLO check failure, got:\n%s", errb.String())
	}
}
