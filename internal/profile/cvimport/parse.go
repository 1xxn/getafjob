package cvimport

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/1xxn/getafjob/internal/platform/vocab"
	"github.com/1xxn/getafjob/internal/profile"
)

// Heuristics: only what a pattern finds with good confidence. Everything else stays empty; the user corrects and
// completes it in the assistant.

var (
	emailRx = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
	phoneRx = regexp.MustCompile(`(?:\+\d{1,3}[\s./-]?)?(?:\(\d{1,5}\)[\s./-]?)?\d[\d\s./-]{6,18}\d`)
	// The host must start the line or follow a separator, so evil-linkedin.com or xgithub.com never count.
	linkedinRx = regexp.MustCompile(`(?i)(?:^|[\s(<|,;:])((?:https?://)?(?:www\.|[a-z]{2}\.)?linkedin\.com/in/[A-Za-z0-9_%-]+/?)`)
	githubRx   = regexp.MustCompile(`(?i)(?:^|[\s(<|,;:])((?:https?://)?(?:www\.)?github\.com/[A-Za-z0-9-]{1,39}/?)`)
	webRx      = regexp.MustCompile(`(?i)\bhttps?://[A-Za-z0-9.-]+\.[A-Za-z]{2,}(?:/[^\s]*)?`)
	postalRx   = regexp.MustCompile(`\b(\d{5})\s+([\p{Lu}][\p{L}.-]+(?:\s[\p{Lu}][\p{L}.-]+)?)`)
	monthNum   = `(?:0?[1-9]|1[0-2])`
	monthName  = `(?:jan|feb|mar|mär|apr|may|mai|jun|jul|aug|sep|oct|okt|nov|dec|dez)[a-zä]*\.?`
	yearTok    = `(?:19[5-9]\d|20[0-3]\d)`
	dateTok    = `(?:` + monthNum + `[./]` + yearTok + `|` + monthName + `\s+` + yearTok + `|` + yearTok + `)`
	rangeRx    = regexp.MustCompile(`(?i)(` + dateTok + `)\s*(?:-|\x{2013}|\x{2014}|to|bis)\s*(` + dateTok + `|present|current|now|today|heute|jetzt|aktuell)`)
	bulletRx   = regexp.MustCompile(`^[•·▪●◦*\-\x{2013}]\s*`)
)

// enDash and emDash as escapes: CVs use them in date ranges and between title and company.
const enDash, emDash = "\xe2\x80\x93", "\xe2\x80\x94"

// section names in English and German, lower case without punctuation.
var sections = map[string][]string{
	"summary": {"summary", "profile", "profil", "about me", "über mich", "kurzprofil", "professional summary"},
	"skills": {"skills", "technical skills", "kenntnisse", "fähigkeiten", "it kenntnisse", "fachkenntnisse",
		"technische kenntnisse", "technologies", "technologien", "technologien tools", "tools technologien",
		"tech stack", "technischer stack", "kompetenzen", "kernkompetenzen", "kenntnisse fähigkeiten",
		"skills technologies", "edv kenntnisse"},
	"languages":      {"languages", "sprachen", "sprachkenntnisse"},
	"experience":     {"experience", "work experience", "professional experience", "employment", "berufserfahrung", "berufliche erfahrung", "werdegang", "beruflicher werdegang"},
	"education":      {"education", "ausbildung", "studium", "bildung", "academic background"},
	"certifications": {"certifications", "certificates", "zertifikate", "zertifizierungen", "weiterbildung"},
	// other ends the sections above; its lines are not read.
	"other": {"projects", "personal projects", "side projects", "projekte", "awards", "auszeichnungen",
		"publications", "publikationen", "interests", "interessen", "hobbies", "volunteering", "ehrenamt",
		"references", "referenzen"},
}

var notName = []string{"lebenslauf", "curriculum vitae", "resume", "résumé", "cv"}

var headingRepl = strings.NewReplacer("-", " ", "&", " ", "/", " ", "·", " ", ":", "", "#", "", "*", "", "_", " ")

