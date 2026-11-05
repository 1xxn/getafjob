package sources

import (
	"bytes"
	"encoding/json"
	"strings"

	xhtml "golang.org/x/net/html"

	"github.com/1xxn/getafjob/internal/discovery"
	"github.com/1xxn/getafjob/internal/platform/htmltext"
)

// Limits for JSON-LD on foreign pages.
const (
	maxLDBlocks   = 20
	maxLDBlock    = 2 << 20
	maxLDDepth    = 32
	maxLDPostings = 1000
)

// JobPosting is a schema.org JobPosting read from JSON-LD.
type JobPosting struct {
	ID          string
	Title       string
	Company     string
	Locations   []string
	Remote      bool
	Description string
	Posted      string
	URL         string
}

// JobPostings reads the schema.org JobPostings from the JSON-LD blocks of an HTML page: single objects, arrays,
// @graph and ItemList entries. Broken blocks are skipped; sizes and nesting are capped.
func JobPostings(page []byte) []JobPosting {
	var out []JobPosting
	for _, block := range ldBlocks(page) {
		dec := json.NewDecoder(bytes.NewReader(block))
		dec.UseNumber()
		var v any
		if dec.Decode(&v) != nil {
			continue
		}
		collect(v, 0, &out)
		if len(out) >= maxLDPostings {
			return out[:maxLDPostings]
		}
	}
	return out
}

// ldBlocks returns the contents of <script type="application/ld+json"> elements.
func ldBlocks(page []byte) [][]byte {
	if len(page) > MaxBody {
		page = page[:MaxBody]
	}
	z := xhtml.NewTokenizer(bytes.NewReader(page))
	var out [][]byte
	inLD := false
	for len(out) < maxLDBlocks {
		switch z.Next() {
		case xhtml.ErrorToken:
			return out
		case xhtml.StartTagToken:
			name, hasAttr := z.TagName()
			inLD = false
			if string(name) != "script" {
				continue
			}
			for hasAttr {
				var k, v []byte
				k, v, hasAttr = z.TagAttr()
				if string(k) == "type" && strings.Contains(strings.ToLower(string(v)), "ld+json") {
					inLD = true
				}
			}
		case xhtml.TextToken:
			if inLD {
				if t := z.Text(); len(t) <= maxLDBlock {
					out = append(out, bytes.Clone(t))
				}
				inLD = false
			}
		case xhtml.EndTagToken, xhtml.SelfClosingTagToken, xhtml.CommentToken, xhtml.DoctypeToken:
			inLD = false
		}
	}
	return out
}

func collect(v any, depth int, out *[]JobPosting) {
	if depth > maxLDDepth || len(*out) >= maxLDPostings {
		return
	}
	switch x := v.(type) {
	case []any:
		for _, e := range x {
			collect(e, depth+1, out)
		}
	case map[string]any:
		if isType(x["@type"], "JobPosting") {
			if p, ok := jobPosting(x); ok {
				*out = append(*out, p)
			}
			return
		}
		for _, key := range []string{"@graph", "itemListElement", "item", "mainEntity"} {
			if e, ok := x[key]; ok {
				collect(e, depth+1, out)
			}
		}
	}
}

func isType(v any, want string) bool {
	switch x := v.(type) {
	case string:
		return x == want || strings.HasSuffix(x, "/"+want)
	case []any:
		for _, e := range x {
			if isType(e, want) {
				return true
			}
		}
	}
	return false
}

// text reads a value that may be a string, a number, an object with name/value or a list (first entry).
func text(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case json.Number:
		return x.String()
	case map[string]any:
		for _, k := range []string{"name", "value", "@value", "@id"} {
			if s := text(x[k]); s != "" {
				return s
			}
		}
	case []any:
		for _, e := range x {
			if s := text(e); s != "" {
				return s
			}
		}
	}
	return ""
}

// identifier reads schema.org identifiers: a string, or a PropertyValue whose value (not its name) is the ID.
func identifier(v any) string {
	switch x := v.(type) {
	case map[string]any:
		return or(text(x["value"]), text(x["@value"]))
	case []any:
		for _, e := range x {
			if s := identifier(e); s != "" {
				return s
			}
		}
		return ""
	}
	return text(v)
}

func list(v any) []any {
	if l, ok := v.([]any); ok {
		return l
	}
	if v == nil {
		return nil
	}
	return []any{v}
}

func place(v any) string {
	m, ok := v.(map[string]any)
	if !ok {
		return text(v)
	}
	if a, ok := m["address"].(map[string]any); ok {
		return join(", ", text(a["addressLocality"]), text(a["addressRegion"]), text(a["addressCountry"]))
	}
	return or(text(m["address"]), text(m["name"]))
}

func jobPosting(m map[string]any) (JobPosting, bool) {
	p := JobPosting{
		ID:          identifier(m["identifier"]),
		Title:       text(m["title"]),
		Company:     text(m["hiringOrganization"]),
		Description: htmltext.Text(text(m["description"])),
		Posted:      day(text(m["datePosted"])),
		URL:         or(text(m["url"]), text(m["sameAs"])),
	}
	for _, l := range list(m["jobLocation"]) {
		if s := place(l); s != "" {
			p.Locations = append(p.Locations, s)
		}
	}
	if strings.EqualFold(text(m["jobLocationType"]), "TELECOMMUTE") {
		p.Remote = true
		var where []string
		for _, r := range list(m["applicantLocationRequirements"]) {
			where = append(where, text(r))
		}
		p.Locations = append(p.Locations, "Remote"+parens(join(", ", where...)))
	}
	if len(p.Title) > 500 {
		return JobPosting{}, false
	}
	return p, p.Title != ""
}

func parens(s string) string {
	if s == "" {
		return ""
	}
	return " (" + s + ")"
}

// fill copies a JobPosting into a posting, keeping the posting's identity.
func (j JobPosting) fill(p *discovery.Posting, company string) {
	p.Title = j.Title
	p.Company = or(company, j.Company)
	p.Location = join("; ", j.Locations...)
	p.Description = j.Description
	p.Posted = or(j.Posted, p.Posted)
	if j.Remote {
		p.Workplace = discovery.WorkplaceRemote
	}
}
