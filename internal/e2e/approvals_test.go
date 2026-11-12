//go:build e2e

package e2e

import (
	"testing"
	"time"

	"github.com/go-rod/rod/lib/input"
)

// ensureApproval makes sure an application waits for approval: if none does, it runs one on the fixture site and
// answers its questions until it asks.
func ensureApproval(t *testing.T, tab *Tab) {
	t.Helper()
	tab.Go("/approvals")
	if tab.Eval("return !!document.querySelector('[data-approval]')") == "true" {
		return
	}
	app := startRun(t, tab)
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		tab.Go("/approvals")
		if tab.Eval("return !!document.querySelector('[data-approval]')") == "true" {
			return
		}
		tab.Go("/needs-input?app=" + app)
		if tab.Eval("return document.querySelectorAll('.ni-panel .ni-question').length > 0") == "true" {
			tab.Eval(fillQuestions)
			tab.Click(`.ni-questions button[type="submit"]`)
			tab.WaitWithin(15*time.Second, "document.querySelector('[data-answered]')")
		}
		time.Sleep(2 * time.Second)
	}
	tab.Shot("no-approval")
	t.Fatal("the fixture run did not ask for approval")
}

// Waiting approvals can be reviewed in a drawer with the source of every value, selected with the keyboard and
// approved together.
func TestApprovals(t *testing.T) {
	tab := OpenIn(t, Demo(t), 1440, 900, "light")
	ensureApproval(t, tab)
	tab.Shot("list")

	tab.Click(`[data-approval] [data-action="review"]`)
	tab.WaitFor("document.getElementById('dlg-review').open && document.querySelector('#review-body [data-review]')")
	tab.Shot("review")
	must(t, tab.Page.Keyboard.Type(input.Escape))
	tab.WaitFor("!document.getElementById('dlg-review').open")

	// Keyboard: Space on "select all" ticks every row.
	must(t, tab.El("#appr-all").Focus())
	must(t, tab.Page.Keyboard.Type(input.Space))
	tab.WaitFor("[...document.querySelectorAll('[data-approval] .job-check')].every(b => b.checked)")
	before := tab.Eval("return document.querySelectorAll('[data-approval]').length")

	tab.Click(`#bulk button[value="approve"]`)
	tab.WaitWithin(15*time.Second, "document.querySelector('.ui-toast')")
	tab.WaitWithin(30*time.Second, "document.querySelectorAll('[data-approval]').length < "+before)
	tab.Shot("approved")

	phone := OpenIn(t, Demo(t), 375, 812, "dark")
	phone.Go("/approvals")
	phone.El("main h1")
	phone.NoOverflow("page")
}