func heading(line string) string {
	l := strings.ToLower(strings.TrimSpace(line))
	l = strings.Join(strings.Fields(headingRepl.Replace(l)), " ")
	if l == "" || utf8.RuneCountInString(l) > 40 {
		return ""
	}
	for name, words := range sections {
		for _, w := range words {
			if l == w {
				return name
			}
		}
	}
	return ""
}

// Parse fills a profile from CV text.
func Parse(text string) profile.Profile {
	var p profile.Profile
	lines := strings.Split(text, "\n")
	p.Personal.FirstName, p.Personal.LastName = name(lines)
	if m := emailRx.FindString(text); m != "" {
		p.Personal.Email = strings.ToLower(m)
	}
	p.Personal.Phone = phone(text)
	if m := linkedinRx.FindStringSubmatch(text); m != nil {
		p.Personal.LinkedIn = profile.WebURL(m[1])
	}
	if m := githubRx.FindStringSubmatch(text); m != nil {
		p.Personal.GitHub = profile.WebURL(m[1])
	}
	for _, m := range webRx.FindAllString(text, 10) {
		if !linkedinRx.MatchString(" "+m) && !githubRx.MatchString(" "+m) {
			p.Personal.Website = strings.TrimRight(m, ".,;)")
			break
		}
	}
	if m := postalRx.FindStringSubmatch(text); m != nil {
		p.Personal.PostalCode, p.Personal.City = m[1], m[2]
	}
	if city, country := headerPlace(lines); city != "" {
		if p.Personal.City == "" {
			p.Personal.City = city
		}
		p.Personal.Country = country
	}

	parts := split(lines)
	p.Summary = strings.Join(strings.Fields(strings.Join(parts["summary"], " ")), " ")
	if r := []rune(p.Summary); len(r) > 1500 {
		p.Summary = string(r[:1500])
	}
	p.Skills = mergeSkills(skillItems(parts["skills"], 60, 80), vocab.KnownSkills(strings.Join(parts["skills"], "\n")), 80)
	p.Certifications = items(dropDateColumn(parts["certifications"]), 120, 40)
	p.Languages = languages(parts["languages"])
	return p
}

func mergeSkills(a, b []string, maxCount int) []string {
	out := make([]string, 0, len(a)+len(b))
	seen := make(map[string]bool, len(a)+len(b))
	for _, list := range [][]string{a, b} {
		for _, s := range list {
			k := strings.ToLower(strings.TrimSpace(s))
			if k == "" || seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, s)
			if len(out) >= maxCount {
				return out
			}
		}
	}
	return out
}

// name takes the first short line of two to four capitalized words as the person's name.
func name(lines []string) (first, last string) {
	for i, l := range lines {
		if i > 8 {
			break
		}
		l = strings.TrimSpace(l)
		low := strings.ToLower(l)
		skip := l == "" || heading(l) != "" || emailRx.MatchString(l) || strings.ContainsAny(l, "0123456789@/:|,")
		for _, n := range notName {
			skip = skip || low == n
		}
		if skip {
			continue
		}
		words := strings.Fields(l)
		if len(words) < 2 || len(words) > 4 {
			continue
		}
		ok := true
		for _, w := range words {
			r, _ := utf8.DecodeRuneInString(w)
			ok = ok && unicode.IsUpper(r) && strings.IndexFunc(w, func(r rune) bool { return !unicode.IsLetter(r) && r != '-' && r != '.' }) < 0
		}
		if ok {
			return strings.Join(words[:len(words)-1], " "), words[len(words)-1]
		}
	}
	return "", ""
}

func phone(text string) string {
	for _, m := range phoneRx.FindAllString(text, 20) {
		n := 0
		for _, r := range m {
			if unicode.IsDigit(r) {
				n++
			}
		}
		if n >= 8 && n <= 15 && !rangeRx.MatchString(m) && !strings.Contains(m, "..") {
			return strings.TrimSpace(m)
		}
	}
	return ""
}

