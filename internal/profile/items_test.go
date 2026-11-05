package profile

import (
	"slices"
	"testing"
)

func TestNormalizeQuestion(t *testing.T) {
	for in, want := range map[string]string{
		"Why do you want to work here? *":               "why do you want to work here",
		"Notice period (optional)":                      "notice period",
		"  Kündigungsfrist (Pflichtfeld)*":              "kündigungsfrist",
		"Are you legally authorized to work in the EU?": "are you legally authorized to work in the eu",
		"***": "",
	} {
		if got := NormalizeQuestion(in); got != want {
			t.Errorf("NormalizeQuestion(%q) = %q", in, got)
		}
	}
}

func TestFindAnswer(t *testing.T) {
	bank := []Answer{{Question: "Notice period?", Answer: "3 months"}, {Question: "Kündigungsfrist", Answer: "3 Monate"}}
	if a, ok := FindAnswer(bank, "Notice Period * (required)"); !ok || a.Answer != "3 months" {
		t.Errorf("found %+v, %v", a, ok)
	}
	if _, ok := FindAnswer(bank, "Notice period in weeks"); ok {
		t.Error("a different question matched")
	}
}

func TestTextBlock(t *testing.T) {
	tb := TextBlock{Name: "Intro", Body: "Dear {company} team,\n\nI am {full_name} and apply as {job_title}. {first_name} again: {city}."}
	if errs := tb.Validate(); len(errs) != 0 {
		t.Fatal(errs)
	}
	text, missing := tb.Fill(map[string]string{"company": "Muster GmbH", "full_name": "Max Mustermann", "first_name": "Max"})
	if text != "Dear Muster GmbH team,\n\nI am Max Mustermann and apply as {job_title}. Max again: {city}." ||
		!slices.Equal(missing, []string{"job_title", "city"}) {
		t.Errorf("fill = %q, missing %v", text, missing)
	}
	bad := TextBlock{Name: "x", Body: "Hello {employer}", Language: "fr"}
	if got := fields(bad.Validate()); !slices.Equal(got, []string{"body:profile.error.placeholder", "language:profile.error.invalid"}) {
		t.Errorf("errors = %v", got)
	}
	if v := ProfileValues(Profile{Personal: Personal{FirstName: "Max", LastName: "Mustermann"}}); v["full_name"] != "Max Mustermann" {
		t.Errorf("values = %v", v)
	}
}
