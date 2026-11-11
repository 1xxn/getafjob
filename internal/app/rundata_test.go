package app

import (
	"log/slog"
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/1xxn/getafjob/internal/application"
	"github.com/1xxn/getafjob/internal/platform/config"
	"github.com/1xxn/getafjob/internal/profile"
)

func TestFacts(t *testing.T) {
	p := profile.Profile{Personal: profile.Personal{FirstName: "Max", LastName: "Mustermann", City: "Berlin"},
		Work: profile.Work{NoticePeriod: "3 months", NeedsSponsorship: false, WorkAuthorizedEU: true}}
	f := facts(p, profile.Preferences{TalentPool: true, Diversity: "decline"})
	want := map[string]string{"personal.first_name": "Max", "personal.full_name": "Max Mustermann", "personal.city": "Berlin",
		"work.notice_period": "3 months", "work.needs_sponsorship": "false", "work.work_authorized_eu": "true",
		"preferences.talent_pool": "true", "preferences.diversity": "decline"}
	for k, v := range want {
		if f[k] != v {
			t.Errorf("%s = %q, want %q", k, f[k], v)
		}
	}
	if _, ok := f["personal.email"]; ok {
		t.Error("empty value included")
	}
}

func TestOrigins(t *testing.T) {
	cases := []struct {
		job  application.Job
		want []string
	}{
		{application.Job{Source: "greenhouse:acme", URL: "https://www.acme.example/careers/1"},
			[]string{"https://www.acme.example", "https://boards.greenhouse.io", "https://job-boards.greenhouse.io", "https://job-boards.eu.greenhouse.io"}},
		{application.Job{Source: "arbeitnow", URL: "https://www.arbeitnow.com/jobs/x", Apply: "https://jobs.lever.co/muster/1"},
			[]string{"https://www.arbeitnow.com", "https://jobs.lever.co"}},
		{application.Job{Source: "workday:muster", URL: "https://Muster.wd3.myworkdayjobs.com/External/job/1"},
			[]string{"https://muster.wd3.myworkdayjobs.com"}},
		{application.Job{Source: "x", URL: "http://insecure.example/1", Apply: "javascript:alert(1)"}, nil},
	}
	for _, tc := range cases {
		if got := (runData{}).origins(tc.job); !slices.Equal(got, tc.want) {
			t.Errorf("%+v: %v, want %v", tc.job, got, tc.want)
		}
	}
	// Development: only the configured fixture host gets its plain http origin.
	dev := runData{fixture: "fixtures:8080"}
	fixture := application.Job{Source: "demo", URL: "http://fixtures:8080/posting.html?job=1"}
	if got := dev.origins(fixture); !slices.Equal(got, []string{"http://fixtures:8080"}) {
		t.Errorf("fixture origins = %v", got)
	}
	if got := dev.origins(application.Job{Source: "x", URL: "http://insecure.example/1"}); got != nil {
		t.Errorf("other http host = %v, want none", got)
	}
}

func TestDevFixtureHostOnlyOnLocalInstances(t *testing.T) {
	local, _ := url.Parse("https://localhost:8443")
	public, _ := url.Parse("https://jobs.example.org")
	logger := slog.New(slog.DiscardHandler)
	if devFixtureHost(config.Config{PublicURL: local, DevFixtureHost: "fixtures:8080"}, logger) != "fixtures:8080" {
		t.Error("local instance must honor the fixture host")
	}
	if devFixtureHost(config.Config{PublicURL: public, DevFixtureHost: "fixtures:8080"}, logger) != "" {
		t.Error("public instance must ignore the fixture host")
	}
}

func TestFormLanguageAndTexts(t *testing.T) {
	de := application.Job{Title: "Backend-Entwickler", Description: "Wir suchen dich für unser Team und deine Erfahrung mit Go."}
	if formLanguage("auto", de) != "de" || formLanguage("en", de) != "en" || formLanguage("", application.Job{}) != "en" {
		t.Error("formLanguage")
	}
	p := profile.Profile{Personal: profile.Personal{FirstName: "Max", LastName: "Mustermann"}}
	values := placeholders(p, application.Job{Company: "Acme GmbH", Title: "Go Developer"}, "de",
		time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC))
	text, missing := profile.TextBlock{Body: "{full_name} bewirbt sich am {date} bei {company}."}.Fill(values)
	if text != "Max Mustermann bewirbt sich am 06.11.2026 bei Acme GmbH." || len(missing) != 0 {
		t.Fatalf("%q %v", text, missing)
	}
	if _, missing := (profile.TextBlock{Body: "Notice: {notice_period}"}).Fill(values); len(missing) != 1 {
		t.Fatal("missing placeholder not reported")
	}
}

func TestPortalOf(t *testing.T) {
	cases := []struct {
		job   application.Job
		start string
		want  string
	}{
		{application.Job{Source: "linkedin:search"}, "https://www.linkedin.com/jobs/view/123", "linkedin"},
		{application.Job{Source: "indeed"}, "https://de.indeed.com/viewjob?jk=abc", "indeed"},
		{application.Job{Source: "greenhouse:acme"}, "https://boards.greenhouse.io/acme/jobs/1", ""},
		{application.Job{Source: "custom"}, "https://www.linkedin.com/jobs/view/456", "linkedin"},
	}
	for _, tc := range cases {
		if got := portalOf(tc.job, tc.start); got != tc.want {
			t.Errorf("portalOf(%+v, %q) = %q, want %q", tc.job, tc.start, got, tc.want)
		}
	}
}
