//go:build e2e

package e2e

import (
	"testing"
)

// A new user starts the setup at the CV and can skip through every step to the matches; the demo user resumes where
// the profile has gaps, sees ranked jobs on the last step and reaches the job list. Both sizes, no horizontal scroll.
func TestSetup(t *testing.T) {
	fresh := Open(t, 1440, 900, "light")
	fresh.Go("/setup")
	fresh.WaitFor("location.pathname === '/setup/cv'")
	fresh.El(`.wiz[data-setup-step="cv"] #wiz-progress`)
	for _, next := range []string{"profile", "search", "automation", "matches"} {
		fresh.Click(`[data-setup-skip]`)
		fresh.WaitFor("location.pathname === '/setup/" + next + "'")
		fresh.El(`.wiz[data-setup-step="` + next + `"]`)
	}
	fresh.El(`#setup-matches`)
	// A new user's automation is off until they turn it on: the last step says what to do next (ADR 0028).
	fresh.El(`[data-setup-after="false"] a[href="/settings/automation"]`)
	fresh.Shot("fresh-matches")

	for _, v := range []struct {
		name          string
		width, height int
		scheme        string
	}{{"desktop-dark", 1440, 900, "dark"}, {"phone-light", 375, 812, "light"}} {
		t.Run(v.name, func(t *testing.T) {
			tab := OpenIn(t, Demo(t), v.width, v.height, v.scheme)
			tab.Go("/setup/profile")
			tab.El(`.wiz[data-setup-step="profile"] [data-setup-next]`)
			tab.NoOverflow("profile")
			tab.Go("/setup")
			tab.WaitFor("location.pathname.startsWith('/setup/')")
			tab.Go("/setup/matches")
			tab.El(`#setup-matches [data-setup-match]`)
			tab.NoOverflow("matches")
			tab.Shot("matches")
			tab.Click(`[data-setup-jobs]`)
			tab.WaitFor("location.pathname === '/jobs'")
		})
	}
}
