//go:build e2e

package e2e

import (
	"strconv"
	"testing"
	"time"

	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/proto"
)

// The candidate pages share a head with completeness and tabs; the answer bank adds, finds, edits and deletes an
// answer, and leaving with unsaved changes asks first.
func TestCandidateHub(t *testing.T) {
	tab := OpenIn(t, Demo(t), 1440, 900, "light")
	tab.Go("/profile")
	tab.El(".cand-head[data-done]")
	if n := tab.Eval("return document.querySelectorAll('.cand-head .ui-tab').length"); n != "4" {
		t.Fatalf("%s tabs, want 4", n)
	}
	tab.Click(`.cand-head .ui-tab[href="/answers"]`)
	tab.WaitFor("location.pathname === '/answers'")

	// Add an answer from the dialog, then find it.
	q := "E2E question " + strconv.FormatInt(time.Now().UnixNano(), 36)
	tab.Click(`.cand-tools button[commandfor="dlg-answer"]`)
	tab.WaitFor("document.getElementById('dlg-answer').open")
	must(t, tab.El(`#dlg-answer input[name="question"]`).Input(q))
	must(t, tab.El(`#dlg-answer textarea[name="answer"]`).Input("Yes"))
	tab.Click(`#dlg-answer button[type="submit"]`)
	tab.WaitWithin(10*time.Second, "!document.getElementById('dlg-answer').open")
	must(t, tab.El(`.cand-tools input[name="q"]`).Input(q))
	tab.WaitWithin(10*time.Second, "document.querySelectorAll('#answer-list [data-answer]').length === 1")
	item := "#answer-list [data-answer]"
	tab.Click(item + " > summary")
	tab.Shot("answer")

	// Edit without saving, then try to leave: the question comes, and "stay" keeps the page.
	must(t, tab.El(item+` textarea[name="answer"]`).Input(" and more"))
	wait, handle := tab.Page.HandleDialog()
	go func() {
		if e := wait(); e.Type == proto.PageDialogTypeConfirm {
			_ = handle(&proto.PageHandleJavaScriptDialog{Accept: false})
		}
	}()
	tab.Click(`.cand-head .ui-tab[href="/profile"]`)
	time.Sleep(500 * time.Millisecond)
	if tab.Eval("return location.pathname") != `"/answers"` {
		t.Fatal("left the page with unsaved changes without asking")
	}

	// Save, then delete with the confirm dialog. The toast of the add would cover the button at the bottom: close it.
	tab.Eval("document.querySelectorAll('.ui-toast').forEach(t => t.remove()); return true")
	// Keyboard: the save button sits at the very bottom of a short page, so focus it and press Enter.
	must(t, tab.El(item+` button[type="submit"]:not([formaction])`).Focus())
	must(t, tab.Page.Keyboard.Type(input.Enter))
	tab.WaitWithin(10*time.Second, "location.search.includes('saved') && !document.querySelector('.htmx-request')")
	tab.Go("/answers?q=" + q)
	tab.Click(item + " > summary")
	tab.Click(item + ` button[formaction$="/delete"]`)
	tab.WaitFor("document.getElementById('confirm-dialog').open")
	tab.Click("[data-confirm-ok]")
	tab.WaitWithin(10*time.Second, "location.pathname === '/answers' && ![...document.querySelectorAll('.cand-item-title')].some(e => e.textContent === '"+q+"')")

	phone := OpenIn(t, Demo(t), 375, 812, "dark")
	for _, p := range []string{"/profile", "/documents", "/answers", "/texts"} {
		phone.Go(p)
		phone.El(".cand-head h1")
		phone.NoOverflow(p)
	}
}
