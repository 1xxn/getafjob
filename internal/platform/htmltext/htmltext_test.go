package htmltext

import (
	"html"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestText(t *testing.T) {
	cases := map[string]string{
		"":                                     "",
		"plain":                                "plain",
		"<p>Hello <b>Max</b></p><p>Second</p>": "Hello Max\n\nSecond",
		"<ul><li>Go</li><li>Kubernetes</li></ul>": "• Go\n\n• Kubernetes",
		"<script>alert(1)</script>Visible":        "Visible",
		"<style>p{}</style><p>Text</p>":           "Text",
		"&lt;p&gt;escaped &amp; fine&lt;/p&gt;":   "escaped & fine",
		"a&nbsp;&nbsp;b":                          "a b",
		"line<br>break":                           "line\nbreak",
		"<![CTA[ broken":                          "",
		"&#60;script&#62;x&#60;/script&#62;Shown": "Shown",
		"&LT;b&GT;Bold&LT;/b&GT;":                 "Bold",
		"<div>   lots   of\t\tspace   </div>":     "lots of space",
	}
	for in, want := range cases {
		if got := Text(in); got != want {
			t.Errorf("Text(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTextCapsInput(t *testing.T) {
	big := strings.Repeat("<p>x</p>", MaxInput)
	if got := Text(big); len(got) > MaxInput {
		t.Fatalf("output %d bytes", len(got))
	}
}

// unescapeAll decodes entities until nothing changes; text can only show "<script" if the input spells it in some
// escaping depth.
func unescapeAll(s string) string {
	for range 8 {
		u := html.UnescapeString(s)
		if u == s {
			break
		}
		s = u
	}
	return s
}

func FuzzText(f *testing.F) {
	for _, s := range []string{"<p>a</p>", "<li>x", "<script>", "&lt;b&gt;", "<![CDATA[x]]>", "<a href='x'>y</a>",
		"&#60;script&#62;x&#60;/script&#62;", "&LT;script&GT;x", "&amp;lt;script&amp;gt;"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		out := Text(s)
		if strings.Contains(out, "<script") && !strings.Contains(unescapeAll(s), "<script") {
			t.Fatalf("script tag appeared from nowhere: %q", out)
		}
		if utf8.ValidString(s) && !utf8.ValidString(out) {
			t.Fatalf("invalid UTF-8 output for valid input %q", s)
		}
	})
}
