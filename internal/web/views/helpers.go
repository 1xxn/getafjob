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

func describedBy(f FieldProps) string {
	var ids []string
	if f.Hint != "" {
		ids = append(ids, fieldID(f)+"-hint")
	}
	if f.Error != "" {
		ids = append(ids, fieldID(f)+"-error")
	}
	return strings.Join(ids, " ")
}

func noticeRole(kind string) string {
	if kind == "error" {
		return "alert"
	}
	return "status"
}

func statusGlyph(status string) string {
	switch status {
	case "done":
		return "✓"
	case "failed":
		return "✗"
	case "your_turn":
		return "●"
	default:
		return "○"
	}
}

// When formats a time for the reader's locale (UTC; the UI has no time zone setting yet).
func When(ctx context.Context, t time.Time) string {
	if Locale(ctx) == "de" {
		return t.UTC().Format("02.01.2006 15:04") + " UTC"
	}
	return t.UTC().Format("Jan 2, 2006 15:04") + " UTC"
}

func or(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// Size formats a byte count (KB, MB with one decimal).
func Size(n int) string {
	switch {
	case n >= 1<<20:
		return strconv.FormatFloat(float64(n)/(1<<20), 'f', 1, 64) + " MB"
	case n >= 1<<10:
		return strconv.Itoa(n>>10) + " KB"
	default:
		return strconv.Itoa(n) + " B"
	}
}

func fieldID(f FieldProps) string {
	if f.ID != "" {
		return f.ID
	}
	return "f-" + f.Name
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

// pageLink loads another page of the job list into #results.
func pageLink(ctx context.Context, url string) templ.Attributes {
	return HX(ctx, "hx-get", url, "hx-target", "#results", "hx-swap", "outerHTML", "hx-push-url", "true")
}

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

// firstLive picks the run the live indicator links to: one that waits for the user, else the first.
func firstLive(runs []ActiveRunItem) string {
	for _, r := range runs {
		if r.Waiting {
			return r.AppID
		}
	}
	return runs[0].AppID
}
