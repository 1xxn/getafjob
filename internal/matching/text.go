package matching

import (
	"strings"

	"github.com/1xxn/getafjob/internal/platform/vocab"
)

// maxText bounds the text one job is matched on; descriptions are far shorter.
const maxText = 100_000

func normalize(s string) string { return vocab.Normalize(s) }

// term is a normalized search term. Plain substring tests, no patterns: a user term can never be slow.
type term string

// newTerm normalizes a user or dictionary term; empty when nothing is left.
func newTerm(s string) term { return term(strings.TrimSpace(normalize(s))) }

// in reports whether t occurs in the normalized text. Short terms must stand alone ("go" not in "google"); a term
// with a short first word must start a word ("it support" not in "audit support"); longer terms may sit inside
// German compounds ("entwickler" in "softwareentwickler").
func (t term) in(text string) bool {
	if t == "" {
		return false
	}
	s := string(t)
	first, _, _ := strings.Cut(s, " ")
	switch {
	case len(s) <= 4:
		return strings.Contains(text, " "+s+" ")
	case len(first) <= 3:
		return strings.Contains(text, " "+s)
	default:
		return strings.Contains(text, s)
	}
}

// anyIn returns the first term found in text.
func anyIn(terms []term, text string) (term, bool) {
	for _, t := range terms {
		if t.in(text) {
			return t, true
		}
	}
	return "", false
}

func terms(list []string) []term {
	out := make([]term, 0, len(list))
	for _, s := range list {
		if t := newTerm(s); t != "" {
			out = append(out, t)
		}
	}
	return out
}
