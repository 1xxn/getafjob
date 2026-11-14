//go:build e2e

package e2e

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
)

// WaitWithin polls a JavaScript condition for up to d.
func (tab *Tab) WaitWithin(d time.Duration, cond string) {
	tab.t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if tab.Eval("return Boolean("+cond+")") == "true" {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	tab.Shot("timeout")
	tab.t.Fatalf("timed out after %s waiting for %s", d, cond)
}

// freshJobs are fixture jobs of their own for the journeys that start a run (admin demo -fresh, see test:e2e):
// each at a new company, so neither the company window nor another journey's application stands in the way.
var (
	freshMu   sync.Mutex
	freshJobs = strings.FieldsFunc(os.Getenv("GETAFJOB_E2E_JOBS"), func(r rune) bool { return r == ',' })
)

// startRun applies to a fixture job of its own through the job page and its preview drawer and returns the
// application id. The run is parked when the test ends, so it does not hold a browser slot for the next journey.
func startRun(t *testing.T, tab *Tab) string {
	t.Helper()
	freshMu.Lock()
	if len(freshJobs) == 0 {
		freshMu.Unlock()
		t.Fatal("no fixture job left: GETAFJOB_E2E_JOBS lists too few (admin demo -fresh, see test:e2e)")
	}
	id := freshJobs[0]
	freshJobs = freshJobs[1:]
	freshMu.Unlock()

	tab.Go("/jobs/" + id)
	tab.Click(`.jd-actions [data-action="apply"]`)
	tab.WaitFor("document.getElementById('dlg-apply').open && document.querySelector('#apply-body [data-action=\"apply-confirm\"]')")
	if tab.Eval(`return document.querySelector('#apply-body [data-action="apply-confirm"]').disabled`) == "true" {
		tab.Shot("apply-blocked")
		t.Fatalf("a guard stops the application to fixture job %s", id)
	}
	tab.Click(`#apply-body [data-action="apply-confirm"]`)
	tab.WaitWithin(15*time.Second, "location.pathname === '/live' && location.search.startsWith('?run=')")
	app := strings.TrimPrefix(strings.Trim(tab.Eval("return location.search"), `"`), "?run=")
	t.Cleanup(func() {
		// Parking an application that is already done is refused; that is fine here.
		_, _ = tab.Page.Timeout(10 * time.Second).Evaluate(rod.Eval(
			`id => fetch('/applications/' + id + '/park', {method: 'POST'}).then(r => r.status)`, app).ByPromise())
	})
	return app
}

// A run started on the fixture site shows in the live center, opens in focus without a reload, plays in the dock
// across navigation and can be parked from the focus view.
func TestLiveCenter(t *testing.T) {
	tab := OpenIn(t, Demo(t), 1440, 900, "light")
	app := startRun(t, tab)
	live := `['view', 'interactive', 'answers'].includes(document.querySelector('%s')?.dataset.mode)`

	tab.Go("/live")
	tab.WaitWithin(60*time.Second, "document.getElementById('run-"+app+"')")
	tab.WaitWithin(60*time.Second, strings.Replace(live, "%s", "#run-"+app+" [data-live-view]", 1))
	tab.Shot("grid")
	tab.Eval("window.__e2e = 1; return true")

	tab.Click("#run-" + app + " .live-card-title a")
	tab.WaitFor("location.search === '?run=" + app + "'")
	tab.WaitWithin(30*time.Second, strings.Replace(live, "%s", ".live-screen-full", 1))
	if tab.Eval("return window.__e2e") != "1" {
		t.Fatal("focus view reloaded the page")
	}
	tab.Shot("focus")

	// The live center shows the run itself: no floating dock on top of it.
	if tab.Eval("return getComputedStyle(document.getElementById('live-dock')).display") != `"none"` {
		t.Fatal("the dock floats over the live center")
	}

	// Dock: open, pick the run, keep the same live view across a page change.
	tab.Click(`.shell-side a[href="/jobs"]`)
	tab.WaitFor("location.pathname === '/jobs'")
	tab.Click("[data-dock-toggle]")
	tab.WaitFor(`document.querySelector('[data-dock-run="` + app + `"]')`)
	tab.Click(`[data-dock-run="` + app + `"]`)
	// Whether the picture is live right now depends on the run (its browser closes between steps); the dock must
	// connect, and keep the same view and connection state across a page change.
	tab.WaitWithin(30*time.Second, "(document.querySelector('.dock-screen [data-live-view]')?.dataset.mode || 'connecting') !== 'connecting'")
	tab.Eval("document.querySelector('.dock-screen [data-live-view]').dataset.e2e = 'kept'; return true")
	before := tab.Eval("return document.querySelector('.dock-screen [data-live-view]').dataset.mode")
	tab.Click(`.shell-side a[href="/"]`)
	tab.WaitFor("location.pathname === '/'")
	if tab.Eval("return document.querySelector('.dock-screen [data-live-view]')?.dataset.e2e") != `"kept"` {
		t.Fatal("the dock's live view did not survive navigation")
	}
	if after := tab.Eval("return document.querySelector('.dock-screen [data-live-view]').dataset.mode"); after == `"connecting"` {
		t.Fatalf("the dock reconnected after navigation (mode %s before)", before)
	}
	tab.Shot("dock")

	// Layout: large and on the left, with the keyboard; it is saved for the user and comes back after a reload.
	must(t, tab.El(`[data-dock-size-set="lg"]`).Focus())
	must(t, tab.Page.Keyboard.Type(input.Enter))
	must(t, tab.El(`[data-dock-side-toggle]`).Focus())
	must(t, tab.Page.Keyboard.Type(input.Enter))
	tab.WaitFor("document.getElementById('live-dock').dataset.dockSide === 'left' && document.getElementById('live-dock').dataset.dockSize === 'lg'")
	// A timing entry appears once a save has finished.
	tab.WaitFor("performance.getEntriesByName(location.origin + '/live/dock/layout').length >= 2")
	tab.Go("/applications")
	tab.WaitFor("document.getElementById('live-dock').dataset.dockSide === 'left' && document.getElementById('live-dock').dataset.dockSize === 'lg' && " +
		"document.querySelector('[data-dock-size-set=\"lg\"]')?.getAttribute('aria-pressed') === 'true'")
	tab.Shot("dock-left")
	// Back to the default for the other journeys.
	tab.Eval("document.querySelector('[data-dock-size-set=\"md\"]').click(); document.querySelector('[data-dock-side-toggle]').click(); return true")
	tab.WaitFor("document.getElementById('live-dock').dataset.dockSide === 'right' && " +
		"performance.getEntriesByName(location.origin + '/live/dock/layout').length >= 2")
	if tab.Eval("return document.querySelector('[data-dock-toggle]').getAttribute('aria-expanded')") == `"true"` {
		tab.Click("[data-dock-toggle]")
	}

	// Park the run from the focus view; the confirm dialog guards it.
	tab.Go("/live?run=" + app)
	tab.Click(`.live-actions form[action$="/park"] button[type="submit"]`)
	tab.WaitFor("document.getElementById('confirm-dialog').open")
	tab.Click("[data-confirm-ok]")
	tab.WaitWithin(15*time.Second, "location.pathname === '/live' && !location.search")
	tab.WaitWithin(30*time.Second, "!document.getElementById('run-"+app+"')")
}
