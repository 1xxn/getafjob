//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"

	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/proto"
)

// externalJob finds a job that is not good for a run (no apply button) and can be marked as applied elsewhere.
const externalJob = `const list = await (await fetch('/jobs?decision=all', {headers: {Accept: 'text/html'}})).text();
const ids = [...list.matchAll(/data-job-row data-id="(\d+)" data-state="(new)?"/g)].map(m => m[1]);
for (let i = 0; i < ids.length; i += 8) {
	const pages = await Promise.all(ids.slice(i, i + 8).map(async id =>
		[id, await (await fetch('/jobs/' + id, {headers: {Accept: 'text/html'}})).text()]));
	for (const [id, html] of pages) {
		if (html.includes('/apply-external"') && !html.includes('data-action="apply"')) return id;
	}
}
return ''`

// dragTo drags an element with the mouse in small steps onto another, like a person.
func (tab *Tab) dragTo(from, to string) {
	tab.t.Helper()
	center := func(sel string) proto.Point {
		box := tab.El(sel).MustShape().Box()
		return proto.Point{X: box.X + box.Width/2, Y: box.Y + box.Height/2}
	}
	a, b := center(from), center(to)
	m := tab.Page.Mouse
	must(tab.t, m.MoveTo(a))
	must(tab.t, m.Down(proto.InputMouseButtonLeft, 1))
	for i := 1; i <= 10; i++ {
		f := float64(i) / 10
		must(tab.t, m.MoveTo(proto.Point{X: a.X + (b.X-a.X)*f, Y: a.Y + (b.Y-a.Y)*f}))
	}
	must(tab.t, m.Up(proto.InputMouseButtonLeft, 1))
}

// Sent applications move between stages by menu (keyboard), by dragging, and the move reaches another tab live;
// the card drawer keeps notes and hands out the interview as a calendar entry.
func TestPipeline(t *testing.T) {
	tab := OpenIn(t, Demo(t), 1440, 900, "light")
	tab.Go("/jobs")
	id := strings.Trim(tab.EvalAsync(externalJob), `"`)
	if id == "" {
		t.Skip("no job left to mark as applied elsewhere")
	}
	tab.Go("/jobs/" + id)
	tab.Click(`.jd-actions form[action$="/apply-external"] button`)
	tab.WaitWithin(15*time.Second, "location.pathname === '/pipeline'")
	card := `[data-col="applied"] [data-card][data-job="` + id + `"]`
	tab.El(card)
	app := strings.Trim(tab.Eval("return document.querySelector('"+strings.ReplaceAll(card, `"`, `\"`)+"').dataset.id"), `"`)
	sel := `[data-card][data-id="` + app + `"]`

	// A second tab watches the board.
	other := OpenIn(t, Demo(t), 1440, 900, "light")
	other.Go("/pipeline")
	other.El(`[data-col="applied"] ` + sel)

	// Keyboard: open the card's menu and move it to Interview.
	must(t, tab.El(sel+` .ui-menu > button`).Focus())
	must(t, tab.Page.Keyboard.Type(input.Enter))
	tab.WaitFor("document.querySelector('" + sel + " [data-move=\"interview\"]')?.checkVisibility()")
	tab.Click(sel + ` [data-move="interview"]`)
	tab.WaitWithin(10*time.Second, "document.querySelector('[data-col=\"interview\"] "+strings.ReplaceAll(sel, `"`, `\"`)+"')")
	if tab.Eval("return document.getElementById('pl-status').textContent.trim() !== ''") != "true" {
		t.Error("the move was not announced")
	}
	other.WaitWithin(15*time.Second, "document.querySelector('[data-col=\"interview\"] "+strings.ReplaceAll(sel, `"`, `\"`)+"')")
	tab.Shot("moved")

	// Mouse: drag it to Offer.
	tab.dragTo(sel+" [data-drag-handle]", `[data-col="offer"]`)
	tab.WaitWithin(10*time.Second, "document.querySelector('[data-col=\"offer\"] "+strings.ReplaceAll(sel, `"`, `\"`)+"')")

	// Drawer: notes with an interview date, then the calendar entry.
	tab.Click(sel + " .pl-card-title")
	tab.WaitFor("document.getElementById('dlg-card').open && document.querySelector('#card-body [data-card-detail]')")
	tab.Eval("document.getElementById('card-interview').value = '2027-01-15T10:00'; return true")
	tab.Click(`#card-body form.app-notes button[type="submit"]`)
	tab.WaitFor("document.querySelector('#card-body a[href$=\"/interview.ics\"]')")
	if ics := tab.EvalAsync("return (await fetch('/pipeline/" + app + "/interview.ics')).text()"); !strings.Contains(ics, "DTSTART:20270115T100000Z") {
		t.Fatalf("calendar entry: %s", ics)
	}
	tab.Shot("drawer")

	phone := OpenIn(t, Demo(t), 375, 812, "dark")
	phone.Go("/pipeline")
	phone.El("#pl-board")
	phone.NoOverflow("page")
}
