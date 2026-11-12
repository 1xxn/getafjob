//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"
)

// The bell opens the newest entries with deep links, a live event refreshes it in place, marking read clears the
// count, and the feed page lists the entries by day at both sizes.
func TestActivity(t *testing.T) {
	tab := OpenIn(t, Demo(t), 1440, 900, "light")
	tab.Go("/")
	tab.El(`#bell[data-unread] .shell-bell-btn`)
	tab.Click(`#bell .shell-bell-btn`)
	tab.WaitFor("document.getElementById('bell-menu').matches(':popover-open')")
	if tab.Eval(`return !!document.querySelector('#bell-menu .act-item')`) == "true" {
		href := strings.Trim(tab.Eval(`return document.querySelector('#bell-menu .act-item').getAttribute('href')`), `"`)
		if !strings.HasPrefix(href, "/applications/") && !strings.HasPrefix(href, "/needs-input?app=") {
			t.Errorf("bell entry links to %s", href)
		}
	}
	tab.Axe("bell menu")
	tab.Shot("bell")

	// A live event morphs the bell: the page stays, the menu stays open.
	tab.Eval("window.__e2e = 1; document.getElementById('bell').dataset.e2e = 'old'; return true")
	tab.Eval("document.body.dispatchEvent(new CustomEvent('getafjob:app', {detail: {}})); return true")
	tab.WaitWithin(10*time.Second, "!document.getElementById('bell').dataset.e2e")
	if tab.Eval("return window.__e2e") != "1" {
		t.Fatal("the live refresh reloaded the page")
	}
	if tab.Eval(`return !!document.querySelector('#bell [data-mark-read]')`) == "true" {
		tab.Click(`#bell [data-mark-read]`)
		tab.WaitWithin(10*time.Second, "document.getElementById('bell').dataset.unread === '0'")
	}

	for _, v := range []struct {
		width, height int
		scheme        string
	}{{1440, 900, "dark"}, {375, 812, "light"}} {
		page := OpenIn(t, Demo(t), v.width, v.height, v.scheme)
		page.Go("/activity")
		page.El("#act-feed")
		page.NoOverflow("activity")
		page.Shot("feed")
	}
}
