//go:build e2e

package e2e

import (
	"testing"
	"time"
)

// A new user is led to the setup; the demo user sees numbers, actions, runs, matches and the trend, at both sizes,
// and a live event refreshes the dashboard in place.
func TestDashboard(t *testing.T) {
	fresh := Open(t, 1440, 900, "light")
	fresh.Go("/")
	fresh.El(`#dashboard .ui-empty a[href="/setup"]`)
	fresh.Shot("fresh")

	for _, v := range []struct {
		name          string
		width, height int
		scheme        string
	}{{"desktop-dark", 1440, 900, "dark"}, {"phone-light", 375, 812, "light"}} {
		t.Run(v.name, func(t *testing.T) {
			tab := OpenIn(t, Demo(t), v.width, v.height, v.scheme)
			tab.Go("/")
			tab.El("#dashboard .dash-kpis .ui-kpi")
			tab.El("#dashboard .dash-trend svg rect")
			if over := tab.Eval("return Math.max(0, document.documentElement.scrollWidth - document.documentElement.clientWidth)"); over != "0" {
				t.Errorf("%s px horizontal overflow", over)
			}
			tab.Shot("demo")

			// A live event refreshes the dashboard by morphing it: the page stays, the server's markup wins.
			tab.Eval("window.__e2e = 1; document.getElementById('dashboard').dataset.e2e = 'old'; return true")
			tab.Eval("document.body.dispatchEvent(new CustomEvent('getafjob:app', {detail: {}})); return true")
			tab.WaitWithin(10*time.Second, "!document.getElementById('dashboard').dataset.e2e")
			if tab.Eval("return window.__e2e") != "1" {
				t.Fatal("the live refresh reloaded the page")
			}
		})
	}
}
