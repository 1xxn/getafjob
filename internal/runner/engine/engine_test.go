package engine

import (
	"testing"

	"github.com/1xxn/getafjob/internal/runner/browser"
	"github.com/1xxn/getafjob/internal/runner/executor"
)

func TestButtonTexts(t *testing.T) {
	for text, want := range map[string]string{
		"Submit application": "final", "Bewerbung absenden": "final", "Jetzt bewerben": "final", "Apply": "final",
		"Send": "final", "Bewerbung einreichen": "final", "Bewerben": "final", "Senden": "final",
		"Weiter": "next", "Next": "next", "Continue": "next", "Fortfahren": "next",
		"Save draft": "", "Sender settings": "", "Applying tips": "",
	} {
		got := ""
		switch {
		case finalRx.MatchString(text):
			got = "final"
		case nextRx.MatchString(text):
			got = "next"
		}
		if got != want {
			t.Errorf("%q = %q, want %q", text, got, want)
		}
	}
}

// Only a clear send or next text is clicked; any other form button parks the run, so a click never sends an
// application past the register, and a submit-type helper button (add position) is never taken for "next".
func TestFormButton(t *testing.T) {
	st := func(texts ...string) *browser.State {
		f := &browser.Frame{Fields: []browser.Field{{ID: "0-1", Kind: "text", Form: "f"}}}
		for i, t := range texts {
			f.Buttons = append(f.Buttons, browser.Button{ID: "0-b" + string(rune('a'+i)), Text: t, Kind: "submit", Form: "f"})
		}
		return &browser.State{Frames: []*browser.Frame{f}}
	}
	for _, c := range []struct {
		texts []string
		want  string
		final bool
	}{
		{[]string{"+ Add position", "Weiter"}, "Weiter", false},
		{[]string{"Save draft", "Submit application"}, "Submit application", true},
		{[]string{"Bewerben"}, "Bewerben", true},
	} {
		b, final, err := formButton(st(c.texts...), executor.Plan{})
		if err != nil || b.Text != c.want || final != c.final {
			t.Errorf("%v = %q final=%v err=%v; want %q final=%v", c.texts, b.Text, final, err, c.want, c.final)
		}
	}
	if _, _, err := formButton(st("Los"), executor.Plan{}); err == nil {
		t.Error("an unclear button was accepted")
	}
}

func TestConfirmation(t *testing.T) {
	yes := []browser.Frame{
		{Headings: []string{"Vielen Dank für Ihre Bewerbung!"}},
		{Text: "Thank you for applying. Your application has been received."},
		{URL: "https://jobs.example.org/apply/confirmation"},
		{Text: "Wir haben deine Bewerbung erhalten."},
	}
	no := []browser.Frame{
		{Headings: []string{"Apply: Backend Engineer"}, Text: "Please fill in the form."},
		{Text: "Thank you for your interest in our company."},
	}
	for _, f := range yes {
		if !confirmation(&browser.State{Frames: []*browser.Frame{&f}}) {
			t.Errorf("not recognized: %+v", f)
		}
	}
	for _, f := range no {
		if confirmation(&browser.State{Frames: []*browser.Frame{&f}}) {
			t.Errorf("false confirmation: %+v", f)
		}
	}
}

func TestCaptchaRequiresToken(t *testing.T) {
	unsolved := &browser.State{Frames: []*browser.Frame{{
		Captcha:      []browser.Captcha{{Cls: "cf-turnstile", Visible: true, Challenge: true}},
		CaptchaToken: false,
	}}}
	if qs := captcha(unsolved); len(qs) != 1 || qs[0].Kind != "captcha" || qs[0].Label != "cf-turnstile" {
		t.Fatalf("unsolved captcha = %+v", qs)
	}
	solved := &browser.State{Frames: []*browser.Frame{{
		Captcha:      []browser.Captcha{{Cls: "cf-turnstile", Visible: true, Challenge: true}},
		CaptchaToken: true,
	}}}
	if qs := captcha(solved); len(qs) != 0 {
		t.Fatalf("solved captcha still asked: %+v", qs)
	}
}

func TestSecurityCodeDetection(t *testing.T) {
	st := &browser.State{Frames: []*browser.Frame{{Fields: []browser.Field{
		{ID: "0-1", Kind: "text", Label: "First Name *", Value: "Max"},
		{ID: "0-2", Kind: "text", Label: "Security code sent to your email *", Name: "security_code"},
	}}}}
	qs := securityCode(st)
	if len(qs) != 1 || qs[0].Kind != "security_code" || qs[0].FieldID != "0-2" {
		t.Fatalf("securityCode = %+v", qs)
	}
	st.Frames[0].Fields[1].Value = "A1B2C3D4"
	if qs := securityCode(st); len(qs) != 0 {
		t.Fatalf("filled security code still asked: %+v", qs)
	}
}
