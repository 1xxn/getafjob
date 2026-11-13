//go:build e2e

package e2e

import (
	"strconv"
	"testing"
	"time"
)

// The filters of the job list are saved under a name with an alert, open again from the saved searches, are renamed,
// lose their alert and are deleted; the page fits a phone.
func TestSavedSearches(t *testing.T) {
	tab := OpenIn(t, Demo(t), 1440, 900, "light")
	name := "E2E Go " + strconv.FormatInt(time.Now().UnixNano(), 36)
	row := `[data-search]:has([data-search-name][value="` + name + `"])`

	tab.Go("/jobs?q=Developer&workplace=remote")
	tab.Click(`[popovertarget="jobs-more"]`)
	tab.Click(`[data-save-search]`)
	tab.WaitFor("document.getElementById('dlg-save-search').open")
	must(t, tab.El("#search-name").Input(name))
	tab.Click("#search-alert")
	tab.Click(`[data-save-search-form] button[type="submit"]`)
	tab.WaitWithin(10*time.Second, "location.pathname === '/searches' && location.search === '?done=saved'")
	tab.El(row)
	if tab.Eval("return !!document.querySelector('"+row+" [data-search-alert=\"off\"]')") != "true" {
		t.Error("the alert is not on")
	}
	tab.Axe("/searches")
	tab.Shot("list")

	// Open: the job list with the saved filters.
	tab.Click(row + " [data-search-open]")
	tab.WaitFor("location.pathname === '/jobs' && new URLSearchParams(location.search).get('q') === 'Developer' && " +
		"new URLSearchParams(location.search).get('workplace') === 'remote'")

	// Rename, alert off, delete.
	tab.Go("/searches")
	renamed := name + " remote"
	el := tab.El(row + " [data-search-name]")
	must(t, el.SelectAllText())
	must(t, el.Input(renamed))
	tab.Click(row + " [data-search-rename]")
	row = `[data-search]:has([data-search-name][value="` + renamed + `"])`
	tab.WaitWithin(10*time.Second, "location.search === '?done=renamed' && document.querySelector('"+row+"')")
	tab.Click(row + ` [data-search-alert="off"]`)
	tab.WaitWithin(10*time.Second, "location.search === '?done=alert' && document.querySelector('"+row+" [data-search-alert=\"on\"]')")
	tab.Click(row + " [data-search-delete]")
	tab.WaitFor("document.getElementById('confirm-dialog').open")
	tab.Click("[data-confirm-ok]")
	tab.WaitWithin(10*time.Second, "location.search === '?done=deleted' && !document.querySelector('"+row+"')")

	phone := OpenIn(t, Demo(t), 375, 812, "dark")
	phone.Go("/searches")
	phone.NoOverflow("saved searches")
}
