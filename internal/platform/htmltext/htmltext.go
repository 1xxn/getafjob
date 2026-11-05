// Package htmltext turns foreign HTML (job descriptions, mails) into plain text. Input is capped so a crafted
// document cannot keep the parser busy.
package htmltext

import (
	"html"
	"regexp"
	"strings"

	xhtml "golang.org/x/net/html"
)

// MaxInput caps the input in bytes; job descriptions and mails are far smaller.
const MaxInput = 2_000_000

var block = map[string]bool{
	"p": true, "div": true, "br": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"tr": true, "section": true, "ul": true, "ol": true, "article": true, "header": true, "footer": true, "table": true,
}

var (
	spaces = regexp.MustCompile(`[ \t\x{00a0}]+`)
	blank  = regexp.MustCompile(`\n[ \t]*\n[\s]*`)
)

// Text extracts readable text: blocks become line breaks, list items bullets, scripts and styles are dropped.
func Text(raw string) string {
	if raw == "" {
		return ""
	}
	if len(raw) > MaxInput {
		raw = raw[:MaxInput]
	}
	// Some APIs deliver escaped HTML ("&lt;p&gt;").
	if strings.Contains(raw, "&lt;") && !strings.Contains(raw, "<") {
		raw = html.UnescapeString(raw)
	}
	z := xhtml.NewTokenizer(strings.NewReader(raw))
	var b strings.Builder
	skip := 0
	for {
		switch z.Next() {
		case xhtml.ErrorToken:
			out := spaces.ReplaceAllString(b.String(), " ")
			out = blank.ReplaceAllString(out, "\n\n")
			lines := strings.Split(out, "\n")
			for i, l := range lines {
				lines[i] = strings.TrimSpace(l)
			}
			return strings.TrimSpace(strings.Join(lines, "\n"))
		case xhtml.StartTagToken, xhtml.SelfClosingTagToken:
			name, _ := z.TagName()
			switch tag := string(name); {
			case tag == "script" || tag == "style" || tag == "noscript" || tag == "template":
				if z.Token().Type == xhtml.StartTagToken {
					skip++
				}
			case tag == "li":
				b.WriteString("\n• ")
			case block[tag]:
				b.WriteString("\n")
			}
		case xhtml.EndTagToken:
			name, _ := z.TagName()
			switch tag := string(name); {
			case tag == "script" || tag == "style" || tag == "noscript" || tag == "template":
				if skip > 0 {
					skip--
				}
			case block[tag] || tag == "li":
				b.WriteString("\n")
			}
		case xhtml.TextToken:
			if skip == 0 {
				b.Write(z.Text())
			}
		case xhtml.CommentToken, xhtml.DoctypeToken:
		}
	}
}
