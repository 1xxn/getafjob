// Package vocab provides shared, pure job-role and technical-skill dictionaries plus text normalization
// used across discovery/matching, CV import, and onboarding without coupling business modules.
package vocab

import (
	"math"
	"slices"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// maxText bounds the text normalized in one call; job descriptions and CVs are far shorter.
const maxText = 100_000

var symbols = strings.NewReplacer("c++", " cpp ", "c#", " csharp ", "f#", " fsharp ", ".net", " dotnet ",
	"node.js", " nodejs ", "vue.js", " vue ", "next.js", " nextjs ", "react.js", " react ", "ci/cd", " cicd ")

var umlauts = strings.NewReplacer("ä", "ae", "ö", "oe", "ü", "ue", "ß", "ss")

// Normalize lowercases text, folds umlauts and accents and turns everything but letters and digits into single
// spaces, with one space on each side: " senior go developer m w d ".
func Normalize(s string) string {
	if len(s) > maxText {
		s = s[:maxText]
	}
	s = umlauts.Replace(symbols.Replace(strings.ToLower(s)))
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte(' ')
	space := true
	for _, r := range norm.NFKD.String(s) {
		switch {
		case unicode.Is(unicode.Mn, r):
			continue // combining accent
		case r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			b.WriteRune(r)
			space = false
		case !space:
			b.WriteByte(' ')
			space = true
		}
	}
	if !space {
		b.WriteByte(' ')
	}
	return b.String()
}

// RoleGroups are job titles that mean the same role, in English and German.
var RoleGroups = [][]string{
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

// IsKnownRole reports whether title names an occupational role rather than an organization or blank value (#506).
func IsKnownRole(title string) bool {
	norm := Normalize(title)
	words := strings.Fields(norm)
	if len(words) == 0 {
		return false
	}
	for _, w := range words {
		if companyMarkers[w] {
			return false
		}
	}
	for _, grp := range RoleGroups {
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

// Skill is a technical skill with its spellings and relevance weight (1 common, 3 specialised).
type Skill struct {
	Name    string
	Weight  int
	Aliases []string
}

type skillAlias struct {
	words []string
	skill int
}

var skillIndex = func() map[string][]skillAlias {
	idx := map[string][]skillAlias{}
	for i, k := range Skills {
		for _, raw := range k.Aliases { // not the name: "Spring", "Chef", "C" and "R" mean other things in prose
			words := strings.Fields(strings.TrimSpace(Normalize(raw)))
			if len(words) == 0 || slices.ContainsFunc(idx[words[0]], func(a skillAlias) bool {
				return a.skill == i && slices.Equal(a.words, words)
			}) {
				continue
			}
			idx[words[0]] = append(idx[words[0]], skillAlias{words: words, skill: i})
		}
	}
	return idx
}()

// SkillCounts counts occurrences of every dictionary skill inside normalized text.
func SkillCounts(normalized string) []uint16 {
	counts := make([]uint16, len(Skills))
	words := strings.Fields(normalized)
	for i, w := range words {
		for _, a := range skillIndex[w] {
			if i+len(a.words) <= len(words) && slices.Equal(words[i:i+len(a.words)], a.words) && counts[a.skill] < math.MaxUint16 {
				counts[a.skill]++
			}
		}
	}
	return counts
}

// KnownSkills extracts canonical skill names from free text using Skills (#506).
func KnownSkills(text string) []string {
	counts := SkillCounts(Normalize(text))
	var out []string
	for i, n := range counts {
		if n > 0 {
			out = append(out, Skills[i].Name)
		}
	}
	return out
}