// placeRx finds "City, Country" with a country CVs in this market name, e.g. "Hamburg, Germany".
var placeRx = regexp.MustCompile(`(?:^|[\s|·•,])([\p{Lu}][\p{L}.-]+(?:[\s-][\p{Lu}][\p{L}.-]+)?),\s*(Germany|Deutschland|Austria|Österreich|Switzerland|Schweiz|Netherlands|Niederlande|Poland|Polen|France|Frankreich|Spain|Spanien|Italy|Italien|Portugal|Ireland|Irland|Denmark|Dänemark|Sweden|Schweden|Norway|Norwegen|Finland|Finnland|Belgium|Belgien|Luxembourg|Luxemburg|Czechia|Tschechien|United Kingdom|UK)\b`)

// headerPlace takes the city and country from the lines before the first section heading (the contact block).
func headerPlace(lines []string) (city, country string) {
	for _, l := range lines {
		if heading(l) != "" {
			break
		}
		if m := placeRx.FindStringSubmatch(l); m != nil {
			return cut(m[1], 200), cut(m[2], 200)
		}
	}
	return "", ""
}

// dropDateColumn removes a right column that holds only a date or year ("Certified X  2023").
func dropDateColumn(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = l
		if locs := colGapRx.FindAllStringIndex(l, -1); len(locs) > 0 {
			last := locs[len(locs)-1]
			if dateOnlyRx.MatchString(strings.TrimSpace(l[last[1]:])) {
				out[i] = strings.TrimSpace(l[:last[0]])
			}
		}
	}
	return out
}

var dateOnlyRx = regexp.MustCompile(`(?i)^(?:` + dateTok + `)(?:\s*(?:-|\x{2013}|\x{2014})\s*(?:` + dateTok + `|present|heute))?$`)

// split groups the lines under the section headings they follow.
func split(lines []string) map[string][]string {
	out := map[string][]string{}
	cur := ""
	for _, l := range lines {
		if h := heading(l); h != "" {
			cur = h
			continue
		}
		if cur != "" && strings.TrimSpace(l) != "" {
			out[cur] = append(out[cur], strings.TrimSpace(l))
		}
	}
	return out
}

// items splits lines into short entries (by bullets, commas, pipes, semicolons).
func items(lines []string, maxLen, maxCount int) []string {
	return parseItems(lines, maxLen, maxCount, false)
}

func skillItems(lines []string, maxLen, maxCount int) []string {
	return parseItems(lines, maxLen, maxCount, true)
}

func parseItems(lines []string, maxLen, maxCount int, stripCategory bool) []string {
	var out []string
	seen := map[string]bool{}
	for _, l := range lines {
		l = bulletRx.ReplaceAllString(l, "")
		if left, rest, ok := strings.Cut(l, ":"); ok && !strings.HasPrefix(rest, "//") && strings.TrimSpace(rest) != "" {
			if (stripCategory && utf8.RuneCountInString(strings.TrimSpace(left)) <= 45 && !strings.ContainsAny(left, ",|;")) ||
				utf8.RuneCountInString(l) > 30 {
				l = rest // "Category: Go, Python" style
			}
		}
		for _, it := range strings.FieldsFunc(l, func(r rune) bool { return r == ',' || r == '|' || r == ';' || r == '•' || r == '·' }) {
			it = strings.Join(strings.Fields(it), " ")
			if it == "" || utf8.RuneCountInString(it) > maxLen || seen[strings.ToLower(it)] {
				continue
			}
			seen[strings.ToLower(it)] = true
			out = append(out, it)
			if len(out) >= maxCount {
				return out
			}
		}
	}
	return out
}

var levelRx = regexp.MustCompile(`(?i)\b(native|muttersprache|fluent|fließend|verhandlungssicher|business|advanced|intermediate|basic|grundkenntnisse|gut|sehr gut|[abc][12])\b`)

func languages(lines []string) []profile.Language {
	var out []profile.Language
	for _, l := range items(lines, 60, 20) {
		name, level := l, ""
		if m := levelRx.FindStringIndex(l); m != nil {
			name, level = l[:m[0]], l[m[0]:]
		}
		name = strings.Trim(name, " :-()"+enDash+emDash)
		level = strings.Trim(level, " :-()"+enDash+emDash)
		if name == "" || strings.ContainsAny(name, "0123456789") || utf8.RuneCountInString(name) > 30 {
			continue
		}
		out = append(out, profile.Language{Name: name, Level: level})
	}
	return out
}

