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

// questionText is the label of a form question for people: the runner joins label parts with " | " and adds the
// placeholder as "(placeholder: ...)"; the raw text stays the key of the answer bank.
func questionText(q string) string {
	var keep, ph []string
	for _, p := range strings.Split(q, " | ") {
		if v, ok := strings.CutPrefix(p, "(placeholder: "); ok {
			ph = append(ph, strings.TrimSuffix(v, ")"))
			continue
		}
		keep = append(keep, p)
	}
	if len(keep) == 0 {
		return strings.Join(ph, " ")
	}
	return strings.Join(keep, " ")
}
