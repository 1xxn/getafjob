package views

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/1xxn/getafjob/internal/platform/i18n"
)

func TestAmount(t *testing.T) {
	en, de := WithLocale(t.Context(), i18n.Locale("en")), WithLocale(t.Context(), i18n.Locale("de"))
	for _, tc := range []struct{ in, en, de string }{
		{"90000", "90,000", "90.000"}, {"1250000", "1,250,000", "1.250.000"}, {"500", "500", "500"}, {"", "", ""}, {"9x", "9x", "9x"},
	} {
		if got := Amount(en, tc.in); got != tc.en {
			t.Errorf("en %q = %q", tc.in, got)
		}
		if got := Amount(de, tc.in); got != tc.de {
			t.Errorf("de %q = %q", tc.in, got)
		}
	}
}

// Inline event handlers never run under the nonce CSP; every interaction goes through htmx or plain links.
func TestTemplatesHaveNoInlineHandlers(t *testing.T) {
	files, err := filepath.Glob("*.templ")
	more, _ := filepath.Glob("ui/*.templ")
	files = append(files, more...)
	if err != nil || len(files) == 0 {
		t.Fatalf("templates: %v", err)
	}
	rx := regexp.MustCompile(`\son[a-z]+=`)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if loc := rx.FindIndex(b); loc != nil {
			t.Errorf("%s: inline handler near %q", f, b[loc[0]:min(loc[1]+30, len(b))])
		}
	}
}

// Inline styles are blocked by the CSP (style-src 'self'); use classes or native elements (meter, progress).
func TestTemplatesHaveNoInlineStyles(t *testing.T) {
	files, _ := filepath.Glob("*.templ")
	more, _ := filepath.Glob("ui/*.templ")
	rx := regexp.MustCompile(`\sstyle=`)
	for _, f := range append(files, more...) {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if loc := rx.FindIndex(b); loc != nil {
			t.Errorf("%s: inline style near %q", f, b[loc[0]:min(loc[1]+30, len(b))])
		}
	}
}

// A submit button that overrides the form's action needs a nonce, or htmx's CSP extension blocks the boosted form.
func TestFormactionButtonsHaveNonce(t *testing.T) {
	files, _ := filepath.Glob("*.templ")
	rx := regexp.MustCompile(`<button\b[^>]*formaction=[^>]*>`)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range rx.FindAll(b, -1) {
			if !strings.Contains(string(m), "HX(ctx") {
				t.Errorf("%s: button with formaction and no nonce: %.80s", f, m)
			}
		}
	}
}
