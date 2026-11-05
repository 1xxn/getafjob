package views

import (
	"strconv"
	"strings"
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
