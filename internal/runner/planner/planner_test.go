package planner

import (
	"context"
	"testing"

	"github.com/1xxn/getafjob/internal/runner/browser"
)

var know = Knowledge{
	Profile: map[string]string{
		"personal.first_name": "Max", "personal.last_name": "Mustermann", "personal.full_name": "Max Mustermann",
		"personal.email": "max.mustermann@example.org", "personal.phone": "+49 30 1234567", "personal.city": "Berlin",
		"personal.country": "Germany", "personal.postal_code": "10115", "personal.street": "Musterstraße 1",
		"personal.linkedin": "https://www.linkedin.com/in/max-mustermann", "personal.github": "https://github.com/maxmustermann",
		"work.notice_period": "3 months", "work.earliest_start": "2027-01-01", "work.salary_expectation": "75000",
		"work.years_total": "7", "work.current_title": "Backend Engineer", "work.needs_sponsorship": "false",
		"work.work_authorized_eu": "true", "work.willing_to_relocate": "false", "preferences.talent_pool": "true",
		"preferences.diversity": "decline",
	},
	Answers: []Answer{{ID: "0192f3a4-5b6c-7d8e-9f01-000000000001", Question: "How did you hear about us?", Answer: "Company website"},
		{ID: "0192f3a4-5b6c-7d8e-9f01-000000000002", Question: "First name", Answer: "Maximilian"}},
	Texts: []Text{{ID: "0192f3a4-5b6c-7d8e-9f01-0000000000f1", Name: "Cover letter", Language: "en", Body: "Dear team, ..."},
		{ID: "0192f3a4-5b6c-7d8e-9f01-0000000000f2", Name: "Anschreiben", Language: "de", Body: "Liebes Team, ..."}},
	Documents: []Document{{ID: "0192f3a4-5b6c-7d8e-9f01-0000000000c1", File: "Max_Mustermann_CV.pdf", Kind: "cv"}, {ID: "0192f3a4-5b6c-7d8e-9f01-0000000000c2", File: "CV_short.pdf", Kind: "cv"},
		{ID: "0192f3a4-5b6c-7d8e-9f01-0000000000d1", File: "Anschreiben.pdf", Kind: "cover_letter", Language: "de"},
		{ID: "0192f3a4-5b6c-7d8e-9f01-0000000000e1", File: "Zeugnis_1.pdf", Kind: "certificate"}, {ID: "0192f3a4-5b6c-7d8e-9f01-0000000000e2", File: "Zeugnis_2.pdf", Kind: "certificate"}},
	CV: "0192f3a4-5b6c-7d8e-9f01-0000000000c1", Language: "en", Origins: []string{"https://boards.example.org"},
}

func state(fields ...browser.Field) *browser.State {
	for i := range fields {
		if fields[i].ID == "" {
			fields[i].ID = "0-" + string(rune('a'+i))
		}
	}
	return &browser.State{Frames: []*browser.Frame{{Fields: fields}}}
}

