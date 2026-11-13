package executor

import "testing"

func TestValidSource(t *testing.T) {
	good := []string{"profile.email", "profile.personal.first_name", "answer#123", "textblock#4",
		"document#0192f3a4-5b6c-7d8e-9f01-23456789abcd",
		"answer#0192f3a4-5b6c-7d8e-9f01-23456789abcd", "textblock#0192f3a4-5b6c-7d8e-9f01-23456789abcd",
		"profile.work_experience.0.company",
		"profile.education.1.institution"}
	bad := []string{"", "profile.", "Profile.email", "answer#", "answer#x", "textblock#4 ", "ai_draft#9 (approved)",
		"station#0192f3a4-5b6c-7d8e-9f01-23456789abcd", "document#123",
		"answer#0192F3A4-5b6c-7d8e-9f01-23456789abcd", "guess", "page", "profile.email; answer#1"}
	for _, s := range good {
		if !ValidSource(s) {
			t.Errorf("%q refused", s)
		}
	}
	for _, s := range bad {
		if ValidSource(s) {
			t.Errorf("%q accepted", s)
		}
	}
}

func TestMatch(t *testing.T) {
	cases := []struct {
		want    string
		options []string
		at      int
		ok      bool
	}{
		{"Germany", []string{"France", "Germany", "Austria"}, 1, true},
		{"germany ", []string{"France", "GERMANY"}, 1, true},
		{"Yes", []string{"Bitte wählen", "Ja", "Nein"}, 1, true},
		{"Nein", []string{"Yes", "No"}, 1, true},
		{"Keine Angabe", []string{"Male", "Female", "Prefer not to say"}, 2, true},
		{"Prefer not to say", []string{"Male", "I don't wish to answer"}, 1, true},
		{"Köln", []string{"Berlin", "Koeln", "Hamburg"}, 1, true},
		{"Deutschland", []string{"France", "Germany", "Austria"}, 1, true},
		{"Österreich", []string{"Germany", "Austria"}, 1, true},
		{"UK", []string{"Ireland", "United Kingdom"}, 1, true},
		{"Englisch", []string{"German", "English"}, 1, true},
		{"Germany", []string{"Österreich +43", "Deutschland +49", "Schweiz +41"}, 1, true},
		{"Café", []string{"Cafe", "Bar"}, 0, true},
		{"Bachelor", []string{"Bachelor of Science", "Master of Science"}, 0, true},
		{"Bachelor of Science", []string{"Bachelor", "Master"}, 0, true},
		{"Master of Science in Computer Science", []string{"Master of Arts", "Master of Science"}, 1, true},
		{"Science", []string{"Bachelor of Science", "Master of Science"}, -1, false},
		{"Spain", []string{"France", "Germany"}, -1, false},
		{"", []string{"Yes"}, -1, false},
		{"Yes", []string{"Yes, full time", "Yes, part time"}, -1, false}, // two "yes" answers: no guessing
	}
	for _, tc := range cases {
		at, ok := Match(tc.want, tc.options)
		if at != tc.at || ok != tc.ok {
			t.Errorf("Match(%q, %q) = %d, %v; want %d, %v", tc.want, tc.options, at, ok, tc.at, tc.ok)
		}
	}
}

func FuzzMatch(f *testing.F) {
	f.Add("Ja", "Yes", "No")
	f.Fuzz(func(t *testing.T, want, a, b string) {
		if at, ok := Match(want, []string{a, b}); ok != (at >= 0) || at > 1 {
			t.Fatalf("Match = %d, %v", at, ok)
		}
	})
}
