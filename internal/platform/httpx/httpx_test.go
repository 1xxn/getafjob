package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestHealthz(t *testing.T) {
	mux := http.NewServeMux()
	NewHealth(quiet()).Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "ok\n" {
		t.Fatalf("healthz = %d %q", rec.Code, rec.Body.String())
	}
}

func TestReadyzReportsEachCheckWithoutDetails(t *testing.T) {
	h := NewHealth(quiet())
	h.Add("db", func(context.Context) error { return nil })
	h.Add("browser", func(context.Context) error { return errors.New("secret detail /home/x") })
	mux := http.NewServeMux()
	h.Register(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("code = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "secret detail") {
		t.Fatalf("error details leaked: %s", rec.Body.String())
	}
	var r readiness
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r.Status != "fail" || r.Checks["db"] != "ok" || r.Checks["browser"] != "fail" {
		t.Fatalf("readiness = %+v", r)
	}
}

func TestReadyzTimesOutSlowChecks(t *testing.T) {
	h := NewHealth(quiet())
	h.Timeout = 50 * time.Millisecond
	h.Add("slow", func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() })
	mux := http.NewServeMux()
	h.Register(mux)
	rec := httptest.NewRecorder()
	start := time.Now()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable || time.Since(start) > 2*time.Second {
		t.Fatalf("code = %d after %v", rec.Code, time.Since(start))
	}
}

func TestReadyzOKWithoutChecks(t *testing.T) {
	mux := http.NewServeMux()
	NewHealth(quiet()).Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
}

func TestProtectSetsHeadersAndBlocksCrossOriginPosts(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			http.Error(w, "too large", http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	h := Protect(inner, 16, true)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	for _, k := range []string{"Content-Security-Policy", "X-Frame-Options", "X-Content-Type-Options", "Strict-Transport-Security", "Referrer-Policy"} {
		if rec.Header().Get(k) == "" {
			t.Errorf("header %s missing", k)
		}
	}
	if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "script-src 'self'") {
		t.Errorf("CSP = %q", rec.Header().Get("Content-Security-Policy"))
	}

	// A foreign site sends a POST with the user's cookie (CSRF) → rejected.
	req := httptest.NewRequest(http.MethodPost, "https://getafjob.example/x", strings.NewReader("a"))
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("cross-site POST = %d, want 403", rec.Code)
	}

	// Same origin, but the body is too large → 413.
	req = httptest.NewRequest(http.MethodPost, "https://getafjob.example/x", strings.NewReader(strings.Repeat("a", 64)))
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("large body = %d, want 413", rec.Code)
	}
}

func TestRunShutsDownOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	srv := NewServer("127.0.0.1:0", http.NotFoundHandler(), quiet())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, srv, time.Second, quiet()) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

func TestProtectUploadLimit(t *testing.T) {
	read := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
		}
	})
	h := Protect(read, 10, false, Upload{Path: "/documents", Max: 100})
	for _, c := range []struct {
		path string
		size int
		want int
	}{{"/profile", 11, 413}, {"/documents", 100, 200}, {"/documents", 101, 413}, {"/documents/x", 11, 413}} {
		req := httptest.NewRequest(http.MethodPost, c.path, strings.NewReader(strings.Repeat("x", c.size)))
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != c.want {
			t.Errorf("%s with %d bytes: %d", c.path, c.size, rec.Code)
		}
	}
}
