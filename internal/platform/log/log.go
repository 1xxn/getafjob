// Package log sets up slog. Logs contain no personal data: attributes with sensitive names
// (email, phone, password, token, cookie …) are redacted whatever their value: a safety net below the net,
// because code should not log such values in the first place.
package log

import (
	"context"
	"io"
	"log/slog"
	"strings"

	"go.opentelemetry.io/otel/trace"
)

// Redacted replaces redacted values.
const Redacted = "[redacted]"

// sensitive lists parts of attribute names whose values never belong in a log (English and German).
var sensitive = []string{
	"password", "passwd", "secret", "token", "cookie", "authorization", "api_key", "apikey", "private_key",
	"email", "e_mail", "phone", "telefon", "address", "adresse", "birth", "salary", "gehalt", "iban",
	"session", "otp", "code_value", "credential", "passkey",
}

// Sensitive reports whether an attribute name hints at a secret or personal data.
func Sensitive(key string) bool {
	k := strings.ToLower(strings.ReplaceAll(key, "-", "_"))
	for _, s := range sensitive {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}

// New creates a logger in the requested format ("json" or "text").
func New(w io.Writer, level slog.Leveler, format string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: level, ReplaceAttr: replace}
	var h slog.Handler
	if format == "text" {
		h = slog.NewTextHandler(w, opts)
	} else {
		h = slog.NewJSONHandler(w, opts)
	}
	return slog.New(traced{h})
}

// traced adds trace_id and span_id of the active span to every record (logs and traces line up in Grafana).
type traced struct{ slog.Handler }

func (h traced) Handle(ctx context.Context, r slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		r.AddAttrs(slog.String("trace_id", sc.TraceID().String()), slog.String("span_id", sc.SpanID().String()))
	}
	return h.Handler.Handle(ctx, r)
}

func (h traced) WithAttrs(as []slog.Attr) slog.Handler { return traced{h.Handler.WithAttrs(as)} }

func (h traced) WithGroup(name string) slog.Handler { return traced{h.Handler.WithGroup(name)} }

func replace(_ []string, a slog.Attr) slog.Attr {
	if a.Value.Kind() != slog.KindGroup && Sensitive(a.Key) {
		return slog.String(a.Key, Redacted)
	}
	return a
}
