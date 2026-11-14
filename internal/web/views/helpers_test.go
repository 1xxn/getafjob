package views

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

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
func TestWhenUsesTheInstanceZone(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skip("no tzdata:", err)
	}
	at := time.Date(2026, 11, 13, 22, 0, 0, 0, time.UTC)
	de := WithLocale(t.Context(), i18n.Locale("de"))
	if got := When(de, at); got != "13.11.2026 22:00 UTC" {
		t.Errorf("without zone = %q", got)
	}
	if got := When(WithZone(de, berlin), at); got != "13.11.2026 23:00 CET" {
		t.Errorf("de = %q", got)
	}
	if got := When(WithZone(WithLocale(t.Context(), i18n.Locale("en")), berlin), at); got != "Nov 13, 2026 23:00 CET" {
		t.Errorf("en = %q", got)
	}
}

// Short times and day labels follow the instance zone: 23:30 UTC is already the next day in Berlin.
func TestShortTimesUseTheInstanceZone(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skip("no tzdata:", err)
	}
	ctx := WithZone(WithLocale(t.Context(), i18n.Locale("de")), berlin)
	at := time.Date(2026, 11, 13, 22, 23, 0, 0, time.UTC)
	if got := shortWhen(ctx, at, at.Add(time.Minute)); got != "23:23" {
		t.Errorf("shortWhen = %q, want 23:23", got)
	}
	if got := dayLabel(ctx, time.Date(2026, 11, 13, 23, 30, 0, 0, time.UTC)); got != "14.11." {
		t.Errorf("dayLabel = %q, want 14.11.", got)
	}
}

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
