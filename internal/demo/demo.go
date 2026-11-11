// Package demo invents users, profiles and job postings for local development, screenshots and load tests. All
// names, companies and addresses are made up; URLs point to example.org or, with PointAt, to the fixture site.
package demo

import (
	"fmt"
	"math/rand/v2"
	"net/url"
	"strings"
	"time"

	"github.com/1xxn/getafjob/internal/discovery"
	"github.com/1xxn/getafjob/internal/profile"
)

// Source is the source name of demo postings.
const Source = "demo"

// Person is an invented user with profile and search preferences.
type Person struct {
	Email, Name string
	Profile     profile.Profile
	Preferences profile.Preferences
}

// People returns the demo users.
func People() []Person {
	return []Person{
		{Email: "max.mustermann@example.org", Name: "Max Mustermann",
			Profile: profile.Profile{
				Personal: profile.Personal{FirstName: "Max", LastName: "Mustermann", Email: "max.mustermann@example.org",
					Phone: "+49 30 1234567", City: "Berlin", Country: "Germany"},
				Work:      profile.Work{CurrentTitle: "Backend Engineer", YearsTotal: "7", WorkAuthorizedEU: true},
				Skills:    []string{"Go", "PostgreSQL", "Kubernetes", "Terraform", "gRPC", "Linux"},
				Languages: []profile.Language{{Name: "German", Level: "native"}, {Name: "English", Level: "C1"}},
			},
			Preferences: profile.Preferences{Titles: []string{"Backend Engineer", "Go Developer", "Platform Engineer"},
				ExcludeTitles: []string{"Werkstudent", "Sales"}, Locations: []string{"Berlin"}, Remote: true,
				RemoteRegions: []string{"Germany"}, MinSalary: 65_000, Diversity: "decline", CoverLetterLang: "auto"}},
		{Email: "erika.mustermann@example.org", Name: "Erika Mustermann",
			Profile: profile.Profile{
				Personal: profile.Personal{FirstName: "Erika", LastName: "Mustermann", Email: "erika.mustermann@example.org",
					Phone: "+49 40 7654321", City: "Hamburg", Country: "Germany"},
				Work:      profile.Work{CurrentTitle: "Data Engineer", YearsTotal: "4", WorkAuthorizedEU: true},
				Skills:    []string{"Python", "SQL", "Spark", "Kafka", "AWS"},
				Languages: []profile.Language{{Name: "German", Level: "native"}, {Name: "English", Level: "C1"}},
			},
			Preferences: profile.Preferences{Titles: []string{"Data Engineer", "Analytics Engineer"},
				Locations: []string{"Hamburg"}, Remote: true, RemoteRegions: []string{"EU"}, Diversity: "decline",
				CoverLetterLang: "auto"}},
		{Email: "jonas.beispiel@example.org", Name: "Jonas Beispiel",
			Profile: profile.Profile{
				Personal: profile.Personal{FirstName: "Jonas", LastName: "Beispiel", Email: "jonas.beispiel@example.org",
					Phone: "+49 221 555123", City: "Köln", Country: "Germany"},
				Work:      profile.Work{CurrentTitle: "IT-Supporter", YearsTotal: "2", WorkAuthorizedEU: true},
				Skills:    []string{"Windows Server", "Active Directory", "ITIL", "Networking", "PowerShell"},
				Languages: []profile.Language{{Name: "Deutsch", Level: "Muttersprache"}, {Name: "Englisch", Level: "B1"}},
			},
			Preferences: profile.Preferences{Titles: []string{"IT Support", "Systemadministrator"},
				Locations: []string{"Köln", "Bonn"}, Remote: false, Diversity: "decline", CoverLetterLang: "de"}},
	}
}

type role struct {
	en, de string
	skills []string
}

var roles = []role{
	{"Backend Engineer", "Backend-Entwickler", []string{"Go", "Java", "PostgreSQL", "Kafka", "gRPC", "Docker"}},
	{"Go Developer", "Go-Entwickler", []string{"Go", "PostgreSQL", "Kubernetes", "gRPC", "Redis"}},
	{"Platform Engineer", "Platform Engineer", []string{"Kubernetes", "Terraform", "AWS", "Go", "Prometheus"}},
	{"Site Reliability Engineer", "Site Reliability Engineer", []string{"Linux", "Kubernetes", "Prometheus", "Python"}},
	{"Frontend Developer", "Frontend-Entwickler", []string{"TypeScript", "React", "Vue", "CSS"}},
	{"Data Engineer", "Data Engineer", []string{"Python", "SQL", "Spark", "Kafka", "AWS"}},
	{"Data Scientist", "Data Scientist", []string{"Python", "Machine Learning", "SQL"}},
	{"IT Support Specialist", "IT-Supporter", []string{"Windows Server", "Active Directory", "ITIL"}},
	{"System Administrator", "Systemadministrator", []string{"Linux", "Windows Server", "Networking", "PowerShell"}},
	{"QA Engineer", "Softwaretester", []string{"Java", "Python", "CI/CD"}},
	{"Product Manager", "Produktmanager", []string{"Agile", "SQL"}},
	{"Sales Manager", "Vertriebsmitarbeiter", nil},
	{"Working Student Software Development", "Werkstudent Softwareentwicklung", []string{"Python", "Git"}},
}

