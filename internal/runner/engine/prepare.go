package engine

import (
	"context"
	"errors"
	"regexp"
	"time"

	"github.com/go-rod/rod"

	"github.com/1xxn/getafjob/internal/runner/browser"
)

// ErrNoForm reports a posting from which no application form could be reached.
var ErrNoForm = errors.New("no application form found")

// maxHops bounds the clicks from the posting to the form.
const maxHops = 4

var (
	// applyRx names the link or button that leads from a posting to its form.
	applyRx = regexp.MustCompile(`(?i)\bapply\b|apply now|bewerb|zur bewerbung|jetzt bewerben|interessiert`)
	// rejectRx names the least consent a cookie banner offers.
	rejectRx = regexp.MustCompile(`(?i)reject|decline|deny|ablehnen|nur (notwendige|erforderliche|essenzielle)|` +
		`only (necessary|essential|required)|necessary only|essential only|nicht zustimmen|alle ablehnen`)
)

// scrollJS scrolls to the end so that lazy iframes load.
const scrollJS = `() => { window.scrollTo(0, document.documentElement.scrollHeight); return true; }`

// Prepare brings a run from the posting to the application form. It declines optional cookies (it never accepts
// them), loads lazy iframes, and follows the apply link or button, also into a new tab; a popup that closes
// itself leaves the flow where it was. Buttons that import data from third parties (autofill, apply with ...)
// are never used. It returns the page that shows the form.
func (e *Engine) Prepare(ctx context.Context, p *rod.Page) (*rod.Page, error) {
	p = p.Context(ctx)
	for hop := 0; ; hop++ {
		st, err := browser.Extract(p)
		if err != nil {
			return p, err
		}
		if hasForm(st) {
			return p, nil
		}
		if b, ok := pick(st, func(b browser.Button) bool { return b.Cookie && rejectRx.MatchString(b.Text) }); ok {
			if _, err := st.Click(b.ID, e.wait()); err != nil { //nolint:contextcheck // the page carries ctx
				return p, err
			}
			continue // the banner is gone; read the page again (does not count as a hop)
		}
		if _, err := p.Eval(scrollJS); err == nil {
			time.Sleep(500 * time.Millisecond)
			if st, err = browser.Extract(p); err == nil && hasForm(st) {
				return p, nil
			}
		}
		if hop >= maxHops {
			return p, ErrNoForm
		}
		b, ok := pick(st, func(b browser.Button) bool {
			return !b.Cookie && !b.Autofill && !b.Disabled && applyRx.MatchString(b.Text)
		})
		if !ok {
			return p, ErrNoForm
		}
		eff, err := st.Click(b.ID, e.wait()) //nolint:contextcheck // the page carries ctx
		if err != nil {
			return p, err
		}
		for i := len(eff.NewTargets) - 1; i >= 0; i-- { // the newest tab is the form
			tab, guard, err := browser.Adopt(p.Browser(), eff.NewTargets[i], 3*time.Second)
			if errors.Is(err, browser.ErrPopupClosed) {
				continue
			}
			if err != nil {
				return p, err
			}
			guard.Stop()
			p = tab.Context(ctx)
			break
		}
		_ = p.Timeout(e.wait()).WaitLoad()
	}
}

// hasForm reports a page that shows an application form: a file field or at least three fields.
func hasForm(st *browser.State) bool {
	fields := st.Fields()
	for _, f := range fields {
		if f.Kind == "file" {
			return true
		}
	}
	return len(fields) >= 3
}

func pick(st *browser.State, ok func(browser.Button) bool) (browser.Button, bool) {
	for _, b := range st.Buttons() {
		if ok(b) {
			return b, true
		}
	}
	return browser.Button{}, false
}