func TestLabels(t *testing.T) {
	cases := map[string]string{
		"First Name *":          "answer#0192f3a4-5b6c-7d8e-9f01-000000000002", // the answer bank wins for the same question
		"Vorname":               "profile.personal.first_name",
		"Last name":             "profile.personal.last_name",
		"Nachname*":             "profile.personal.last_name",
		"Email address":         "profile.personal.email",
		"E-Mail-Adresse":        "profile.personal.email",
		"Phone":                 "profile.personal.phone",
		"Mobilnummer":           "profile.personal.phone",
		"Full name":             "profile.personal.full_name",
		"First and last name":   "profile.personal.full_name",
		"Vor- und Nachname":     "profile.personal.full_name",
		"Postleitzahl":          "profile.personal.postal_code",
		"Straße und Hausnummer": "profile.personal.street",
		"Wohnort":               "profile.personal.city",
		"Location (City)":       "profile.personal.city",
		"What is your current city and country of residence?":                                                            "profile.personal.city",
		"What would be your preferred main location for your contract? We can sponsor visas for Germany for most roles.": "profile.personal.country",
		"LinkedIn Profile":                                        "profile.personal.linkedin",
		"Kündigungsfrist":                                         "profile.work.notice_period",
		"Earliest start date":                                     "profile.work.earliest_start",
		"Gehaltsvorstellung (brutto/Jahr)":                        "profile.work.salary_expectation",
		"Years of professional experience":                        "profile.work.years_total",
		"Current job title":                                       "profile.work.current_title",
		"How did you hear about us?":                              "answer#0192f3a4-5b6c-7d8e-9f01-000000000001",
		"Will you now or in the future require visa sponsorship?": "profile.work.needs_sponsorship",
		"Do you need a work permit?":                              "profile.work.needs_sponsorship",
		"Are you legally authorized to work in the EU?":           "profile.work.work_authorized_eu",
		"Benötigen Sie eine Arbeitserlaubnis?":                    "profile.work.needs_sponsorship",
		"Haben Sie eine Arbeitserlaubnis für Deutschland?":        "profile.work.work_authorized_eu",
		"Would you be willing to relocate?":                       "profile.work.willing_to_relocate",
	}
	p := &Planner{K: know}
	for label, want := range cases {
		plan, qs, _ := p.Plan(context.Background(), state(browser.Field{Kind: "text", Label: label, Required: true}))
		if len(plan.Values) != 1 || plan.Values[0].Source != want || len(qs) != 0 {
			t.Errorf("%q: %+v %+v, want %s", label, plan.Values, qs, want)
		}
	}
}

