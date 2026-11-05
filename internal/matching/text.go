package matching

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// maxText bounds the text one job is matched on; descriptions are far shorter.
const maxText = 100_000

// symbols keeps the names of skills that are mostly punctuation.
var symbols = strings.NewReplacer("c++", " cpp ", "c#", " csharp ", "f#", " fsharp ", ".net", " dotnet ",
	"node.js", " nodejs ", "vue.js", " vue ", "next.js", " nextjs ", "react.js", " react ", "ci/cd", " cicd ")

// umlauts folds German letters the way people type them without a German keyboard.
var umlauts = strings.NewReplacer("ä", "ae", "ö", "oe", "ü", "ue", "ß", "ss")

// normalize lowercases text, folds umlauts and accents and turns everything but letters and digits into single
// spaces, with one space on each side: " senior go developer m w d ".
func normalize(s string) string {
	if len(s) > maxText {
		s = s[:maxText]
	}
	s = umlauts.Replace(symbols.Replace(strings.ToLower(s)))
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte(' ')
	space := true
	for _, r := range norm.NFKD.String(s) {
		switch {
		case unicode.Is(unicode.Mn, r):
			continue // combining accent
		case r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			b.WriteRune(r)
			space = false
		case !space:
			b.WriteByte(' ')
			space = true
		}
	}
	if !space {
		b.WriteByte(' ')
	}
	return b.String()
}

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
