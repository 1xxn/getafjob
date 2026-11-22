package matching

import "github.com/1xxn/getafjob/internal/platform/vocab"

// roleGroups are job titles that mean the same role, in English and German. A wanted title that names one of
// them makes the others count as synonyms.
var roleGroups = vocab.RoleGroups

// skill is a skill with its spellings; weight is how much it says about a role: 1 for common tools, 3 for
// specialised ones.
type skill = vocab.Skill

// regionAliases names a remote region in the ways postings write it; a remote job in a wider region (Europe)
// counts for a country in it.
var regionAliases = map[string][]string{
	"germany":     {"germany", "deutschland", "dach", "de", "europe", "europa", "eu", "emea", "cet", "cest"},
	"austria":     {"austria", "oesterreich", "dach", "europe", "europa", "eu", "emea", "cet"},
	"switzerland": {"switzerland", "schweiz", "dach", "europe", "europa", "eu", "emea", "cet"},
	"dach":        {"dach", "germany", "deutschland", "austria", "oesterreich", "switzerland", "schweiz"},
	"eu":          {"eu", "europe", "europa", "emea", "european union"},
	"europe":      {"europe", "europa", "eu", "emea"},
}

// everywhere are remote locations that cover every region.
var everywhere = terms([]string{"anywhere", "worldwide", "weltweit", "global", "remote first"})

// remoteWords mark a location as remote.
var remoteWords = terms([]string{"remote", "home office", "homeoffice", "work from home", "mobiles arbeiten",
	"telearbeit", "fully remote", "distributed"})

// generic title words say nothing about the role on their own.
var generic = map[string]bool{"engineer": true, "developer": true, "entwickler": true, "manager": true,
	"specialist": true, "spezialist": true, "mitarbeiter": true, "consultant": true, "berater": true, "and": true,
	"und": true, "the": true, "for": true, "fuer": true, "with": true, "mit": true, "senior": true, "junior": true,
	"lead": true, "staff": true, "principal": true, "head": true, "intern": true, "werkstudent": true,
	"praktikant": true, "remote": true}

// IsKnownRole reports whether title names an occupational role rather than an organization or blank value (#506).
func IsKnownRole(title string) bool { return vocab.IsKnownRole(title) }
