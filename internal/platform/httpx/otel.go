package httpx

import (
	"net/http"
	"strings"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// Instrument records a span and the request metrics (http.server.request.duration) per request. Wrap the outermost
// handler with it and the mux with Routes, so spans and metrics carry the route pattern, never the raw path (it
// may contain tokens). Health checks are left out.
func Instrument(next http.Handler) http.Handler {
	return otelhttp.NewHandler(next, "http",
		otelhttp.WithFilter(func(r *http.Request) bool { return r.URL.Path != "/healthz" && r.URL.Path != "/readyz" }),
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string { return r.Method }))
}

// Routes names span and metrics after the pattern the mux matched ("GET /jobs/{id}") and replaces the raw path and
// query in the span (sign-in links carry tokens) with the path template, or nothing for unmatched requests.
func Routes(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer name(r) // also after a panic, before the span ends
		mux.ServeHTTP(w, r)
	})
}

func name(r *http.Request) {
	span := trace.SpanFromContext(r.Context())
	_, tmpl, _ := strings.Cut(r.Pattern, " ")
	if tmpl == "" {
		tmpl = r.Pattern
	}
	span.SetAttributes(attribute.String("url.path", tmpl), attribute.String("url.query", ""))
	if r.Pattern == "" {
		return
	}
	route := attribute.String("http.route", r.Pattern)
	span.SetName(r.Pattern)
	span.SetAttributes(route)
	if l, ok := otelhttp.LabelerFromContext(r.Context()); ok {
		l.Add(route)
	}
}
