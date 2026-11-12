//go:build e2e

package e2e

import (
	"testing"
	"time"

	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/proto"
)

// WaitFor polls a JavaScript condition (a function body returning a boolean) for up to ten seconds.
func (tab *Tab) WaitFor(cond string) {
	tab.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if tab.Eval("return Boolean("+cond+")") == "true" {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	tab.Shot("timeout")
	tab.t.Fatalf("timed out waiting for %s", cond)
}

// Click waits until the element is visible, no view transition runs and the element itself is under its middle
// (its own ::after counts as itself, unlike in rod's covered check), then clicks there with the mouse, like a user.
func (tab *Tab) Click(selector string) {
	tab.t.Helper()
	must(tab.t, tab.El(selector).Timeout(actionTimeout).WaitVisible())
	deadline := time.Now().Add(10 * time.Second)
	for {
		// Look the element up on every try: htmx may swap it for a new node (history restore, live refresh); wait
		// while a request runs, a click then can be dropped or undone by the swap.
		res, err := tab.El(selector).Timeout(actionTimeout).Eval(`function () {
			if (document.activeViewTransition || !this.isConnected || document.querySelector('.htmx-request')) return null;
			this.scrollIntoView({block: 'center', inline: 'nearest'});
			const r = this.getBoundingClientRect();
			const x = r.left + r.width / 2, y = r.top + r.height / 2;
			const top = document.elementFromPoint(x, y);
			return top && (top === this || this.contains(top)) ? {x, y} : null;
		}`)
		if err == nil && !res.Value.Nil() {
			pt := proto.Point{X: res.Value.Get("x").Num(), Y: res.Value.Get("y").Num()}
			mouse := tab.Page.Timeout(actionTimeout).Mouse
			must(tab.t, mouse.MoveTo(pt))
			must(tab.t, mouse.Click(proto.InputMouseButtonLeft, 1))
			return
		}
		if time.Now().After(deadline) {
			tab.t.Fatalf("%s: covered, detached or in a view transition (%v)", selector, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

var pages = []string{"/", "/jobs", "/applications", "/needs-input", "/approvals", "/pipeline", "/profile", "/documents", "/answers", "/texts",
	"/settings/search", "/settings/automation", "/settings/notifications", "/settings/integrations", "/settings/account", "/privacy", "/activity", "/setup/profile", "/setup/matches"}

// Every page renders in the shell with a heading, without console errors, horizontal scrolling or WCAG A/AA
// violations found by axe, in light and dark.
func TestPagesRender(t *testing.T) {
	t.Parallel()
	for _, v := range []struct {
		name          string
		width, height int
		scheme        string
	}{{"desktop-light", 1440, 900, "light"}, {"phone-dark", 375, 812, "dark"}} {
		t.Run(v.name, func(t *testing.T) {
			t.Parallel()
			tab := Open(t, v.width, v.height, v.scheme)
			for _, p := range pages {
				tab.Go(p)
				tab.El("body.shell main h1")
				tab.NoOverflow(p)
				tab.Axe(p)
				tab.Shot(p)
			}
		})
	}
}

// Links swap the page without a reload, keep the live dock, move focus to the heading and mark the active item.
func TestShellNavigation(t *testing.T) {
	tab := Open(t, 1440, 900, "light")
	tab.Go("/")
	tab.Eval("window.__e2e = 1; document.getElementById('live-dock').dataset.e2e = 'kept'; return true")

	tab.Click(`.shell-side a[href="/jobs"]`)
	tab.WaitFor("location.pathname === '/jobs' && document.title.startsWith('Jobs')")
	tab.WaitFor("document.activeElement && document.activeElement.tagName === 'H1'")
	if tab.Eval("return window.__e2e") != "1" {
		t.Fatal("the link reloaded the page instead of swapping it")
	}
	if tab.Eval("return document.getElementById('live-dock').dataset.e2e") != `"kept"` {
		t.Fatal("live dock not preserved")
	}
	if tab.Eval(`return document.querySelector('.shell-side [aria-current="page"]').getAttribute('href')`) != `"/jobs"` {
		t.Fatal("active navigation item not updated")
	}

	// Command palette: Ctrl+K, filter, Enter.
	must(t, tab.Page.KeyActions().Press(input.ControlLeft).Type(input.KeyK).Release(input.ControlLeft).Do())
	tab.WaitFor("document.getElementById('palette').open")
	must(t, tab.El("#palette-q").Input("profi"))
	must(t, tab.Page.Keyboard.Type(input.Enter))
	tab.WaitFor("location.pathname === '/profile'")
	if tab.Eval("return window.__e2e") != "1" {
		t.Fatal("palette navigation reloaded the page")
	}

	// Theme from the account menu, then back to the system setting.
	tab.Click(".shell-user")
	tab.Click(`#user-menu button[name="theme"][value="dark"]`)
	tab.WaitFor("document.documentElement.dataset.theme === 'dark'")
	tab.Shot("dark")
	tab.Click(".shell-user")
	tab.Click(`#user-menu button[name="theme"][value=""]`)
	tab.WaitFor("!document.documentElement.dataset.theme")
}

// On a phone the tab bar replaces the sidebar and the menu opens the navigation as a drawer.
func TestShellPhone(t *testing.T) {
	tab := Open(t, 375, 812, "dark")
	tab.Go("/")
	if tab.Eval("return getComputedStyle(document.querySelector('.shell-side')).display") != `"none"` {
		t.Fatal("sidebar visible on a phone")
	}
	tab.Click(`.shell-bottom a[href="/jobs"]`)
	tab.WaitFor("location.pathname === '/jobs'")
	tab.Click(".shell-bottom button")
	tab.WaitFor("document.getElementById('nav-drawer').open")
	tab.Shot("drawer")
	tab.Click(`#nav-drawer a[href="/documents"]`)
	tab.WaitFor("location.pathname === '/documents' && !document.getElementById('nav-drawer')?.open")
}