func isOngoing(s string) bool {
	switch strings.ToLower(s) {
	case "present", "current", "now", "today", "heute", "jetzt", "aktuell":
		return true
	}
	return false
}

func titleCompany(s string) (title, company string) {
	s = strings.Join(strings.Fields(s), " ")
	for _, sep := range []string{" at ", " bei ", " @ ", " | ", ", ", " " + enDash + " ", " - "} {
		if t, c, ok := strings.Cut(s, sep); ok {
			return cut(t, 200), cut(c, 200)
		}
	}
	return cut(s, 200), ""
}

func cut(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// ExtractedStations contains structured career stations extracted from a CV.
type ExtractedStations struct {
	Experiences []profile.WorkExperience
	Educations  []profile.EducationStation
}

// ParsedCV bundles the flat profile with the structured career stations.
type ParsedCV struct {
	Profile     profile.Profile
	Experiences []profile.WorkExperience
	Educations  []profile.EducationStation
}

// ParseAll extracts both profile details and structured career stations from CV text.
func ParseAll(text string) ParsedCV {
	p := Parse(text)
	exps, edus := ExtractStations(text)
	if len(exps) > 0 && exps[0].IsCurrent {
		p.Work.CurrentTitle, p.Work.CurrentEmployer = exps[0].Title, exps[0].Company
	}
	return ParsedCV{
		Profile:     p,
		Experiences: exps,
		Educations:  edus,
	}
}

// ExtractStations extracts structured work experiences and educations from CV text.
func ExtractStations(text string) ([]profile.WorkExperience, []profile.EducationStation) {
	lines := strings.Split(text, "\n")
	parts := split(lines)
	exps := extractWorkExperiences(parts["experience"])
	edus := extractEducations(parts["education"])
	return exps, edus
}

var (
	remoteRx = regexp.MustCompile(`(?i)\b(?:remote|vollständig\s+remote|home\s*office)\b`)
	hybridRx = regexp.MustCompile(`(?i)\b(?:hybrid|teilweise\s+remote)\b`)
	onsiteRx = regexp.MustCompile(`(?i)\b(?:onsite|on-site|vor\s+ort|präsenz)\b`)
	gradeRx  = regexp.MustCompile(`(?i)\b(?:grade|gpa|note|bewertung|schnitt|abschlussschnitt)[:\s]+([0-9.,]+(?:\s*/\s*[0-9.,]+)?|\bmit\s+auszeichnung\b|\bsumma\s+cum\s+laude\b)`)
)

func extractWorkplaceType(s string) (string, string) {
	wp := ""
	switch {
	case remoteRx.MatchString(s):
		wp = profile.WorkplaceRemote
	case hybridRx.MatchString(s):
		wp = profile.WorkplaceHybrid
	case onsiteRx.MatchString(s):
		wp = profile.WorkplaceOnsite
	}
	s = regexp.MustCompile(`(?i)\s*[\(\[]\s*(?:remote|hybrid|onsite|on-site|vor\s+ort|präsenz)\s*[\)\]]`).ReplaceAllString(s, "")
	return strings.TrimSpace(s), wp
}

var companySuffixes = map[string]bool{
	"gmbh": true, "ag": true, "se": true, "kgaa": true, "kg": true, "ug": true, "ohg": true, "mbh": true,
	"inc": true, "ltd": true, "llc": true, "corp": true, "gbr": true, "partg": true, "ev": true,
	"stiftung": true, "university": true, "universität": true, "hochschule": true,
}

func looksLikeCompany(s string) bool {
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if companySuffixes[w] {
			return true
		}
	}
	return false
}

func chooseRoleAndCompany(a, b string) (title, company string) {
	if (looksLikeCompany(a) && !looksLikeCompany(b)) || (!vocab.IsKnownRole(a) && vocab.IsKnownRole(b)) {
		return cut(b, 200), cut(a, 200)
	}
	return cut(a, 200), cut(b, 200)
}

var colGapRx = regexp.MustCompile(`(?:\t| {2,})`)

