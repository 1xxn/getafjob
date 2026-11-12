//go:build e2e

package e2e

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/go-rod/rod/lib/input"
)

// Filter, open a job beside the list without a reload, and queue it from there (fixture site only: source demo).
func TestJobsFilterOpenQueue(t *testing.T) {
	tab := OpenIn(t, Demo(t), 1440, 900, "light")
	tab.Go("/jobs?source=demo&decision=all")
	tab.El(".job-rows .job-row")
	tab.Eval("window.__e2e = 1; return true")

	// Typing in the search filters in place and keeps the filter in the URL.
	must(t, tab.El(`.jobs-filters input[name="q"]`).Input("engineer"))
	tab.WaitWithin(10*time.Second, "location.search.includes('q=engineer') && !!document.querySelector('.jobs-chip')")
	if tab.Eval("return window.__e2e") != "1" {
		t.Fatal("filtering reloaded the page")
	}
	tab.Shot("filtered")

	// Open jobs without an application until one can be queued here (portal and mail postings cannot).
	var ids []string
	must(t, json.Unmarshal([]byte(tab.Eval(`return [...document.querySelectorAll('.job-row')]
		.filter(r => !r.dataset.state || r.dataset.state === 'new').map(r => r.dataset.id)`)), &ids))
	id := ""
	for _, cand := range ids {
		tab.Click(`.job-row[data-id="` + cand + `"] .job-row-link`)
		tab.WaitFor("location.search.includes('job=" + cand + "') && document.querySelector('#job-panel [data-job-id=\"" + cand + "\"]')")
		if tab.Eval(`return !!document.querySelector('#job-panel button[name="action"][value="queue"]')`) == "true" {
			id = cand
			break
		}
	}
	if id == "" {
		t.Skip("no listed demo job can be queued; reset the dev database")
	}
	if tab.Eval("return window.__e2e") != "1" {
		t.Fatal("opening a job reloaded the page")
	}
	tab.Shot("panel")

	// The back button leaves this job, forward returns to it.
	must(t, tab.Page.NavigateBack())
	tab.WaitWithin(10*time.Second, "!location.search.includes('job="+id+"')")
	must(t, tab.Page.NavigateForward())
	tab.WaitWithin(10*time.Second, "location.search.includes('job="+id+"') && document.querySelector('#job-panel [data-job-id]')")

	// Queue it: back on the list with a toast, the row now applying.
	tab.Click(`#job-panel form button[name="action"][value="queue"]`)
	tab.WaitWithin(15*time.Second, "document.querySelector('.ui-toast')")
	tab.WaitWithin(15*time.Second, `['applying', 'applied', 'stopped'].includes(document.querySelector('.job-row[data-id="`+id+`"]')?.dataset.state)`)
	tab.Shot("queued")
}

// On a phone the open job covers the list and closes again; keyboard j/k move in the list.
func TestJobsPhoneAndKeys(t *testing.T) {
	tab := OpenIn(t, Demo(t), 375, 812, "dark")
	tab.Go("/jobs?source=demo&decision=all")
	if over := tab.Eval("return Math.max(0, document.documentElement.scrollWidth - document.documentElement.clientWidth)"); over != "0" {
		t.Errorf("%s px horizontal overflow", over)
	}
	tab.Click(".job-row .job-row-link")
	tab.WaitFor("getComputedStyle(document.getElementById('job-panel')).position === 'fixed'")
	tab.Shot("sheet")
	tab.Click(`#job-panel .job-panel-top a[href^="/jobs"]`)
	tab.WaitFor("!document.querySelector('#job-panel .job-panel-inner')")

	desk := OpenIn(t, Demo(t), 1440, 900, "light")
	desk.Go("/jobs?source=demo&decision=all")
	must(t, desk.El(".job-row .job-row-link").Focus())
	must(t, desk.Page.Keyboard.Type(input.KeyJ))
	if desk.Eval("return document.activeElement === document.querySelectorAll('.job-row-link')[1]") != "true" {
		t.Fatal("j did not move to the next job")
	}
	must(t, desk.Page.Keyboard.Type(input.KeyK))
	if desk.Eval("return document.activeElement === document.querySelectorAll('.job-row-link')[0]") != "true" {
		t.Fatal("k did not move back")
	}
}
