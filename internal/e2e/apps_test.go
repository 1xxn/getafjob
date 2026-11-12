//go:build e2e

package e2e

import (
	"testing"
	"time"
)

// A run started on the fixture site shows in the application list, follows live changes there, parks from the bulk
// bar, and is tried again from its page; the tabs render and the list fits a phone.
func TestApplications(t *testing.T) {
	tab := OpenIn(t, Demo(t), 1440, 900, "light")
	app := startRun(t, tab)
	row := `tr[data-app-row][data-id="` + app + `"]`

	tab.Go("/applications")
	tab.WaitWithin(30*time.Second, "document.querySelector('"+row+"')")
	tab.Shot("list")

	// Park it from the bulk bar: back on the list with a toast, the row now parked.
	tab.Click(row + ` input[type="checkbox"]`)
	tab.WaitFor("document.querySelector('[data-bulk]').classList.contains('jobs-bulk-active')")
	tab.Click(`#bulk button[value="park"]`)
	tab.WaitWithin(15*time.Second, "document.querySelector('.ui-toast')")
	tab.Go("/applications?group=attention")
	tab.WaitWithin(30*time.Second, "document.querySelector('"+row+"')?.dataset.state === 'parked'")

	// The page says why and offers to try again; the head follows the new state without a reload.
	tab.Click(row + " a.apps-title")
	tab.WaitFor("location.pathname === '/applications/" + app + "'")
	tab.WaitFor("document.getElementById('app-head')?.dataset.state === 'parked'")
	tab.Shot("parked")
	tab.Click(`#app-head [data-action="apps.action.retry"]`)
	tab.WaitWithin(15*time.Second, "['queued', 'running', 'needs_input'].includes(document.getElementById('app-head')?.dataset.state)")
	tab.Eval("window.__e2e = 1; return true")
	tab.WaitWithin(60*time.Second, "document.getElementById('app-head')?.dataset.state !== 'queued'")
	if tab.Eval("return window.__e2e") != "1" {
		t.Fatal("a live change reloaded the page")
	}

	for _, name := range []string{"live", "fields", "evidence", "notes", "timeline"} {
		tab.Go("/applications/" + app + "?tab=" + name)
		tab.El(`.app-tab[data-tab="` + name + `"]`)
	}
	tab.El(".app-step")
	tab.Shot("timeline")

	// Clean up: park it again so later journeys find a quiet stack.
	tab.Go("/applications/" + app)
	if tab.Eval(`return !!document.querySelector('#app-head [data-action="apps.action.park"]')`) == "true" {
		tab.Click(`#app-head [data-action="apps.action.park"]`)
		tab.WaitWithin(15*time.Second, "document.getElementById('app-head')?.dataset.state === 'parked'")
	}

	phone := OpenIn(t, Demo(t), 375, 812, "dark")
	phone.Go("/applications")
	phone.El("tr[data-app-row]")
	phone.NoOverflow("page")
	phone.Go("/applications/" + app)
	phone.El("#app-head")
	phone.NoOverflow("detail")
	phone.Shot("phone")
}
