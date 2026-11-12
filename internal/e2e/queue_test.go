//go:build e2e

package e2e

import (
	"testing"
	"time"

	"github.com/go-rod/rod/lib/input"
)

// setRunnerChoice saves the execution environment on the automation settings and waits for the saved page.
func setRunnerChoice(t *testing.T, tab *Tab, v string) {
	t.Helper()
	tab.Go("/settings/automation")
	tab.Eval("const s = document.getElementById('f-runner_choice'); s.value = '" + v + "'; s.dispatchEvent(new Event('input', {bubbles: true})); s.form.dataset.e2e = 'old'; return true")
	tab.Click(`.set-form button[data-save]`)
	tab.WaitWithin(10*time.Second, "location.search.includes('saved=1') && !document.querySelector('.set-form[data-e2e]') && document.getElementById('f-runner_choice')?.value === '"+v+"'")
}

// Queued runs wait in the queue panel of the live center: they move up, pause and resume with the keyboard, and
// cancel; a change in one tab reaches another without a reload.
func TestQueuePanel(t *testing.T) {
	tab := OpenIn(t, Demo(t), 1440, 900, "light")
	// No home runner is connected: the runs stay queued for as long as the journey needs them.
	setRunnerChoice(t, tab, "home_runner")
	t.Cleanup(func() { setRunnerChoice(t, tab, "server") })
	first := startRun(t, tab)
	second := startRun(t, tab)
	item := func(id string) string { return `[data-queue-item][data-id="` + id + `"]` }
	order := `[...document.querySelectorAll('[data-queue-item]')].map(li => li.dataset.id).filter(id => id === '` + first + `' || id === '` + second + `').join(',')`

	tab.Go("/live")
	tab.WaitWithin(15*time.Second, "document.querySelector('"+item(second)+"')")
	if tab.Eval("return !!document.getElementById('run-"+first+"')") == "true" {
		t.Error("a queued run shows in the grid as well")
	}
	tab.Axe("/live queue")
	tab.Shot("queue")
	tab.Eval("window.__e2e = 1; return true")

	other := OpenIn(t, Demo(t), 375, 812, "dark")
	other.Go("/live")
	other.El(item(second))
	other.NoOverflow("queue on a phone")
	other.Eval("window.__e2e = 1; return true")

	// Move the second run up: the order changes here and in the other tab.
	tab.Click(item(second) + ` [data-queue-action="up"]`)
	want := second + "," + first
	tab.WaitFor(order + " === '" + want + "'")
	other.WaitWithin(15*time.Second, order+" === '"+want+"'")

	// Pause with the keyboard; the other tab shows it.
	must(t, tab.El(item(second)+` [data-queue-action="pause"]`).Focus())
	must(t, tab.Page.Keyboard.Type(input.Enter))
	tab.WaitFor("document.querySelector('" + item(second) + "')?.hasAttribute('data-paused')")
	other.WaitWithin(15*time.Second, "document.querySelector('"+item(second)+"')?.hasAttribute('data-paused')")
	tab.Click(item(second) + ` [data-queue-action="resume"]`)
	tab.WaitFor("!document.querySelector('" + item(second) + "')?.hasAttribute('data-paused')")

	// Cancel parks the run: it leaves the queue.
	tab.Click(item(first) + ` [data-queue-action="cancel"]`)
	tab.WaitFor("!document.querySelector('" + item(first) + "')")
	other.WaitWithin(15*time.Second, "!document.querySelector('"+item(first)+"')")

	for _, p := range []*Tab{tab, other} {
		if p.Eval("return window.__e2e") != "1" {
			t.Fatal("a queue change reloaded the page")
		}
	}
}
