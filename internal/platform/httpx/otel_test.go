package httpx

import (
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
