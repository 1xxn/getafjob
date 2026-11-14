package views

import (
	"context"
	"maps"
	"strings"
	"time"

	"github.com/a-h/templ"
)

// plDateTime formats an optional time for a datetime-local input in the instance zone; plDate formats a date
// input (dates are stored as UTC midnight and need no zone).
func plDateTime(ctx context.Context, t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.In(Zone(ctx)).Format("2006-01-02T15:04")
}

func plDate(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format("2006-01-02")
}

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

// attrs merges attribute sets; later sets win.
func attrs(sets ...templ.Attributes) templ.Attributes {
	out := templ.Attributes{}
	for _, s := range sets {
		maps.Copy(out, s)
	}
	return out
}

// nonEmpty drops empty strings.
func nonEmpty(vs ...string) []string {
	var out []string
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			out = append(out, v)
		}
	}
	return out
}

// descBlock is a paragraph or a bullet list of a job description.
type descBlock struct {
	Text  string
	List  bool
	Items []string
}

// descBlocks turns a plain text description into paragraphs and lists: blank lines end a paragraph, lines that
// start with a bullet sign become list items.
func descBlocks(s string) []descBlock {
	var out []descBlock
	var para []string
	flush := func() {
		if len(para) > 0 {
			out = append(out, descBlock{Text: strings.Join(para, " ")})
			para = nil
		}
	}
	for _, line := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		item, bullet := cutBullet(line)
		switch {
		case line == "":
			flush()
		case bullet:
			flush()
			if n := len(out); n > 0 && out[n-1].List {
				out[n-1].Items = append(out[n-1].Items, item)
			} else {
				out = append(out, descBlock{List: true, Items: []string{item}})
			}
		default:
			para = append(para, line)
		}
	}
	flush()
	return out
}

func cutBullet(line string) (string, bool) {
	for _, b := range []string{"- ", "* ", "• ", "· ", "– "} {
		if rest, ok := strings.CutPrefix(line, b); ok && strings.TrimSpace(rest) != "" {
			return strings.TrimSpace(rest), true
		}
	}
	return line, false
}
