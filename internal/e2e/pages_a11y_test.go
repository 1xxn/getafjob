//go:build e2e

package e2e

import (
	"strings"
	"testing"
)

// The core pages with the demo user's data pass axe (WCAG A/AA) and fit a tablet, light and dark. Phone and
// desktop sizes are covered by TestPagesRender.
func TestAccessibility(t *testing.T) {
	all := []string{"/", "/jobs", "/applications", "/needs-input", "/approvals", "/pipeline", "/activity", "/profile",
		"/settings/automation", "/setup/matches", "/searches"}
	for _, v := range []struct {
		name   string
		scheme string
		paths  []string // the dark run checks the pages with the most colors only
	}{{"tablet-light", "light", all}, {"tablet-dark", "dark", []string{"/", "/jobs", "/pipeline", "/activity"}}} {
		t.Run(v.name, func(t *testing.T) {
			tab := OpenIn(t, Demo(t), 768, 1024, v.scheme)
			tab.Go("/jobs?source=demo")
			job := strings.Trim(tab.Eval("return document.querySelector('.job-row')?.dataset.id || ''"), `"`)
			tab.Go("/applications")
			app := strings.Trim(tab.Eval("return document.querySelector('[data-app-row]')?.dataset.id || ''"), `"`)
			paths := v.paths
			if job != "" {
				paths = append(paths, "/jobs/"+job)
			}
			if app != "" {
				paths = append(paths, "/applications/"+app)
			}
			for _, p := range paths {
				tab.Go(p)
				tab.El("body.shell main h1")
				tab.NoOverflow(p)
				tab.Axe(p)
			}
		})
	}
}
