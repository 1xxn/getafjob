package views

import (
	"context"
	"strconv"
	"strings"
	"time"
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
