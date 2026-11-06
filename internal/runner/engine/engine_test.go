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
