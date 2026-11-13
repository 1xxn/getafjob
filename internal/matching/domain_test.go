package matching

import (
	"slices"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

func TestNormalizeAndTerms(t *testing.T) {
	if got := normalize("Senior Go-Entwickler (m/w/d), Köln"); got != " senior go entwickler m w d koeln " {
		t.Fatalf("normalize = %q", got)
	}
	if got := normalize("C++ / C# / .NET / Node.js"); got != " cpp csharp dotnet nodejs " {
		t.Fatalf("normalize symbols = %q", got)
	}
	tests := []struct {
		term, text string
		want       bool
	}{
		{"go", "Senior Go Developer", true},
		{"go", "Google Cloud Engineer", false},
		{"it support", "IT Support Specialist", true},
		{"it support", "Audit Support", false},
		{"entwickler", "Softwareentwickler (m/w/d)", true},
		{"Café", "cafe manager", true},
		{"", "anything", false},
		{strings.Repeat("a(", 50) + "*", strings.Repeat("a", 1000), false}, // patterns are plain text
	}
	for _, tt := range tests {
		if got := newTerm(tt.term).in(normalize(tt.text)); got != tt.want {
			t.Errorf("%q in %q = %v, want %v", tt.term, tt.text, got, tt.want)
		}
	}
}

func TestTitle(t *testing.T) {
	m := NewMatcher(Criteria{Titles: []string{"Software Engineer", "Go Developer"}})
	tests := []struct {
		title, code string
		hard        bool
	}{
		{"Senior Software Engineer (m/w/d)", "title.exact", false},
		{"Softwareentwickler Backend", "title.synonym", false},
		{"Go Engineer", "title.partial", false},
		{"Sales Manager", "title.none", true},
	}
	for _, tt := range tests {
		r := m.title(normalize(tt.title))
		if r.Code != tt.code || r.Hard != tt.hard {
			t.Errorf("%q: %+v, want %s", tt.title, r, tt.code)
		}
	}
	if r := m.title(normalize("Lead Go Developer")); r.Params["term"] != "Go Developer" {
		t.Errorf("term not as typed: %+v", r)
	}
	if r := NewMatcher(Criteria{}).title(normalize("Anything")); r.Code != "title.any" {
		t.Errorf("no titles: %+v", r)
	}
}

func TestExcludedTitle(t *testing.T) {
	m := NewMatcher(Criteria{Titles: []string{"engineer"}, ExcludeTitles: []string{"sales", "werkstudent"}})
	r := m.Evaluate(Job{Title: "Sales Engineer"}, now)
	if r.Decision != Skip || len(r.Reasons) != 1 || r.Reasons[0].Code != "title.excluded" || r.Reasons[0].Params["term"] != "sales" {
		t.Fatalf("excluded: %+v", r)
	}
}

func TestSkills(t *testing.T) {
	m := NewMatcher(Criteria{Skills: []string{"Golang", "Kubernetes (CKA)", "PostgreSQL", "Kochen"}})
	tests := []struct {
		text, code string
		min, max   int
		missing    string
	}{
		{"We use Go, Kubernetes and Postgres.", "skills.fit", 20, 25, ""},
		{"Go and Java, Spring, Kafka, Terraform, AWS", "skills.fit", 4, 10, "Java"},
		{"Java, Spring and Angular", "skills.none", 0, 0, "Java"},
		{"A friendly team.", "skills.unknown", 10, 10, ""},
		{"Kochen im Team", "skills.fit", 4, 4, ""},
	}
	for _, tt := range tests {
		r := skillReason(m.skillHitsAndMissing(normalize(tt.text)))
		if r.Code != tt.code || r.Points < tt.min || r.Points > tt.max || !strings.Contains(r.Params["missing"], tt.missing) {
			t.Errorf("%q: %+v", tt.text, r)
		}
	}
}

func TestSeniority(t *testing.T) {
	tests := []struct {
		years, title, code string
	}{
		{"7", "Senior Backend Engineer", "level.fit"},
		{"7", "Staff Engineer", "level.stretch"},
		{"4", "Principal Engineer", "level.too_senior"},
		{"7", "Backend Engineer II", "level.below"},
		{"12", "Junior Developer", "level.too_junior"},
		{"0", "Werkstudent Softwareentwicklung", "level.fit"},
		{"5-7", "Developer", "level.open"},
		{"viele", "Senior Developer", "level.open"},
	}
	for _, tt := range tests {
		r := NewMatcher(Criteria{YearsTotal: tt.years}).seniority(normalize(tt.title))
		if r.Code != tt.code {
			t.Errorf("%s years, %q: %+v, want %s", tt.years, tt.title, r, tt.code)
		}
	}
	if levelForYears("5+") != levelMid || levelForYears("") != levelUnknown {
		t.Error("levelForYears")
	}
}

func TestLocation(t *testing.T) {
	de := Criteria{Locations: []string{"Köln"}, Remote: true, RemoteRegions: []string{"Germany"}}
	tests := []struct {
		c       Criteria
		loc, wp string
		code    string
		hard    bool
	}{
		{de, "Cologne; Köln, Germany", "hybrid", "location.city", false},
		{de, "Remote - Germany", "", "location.remote", false},
		{de, "Germany (Remote)", "", "location.remote", false},
		{de, "Remote, Germany", "", "location.remote", false},
		{de, "Remote - Europe", "", "location.remote", false},
		{de, "Remote, Anywhere", "", "location.remote", false},
		{de, "Germany", "remote", "location.remote", false},
		{de, "Munich, Germany; Remote Sweden", "hybrid", "location.none", true},
		{de, "Munich, Germany", "onsite", "location.none", true},
		{de, "Remote - USA", "", "location.none", true},
		{de, "", "remote", "location.remote_unknown", false},
		{de, "", "", "location.unknown", false},
		{Criteria{Remote: true}, "Remote - USA", "", "location.remote", false},
		{Criteria{}, "Berlin", "onsite", "location.any", false},
		{Criteria{Locations: []string{"Berlin"}}, "Remote - Germany", "remote", "location.none", true},
	}
	for _, tt := range tests {
		r := NewMatcher(tt.c).location(Job{Location: tt.loc, Workplace: tt.wp})
		if r.Code != tt.code || r.Hard != tt.hard {
			t.Errorf("%q/%s: %+v, want %s", tt.loc, tt.wp, r, tt.code)
		}
	}
}

func TestLanguage(t *testing.T) {
	german := "Wir suchen dich für unser Team. Du hast Erfahrung mit Go und Kenntnisse in der Cloud."
	english := "We are looking for you to join our team with experience in Go and skills in the cloud."
	both := []Language{{"Deutsch", "Muttersprache"}, {"English", "C1"}}
	tests := []struct {
		langs []Language
		text  string
		code  string
	}{
		{both, german, "language.fit"},
		{both, english, "language.fit"},
		{[]Language{{"German", "native"}, {"English", "B1"}}, english, "language.missing"},
		{[]Language{{"English", ""}}, german, "language.missing"},
		{nil, german, "language.unknown"},
		{[]Language{{"Polnisch", "Muttersprache"}}, german, "language.unknown"},
		{[]Language{{"English", "A2"}}, english, "language.missing"},
	}
	for _, tt := range tests {
		r := NewMatcher(Criteria{Languages: tt.langs}).language(normalize(tt.text))
		if r.Code != tt.code {
			t.Errorf("%v %q: %+v, want %s", tt.langs, tt.text[:20], r, tt.code)
		}
	}
	if DetectLanguage(german) != "de" || DetectLanguage(english) != "en" || DetectLanguage("") != "en" {
		t.Error("DetectLanguage")
	}
}

func TestSalary(t *testing.T) {
	tests := []struct {
		text string
		top  int
	}{
		{"Gehalt: 60.000 - 80.000 € brutto", 80_000},
		{"€60k - 80k", 80_000},
		{"65,000 EUR per year", 65_000},
		{"bis zu 75 Tsd. EUR", 75_000},
		{"EUR 70.000,50", 70_000},
		{"4.500 € monatlich", 0},
		{"Team of 120 people since 2010", 0},
		{"99999999999999999999 €", 0},
	}
	for _, tt := range tests {
		if got := salaryTop(tt.text); got != tt.top {
			t.Errorf("salaryTop(%q) = %d, want %d", tt.text, got, tt.top)
		}
	}
	m := NewMatcher(Criteria{MinSalary: 70_000})
	if r := m.salary("50.000 - 60.000 €"); r.Code != "salary.below" || !r.Hard {
		t.Errorf("below: %+v", r)
	}
	if r := m.salary("bis 90.000 €"); r.Code != "salary.fit" {
		t.Errorf("fit: %+v", r)
	}
	if r := m.salary("Attractive salary"); r.Code != "" {
		t.Errorf("unknown: %+v", r)
	}
}

func TestRecency(t *testing.T) {
	day := 24 * time.Hour
	tests := []struct {
		j    Job
		code string
	}{
		{Job{Posted: now.Add(-day)}, "recency.new"},
		{Job{Posted: now.Add(-10 * day)}, "recency.recent"},
		{Job{FirstSeen: now.Add(-20 * day)}, "recency.month"},
		{Job{Posted: now.Add(-90 * day), FirstSeen: now}, "recency.old"},
		{Job{Posted: now, FirstSeen: now.Add(-40 * day)}, "recency.old"},
		{Job{}, ""},
	}
	for _, tt := range tests {
		if r := recency(tt.j, now); r.Code != tt.code {
			t.Errorf("%+v: %+v, want %s", tt.j, r, tt.code)
		}
	}
}

func TestEvaluateDecision(t *testing.T) {
	m := NewMatcher(Criteria{Titles: []string{"Backend Engineer"}, Locations: []string{"Berlin"}, Remote: true,
		RemoteRegions: []string{"Germany"}, Skills: []string{"Go", "PostgreSQL", "Kubernetes"}, YearsTotal: "6",
		Languages: []Language{{"English", "C1"}, {"Deutsch", "Muttersprache"}}, MinSalary: 60_000})
	desc := "We build our platform with Go, PostgreSQL and Kubernetes. Salary 70.000 - 85.000 €."
	tests := []struct {
		j    Job
		want Decision
	}{
		{Job{Title: "Senior Backend Engineer", Location: "Berlin, Germany", Workplace: "hybrid", Description: desc,
			Posted: now}, Match},
		{Job{Title: "Backend Engineer", Location: "Remote - Germany", Description: "We use Java and Spring with our team.",
			Posted: now.AddDate(0, -2, 0)}, Maybe},
		{Job{Title: "Senior Backend Engineer", Location: "Paris, France", Workplace: "onsite", Description: desc}, Skip},
		{Job{Title: "Marketing Lead", Location: "Berlin", Description: desc}, Skip},
	}
	for _, tt := range tests {
		r := m.Evaluate(tt.j, now)
		if r.Decision != tt.want || r.Score < 0 || r.Score > 100 {
			t.Errorf("%q: %s %d %+v, want %s", tt.j.Title, r.Decision, r.Score, r.Reasons, tt.want)
		}
	}
}

func TestInspectFit(t *testing.T) {
	c := Criteria{
		Titles:     []string{"Senior Go Engineer"},
		Skills:     []string{"Go", "Kubernetes"},
		YearsTotal: "6",
		Locations:  []string{"Berlin"},
		Remote:     true,
	}
	j := Job{
		ID:          1,
		Title:       "Senior Go Developer",
		Location:    "Berlin",
		Workplace:   "hybrid",
		Description: "We need strong Go and Kubernetes experience. PostgreSQL and Docker are also required.",
		Posted:      now,
	}
	fit := InspectFit(c, j, now)
	if fit.Score <= 0 {
		t.Fatalf("Score = %d, want > 0", fit.Score)
	}
	if !slices.Contains(fit.MatchingSkills, "Go") {
		t.Errorf("MatchingSkills missing Go: %v", fit.MatchingSkills)
	}
	if !slices.Contains(fit.MatchingSkills, "Kubernetes") {
		t.Errorf("MatchingSkills missing Kubernetes: %v", fit.MatchingSkills)
	}
	if !slices.Contains(fit.MissingSkills, "PostgreSQL") {
		t.Errorf("MissingSkills missing PostgreSQL: %v", fit.MissingSkills)
	}
	if !slices.Contains(fit.MissingSkills, "Docker") {
		t.Errorf("MissingSkills missing Docker: %v", fit.MissingSkills)
	}
	if fit.CandidateLevel != "senior" {
		t.Errorf("CandidateLevel = %q, want senior", fit.CandidateLevel)
	}
	if fit.JobLevel != "senior" {
		t.Errorf("JobLevel = %q, want senior", fit.JobLevel)
	}
	if fit.LocationReason != "location.city" || fit.LocationParams["city"] == "" {
		t.Errorf("location reason %q without its city: %v", fit.LocationReason, fit.LocationParams)
	}
	if fit.SkillPoints <= 0 || fit.MaxSkillPoints != 25 {
		t.Errorf("SkillPoints = %d / %d", fit.SkillPoints, fit.MaxSkillPoints)
	}
	if fit.TitlePoints <= 0 || fit.MaxTitlePoints != 35 {
		t.Errorf("TitlePoints = %d / %d", fit.TitlePoints, fit.MaxTitlePoints)
	}
	// The breakdown adds up to the score of Evaluate.
	if r := NewMatcher(c).Evaluate(j, now); r.Score != fit.Score || r.Decision != fit.Decision {
		t.Errorf("Evaluate = %d %s, InspectFit = %d %s", r.Score, r.Decision, fit.Score, fit.Decision)
	}
	// An excluded title scores 0, and so do its parts.
	c.ExcludeTitles = []string{"developer"}
	ex := InspectFit(c, j, now)
	if ex.Score != 0 || ex.Decision != Skip || ex.TitlePoints != 0 || ex.SkillPoints != 0 || len(ex.MatchingSkills) != 0 ||
		len(ex.Reasons) != 1 || ex.Reasons[0].Code != "title.excluded" {
		t.Errorf("excluded: %+v", ex)
	}
}

func FuzzEvaluate(f *testing.F) {
	f.Add("Senior Go Developer", "Remote - Germany", "Go, 80.000 €")
	m := NewMatcher(Criteria{Titles: []string{"go developer"}, Remote: true, RemoteRegions: []string{"eu"},
		Skills: []string{"Go"}, MinSalary: 50_000})
	f.Fuzz(func(t *testing.T, title, loc, desc string) {
		r := m.Evaluate(Job{Title: title, Location: loc, Description: desc}, now)
		if r.Score < 0 || r.Score > 100 {
			t.Fatalf("score %d", r.Score)
		}
	})
}
