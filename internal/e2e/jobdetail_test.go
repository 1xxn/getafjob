//go:build e2e

package e2e

import (
	"testing"
	"time"

	"github.com/go-rod/rod/lib/input"
)

// The job page explains the score, previews the filled values with their source in a drawer, and Apply now starts a
// run on the fixture site that opens in the live focus view; on a phone the page fits.
func TestJobDetail(t *testing.T) {
	tab := OpenIn(t, Demo(t), 1440, 900, "light")
	tab.Go("/jobs?source=demo")
	id := tab.Eval("return document.querySelector('.job-row')?.dataset.id || ''")
	if id == `""` {
		t.Fatal("no demo job listed")
	}
	tab.Go("/jobs/" + id[1:len(id)-1])
	tab.El("#fit-inspector .ui-card")
	tab.El(".job-reasons")
	tab.Shot("detail")

	if tab.Eval(`return !!document.querySelector('.jd-actions [data-action="apply"]')`) == "true" {
		tab.Click(`.jd-actions [data-action="apply"]`)
		tab.WaitFor("document.getElementById('dlg-apply').open && document.querySelector('#apply-preview .ui-badge')")
		tab.Shot("preview")
		must(t, tab.Page.Keyboard.Type(input.Escape))
		tab.WaitFor("!document.getElementById('dlg-apply').open")
	}

	app := startRun(t, tab)
	tab.WaitWithin(30*time.Second, "document.querySelector('.live-screen-full')")
	tab.Shot("focus")

	// Clean up: park the run from its application page.
	tab.Go("/applications/" + app)
	if tab.Eval(`return !!document.querySelector('#app-head [data-action="apps.action.park"]')`) == "true" {
		tab.Click(`#app-head [data-action="apps.action.park"]`)
		tab.WaitWithin(15*time.Second, "document.getElementById('app-head')?.dataset.state === 'parked'")
	}

	phone := OpenIn(t, Demo(t), 375, 812, "dark")
	phone.Go("/jobs/" + id[1:len(id)-1])
	phone.El(".jd-head")
	if over := phone.Eval("return Math.max(0, document.documentElement.scrollWidth - document.documentElement.clientWidth)"); over != "0" {
		t.Errorf("%s px horizontal overflow", over)
	}
	phone.Shot("phone")
}
