package log

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

func TestSensitiveKeysAreRedacted(t *testing.T) {
	for _, format := range []string{"json", "text"} {
		var buf bytes.Buffer
		l := New(&buf, slog.LevelDebug, format)
		l.Info("event",
			"email", "max.mustermann@example.com",
			"user_phone", "+49 30 1234567",
			"X-Api-Key", "abc",
			"session_id", "s1",
			slog.Group("req", "authorization", "Bearer xyz", "path", "/healthz"),
			"count", 3,
		)
		out := buf.String()
		for _, leak := range []string{"max.mustermann", "1234567", "abc", "s1", "Bearer xyz"} {
			if strings.Contains(out, leak) {
				t.Errorf("%s: %q leaked: %s", format, leak, out)
			}
		}
		for _, keep := range []string{"/healthz", "3"} {
			if !strings.Contains(out, keep) {
				t.Errorf("%s: %q missing: %s", format, keep, out)
			}
		}
	}
}

func TestSensitive(t *testing.T) {
	cases := map[string]bool{
		"email": true, "Email": true, "db_password": true, "csrf-token": true, "phone_number": true,
		"user_id": false, "path": false, "status": false, "duration": false, "ats": false,
	}
	for k, want := range cases {
		if got := Sensitive(k); got != want {
			t.Errorf("Sensitive(%q) = %v, want %v", k, got, want)
		}
	}
}

func TestTraceCorrelation(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, slog.LevelInfo, "json").With("role", "worker")
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1, 2, 3}, SpanID: trace.SpanID{4, 5}, TraceFlags: trace.FlagsSampled,
	})
	l.InfoContext(trace.ContextWithSpanContext(context.Background(), sc), "traced")
	l.InfoContext(context.Background(), "plain")
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"trace_id":"01020300000000000000000000000000"`) ||
		!strings.Contains(lines[0], `"span_id":"0405000000000000"`) || strings.Contains(lines[1], "trace_id") {
		t.Errorf("logs = %s", buf.String())
	}
}
