//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"
)

// Settings live in sections with a list beside them; old addresses lead there, and saving one section keeps the
// others as they were.
func TestSettings(t *testing.T) {
	tab := OpenIn(t, Demo(t), 1440, 900, "light")
	tab.Go("/preferences")
	tab.WaitFor("location.pathname === '/settings/search'")
	tab.Go("/account")
	tab.WaitFor("location.pathname === '/settings/account'")

	for _, s := range []string{"search", "automation", "notifications", "integrations", "account"} {
		tab.Go("/settings/" + s)
		tab.El(`.set[data-section="` + s + `"] #set-title`)
		if tab.Eval(`return document.querySelector('.set-nav a[aria-current="page"]')?.getAttribute('href')`) != `"/settings/`+s+`"` {
			t.Errorf("%s: the section list does not mark it", s)
		}
	}

	tab.Go("/settings/automation")
	level := tab.Eval(`return document.querySelector('input[name="autonomy_level"]:checked')?.value`)

	// Change one search field, save, and put it back.
	tab.Go("/settings/search")
	before := strings.Trim(tab.Eval(`return document.getElementById('f-cover_letter_lang').value`), `"`)
	other := "de"
	if before == "de" {
		other = "en"
	}
	for _, v := range []string{other, before} {
		// Mark the page: only the server's answer replaces the form, and only then the save is done.
		tab.Eval("const s = document.getElementById('f-cover_letter_lang'); s.value = '" + v + "'; s.dispatchEvent(new Event('input', {bubbles: true})); s.form.dataset.e2e = 'old'; return true")
		tab.Click(`.set-form button[data-save]`)
		tab.WaitWithin(10*time.Second, "location.search.includes('saved=1') && !document.querySelector('.set-form[data-e2e]') && document.getElementById('f-cover_letter_lang')?.value === '"+v+"'")
	}
	tab.Shot("search")

	tab.Go("/settings/automation")
	if got := tab.Eval(`return document.querySelector('input[name="autonomy_level"]:checked')?.value`); got != level {
		t.Fatalf("saving the search changed the automation level: %s, was %s", got, level)
	}

	phone := OpenIn(t, Demo(t), 375, 812, "dark")
	for _, s := range []string{"automation", "account"} {
		phone.Go("/settings/" + s)
		phone.El("#set-title")
		phone.NoOverflow("/settings/" + s)
	}
}
