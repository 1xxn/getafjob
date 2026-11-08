package engine

import (
	"testing"

	"github.com/1xxn/getafjob/internal/runner/browser"
)

func TestButtonTexts(t *testing.T) {
	for text, want := range map[string]string{
		"Submit application": "final", "Bewerbung absenden": "final", "Jetzt bewerben": "final", "Apply": "final",
		"Send": "final", "Bewerbung einreichen": "final",
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
