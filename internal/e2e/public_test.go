//go:build e2e

package e2e

import (
	"testing"
)

// The pages without sign-in (login, link sent, an invalid link) use the design system: one card, readable and
// without WCAG violations at phone and desktop size, light and dark.
func TestPublicPages(t *testing.T) {
	for _, v := range []struct {
		name          string
		width, height int
		scheme        string
	}{{"desktop-light", 1440, 900, "light"}, {"phone-dark", 375, 812, "dark"}} {
		t.Run(v.name, func(t *testing.T) {
			b, err := shared.Incognito()
			must(t, err)
			tab := OpenIn(t, b, v.width, v.height, v.scheme)
			tab.Go("/login")
			tab.El("body.pub .ui-card form[data-login] input[name=email]")
			tab.NoOverflow("login")
			tab.Axe("login")
			tab.Shot("login")

			must(t, tab.El("input[name=email]").Input("nobody@example.org"))
			tab.Eval("window.__e2e = 1; return true") // gone once the answer page has loaded
			tab.Click(`form[data-login] button[type="submit"]`)
			tab.WaitFor("!window.__e2e && document.readyState === 'complete' && !document.querySelector('form[data-login]')")
			tab.EvalAsync("await document.activeViewTransition?.finished.catch(() => {}); return 1") // as Tab.Go does
			tab.El("body.pub .pub-head h1")
			tab.NoOverflow("sent")
			tab.Axe("sent")
			tab.Shot("sent")
		})
	}
}
