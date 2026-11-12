//go:build e2e

package e2e

import (
	"encoding/json"
	"math/rand/v2"
	"strconv"
	"strings"
	"testing"
	"time"
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

// startRun applies to the first listed fixture job that has no application yet and returns the application id.
func startRun(t *testing.T, tab *Tab) string {
	t.Helper()
	var hrefs []string
	for page := 1; page <= 3; page++ {
		tab.Go("/jobs?source=demo&decision=all&page=" + strconv.Itoa(page))
		var more []string
		if err := json.Unmarshal([]byte(tab.Eval(`return [...document.querySelectorAll('.job-row')]
			.filter(r => !r.dataset.state || r.dataset.state === 'new').map(r => '/jobs/' + r.dataset.id)`)), &more); err != nil {
			t.Fatal(err)
		}
		hrefs = append(hrefs, more...)
	}
	// A fresh stack (CI) starts at the top; locally the order is shuffled so older runs rarely get in the way.
	rand.Shuffle(len(hrefs), func(i, j int) { hrefs[i], hrefs[j] = hrefs[j], hrefs[i] })
	for _, h := range hrefs {
		tab.Go(h)
		if tab.Eval(`return !!document.querySelector('main a[href^="/applications/"]')`) == "true" {
			continue // applied before: its state would decide the journey
		}
		if tab.Eval(`return !!document.querySelector('main a[href^="http://fixtures:8080/"]')`) != "true" {
			continue // a crawled posting: the runner's egress policy blocks it in the dev stack
		}
		if tab.Eval(`return !!document.querySelector('.jd-actions [data-action="apply"]')`) != "true" {
			continue // applied elsewhere, excluded by the search settings or not a form
		}
		// Apply now opens the preview in a drawer; starting there leads to the run in focus.
		tab.Click(`.jd-actions [data-action="apply"]`)
		tab.WaitFor("document.getElementById('dlg-apply').open && document.querySelector('#apply-body [data-action=\"apply-confirm\"]')")
		if tab.Eval(`return document.querySelector('#apply-body [data-action="apply-confirm"]').disabled`) == "true" {
			continue // a guard stops it (company window, duplicate)
		}
		tab.Click(`#apply-body [data-action="apply-confirm"]`)
		tab.WaitWithin(15*time.Second, "location.pathname === '/live' && location.search.startsWith('?run=')")
		return strings.TrimPrefix(strings.Trim(tab.Eval("return location.search"), `"`), "?run=")
	}
	if app := retryRun(t, tab); app != "" {
		return app
	}
	t.Fatal("no fixture job without an application and no parked fixture application (reset the dev database)")
	return ""
}

// retryRun starts a parked application on the fixture site again: a fresh stack has one fixture job per company
// that the company window lets through, fewer than the journeys need.
func retryRun(t *testing.T, tab *Tab) string {
	t.Helper()
	tab.Go("/applications?group=attention")
	var ids []string
	if err := json.Unmarshal([]byte(tab.Eval(`return [...document.querySelectorAll('tr[data-app-row]')]
		.filter(r => r.dataset.state === 'parked' || r.dataset.state === 'failed').map(r => r.dataset.id)`)), &ids); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		tab.Go("/applications/" + id)
		if tab.Eval(`return !!document.querySelector('#app-head a[href^="http://fixtures:8080/"]') &&
			!!document.querySelector('#app-head [data-action="apps.action.retry"]')`) != "true" {
			continue
		}
		tab.Click(`#app-head [data-action="apps.action.retry"]`)
		tab.WaitWithin(15*time.Second, "document.getElementById('app-head')?.dataset.state !== 'parked'")
		return id
	}
	return ""
}

// A run started on the fixture site shows in the live center, opens in focus without a reload, plays in the dock
// across navigation and can be parked from the focus view.
func TestLiveCenter(t *testing.T) {
	tab := OpenIn(t, Demo(t), 1440, 900, "light")
	app := startRun(t, tab)
	live := `['view', 'interactive'].includes(document.querySelector('%s')?.dataset.mode)`

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

	// Dock: open, pick the run, keep the same live view across a page change.
	tab.Click("[data-dock-toggle]")
	tab.WaitFor(`document.querySelector('[data-dock-run="` + app + `"]')`)
	tab.Click(`[data-dock-run="` + app + `"]`)
	// Whether the picture is live right now depends on the run (its browser closes between steps); the dock must
	// connect, and keep the same view and connection state across a page change.
	tab.WaitWithin(30*time.Second, "(document.querySelector('.dock-screen [data-live-view]')?.dataset.mode || 'connecting') !== 'connecting'")
	tab.Eval("document.querySelector('.dock-screen [data-live-view]').dataset.e2e = 'kept'; return true")
	before := tab.Eval("return document.querySelector('.dock-screen [data-live-view]').dataset.mode")
	tab.Click(`.shell-side a[href="/jobs"]`)
	tab.WaitFor("location.pathname === '/jobs'")
	if tab.Eval("return document.querySelector('.dock-screen [data-live-view]')?.dataset.e2e") != `"kept"` {
		t.Fatal("the dock's live view did not survive navigation")
	}
	if after := tab.Eval("return document.querySelector('.dock-screen [data-live-view]').dataset.mode"); after == `"connecting"` {
		t.Fatalf("the dock reconnected after navigation (mode %s before)", before)
	}
	tab.Shot("dock")
	tab.Click("[data-dock-toggle]")

	// Park the run from the focus view; the confirm dialog guards it.
	tab.Go("/live?run=" + app)
	tab.Click(`.live-actions form[action$="/park"] button[type="submit"]`)
	tab.WaitFor("document.getElementById('confirm-dialog').open")
	tab.Click("[data-confirm-ok]")
	tab.WaitWithin(15*time.Second, "location.pathname === '/live' && !location.search")
	tab.WaitWithin(30*time.Second, "!document.getElementById('run-"+app+"')")
}
