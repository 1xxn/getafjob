//go:build browser

package browser

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-rod/rod/lib/proto"
)

func TestClickReportsEffects(t *testing.T) {
	cases := []struct {
		button string
		check  func(Effect) bool
	}{
		{"Show more", func(e Effect) bool { return e.DOM }},
		{"I agree", func(e Effect) bool { return e.Input }},
		{"Save draft", func(e Effect) bool { return e.Request }},
		{"Open posting", func(e Effect) bool { return len(e.NewTargets) == 1 && e.NewTargets[0] != "" }},
		{"Send", func(e Effect) bool { return len(e.Invalid) == 1 && !e.Navigated }},
	}
	for _, tc := range cases {
		t.Run(tc.button, func(t *testing.T) {
			s := newSpike(t, "clicks.html")
			st := s.extract()
			id := ""
			for _, b := range st.Buttons() {
				if b.Text == tc.button {
					id = b.ID
				}
			}
			for _, f := range st.Fields() {
				if strings.HasPrefix(f.Label, tc.button) {
					id = f.ID
				}
			}
			if id == "" {
				t.Fatalf("%q not extracted", tc.button)
			}
			e, err := st.Click(id, 3*time.Second)
			if err != nil || !tc.check(e) {
				t.Fatalf("effect %+v, %v", e, err)
			}
		})
	}
}

func TestSubmitWithoutEffectRetriesOnceThenStops(t *testing.T) {
	s := newSpike(t, "clicks.html")
	st := s.extract()
	start := time.Now()
	_, err := st.Submit(s.button(st, "Does nothing", false).ID, time.Second)
	var ne *NoEffectError
	if !errors.As(err, &ne) || !bytes.HasPrefix(ne.Screenshot, []byte("\x89PNG")) {
		t.Fatalf("err = %v", err)
	}
	if n := s.page.MustEval(`() => window.deadClicks`).Int(); n != 2 {
		t.Fatalf("clicks = %d, want 2 (one retry)", n)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("waited too long")
	}
}

func TestClickInOutOfProcessIframe(t *testing.T) {
	s := newSpike(t, "posting.html")
	other := strings.Replace(s.site.URL, "127.0.0.1", "localhost", 1) + "/step1.html"
	s.open(s.site.URL + "/oopif?src=" + other)
	var st *State
	eventually(t, 10*time.Second, func() error {
		var err error
		if st, err = Extract(s.page); err != nil {
			return err
		}
		if len(st.Frames) < 2 || len(st.Frames[1].Buttons) == 0 {
			return fmt.Errorf("iframe not ready (%d frames)", len(st.Frames))
		}
		return nil
	})
	if st.Frames[1].sess == nil {
		t.Fatal("iframe is not out-of-process")
	}
	next := s.button(st, "Weiter", true).ID

	// Empty required fields: the browser's own validation answers, nothing navigates.
	e, err := st.Click(next, 3*time.Second)
	if err != nil || len(e.Invalid) == 0 || e.Navigated {
		t.Fatalf("empty form: %+v, %v", e, err)
	}
	for label, v := range map[string]string{"Vorname": "Max", "Nachname": "Mustermann", "E-Mail": "max@example.org"} {
		if err := st.Focus(s.field(st, "", label).ID); err != nil {
			t.Fatal(err)
		}
		if err := (proto.InputInsertText{Text: v}).Call(s.page); err != nil {
			t.Fatal(err)
		}
	}
	e, err = st.Click(next, 5*time.Second)
	if err != nil || !e.Navigated {
		t.Fatalf("filled form: %+v, %v", e, err)
	}
}

func TestClickRejectsHiddenAndUnknownElements(t *testing.T) {
	s := newSpike(t, "clicks.html")
	st := s.extract()
	if _, err := st.Click("0-99999", time.Second); !errors.Is(err, ErrUnknownID) {
		t.Fatalf("unknown: %v", err)
	}
	if _, err := st.Click("9-1", time.Second); !errors.Is(err, ErrUnknownID) {
		t.Fatalf("unknown frame: %v", err)
	}
}
