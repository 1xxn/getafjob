//go:build e2e

package e2e

import (
	"encoding/json"
	"math/rand/v2"
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
	tab.Go("/jobs")
	var hrefs []string
	if err := json.Unmarshal([]byte(tab.Eval(`return [...new Set([...document.querySelectorAll('main a[href^="/jobs/"]')]
		.map(a => a.getAttribute('href')).filter(h => /^\/jobs\/\d+$/.test(h)))]`)), &hrefs); err != nil {
		t.Fatal(err)
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
		tab.Go(h + "/apply")
		if tab.Eval(`return !!document.querySelector('main form[method="post"][action$="/apply"] button[type="submit"]')`) != "true" {
			continue
		}
		tab.Click(`main form[method="post"][action$="/apply"] button[type="submit"]`)
		tab.WaitWithin(15*time.Second, "location.pathname.startsWith('/applications/')")
		return strings.TrimPrefix(strings.Trim(tab.Eval("return location.pathname"), `"`), "/applications/")
	}
	t.Fatal("no fixture job without an application on the first page (reset the dev database or run admin demo again)")
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
