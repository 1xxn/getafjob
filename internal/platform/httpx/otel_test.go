package httpx

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gotel "go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestInstrumentNamesSpansByRoute(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	prev := gotel.GetTracerProvider()
	gotel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	t.Cleanup(func() { gotel.SetTracerProvider(prev) })

	mux := http.NewServeMux()
	mux.HandleFunc("GET /signin/{token}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {})
	h := Instrument(Protect(Routes(mux), 1<<10, false))
	for _, path := range []string{"/signin/secret-token-123?next=secret-token-q", "/healthz", "/missing/secret-token-404"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}

	spans := rec.Ended()
	if len(spans) != 2 {
		t.Fatalf("spans = %d (health checks are not traced)", len(spans))
	}
	if spans[0].Name() != "GET /signin/{token}" || spans[1].Name() != "GET" {
		t.Errorf("names = %q, %q", spans[0].Name(), spans[1].Name())
	}
	for _, s := range spans {
		for _, a := range s.Attributes() {
			if v := a.Value.String(); len(v) > 0 && strings.Contains(v, "secret-token") {
				t.Errorf("span %q carries the raw path in %s", s.Name(), a.Key)
			}
		}
	}
}

func TestRequestLogUsesRoutePatternAndOmitsTokens(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /login/link/{token}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("GET /invite/{token}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("GET /static/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

	h := Instrument(RequestLog(Protect(Routes(mux), 1<<10, false), logger))
	for _, path := range []string{
		"/login/link/secret-login-token-123?next=secret-next",
		"/invite/secret-invite-token-456?email=user@example.com",
		"/static/app.js?v=123",
		"/healthz",
		"/missing/secret-404-token",
	} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}

	out := buf.String()
	for _, leaked := range []string{"secret-login-token", "secret-invite-token", "secret-404-token", "secret-next", "user@example.com"} {
		if strings.Contains(out, leaked) {
			t.Fatalf("request log leaked %q:\n%s", leaked, out)
		}
	}
	for _, want := range []string{
		`level=INFO msg="http request" route="GET /login/link/{token}" status=200`,
		`level=INFO msg="http request" route="GET /invite/{token}" status=200`,
		`level=DEBUG msg="http request" route="GET /static/" status=200`,
		`level=DEBUG msg="http request" route="GET /healthz" status=204`,
		`level=INFO msg="http request" route=GET status=404`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("request log missing %q in:\n%s", want, out)
		}
	}
}
