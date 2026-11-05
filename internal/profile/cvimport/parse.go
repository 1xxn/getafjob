package cvimport

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/1xxn/getafjob/internal/profile"
)

// Heuristics: only what a pattern finds with good confidence. Everything else stays empty; the user corrects and
// completes it in the assistant.

var (
	emailRx    = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
	phoneRx    = regexp.MustCompile(`(?:\+\d{1,3}[\s./-]?)?(?:\(\d{1,5}\)[\s./-]?)?\d[\d\s./-]{6,18}\d`)
	linkedinRx = regexp.MustCompile(`(?i)(?:https?://)?(?:[a-z]{2,3}\.)?linkedin\.com/in/[A-Za-z0-9_%-]+/?`)
	githubRx   = regexp.MustCompile(`(?i)(?:https?://)?github\.com/[A-Za-z0-9-]{1,39}/?`)
	webRx      = regexp.MustCompile(`(?i)\bhttps?://[A-Za-z0-9.-]+\.[A-Za-z]{2,}(?:/[^\s]*)?`)
	postalRx   = regexp.MustCompile(`\b(\d{5})\s+([\p{Lu}][\p{L}.-]+(?:\s[\p{Lu}][\p{L}.-]+)?)`)
	monthName  = `(?:jan|feb|mar|mär|apr|may|mai|jun|jul|aug|sep|oct|okt|nov|dec|dez)[a-zä]*\.?`
	dateTok    = `(?:\d{1,2}[./]\d{4}|` + monthName + `\s+\d{4}|\d{4})`
	rangeRx    = regexp.MustCompile(`(?i)(` + dateTok + `)\s*(?:-|\x{2013}|\x{2014}|to|bis)\s*(` + dateTok + `|present|current|now|today|heute|jetzt|aktuell)`)
	bulletRx   = regexp.MustCompile(`^[•·▪●◦*\-\x{2013}]\s*`)
)

// enDash and emDash as escapes: CVs use them in date ranges and between title and company.
const enDash, emDash = "\xe2\x80\x93", "\xe2\x80\x94"

// section names in English and German, lower case without punctuation.
var sections = map[string][]string{
	"summary":        {"summary", "profile", "profil", "about me", "über mich", "kurzprofil", "professional summary"},
	"skills":         {"skills", "technical skills", "kenntnisse", "fähigkeiten", "it kenntnisse", "technologies", "tech stack", "kompetenzen"},
	"languages":      {"languages", "sprachen", "sprachkenntnisse"},
	"experience":     {"experience", "work experience", "professional experience", "employment", "berufserfahrung", "berufliche erfahrung", "werdegang", "beruflicher werdegang"},
	"education":      {"education", "ausbildung", "studium", "bildung", "academic background"},
	"certifications": {"certifications", "certificates", "zertifikate", "zertifizierungen", "weiterbildung"},
}

var notName = []string{"lebenslauf", "curriculum vitae", "resume", "résumé", "cv"}

func heading(line string) string {
	l := strings.ToLower(strings.Trim(strings.TrimSpace(line), ":#*_ "))
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
	if m := linkedinRx.FindString(text); m != "" {
		p.Personal.LinkedIn = profile.WebURL(m)
	}
	if m := githubRx.FindString(text); m != "" {
		p.Personal.GitHub = profile.WebURL(m)
	}
	for _, m := range webRx.FindAllString(text, 10) {
		if !linkedinRx.MatchString(m) && !githubRx.MatchString(m) {
			p.Personal.Website = strings.TrimRight(m, ".,;)")
			break
		}
	}
	if m := postalRx.FindStringSubmatch(text); m != nil {
		p.Personal.PostalCode, p.Personal.City = m[1], m[2]
	}

	parts := split(lines)
	p.Summary = strings.Join(strings.Fields(strings.Join(parts["summary"], " ")), " ")
	if r := []rune(p.Summary); len(r) > 1500 {
		p.Summary = string(r[:1500])
	}
	p.Skills = items(parts["skills"], 60, 80)
	p.Certifications = items(parts["certifications"], 120, 40)
	p.Languages = languages(parts["languages"])
	p.Experience = experience(parts["experience"])
	if len(p.Experience) > 0 && p.Experience[0].End == "" {
		p.Work.CurrentTitle, p.Work.CurrentEmployer = p.Experience[0].Title, p.Experience[0].Company
	}
	p.Education = education(parts["education"])
	return p
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
	var out []string
	seen := map[string]bool{}
	for _, l := range lines {
		l = bulletRx.ReplaceAllString(l, "")
		if _, rest, ok := strings.Cut(l, ":"); ok && utf8.RuneCountInString(l) > 30 {
			l = rest // "Languages: Go, Python" style
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

// experience reads entries that start at a line with a date range; title and company come from the text around
// it ("Title at Company", "Title, Company", "Title | Company"), the bullets after it are highlights.
func experience(lines []string) []profile.Experience {
	var out []profile.Experience
	for i, l := range lines {
		m := rangeRx.FindStringSubmatchIndex(l)
		if m == nil {
			if n := len(out); n > 0 && bulletRx.MatchString(l) && len(out[n-1].Highlights) < 10 {
				out[n-1].Highlights = append(out[n-1].Highlights, bulletRx.ReplaceAllString(l, ""))
			}
			continue
		}
		e := profile.Experience{Start: l[m[2]:m[3]], End: l[m[4]:m[5]]}
		if isOngoing(e.End) {
			e.End = ""
		}
		rest := strings.Trim(l[:m[0]]+" "+l[m[1]:], " |,-:"+enDash+emDash)
		if rest == "" && i > 0 {
			rest = lines[i-1]
		}
		e.Title, e.Company = titleCompany(rest)
		if e.Company == "" && i+1 < len(lines) && rangeRx.FindStringIndex(lines[i+1]) == nil && !bulletRx.MatchString(lines[i+1]) {
			e.Company = cut(lines[i+1], 200)
		}
		out = append(out, e)
		if len(out) >= 30 {
			break
		}
	}
	return out
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

func education(lines []string) []profile.Education {
	var out []profile.Education
	for i, l := range lines {
		m := rangeRx.FindStringSubmatchIndex(l)
		if m == nil {
			continue
		}
		e := profile.Education{Start: l[m[2]:m[3]], End: l[m[4]:m[5]]}
		if isOngoing(e.End) {
			e.End = ""
		}
		rest := strings.Trim(l[:m[0]]+" "+l[m[1]:], " |,-:"+enDash+emDash)
		if rest == "" && i > 0 {
			rest = lines[i-1]
		}
		e.Degree, e.Institution = titleCompany(rest)
		if e.Institution == "" && i+1 < len(lines) && rangeRx.FindStringIndex(lines[i+1]) == nil {
			e.Institution = cut(lines[i+1], 200)
		}
		out = append(out, e)
		if len(out) >= 15 {
			break
		}
	}
	return out
}

func cut(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
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
	if len(cur.Experience) == 0 {
		cur.Experience = found.Experience
	}
	if len(cur.Education) == 0 {
		cur.Education = found.Education
	}
	if len(cur.Certifications) == 0 {
		cur.Certifications = found.Certifications
	}
	return cur
}
