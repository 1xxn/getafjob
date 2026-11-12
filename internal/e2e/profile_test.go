//go:build e2e

package e2e

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-rod/rod/lib/proto"
)

// A profile change is stored without pressing save and is still there after leaving the page; a station moves
// by dragging its handle with the mouse.
func TestProfileAutosave(t *testing.T) {
	tab := OpenIn(t, Demo(t), 1440, 900, "light")
	tab.Go("/profile")
	before := strings.Trim(tab.Eval("return document.getElementById('f-city').value"), `"`)
	city := "Berlin " + strconv.FormatInt(time.Now().Unix()%1000, 10)
	el := tab.El("#f-city")
	must(t, el.SelectAllText())
	must(t, el.Input(city))
	tab.WaitWithin(10*time.Second, "document.getElementById('save-profile')?.dataset.saveState === 'saved'")
	tab.Shot("saved")
	tab.Go("/jobs")
	tab.Go("/profile")
	if got := strings.Trim(tab.Eval("return document.getElementById('f-city').value"), `"`); got != city {
		t.Fatalf("city after leaving: %q, want %q", got, city)
	}
	// Put the city back for the other journeys.
	el = tab.El("#f-city")
	must(t, el.SelectAllText())
	must(t, el.Input(before))
	tab.WaitWithin(10*time.Second, "document.getElementById('save-profile')?.dataset.saveState === 'saved'")

	// Dragging with the fresh user, who adds two positions first.
	tab = Open(t, 1440, 900, "light")
	for _, title := range []string{"Junior Developer", "Senior Developer"} {
		tab.Go("/profile")
		add := `form[action="/profile/experiences"]`
		must(t, tab.El(add+` input[name="title"]`).Input(title))
		must(t, tab.El(add+` input[name="company"]`).Input("Muster GmbH"))
		tab.Eval("document.querySelector('" + add + "').dataset.e2e = 'old'; return true")
		tab.Click(add + ` button[type="submit"]`)
		tab.WaitWithin(10*time.Second, "!document.querySelector('form[data-e2e]') && location.pathname === '/profile'")
	}
	ids := func() string {
		return tab.Eval("return [...document.querySelector('.st-list').querySelectorAll(':scope > [data-sort-item]')].map(e => e.dataset.id).join(' ')")
	}
	first := ids()
	drag := func() {
		tab.EvalAsync("await document.activeViewTransition?.finished.catch(() => {}); return 1") // its overlay takes the pointer
		handle := tab.El(".st-list > [data-sort-item]:nth-child(2) [data-drag-handle]")
		must(t, handle.ScrollIntoView())
		box := handle.MustShape().Box()
		target := tab.El(".st-list > [data-sort-item]:first-child").MustShape().Box()
		m := tab.Page.Mouse
		must(t, m.MoveTo(proto.Point{X: box.X + box.Width/2, Y: box.Y + box.Height/2}))
		must(t, m.Down(proto.InputMouseButtonLeft, 1))
		must(t, m.MoveLinear(proto.Point{X: box.X + box.Width/2, Y: target.Y + 4}, 12))
		must(t, m.Up(proto.InputMouseButtonLeft, 1))
	}
	drag()
	moved := ids()
	if moved == first {
		t.Fatal("dragging did not change the order")
	}
	tab.WaitWithin(10*time.Second, "!document.querySelector('.htmx-request')")
	tab.Go("/profile")
	if ids() != moved {
		t.Fatal("the new order was not stored")
	}
	tab.Shot("dragged")
}
