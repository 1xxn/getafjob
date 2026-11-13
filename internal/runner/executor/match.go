// Package executor fills a planned set of values into a form, field by field, and verifies each one. It refuses
// values without provenance (ADR 0013), fields that changed since planning and frames of foreign origins.
package executor

import (
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// sourceRx is the provenance of a value (ADR 0013): a profile field, an answer, a text block or a document.
var sourceRx = regexp.MustCompile(`^(profile\.[a-z][a-z0-9_.]{0,60}|(answer|textblock)#(` + idRx + `)|document#` + uuidRx + `)$`)

// Stored IDs: UUIDs (answers, text blocks, documents) or numbers.
const (
	uuidRx = `[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`
	idRx   = uuidRx + `|[0-9]{1,18}`
)

// Normalize lowercases, folds umlauts and accents and keeps words separated by single spaces.
func Normalize(s string) string { return normalize(s) }

// ValidSource reports whether s names where a value comes from.
func ValidSource(s string) bool { return sourceRx.MatchString(s) }

// synonyms are answers that mean the same across languages and forms: yes/no, gender answers and the countries
// and languages forms ask about most.
var synonyms = [][]string{
	{"yes", "ja", "y", "true", "oui", "si"},
	{"no", "nein", "n", "false", "non"},
	{"male", "mann", "maennlich", "man", "herr"},
	{"female", "frau", "weiblich", "woman"},
	{"diverse", "divers", "non binary", "nonbinary", "nicht binaer"},
	{"prefer not to say", "keine angabe", "decline to answer", "i prefer not to answer", "i dont wish to answer",
		"i do not wish to answer", "ich moechte keine angabe machen", "decline to self identify", "prefer not to answer"},
	{"germany", "deutschland", "de", "deu"},
	{"austria", "oesterreich", "at", "aut"},
	{"switzerland", "schweiz", "ch", "che"},
	{"netherlands", "niederlande", "the netherlands", "holland", "nl"},
	{"belgium", "belgien", "be"},
	{"luxembourg", "luxemburg", "lu"},
	{"france", "frankreich", "fr"},
	{"italy", "italien", "it"},
	{"spain", "spanien", "es"},
	{"portugal", "pt"},
	{"poland", "polen", "pl"},
	{"czech republic", "czechia", "tschechien", "tschechische republik", "cz"},
	{"denmark", "daenemark", "dk"},
	{"sweden", "schweden", "se"},
	{"norway", "norwegen"},
	{"finland", "finnland", "fi"},
	{"ireland", "irland", "ie"},
	{"united kingdom", "uk", "great britain", "vereinigtes koenigreich", "grossbritannien", "gb"},
	{"united states", "usa", "us", "united states of america", "vereinigte staaten", "vereinigte staaten von amerika"},
	{"german", "deutsch"},
	{"english", "englisch"},
	{"french", "franzoesisch"},
	{"spanish", "spanisch"}, //nolint:misspell // German word
	{"italian", "italienisch"},
	{"dutch", "niederlaendisch"},
	{"polish", "polnisch"},
	{"turkish", "tuerkisch"},
	{"russian", "russisch"},
	{"arabic", "arabisch"},
	{"chinese", "chinesisch"},
}

var umlauts = strings.NewReplacer("ä", "ae", "ö", "oe", "ü", "ue", "ß", "ss", "Ä", "ae", "Ö", "oe", "Ü", "ue")

// normalize lowercases, folds umlauts and accents and keeps letters and digits separated by single spaces.
func normalize(s string) string {
	s = umlauts.Replace(strings.ToLower(s))
	var b strings.Builder
	space := false
	for _, r := range norm.NFKD.String(s) {
		switch {
		case unicode.Is(unicode.Mn, r), r == 0x27, r == 0x2019: // accents and apostrophes ("don't" is "dont")
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteRune(r)
			space = false
		default:
			space = true
		}
	}
	return b.String()
}

func synonymGroup(s string) int {
	for i, g := range synonyms {
		for _, w := range g {
			if s == w {
				return i
			}
		}
	}
	return -1
}

// Match finds the option that means want: equal after normalization, the same answer in another language
// (yes/ja), a unique option that starts with want or that want starts with, or a clearly best word overlap.
// It never guesses between close candidates: ok is false then.
func Match(want string, options []string) (int, bool) {
	w := normalize(want)
	if w == "" {
		return -1, false
	}
	norms := make([]string, len(options))
	for i, o := range options {
		norms[i] = normalize(o)
		if norms[i] == w {
			return i, true
		}
	}
	if g := synonymGroup(w); g >= 0 {
		if i, ok := unique(norms, func(o string) bool { return synonymGroup(o) == g }); ok {
			return i, true
		}
		// "Germany" for "Deutschland +49": an option that starts with a synonym
		if i, ok := unique(norms, func(o string) bool {
			for _, syn := range synonyms[g] {
				if len(syn) >= 3 && strings.HasPrefix(o, syn+" ") {
					return true
				}
			}
			return false
		}); ok {
			return i, true
		}
	}
	if len(w) >= 3 {
		if i, ok := unique(norms, func(o string) bool {
			return len(o) >= 3 && (strings.HasPrefix(o, w+" ") || strings.HasPrefix(w, o+" "))
		}); ok {
			return i, true
		}
	}
	best, second, at := 0.0, 0.0, -1
	for i, o := range norms {
		sc := overlap(w, o)
		switch {
		case sc > best:
			best, second, at = sc, best, i
		case sc > second:
			second = sc
		}
	}
	if best >= 0.6 && best-second >= 0.2 {
		return at, true
	}
	return -1, false
}

func unique(norms []string, ok func(string) bool) (int, bool) {
	at := -1
	for i, o := range norms {
		if ok(o) {
			if at >= 0 {
				return -1, false
			}
			at = i
		}
	}
	return at, at >= 0
}

// overlap is the Jaccard similarity of the word sets of a and b.
func overlap(a, b string) float64 {
	wa, wb := strings.Fields(a), strings.Fields(b)
	if len(wa) == 0 || len(wb) == 0 {
		return 0
	}
	set := map[string]int{}
	for _, w := range wa {
		set[w] |= 1
	}
	for _, w := range wb {
		set[w] |= 2
	}
	both := 0
	for _, v := range set {
		if v == 3 {
			both++
		}
	}
	return float64(both) / float64(len(set))
}