func parseTitleCompanyLocation(raw string) (title, company, location string) {
	locCol := ""
	if locs := colGapRx.FindAllStringIndex(strings.TrimSpace(raw), -1); len(locs) > 0 {
		trimmed := strings.TrimSpace(raw)
		last := locs[len(locs)-1]
		left := strings.TrimSpace(trimmed[:last[0]])
		right := strings.Join(strings.Fields(trimmed[last[1]:]), " ")
		if right != "" && utf8.RuneCountInString(right) <= 60 && !strings.ContainsAny(right, "·|") &&
			!strings.Contains(right, " at ") && !strings.Contains(right, " bei ") &&
			!vocab.IsKnownRole(right) && !looksLikeCompany(right) &&
			(strings.ContainsAny(left, "·|,-:"+enDash) || strings.Contains(left, " at ") || strings.Contains(left, " bei ")) {
			locCol = cut(right, 200)
			raw = left
		}
	}

	s := strings.Join(strings.Fields(raw), " ")
	for _, sep := range []string{" at ", " bei ", " @ "} {
		if t, c, ok := strings.Cut(s, sep); ok {
			title = cut(t, 200)
			if locCol != "" {
				return title, cut(c, 200), locCol
			}
			for _, locSep := range []string{" · ", " | ", ", ", " - ", " " + enDash + " "} {
				if comp, loc, ok := strings.Cut(c, locSep); ok {
					return title, cut(comp, 200), cut(loc, 200)
				}
			}
			return title, cut(c, 200), ""
		}
	}
	for _, sep := range []string{" · ", " | ", " " + enDash + " ", " - ", ", "} {
		parts := strings.Split(s, sep)
		if len(parts) >= 2 {
			title, company = chooseRoleAndCompany(parts[0], parts[1])
			if locCol != "" {
				return title, company, locCol
			}
			if len(parts) >= 3 {
				return title, company, cut(strings.Join(parts[2:], ", "), 200)
			}
			return title, company, ""
		}
	}
	return cut(s, 200), "", locCol
}

var knownDegrees = []string{
	"bachelor of science", "master of science", "bachelor of arts", "master of arts",
	"bachelor of engineering", "master of engineering",
	"b.sc.", "b.sc", "m.sc.", "m.sc", "b.a.", "b.a", "m.a.", "m.a",
	"b.eng.", "b.eng", "m.eng.", "m.eng",
	"bachelor", "master", "ph.d.", "ph.d", "phd", "doctorate", "dr.", "promotion", "diplom", "dipl.-ing.",
	"abitur", "fachabitur", "matura", "high school diploma",
	"berufsausbildung", "ausbildung", "apprenticeship",
}

func parseDegreeAndField(raw string) (degree, field string) {
	raw = strings.TrimSpace(raw)
	low := strings.ToLower(raw)
	for _, kd := range knownDegrees {
		if strings.HasPrefix(low, kd) {
			degLen := len(kd)
			degree = strings.TrimSpace(raw[:degLen])
			rest := strings.TrimSpace(raw[degLen:])
			rest = strings.TrimPrefix(rest, "in ")
			rest = strings.TrimPrefix(rest, "In ")
			rest = strings.Trim(rest, " -:,|()")
			field = cut(rest, 200)
			return cut(degree, 100), field
		}
	}
	if d, f, ok := strings.Cut(raw, " in "); ok {
		return cut(d, 100), cut(f, 200)
	}
	return cut(raw, 100), ""
}

func extractGrade(s string) string {
	if m := gradeRx.FindStringSubmatch(s); len(m) > 1 {
		return strings.TrimSpace(m[1])
	}
	return ""
}

func findDateRange(s string) []int {
	for _, m := range rangeRx.FindAllStringSubmatchIndex(s, -1) {
		if m[0] > 0 {
			prev, _ := utf8.DecodeLastRuneInString(s[:m[0]])
			if unicode.IsDigit(prev) {
				continue
			}
		}
		if m[1] < len(s) {
			next, _ := utf8.DecodeRuneInString(s[m[1]:])
			if unicode.IsDigit(next) || unicode.IsLetter(next) {
				continue
			}
		}
		return m
	}
	return nil
}

