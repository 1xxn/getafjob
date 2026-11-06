//go:build browser

package browser

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSelfClosingPopupKeepsTheFlowInTheOpener(t *testing.T) {
	s := newSpike(t, "popup_posting.html")
	st := s.extract()
	e, err := st.Click(s.button(st, "Apply now", false).ID, 3*time.Second)
	if err != nil || len(e.NewTargets) != 1 {
		t.Fatalf("effect %+v, %v", e, err)
	}
	if _, _, err := Adopt(s.b.Browser, e.NewTargets[0], 3*time.Second); !errors.Is(err, ErrPopupClosed) {
		t.Fatalf("adopt: %v", err)
	}
	eventually(t, 5*time.Second, func() error {
		if info, err := s.page.Info(); err != nil || !strings.HasSuffix(info.URL, "/apply.html") {
			return errors.New("opener did not continue")
		}
		return nil
	})
}

func TestNewTabIsAdopted(t *testing.T) {
	s := newSpike(t, "clicks.html")
	st := s.extract()
	e, err := st.Click(s.button(st, "Open posting", false).ID, 3*time.Second)
	if err != nil || len(e.NewTargets) != 1 {
		t.Fatalf("effect %+v, %v", e, err)
	}
	p, d, err := Adopt(s.b.Browser, e.NewTargets[0], time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Stop()
	tab, err := Extract(p)
	if err != nil || !strings.HasSuffix(tab.Frames[0].URL, "/posting.html") {
		t.Fatalf("tab: %+v, %v", tab, err)
	}
}

func TestDialogsAreAnsweredAndRecorded(t *testing.T) {
	s := newSpike(t, "dialogs.html")
	d := GuardDialogs(s.page)
	defer d.Stop()
	st := s.extract()
	for _, b := range []string{"Save draft", "Withdraw"} {
		if e, err := st.Click(s.button(st, b, false).ID, 3*time.Second); err != nil || !e.DOM {
			t.Fatalf("%s: %+v, %v", b, e, err)
		}
	}
	if s.page.MustEval(`() => window.withdrawn`).Bool() {
		t.Fatal("confirm was accepted")
	}
	// Leaving the page asks beforeunload; it is confirmed so the runner can move on.
	s.open(s.site.URL + "/posting.html")
	seen := d.Seen()
	want := []Dialog{{"alert", "Draft saved", true}, {"confirm", "Withdraw your application?", false}}
	if len(seen) < 2 || seen[0] != want[0] || seen[1] != want[1] {
		t.Fatalf("dialogs = %+v", seen)
	}
}
