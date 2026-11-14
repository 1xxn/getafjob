package views

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"

	"github.com/1xxn/getafjob/internal/web/views/ui"
)

func itoa(n int) string { return strconv.Itoa(n) }

// When formats a time for the reader's locale in the instance time zone, with the zone's abbreviation.
func When(ctx context.Context, t time.Time) string {
	if Locale(ctx) == "de" {
		return t.In(Zone(ctx)).Format("02.01.2006 15:04 MST")
	}
	return t.In(Zone(ctx)).Format("Jan 2, 2006 15:04 MST")
}

func or(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// Size formats a byte count (KB; MB and GB with one decimal).
func Size(n int) string {
	switch {
	case n >= 1<<30:
		return strconv.FormatFloat(float64(n)/(1<<30), 'f', 1, 64) + " GB"
	case n >= 1<<20:
		return strconv.FormatFloat(float64(n)/(1<<20), 'f', 1, 64) + " MB"
	case n >= 1<<10:
		return strconv.Itoa(n>>10) + " KB"
	default:
		return strconv.Itoa(n) + " B"
	}
}

func i64(n int64) string { return strconv.FormatInt(n, 10) }

// points shows score points with their sign.
func points(n int) string {
	if n > 0 {
		return "+" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// htmxConfig: only the reviewed extensions, no indicator styles (app.css has them), view transitions on swaps.
const htmxConfig = `extensions:"hx-csp,hx-sse",includeIndicatorCSS:false,transitions:true`

// HX returns htmx attributes as name/value pairs plus the response's hx-nonce; elements without it are inert
// under hx-csp. Use it for every element with hx-* attributes.
func HX(ctx context.Context, kv ...string) templ.Attributes { return ui.HX(ctx, kv...) }

// Amount groups the digits of a whole number for the reader's locale: 90,000 or 90.000.
func Amount(ctx context.Context, digits string) string {
	if digits == "" || strings.Trim(digits, "0123456789") != "" {
		return digits
	}
	sep := ","
	if Locale(ctx) == "de" {
		sep = "."
	}
	var b strings.Builder
	for i, r := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteString(sep)
		}
		b.WriteRune(r)
	}
	return b.String()
}

// anyWaiting reports whether one of the live runs waits for the user.
func anyWaiting(runs []ActiveRunItem) bool {
	for _, r := range runs {
		if r.Waiting {
			return true
		}
	}
	return false
}

// DayLabel is a short date for the reader's locale, as the dashboard shows it.
func DayLabel(ctx context.Context, t time.Time) string { return dayLabel(ctx, t) }