func appendBullet(we *profile.WorkExperience, bullet string, forceJoin bool) {
	if bullet == "" {
		return
	}
	if n := len(we.DescriptionBullets); n > 0 {
		prev := we.DescriptionBullets[n-1]
		last, _ := utf8.DecodeLastRuneInString(prev)
		first, _ := utf8.DecodeRuneInString(bullet)
		endsSentence := strings.ContainsRune(".!?;:", last)
		if !endsSentence && unicode.IsLower(first) && (forceJoin || len(prev) < 600) {
			if strings.HasSuffix(prev, "-") && !strings.HasSuffix(prev, " -") {
				we.DescriptionBullets[n-1] = strings.TrimSuffix(prev, "-") + bullet
			} else {
				we.DescriptionBullets[n-1] = prev + " " + bullet
			}
			return
		}
		if forceJoin {
			we.DescriptionBullets[n-1] = prev + " " + bullet
			return
		}
	}
	if len(we.DescriptionBullets) < 20 {
		we.DescriptionBullets = append(we.DescriptionBullets, bullet)
	}
}

func extractWorkExperiences(lines []string) []profile.WorkExperience {
	var out []profile.WorkExperience
	skipCompanyLine, usedLine := -1, -1
	for i, l := range lines {
		if len(out) > 0 && bulletRx.MatchString(l) {
			bullet := strings.TrimSpace(bulletRx.ReplaceAllString(l, ""))
			appendBullet(&out[len(out)-1], bullet, false)
			continue
		}
		m := findDateRange(l)
		if m == nil {
			if i == skipCompanyLine {
				continue
			}
			if n := len(out); n > 0 && len(out[n-1].DescriptionBullets) > 0 &&
				(i+1 >= len(lines) || findDateRange(lines[i+1]) == nil) {
				if cont := strings.TrimSpace(l); cont != "" {
					appendBullet(&out[n-1], cont, true)
				}
			}
			continue
		}
		we := profile.WorkExperience{
			StartDate: profile.MonthDate(l[m[2]:m[3]]),
			EndDate:   profile.MonthDate(l[m[4]:m[5]]),
			SortOrder: len(out),
		}
		if isOngoing(we.EndDate) {
			we.IsCurrent = true
			we.EndDate = ""
		}
		left := strings.TrimRight(l[:m[0]], " |,-:"+enDash+emDash)
		right := strings.Trim(l[m[1]:], " |,-:"+enDash+emDash)
		rest := left
		if rest != "" && right != "" {
			rest += "  " + right
		} else if rest == "" {
			rest = right
		}
		if rest == "" && i > 0 {
			rest = lines[i-1]
		}
		rest, we.WorkplaceType = extractWorkplaceType(rest)
		we.Title, we.Company, we.Location = parseTitleCompanyLocation(rest)
		if we.Company == "" {
			if j := pairLine(lines, i, usedLine); j >= 0 {
				name, loc := columns(lines[j])
				we.Title, we.Company = chooseRoleAndCompany(we.Title, name)
				if we.Location == "" {
					we.Location = loc
				}
				usedLine = j
				if j > i {
					skipCompanyLine = j
				}
			}
		}
		out = append(out, we)
		if len(out) >= profile.MaxStations {
			break
		}
	}
	return out
}

func extractEducations(lines []string) []profile.EducationStation {
	var out []profile.EducationStation
	usedLine := -1
	for i, l := range lines {
		m := findDateRange(l)
		if m == nil {
			continue
		}
		ed := profile.EducationStation{
			StartDate: profile.MonthDate(l[m[2]:m[3]]),
			EndDate:   profile.MonthDate(l[m[4]:m[5]]),
			SortOrder: len(out),
		}
		if isOngoing(ed.EndDate) {
			ed.IsCurrent = true
			ed.EndDate = ""
		}
		rest := strings.Trim(l[:m[0]]+" "+l[m[1]:], " |,-:"+enDash+emDash)
		if rest == "" && i > 0 {
			rest = lines[i-1]
		}
		rawDegree, inst := titleCompany(rest)
		if left, _ := columns(rest); inst == "" && left != rest {
			rawDegree = left // "M.Sc. Computer Science  Musterstadt" keeps only the degree
		}
		if inst == "" {
			if j := institutionLine(lines, i, usedLine); j >= 0 {
				inst, _ = columns(lines[j])
				usedLine = j
			}
		}
		ed.Institution = inst
		ed.Degree, ed.FieldOfStudy = parseDegreeAndField(rawDegree)
		ed.Grade = extractGrade(rest)
		if ed.Grade == "" && i+1 < len(lines) && findDateRange(lines[i+1]) == nil {
			ed.Grade = extractGrade(lines[i+1])
		}
		out = append(out, ed)
		if len(out) >= profile.MaxStations {
			break
		}
	}
	return out
}

