package i18n

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// TestCatalogsAreComplete: every key exists in every locale, with the same placeholders and a non-empty text.
func TestCatalogsAreComplete(t *testing.T) {
	en := catalogs[EN]
	for _, l := range Locales {
		c := catalogs[l]
		for k, msg := range en {
			other, ok := c[k]
			if !ok {
				t.Errorf("%s: missing key %q", l, k)
				continue
			}
			if strings.TrimSpace(other) == "" {
				t.Errorf("%s: empty text for %q", l, k)
			}
			if !reflect.DeepEqual(placeholders(msg), placeholders(other)) {
				t.Errorf("%s: placeholders of %q differ: %v vs %v", l, k, placeholders(other), placeholders(msg))
			}
		}
		for k := range c {
			if _, ok := en[k]; !ok {
				t.Errorf("%s: key %q not in the English catalog", l, k)
			}
		}
	}
}

func TestT(t *testing.T) {
	if got := T(DE, "msg.identity.user_disabled", nil); got != "Dieses Konto ist deaktiviert." {
		t.Errorf("de = %q", got)
	}
	if got := T(EN, "mail.footer", map[string]any{"site": "jobs.example.org"}); !strings.Contains(got, "jobs.example.org") {
		t.Errorf("params not filled: %q", got)
	}
	if got := T(EN, "no.such.key", nil); got != "no.such.key" {
		t.Errorf("missing key = %q", got)
	}
	if got := T(Locale("fr"), "msg.identity.user_disabled", nil); got != "This account is disabled." {
		t.Errorf("fallback = %q", got)
	}
	if got := fill("{a} and {b}", map[string]any{"a": 1}); got != "1 and {b}" {
		t.Errorf("fill = %q", got)
	}
	if got := (Msg{Code: "identity.session_expired"}).Render(DE); !strings.HasPrefix(got, "Deine Sitzung") {
		t.Errorf("Msg.Render = %q", got)
	}
}

func TestLocaleSelection(t *testing.T) {
	cases := map[string]Locale{
		"":                        EN,
		"de-DE,de;q=0.9,en;q=0.8": DE,
		"en-US,en;q=0.9,de;q=0.8": EN,
		"fr-FR,de;q=0.5,en;q=0.4": DE,
		"fr":                      EN,
		"de;q=0.1, en;q=0.2":      EN,
		"de;q=bogus":              EN,
	}
	for header, want := range cases {
		if got := FromAcceptLanguage(header); got != want {
			t.Errorf("FromAcceptLanguage(%q) = %s, want %s", header, got, want)
		}
	}
	if Parse("de-AT") != DE || Parse("xx") != EN {
		t.Error("Parse")
	}
}

func FuzzFromAcceptLanguage(f *testing.F) {
	f.Add("de-DE,de;q=0.9,en;q=0.8")
	f.Fuzz(func(t *testing.T, h string) {
		l := FromAcceptLanguage(h)
		if l != EN && l != DE {
			t.Fatalf("unsupported locale %q", l)
		}
	})
}

// TestGermanSaysDu: the German catalog speaks to the reader with "du". "Sie" stays only where it means "they" or
// "it" (files, a page), listed here.
func TestGermanSaysDu(t *testing.T) {
	formal := regexp.MustCompile(`\b(Sie|Ihnen|Ihr|Ihre|Ihren|Ihrem|Ihrer|Ihres)\b`)
	third := map[string]bool{"documents.text": true, "jobs.import_error.fetch": true}
	for k, msg := range catalogs[DE] {
		if formal.MatchString(msg) && !third[k] {
			t.Errorf("de: %q uses the formal address: %s", k, msg)
		}
	}
}
