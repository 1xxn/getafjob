package matching

import "strings"

// roleGroups are job titles that mean the same role, in English and German. A wanted title that names one of
// them makes the others count as synonyms.
var roleGroups = [][]string{
	{"software engineer", "software developer", "softwareentwickler", "software entwickler", "softwareingenieur",
		"anwendungsentwickler", "programmer", "programmierer", "application developer"},
	{"backend engineer", "backend developer", "backend entwickler", "backendentwickler", "server engineer"},
	{"frontend engineer", "frontend developer", "frontend entwickler", "frontendentwickler", "web developer",
		"webentwickler", "ui engineer"},
	{"fullstack", "full stack", "fullstackentwickler"},
	{"devops", "site reliability", "sre", "platform engineer", "infrastructure engineer", "cloud engineer",
		"systems engineer", "plattform engineer"},
	{"data engineer", "dateningenieur", "analytics engineer", "etl developer"},
	{"data scientist", "machine learning engineer", "ml engineer", "ai engineer", "data science"},
	{"data analyst", "datenanalyst", "business intelligence", "bi analyst", "bi developer"},
	{"system administrator", "systemadministrator", "sysadmin", "it administrator", "linux administrator",
		"administrator", "systembetreuer"},
	{"it support", "helpdesk", "service desk", "first level support", "second level support", "it supporter",
		"desktop support", "it techniker"},
	{"security engineer", "it security", "cyber security", "cybersecurity", "informationssicherheit",
		"security analyst", "penetration tester"},
	{"network engineer", "netzwerkadministrator", "netzwerktechniker", "network administrator"},
	{"qa engineer", "test engineer", "softwaretester", "quality assurance", "testautomatisierer", "test automation"},
	{"product manager", "product owner", "produktmanager"},
	{"project manager", "projektmanager", "projektleiter", "scrum master", "delivery manager"},
	{"mobile developer", "ios developer", "android developer", "app entwickler", "mobile engineer"},
	{"embedded", "firmware", "embedded softwareentwickler"},
	{"database administrator", "dba", "datenbankadministrator"},
	{"solutions architect", "software architect", "it architekt", "cloud architect", "softwarearchitekt"},
	{"engineering manager", "head of engineering", "teamleiter entwicklung", "team lead"},
}

// skill is a skill with its spellings; weight is how much it says about a role: 1 for common tools, 3 for
// specialised ones.
type skill struct {
	name    string
	weight  int
	aliases []string
}

// regionAliases names a remote region in the ways postings write it; a remote job in a wider region (Europe)
// counts for a country in it.
var regionAliases = map[string][]string{
	"germany":     {"germany", "deutschland", "dach", "de", "europe", "europa", "eu", "emea", "cet", "cest"},
	"austria":     {"austria", "oesterreich", "dach", "europe", "europa", "eu", "emea", "cet"},
	"switzerland": {"switzerland", "schweiz", "dach", "europe", "europa", "emea", "cet"},
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

var companyMarkers = map[string]bool{
	"gmbh": true, "ag": true, "se": true, "kg": true, "ug": true, "ohg": true, "mbh": true,
	"inc": true, "ltd": true, "llc": true, "corp": true, "gbr": true, "partg": true,
	"university": true, "universitaet": true, "hochschule": true,
}

var roleNouns = map[string]bool{
	"engineer": true, "developer": true, "entwickler": true, "entwicklerin": true,
	"architect": true, "architekt": true, "architektin": true,
	"administrator": true, "sysadmin": true, "devops": true, "sre": true,
	"analyst": true, "analystin": true, "scientist": true,
	"manager": true, "managerin": true, "consultant": true, "berater": true, "beraterin": true,
	"specialist": true, "spezialist": true, "spezialistin": true,
	"designer": true, "tester": true, "testerin": true, "techniker": true, "technikerin": true,
	"director": true, "coordinator": true, "koordinator": true, "koordinatorin": true,
	"fachinformatiker": true, "fachinformatikerin": true, "systembetreuer": true,
	"projektleiter": true, "projektleiterin": true, "produktmanager": true,
	"werkstudent": true, "werkstudentin": true, "praktikant": true, "praktikantin": true,
	"intern": true, "trainee": true, "azubi": true, "auszubildender": true, "auszubildende": true,
	"programmer": true, "programmierer": true, "programmiererin": true,
	"ingenieur": true, "ingenieurin": true, "researcher": true,
	"teamleiter": true, "teamleiterin": true,
}

// IsKnownRole reports whether title names an occupational role rather than an organization or blank value (#506).
func IsKnownRole(title string) bool {
	norm := normalize(title)
	words := strings.Fields(norm)
	if len(words) == 0 {
		return false
	}
	for _, w := range words {
		if companyMarkers[w] {
			return false
		}
	}
	for _, grp := range roleGroups {
		for _, alias := range grp {
			if strings.Contains(norm, " "+alias+" ") {
				return true
			}
		}
	}
	for _, w := range words {
		if roleNouns[w] || hasRoleSuffix(w) {
			return true
		}
	}
	return false
}

var roleSuffixes = []string{
	"entwickler", "entwicklerin", "engineer", "developer",
	"architekt", "architektin", "architect",
	"administrator", "administratorin",
	"ingenieur", "ingenieurin",
	"berater", "beraterin", "consultant",
	"spezialist", "spezialistin",
	"techniker", "technikerin",
	"programmierer", "programmiererin",
	"manager", "managerin", "analyst", "analystin",
}

func hasRoleSuffix(w string) bool {
	if len(w) <= 6 {
		return false
	}
	for _, suf := range roleSuffixes {
		if strings.HasSuffix(w, suf) {
			return true
		}
	}
	return false
}
