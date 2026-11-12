package views

import "strings"

// sourceKey names where a filled value came from (its provenance) in words; the raw source stays internal.
func sourceKey(source string) string {
	kind := source
	if i := strings.IndexAny(source, "#.:"); i >= 0 {
		kind = source[:i]
	}
	switch kind {
	case "profile", "answer", "document", "textblock", "user":
		return "apps.source." + kind
	}
	return "apps.source.other"
}
