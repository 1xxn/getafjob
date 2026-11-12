//go:build e2e

package e2e

import (
	"testing"
	"time"
)

// fillQuestions answers every question of the open run with a value its control accepts and keeps the answers out
// of the answer bank, so later runs on the fixture site still ask.
const fillQuestions = `for (const q of document.querySelectorAll('.ni-question')) {
	const input = q.dataset.input;
	const box = q.querySelector('input[type="checkbox"][name^="answer-"]');
	const radio = q.querySelector('input[type="radio"]');
	const select = q.querySelector('select');
	const text = q.querySelector('input[name^="answer-"]:not([type="hidden"]):not([type="checkbox"]):not([type="radio"]), textarea');
	if (box) box.checked = true;
	else if (radio) radio.checked = true;
	else if (select) select.selectedIndex = Math.min(1, select.options.length - 1);
	else if (text && !text.value) text.value = input === 'date' ? '2027-01-01' : (input === 'number' ? '1' : 'Yes');
	const save = q.querySelector('input[type="checkbox"][name^="save-"]');
	if (save) save.checked = false;
}
return document.querySelectorAll('.ni-question').length`

// A run on the fixture site that asks questions shows in the inbox with a countdown; answering there sends the
// answers to the waiting run, which continues without a reload.
func TestNeedsInput(t *testing.T) {
	tab := OpenIn(t, Demo(t), 1440, 900, "light")
	app := startRun(t, tab)

	tab.Go("/applications/" + app)
	tab.WaitWithin(90*time.Second, "document.getElementById('app-head')?.dataset.state === 'needs_input'")

	tab.Go("/needs-input")
	item := `[data-ni-item][data-id="` + app + `"]`
	tab.WaitWithin(30*time.Second, "document.querySelector('"+item+"')")
	tab.Shot("inbox")
	tab.Click(item + " a")
	tab.WaitFor("location.search === '?app=" + app + "' && document.querySelector('.ni-panel')")
	if tab.Eval("return document.querySelectorAll('.ni-question').length") == "0" {
		t.Skip("the run waits for a check, not for questions")
	}
	if n := tab.Eval(fillQuestions); n == "0" {
		t.Fatal("no questions to answer")
	}
	tab.Eval("window.__e2e = 1; return true")
	tab.Shot("questions")
	tab.Click(`.ni-questions button[type="submit"]`)
	tab.WaitWithin(15*time.Second, "document.querySelector('[data-answered]')")
	if tab.Eval("return window.__e2e") != "1" {
		t.Fatal("answering reloaded the page")
	}
	tab.Shot("answered")

	// The run took the answers: its page leaves "needs input" on its own (live head, no reload).
	tab.Go("/applications/" + app)
	tab.WaitWithin(60*time.Second, "document.getElementById('app-head')?.dataset.state !== 'needs_input'")

	// Clean up: park it if it still runs.
	tab.Go("/applications/" + app)
	if tab.Eval(`return !!document.querySelector('#app-head [data-action="apps.action.park"]')`) == "true" {
		tab.Click(`#app-head [data-action="apps.action.park"]`)
	}
}