// columns splits a line of a two-column layout ("Musterfirma GmbH  Hamburg, Germany") into its left text and its
// right column; a line without a column gap is all left.
func columns(l string) (left, right string) {
	l = strings.TrimSpace(l)
	locs := colGapRx.FindAllStringIndex(l, -1)
	if len(locs) == 0 {
		return cut(l, 200), ""
	}
	last := locs[len(locs)-1]
	return cut(strings.TrimSpace(l[:last[0]]), 200), cut(strings.Join(strings.Fields(l[last[1]:]), " "), 200)
}

// plainLine reports whether line j can hold a station's company or institution: no date range, no bullet, not
// taken by another station.
func plainLine(lines []string, j, used int) bool {
	return j >= 0 && j < len(lines) && j != used && findDateRange(lines[j]) == nil && !bulletRx.MatchString(lines[j]) &&
		strings.TrimSpace(lines[j]) != ""
}

// pairLine finds the line that completes the dated line i: below it (title above, company below), else above it
// (company above, title below). A line above only counts when the line before it is not a plain text line, so a
// wrapped bullet is never taken for a company.
func pairLine(lines []string, i, used int) int {
	if plainLine(lines, i+1, used) {
		return i + 1
	}
	if plainLine(lines, i-1, used) && (i-2 < 0 || !plainLine(lines, i-2, used) || bulletRx.MatchString(lines[i-2])) {
		return i - 1
	}
	return -1
}

var institutionRx = regexp.MustCompile(`(?i)universit|hochschule|college|school|schule|institut|academy|akademie|polytechnic|\btu\b|\bfh\b`)

// institutionLine finds the institution next to the dated degree line i: the line that names an institution, else
// the line above when it has a column gap (Jake's template), else the line below.
func institutionLine(lines []string, i, used int) int {
	for _, j := range []int{i - 1, i + 1} {
		if plainLine(lines, j, used) && institutionRx.MatchString(lines[j]) {
			return j
		}
	}
	if plainLine(lines, i-1, used) && colGapRx.MatchString(strings.TrimSpace(lines[i-1])) {
		return i - 1
	}
	if plainLine(lines, i+1, used) {
		return i + 1
	}
	return -1
}

// Merge fills empty fields of cur with found; lists only when cur has none. Nothing the user entered is replaced.
func Merge(cur, found profile.Profile) profile.Profile {
	take := func(dst *string, v string) {
		if strings.TrimSpace(*dst) == "" {
			*dst = v
		}
	}
	pe, fp := &cur.Personal, found.Personal
	for _, f := range []struct {
		dst *string
		v   string
	}{
		{&pe.FirstName, fp.FirstName}, {&pe.LastName, fp.LastName}, {&pe.Email, fp.Email}, {&pe.Phone, fp.Phone},
		{&pe.PostalCode, fp.PostalCode}, {&pe.City, fp.City}, {&pe.LinkedIn, fp.LinkedIn}, {&pe.GitHub, fp.GitHub},
		{&pe.Website, fp.Website}, {&cur.Work.CurrentTitle, found.Work.CurrentTitle},
		{&cur.Work.CurrentEmployer, found.Work.CurrentEmployer}, {&cur.Summary, found.Summary},
	} {
		take(f.dst, f.v)
	}
	if len(cur.Skills) == 0 {
		cur.Skills = found.Skills
	}
	if len(cur.Languages) == 0 {
		cur.Languages = found.Languages
	}
	if len(cur.Certifications) == 0 {
		cur.Certifications = found.Certifications
	}
	return cur
}
