package views

import (
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
