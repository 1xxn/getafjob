//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/1xxn/getafjob/internal/demo"
)

// setLanguage switches the signed-in user's language through the user menu.
func setLanguage(tab *Tab, locale string) {
	tab.Go("/")
	tab.Click(`.shell-user`)
	tab.WaitFor("document.getElementById('user-menu').matches(':popover-open')")
	tab.Click(`#user-menu button[name="locale"][value="` + locale + `"]`)
	tab.WaitFor("document.documentElement.lang === '" + locale + "'")
}

// onlyDemo fails when an entry of the list names no invented company: a development stack also crawls real
// sources, and the README shows demo data only.
func onlyDemo(t *testing.T, tab *Tab, selector string) {
	t.Helper()
	var texts []string
	if err := json.Unmarshal([]byte(tab.Eval(`return [...document.querySelectorAll('`+selector+`')].map(e => e.innerText)`)), &texts); err != nil {
		t.Fatal(err)
	}
	for _, text := range texts {
		ok := false
		for _, c := range demo.Companies() {
			ok = ok || strings.Contains(text, c)
		}
		if !ok {
			t.Errorf("%s: an entry names no demo company: %q", selector, text)
		}
	}
}

// TestDocsScreenshots takes the README screenshots from the demo user's data in English (go tool task
// docs:screenshots copies them to docs/images). Only pages that show demo data alone are taken. Ordinary runs skip
// it.
func TestDocsScreenshots(t *testing.T) {
	if os.Getenv("GETAFJOB_E2E_DOCS") == "" {
		t.Skip("GETAFJOB_E2E_DOCS not set: run go tool task docs:screenshots")
	}
	tab := OpenIn(t, Demo(t), 1280, 800, "light")
	setLanguage(tab, "en")
	t.Cleanup(func() { setLanguage(tab, "de") }) // the demo user reads German

	tab.Go("/jobs?source=demo")
	tab.El(".job-row")
	tab.Shot("job-list")
	id := strings.Trim(tab.Eval("return document.querySelector('.job-row')?.dataset.id || ''"), `"`)
	if id == "" {
		t.Fatal("no demo job listed")
	}
	tab.Go("/jobs/" + id)
	tab.El("#fit-inspector .ui-card")
	tab.Shot("job-detail")

	tab.Go("/needs-input")
	tab.El("main h1")
	onlyDemo(t, tab, "[data-ni-item]")
	tab.Shot("needs-input")

	tab.Go("/settings/automation")
	tab.El("#set-title")
	tab.Shot("settings")

	// Last, as it may start a run on the fixture site and skip when none can ask for approval.
	ensureApproval(t, tab)
	tab.Go("/approvals")
	tab.El("[data-approval]")
	onlyDemo(t, tab, "[data-approval]")
	tab.Shot("approvals")
}