var (
	levels    = []string{"", "", "Junior ", "Senior ", "Senior ", "Lead ", "Staff "}
	companies = []string{"Muster Software GmbH", "Beispiel Cloud AG", "Nordlicht IT GmbH", "Acme Data GmbH",
		"Rheinwerk Systems GmbH", "Alpenblick Digital AG", "Hafenkante Tech GmbH", "Elbtal Analytics GmbH",
		"Spreeufer Labs GmbH", "Fichtenhain Services GmbH"}
	places = []struct {
		loc       string
		workplace discovery.Workplace
	}{
		{"Berlin, Germany", discovery.WorkplaceHybrid}, {"Hamburg, Germany", discovery.WorkplaceOnsite},
		{"Köln, Germany", discovery.WorkplaceOnsite}, {"Bonn", discovery.WorkplaceHybrid},
		{"München, Germany", discovery.WorkplaceHybrid}, {"Remote - Germany", discovery.WorkplaceRemote},
		{"Remote - Europe", discovery.WorkplaceRemote}, {"Remote - USA", discovery.WorkplaceRemote},
		{"Munich, Germany; Remote Sweden", discovery.WorkplaceHybrid}, {"Wien, Austria", discovery.WorkplaceOnsite},
	}
)

// Postings returns n invented postings; the same seed and day give the same postings.
func Postings(n int, seed uint64, now time.Time) []discovery.Posting {
	r := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)) //nolint:gosec // demo data, not security relevant
	out := make([]discovery.Posting, n)
	for i := range out {
		ro := roles[r.IntN(len(roles))]
		german := r.IntN(3) == 0
		title := ro.en
		if german {
			title = ro.de
		}
		if !strings.HasPrefix(title, "Working") && !strings.HasPrefix(title, "Werkstudent") {
			title = levels[r.IntN(len(levels))] + title
		}
		pl := places[r.IntN(len(places))]
		company := companies[r.IntN(len(companies))]
		posted := now.AddDate(0, 0, -r.IntN(60))
		out[i] = discovery.Posting{Source: Source, ExternalID: fmt.Sprintf("%d-%d", seed, i), Company: company,
			Title: title, Location: pl.loc, Workplace: pl.workplace,
			URL:         fmt.Sprintf("https://jobs.example.org/demo/%d-%d", seed, i),
			Description: description(r, ro, company, german), Posted: posted.Format(time.DateOnly)}
	}
	return out
}

func description(r *rand.Rand, ro role, company string, german bool) string {
	skills := append([]string(nil), ro.skills...)
	r.Shuffle(len(skills), func(i, j int) { skills[i], skills[j] = skills[j], skills[i] })
	skills = skills[:min(len(skills), 2+r.IntN(4))]
	var b strings.Builder
	if german {
		fmt.Fprintf(&b, "%s sucht dich für unser Team. Du arbeitest mit uns an Produkten für den Mittelstand.\n\n", company)
		if len(skills) > 0 {
			fmt.Fprintf(&b, "Das bringst du mit: Erfahrung mit %s.\n", strings.Join(skills, ", "))
		}
		if r.IntN(2) == 0 {
			low := 45_000 + 5_000*r.IntN(8)
			fmt.Fprintf(&b, "Gehalt: %d.000 bis %d.000 € brutto im Jahr.\n", low/1000, (low+15_000)/1000)
		}
		b.WriteString("Wir bieten flexible Arbeitszeiten und Weiterbildung.")
		return b.String()
	}
	fmt.Fprintf(&b, "%s is looking for you to join our team and build products for our customers.\n\n", company)
	if len(skills) > 0 {
		fmt.Fprintf(&b, "What you bring: experience with %s.\n", strings.Join(skills, ", "))
	}
	if r.IntN(2) == 0 {
		low := 50_000 + 5_000*r.IntN(10)
		fmt.Fprintf(&b, "Salary: €%dk to €%dk per year.\n", low/1000, (low+20_000)/1000)
	}
	b.WriteString("We offer flexible hours and a learning budget.")
	return b.String()
}

// FixturePages are the application pages of testdata/fixtures/site that end in a submit.
var FixturePages = []string{"posting.html", "greenhouse_like.html", "lever_application.html", "ashby_application.html",
	"personio_application.html", "smartrecruiters_application.html", "recruitee_application.html",
	"teamtailor_application.html"}

// PointAt lets the postings link to the fixture site at base, so runs in development apply to fixtures only.
func PointAt(postings []discovery.Posting, base *url.URL) {
	for i := range postings {
		postings[i].URL = base.JoinPath(FixturePages[i%len(FixturePages)]).String() + "?job=" + postings[i].ExternalID
	}
}