func TestChoicesFilesAndTexts(t *testing.T) {
	p := &Planner{K: know}
	st := state(
		browser.Field{Kind: "radio", Label: "Do you require sponsorship?", Required: true,
			Options: []browser.Option{{ID: "0-y", Label: "Ja"}, {ID: "0-n", Label: "Nein"}}},
		browser.Field{Kind: "checkbox", Label: "Add me to your talent pool"},
		browser.Field{Kind: "select", Label: "Gender", Required: true,
			Options: []browser.Option{{Label: "Male"}, {Label: "Female"}, {Label: "I don't wish to answer"}}},
		browser.Field{Kind: "file", Label: "Resume/CV", Required: true},
		browser.Field{Kind: "file", Label: "Certificates"},
		browser.Field{Kind: "file", Label: "Cover letter"}, // only a German one exists, the form is English
		browser.Field{Kind: "textarea", Label: "Why do you want to join us?", Required: true},
	)
	plan, qs, _ := p.Plan(context.Background(), st)
	got := map[string]string{}
	files := map[string][]string{}
	for _, v := range plan.Values {
		got[v.Label] = v.Text + "|" + v.Source
		files[v.Label] = v.Files
	}
	want := map[string]string{
		"Do you require sponsorship?": "No|profile.work.needs_sponsorship",
		"Add me to your talent pool":  "true|profile.preferences.talent_pool",
		"Gender":                      "Prefer not to say|profile.preferences.diversity",
		"Resume/CV":                   "|document#0192f3a4-5b6c-7d8e-9f01-0000000000c1",
		"Certificates":                "|document#0192f3a4-5b6c-7d8e-9f01-0000000000e1",
		"Why do you want to join us?": "Dear team, ...|textblock#0192f3a4-5b6c-7d8e-9f01-0000000000f1",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if len(files["Certificates"]) != 2 || files["Resume/CV"][0] != "Max_Mustermann_CV.pdf" {
		t.Errorf("files = %v", files)
	}
	if _, ok := got["Cover letter"]; ok || len(qs) != 0 {
		t.Errorf("cover letter planned or questions %+v", qs)
	}
}

type drafter struct{}

func (drafter) Draft(context.Context, browser.Field) (string, error) { return "a draft", nil }

func TestQuestionsInsteadOfGuesses(t *testing.T) {
	k := know
	k.Profile = map[string]string{"personal.country": "Spain", "preferences.diversity": "answer"}
	p := &Planner{K: k, Drafter: drafter{}}
	st := state(
		browser.Field{Kind: "select", Label: "Country", Required: true, Options: []browser.Option{{Label: "Germany"}, {Label: "France"}}},
		browser.Field{Kind: "text", Label: "Favorite color", Required: true},
		browser.Field{Kind: "text", Label: "Nickname"}, // optional and unknown: left empty
		browser.Field{Kind: "radio", Label: "Gender", Required: true, Options: []browser.Option{{Label: "Male"}, {Label: "Female"}}},
		browser.Field{Kind: "password", Label: "Password", Required: true},
	)
	plan, qs, _ := p.Plan(context.Background(), st)
	if len(plan.Values) != 0 || len(qs) != 3 {
		t.Fatalf("values %+v, questions %+v", plan.Values, qs)
	}
	for _, q := range qs {
		if q.Draft != "a draft" {
			t.Errorf("no draft on %+v", q)
		}
	}
}

func TestPrecheck(t *testing.T) {
	p := &Planner{K: know}
	fields := []browser.Field{
		{Kind: "text", Label: "First Name", Required: true},
		{Kind: "select", Label: "Will you now or in the future require visa sponsorship?", Required: true,
			Options: []browser.Option{{Label: "Yes"}, {Label: "No"}}},
		{Kind: "select", Label: "Which of our values speaks to you most?", Required: true,
			Options: []browser.Option{{Label: "Ownership"}, {Label: "Curiosity"}}},
		{Kind: "text", Label: "LinkedIn Profile"},
	}
	qs := p.Precheck(context.Background(), fields)
	if len(qs) != 1 || qs[0].Label != "Which of our values speaks to you most?" || qs[0].FieldID != "" {
		t.Fatalf("questions = %+v", qs)
	}
	if fields[0].ID != "" {
		t.Fatal("precheck changed the caller's fields")
	}
}

func TestSecurityCodeAlwaysGoesToHumanEvenWhenInAnswerBank(t *testing.T) {
	k := know
	k.Answers = append([]Answer{{
		ID:       "0192f3a4-5b6c-7d8e-9f01-000000000088",
		Question: "Security code sent to your email *",
		Answer:   "12345678",
	}}, k.Answers...)
	p := &Planner{K: k, Drafter: drafter{}}
	st := state(
		browser.Field{ID: "0-1", Kind: "text", Label: "First Name", Required: true},
		browser.Field{ID: "0-2", Kind: "text", Label: "Security code sent to your email *", Name: "security_code", Required: true},
	)
	plan, qs, err := p.Plan(context.Background(), st)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Values) != 1 || plan.Values[0].FieldID != "0-1" {
		t.Fatalf("plan values = %+v", plan.Values)
	}
	if len(qs) != 1 || qs[0].Kind != "security_code" || qs[0].FieldID != "0-2" || qs[0].Draft != "" {
		t.Fatalf("security code question = %+v", qs)
	}
}

func TestRealFormQuirks(t *testing.T) {
	p := &Planner{K: know}
	st := state(
		browser.Field{Kind: "file", Label: "Autofill from resume Upload your resume here"},
		browser.Field{Kind: "combobox", Label: "Location (City)", Required: true},
		browser.Field{Kind: "checkbox", Label: "I can be based in Germany and do not need visa support"},
		browser.Field{Kind: "checkbox", Label: "Germany"},
		browser.Field{Kind: "checkbox", Label: "Veteran"},
		browser.Field{Kind: "text", Label: "Would you like to provide any additional context or details for your salary expectation?"},
	)
	plan, qs, err := p.Plan(context.Background(), st)
	if err != nil {
		t.Fatal(err)
	}
	if len(qs) != 0 || len(plan.Values) != 1 {
		t.Fatalf("values = %+v, questions = %+v", plan.Values, qs)
	}
	if got := plan.Values[0]; got.Text != "Berlin, Germany" || got.Source != "profile.personal.city" {
		t.Fatalf("city combobox = %+v, want Berlin, Germany", got)
	}
}
