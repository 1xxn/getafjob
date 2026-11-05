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
		ids = append(ids, "f-"+f.Name+"-hint")
	}
	if f.Error != "" {
		ids = append(ids, "f-"+f.Name+"-error")
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
