package profile

import (
	"slices"
	"strings"
	"testing"
)

func fields(errs []FieldError) []string {
	var out []string
	for _, e := range errs {
		out = append(out, e.Field+":"+e.Code)
	}
	slices.Sort(out)
	return out
}

func TestProfileValidate(t *testing.T) {
	ok := Profile{
		Personal: Personal{FirstName: "Max", LastName: "Mustermann", Email: "max.mustermann@example.org",
			Phone: "+49 30 1234567", PostalCode: "10115", City: "Berlin", LinkedIn: "https://linkedin.com/in/max", Website: "HTTPS://max.example.org"},
		Work:      Work{NoticePeriod: "3 months", SalaryExpectation: "75000", WorkAuthorizedEU: true},
		Skills:    []string{"Go", "PostgreSQL"},
		Languages: []Language{{Name: "German", Level: "native"}},
	}
	if errs := ok.Validate(); len(errs) != 0 {
		t.Fatalf("valid profile: %v", errs)
	}
	bad := ok
	bad.Personal.Email = "Max <max@example.org>"
	bad.Personal.Phone = "call me"
	bad.Personal.GitHub = "javascript:alert(1)"
	bad.Personal.City = strings.Repeat("x", 201)
	bad.Summary = "a\x00b"
	bad.Skills = make([]string, 101)
	bad.Languages = []Language{{Name: " ", Level: "C1"}}
	want := []string{"city:profile.error.too_long", "email:profile.error.email", "github:profile.error.url",
		"languages:profile.error.required", "phone:profile.error.phone", "skills:profile.error.too_many", "summary:profile.error.invalid"}
	if got := fields(bad.Validate()); !slices.Equal(got, want) {
		t.Errorf("errors = %v\nwant %v", got, want)
	}
}

func TestPreferencesValidate(t *testing.T) {
	p := Preferences{Titles: []string{"Platform Engineer"}, MinSalary: 70000, CoverLetterLang: "de", Diversity: "decline"}
	if errs := p.Validate(); len(errs) != 0 {
		t.Fatal(errs)
	}
	p.MinSalary, p.CoverLetterLang, p.Diversity = -1, "fr", "maybe"
	if got := fields(p.Validate()); len(got) != 3 {
		t.Errorf("errors = %v", got)
	}
}

func TestMissing(t *testing.T) {
	p := Profile{Personal: Personal{FirstName: "Max", LastName: "Mustermann", Email: "max@example.org"}}
	want := []string{"phone", "city", "years_total", "notice_period", "earliest_start", "salary_expectation"}
	if got := p.Missing(); !slices.Equal(got, want) {
		t.Errorf("missing = %v", got)
	}
}

func TestLinesAndWebURL(t *testing.T) {
	if got := Lines("Go, PostgreSQL\n\n  go \nKubernetes;  Site  Reliability "); !slices.Equal(got, []string{"Go", "PostgreSQL", "Kubernetes", "Site Reliability"}) {
		t.Errorf("Lines = %q", got)
	}
	for in, want := range map[string]string{
		"linkedin.com/in/max": "https://linkedin.com/in/max", "HTTPS://x.org": "HTTPS://x.org", "": "", "//x.org": "https://x.org",
	} {
		if got := WebURL(in); got != want {
			t.Errorf("WebURL(%q) = %q", in, got)
		}
	}
}
