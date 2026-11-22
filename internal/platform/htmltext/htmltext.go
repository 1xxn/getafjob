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
	// Some APIs deliver escaped HTML ("&lt;p&gt;", "&#60;p&#62;", "&LT;p&GT;").
	if !strings.Contains(raw, "<") {
		if u := html.UnescapeString(raw); strings.Contains(u, "<") {
			raw = u
		}
	}
	z := xhtml.NewTokenizer(strings.NewReader(raw))
	var b strings.Builder
	skip := 0
	openTag := false
	writeStr := func(s string) {
		b.WriteString(s)
		for i := 0; i < len(s); i++ {
			switch s[i] {
			case '<':
				openTag = true
			case '>', ' ', '\t', '\n':
				openTag = false
			}
		}
	}
	for {
		tt := z.Next()
		// A dropped tag must not join an open "<..." before it with the text after it into a new tag ("<<a>script", "&ltscrip<A>t").
		if tt != xhtml.TextToken && tt != xhtml.ErrorToken && openTag {
			b.WriteByte(' ')
			openTag = false
		}
		switch tt {
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
				writeStr("\n• ")
			case block[tag]:
				writeStr("\n")
			}
		case xhtml.EndTagToken:
			name, _ := z.TagName()
			switch tag := string(name); {
			case tag == "script" || tag == "style" || tag == "noscript" || tag == "template":
				if skip > 0 {
					skip--
				}
			case block[tag] || tag == "li":
				writeStr("\n")
			}
		case xhtml.TextToken:
			if skip == 0 {
				txt := z.Text()
				b.Write(txt)
				for _, c := range txt {
					switch c {
					case '<':
						openTag = true
					case '>', ' ', '\t', '\n':
						openTag = false
					}
				}
			}
		case xhtml.CommentToken, xhtml.DoctypeToken:
		}
	}
}
