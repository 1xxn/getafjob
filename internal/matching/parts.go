package matching

import (
	"slices"
	"strconv"
	"strings"
	"time"
)

func (m *Matcher) title(title string) Reason {
	if len(m.titles) == 0 {
		return Reason{Code: "title.any", Points: maxTitle / 2}
	}
	if t, ok := anyIn(m.titles, title); ok {
		return Reason{Code: "title.exact", Points: maxTitle, Params: map[string]string{"term": m.name(t)}}
	}
	if t, ok := anyIn(m.synonyms, title); ok {
		return Reason{Code: "title.synonym", Points: maxTitle * 3 / 4, Params: map[string]string{"term": string(t)}}
	}
	for _, w := range m.tokens {
		if term(w).in(title) {
			return Reason{Code: "title.partial", Points: maxTitle / 3, Params: map[string]string{"term": w}}
		}
	}
	return Reason{Code: "title.none"} // a filter only without the model: the similarity can carry a related title
}

// synonymsOf returns the titles of every role group a wanted title belongs to.
func synonymsOf(titles []term) []term {
	var out []term
	for _, g := range roleTerms {
		if slices.ContainsFunc(g, func(s term) bool {
			return slices.ContainsFunc(titles, func(t term) bool { return s.in(pad(t)) || t.in(pad(s)) })
		}) {
			out = append(out, g...)
		}
	}
	return out
}

func pad(t term) string { return " " + string(t) + " " }

var roleTerms = func() [][]term {
	out := make([][]term, len(roleGroups))
	for i, g := range roleGroups {
		out[i] = terms(g)
	}
	return out
}()

type userSkill struct {
	name   string
	weight int
	terms  []term
}

var skillTerms = func() [][]term {
	out := make([][]term, len(skills))
	for i, k := range skills {
		out[i] = append(terms(k.aliases), newTerm(k.name))
	}
	return out
}()

// userSkills maps the user's skills to the dictionary ("Golang" is Go, "Kubernetes (CKA)" is Kubernetes);
// unknown skills count with weight 1 as typed.
func userSkills(list []string) []userSkill {
	var out []userSkill
	for _, s := range list {
		t := newTerm(s)
		if t == "" {
			continue
		}
		us := userSkill{name: strings.TrimSpace(s), weight: 1, terms: []term{t}}
		if i := slices.IndexFunc(skillTerms, func(ts []term) bool { return slices.Contains(ts, t) }); i >= 0 {
			us = userSkill{name: skills[i].name, weight: skills[i].weight, terms: skillTerms[i]}
		} else if i := slices.IndexFunc(skillTerms, func(ts []term) bool { _, ok := anyIn(ts, pad(t)); return ok }); i >= 0 {
			us = userSkill{name: skills[i].name, weight: skills[i].weight, terms: skillTerms[i]}
		}
		if !slices.ContainsFunc(out, func(o userSkill) bool { return o.name == us.name }) {
			out = append(out, us)
		}
	}
	return out
}

// skillHitsAndMissing compares the user's skills with the known skills a job names. Skills named only in its
// nice-to-have sentences (preferred) count when the user has them and are not missing when not.
func (m *Matcher) skillHitsAndMissing(text, preferred string) (hits []string, missing []string, points int, code string) {
	var have, want int
	for _, s := range m.skills {
		if _, ok := anyIn(s.terms, text); ok {
			have += s.weight
			hits = append(hits, s.name)
		}
	}
	want = have
	for i, k := range skills {
		if slices.Contains(hits, k.name) {
			continue
		}
		if _, ok := anyIn(skillTerms[i], text); ok && !onlyPreferred(skillTerms[i], text, preferred) {
			want += k.weight
			missing = append(missing, k.name)
		}
	}
	if want == 0 {
		return nil, nil, maxSkills * 2 / 5, "skills.unknown"
	}
	code = "skills.fit"
	if have == 0 {
		code = "skills.none"
	}
	points = min(maxSkills*have/min(max(want, 6), 12), maxSkills)
	return hits, missing, points, code
}

// onlyPreferred reports whether every mention of a skill sits in the job's nice-to-have sentences.
func onlyPreferred(ts []term, text, preferred string) bool {
	if preferred == "" {
		return false
	}
	if _, ok := anyIn(ts, preferred); !ok {
		return false
	}
	for _, t := range ts {
		if strings.Count(text, " "+string(t)) > strings.Count(preferred, " "+string(t)) {
			return false
		}
	}
	return true
}

// skillReason puts the skill comparison of skillHitsAndMissing into a reason. Known skills the user lacks lower the
// fit; a job that names no known skill is neutral.
func skillReason(hits, missing []string, points int, code string) Reason {
	if code == "skills.unknown" {
		return Reason{Code: code, Points: points}
	}
	return Reason{
		Code:   code,
		Points: points,
		Params: map[string]string{
			"have":    strings.Join(first(hits, 6), ", "),
			"missing": strings.Join(first(missing, 6), ", "),
		},
	}
}

func first(s []string, n int) []string { return s[:min(len(s), n)] }

// level is a seniority from intern to lead; levelUnknown when a title or profile does not say.
type level int

const (
	levelUnknown level = iota
	levelIntern
	levelJunior
	levelMid
	levelSenior
	levelLead
)

var levelNames = map[level]string{levelIntern: "intern", levelJunior: "junior", levelMid: "mid",
	levelSenior: "senior", levelLead: "lead"}

// levelWords in the order they are tested: "Senior Lead" is lead, "Junior II" is junior.
var levelWords = []struct {
	l     level
	terms []term
}{
	{levelIntern, terms([]string{"intern", "internship", "praktikum", "praktikant", "werkstudent", "working student",
		"trainee", "ausbildung", "azubi"})},
	{levelLead, terms([]string{"lead", "principal", "staff", "head of", "director", "leiter", "teamleiter", "vp",
		"chief", "architect", "architekt"})},
	{levelSenior, terms([]string{"senior", "sr", "expert", "experte"})},
	{levelJunior, terms([]string{"junior", "jr", "entry level", "einsteiger", "graduate", "berufseinsteiger"})},
	{levelMid, terms([]string{"mid level", "mid", "professional", "ii"})},
}

func levelOf(title string) level {
	for _, w := range levelWords {
		if _, ok := anyIn(w.terms, title); ok {
			return w.l
		}
	}
	return levelUnknown
}

// levelForYears maps years of experience ("7", "5+", "5-7") to the level a user fits best.
func levelForYears(s string) level {
	s = strings.TrimSpace(s)
	end := strings.IndexFunc(s, func(r rune) bool { return r < '0' || r > '9' })
	if end < 0 {
		end = len(s)
	}
	y, err := strconv.Atoi(s[:end])
	switch {
	case err != nil || y > 60:
		return levelUnknown
	case y < 1:
		return levelIntern
	case y < 3:
		return levelJunior
	case y < 6:
		return levelMid
	case y < 10:
		return levelSenior
	default:
		return levelLead
	}
}

// seniority compares the level a job asks for (title, else years asked) with the user's.
func (m *Matcher) seniority(job level) Reason {
	if job == levelUnknown || m.level == levelUnknown {
		return Reason{Code: "level.open", Points: maxLevel / 2}
	}
	p := map[string]string{"job": levelNames[job], "user": levelNames[m.level]}
	switch d := int(job) - int(m.level); {
	case d == 0:
		return Reason{Code: "level.fit", Points: maxLevel, Params: p}
	case d == 1:
		return Reason{Code: "level.stretch", Points: maxLevel / 3, Params: p}
	case d == -1:
		return Reason{Code: "level.below", Points: maxLevel / 3, Params: p}
	case d > 1:
		return Reason{Code: "level.too_senior", Points: -maxLevel, Params: p}
	default:
		return Reason{Code: "level.too_junior", Points: -maxLevel / 3, Params: p}
	}
}

func (m *Matcher) location(j Job) Reason {
	loc := normalize(j.Location)
	remoteJob := j.Workplace == "remote"
	empty := strings.TrimSpace(loc) == ""
	switch {
	case empty && !remoteJob:
		return Reason{Code: "location.unknown", Points: maxLocation / 2}
	case empty && m.remote && len(m.regions) > 0:
		return Reason{Code: "location.remote_unknown", Points: maxLocation / 2}
	case m.remote && m.remoteInRegion(j.Location, remoteJob):
		return Reason{Code: "location.remote", Points: maxLocation}
	}
	if c, ok := anyIn(m.cities, loc); ok {
		return Reason{Code: "location.city", Points: maxLocation, Params: map[string]string{"city": m.name(c)}}
	}
	if len(m.cities) == 0 && !m.remote {
		return Reason{Code: "location.any", Points: maxLocation / 2}
	}
	return Reason{Code: "location.none", Hard: true, Params: map[string]string{"location": j.Location}}
}

// remoteInRegion reports whether a job is remote in one of the user's regions. The remote mark and the region
// must belong to the same place: in "Munich, Germany; Remote Sweden" Germany is the office, remote is Sweden.
func (m *Matcher) remoteInRegion(location string, remoteJob bool) bool {
	inRegion := func(text string) bool {
		_, ok := anyIn(m.regions, text)
		_, all := anyIn(everywhere, text)
		return len(m.regions) == 0 || ok || all
	}
	if strings.TrimSpace(normalize(location)) == "" {
		return remoteJob
	}
	bare := false
	for _, part := range strings.FieldsFunc(location, func(r rune) bool { return r == ';' || r == '|' || r == '\n' }) {
		chunks := strings.Split(part, ",")
		for i, chunk := range chunks {
			c := normalize(chunk)
			w, ok := anyIn(remoteWords, c)
			if !ok {
				continue
			}
			if rest := strings.Replace(c, pad(w), " ", 1); strings.TrimSpace(rest) != "" {
				if inRegion(rest) {
					return true // "Remote - Germany", "Germany (Remote)"
				}
				continue
			}
			// only "Remote": the region stands next to it ("Remote, Germany") or is missing
			for k := max(0, i-1); k < min(len(chunks), i+2); k++ {
				if k != i && inRegion(normalize(chunks[k])) {
					return true
				}
			}
			bare = true
		}
	}
	return (bare || remoteJob) && inRegion(normalize(location))
}

// spoken returns the languages a user can work in: level B2 and up, or no level given. It is nil (unknown, no
// filter) when the list names neither German nor English: a profile with only other languages says nothing about
// the languages of the jobs here and must not hide all of them.
func spoken(list []Language) map[string]bool {
	out, named := map[string]bool{}, false
	for _, l := range list {
		var lang string
		switch n := normalize(l.Name); {
		case strings.Contains(n, " deutsch") || strings.Contains(n, " german"):
			lang = "de"
		case strings.Contains(n, " englisch") || strings.Contains(n, " english"):
			lang = "en"
		default:
			continue
		}
		named = true
		if _, low := anyIn(lowLevels, normalize(l.Level)); !low {
			out[lang] = true
		}
	}
	if !named {
		return nil
	}
	return out
}

var lowLevels = terms([]string{"a1", "a2", "b1", "basic", "basics", "beginner", "grundkenntnisse", "anfaenger"})

func (m *Matcher) language(lang string) Reason {
	p := map[string]string{"lang": lang}
	switch {
	case m.langs == nil:
		return Reason{Code: "language.unknown", Points: maxLanguage / 2, Params: p}
	case m.langs[lang]:
		return Reason{Code: "language.fit", Points: maxLanguage, Params: p}
	default:
		return Reason{Code: "language.missing", Hard: true, Params: p}
	}
}

var (
	germanWords = set("und", "wir", "sie", "du", "der", "die", "das", "mit", "fuer", "erfahrung", "kenntnisse",
		"deine", "ihre", "bei", "ein", "eine")
	englishWords = set("and", "we", "you", "the", "with", "for", "experience", "skills", "our", "your", "of", "to")
)

func set(words ...string) map[string]bool {
	out := make(map[string]bool, len(words))
	for _, w := range words {
		out[w] = true
	}
	return out
}

// DetectLanguage tells German from English by counting common words; "en" when in doubt.
func DetectLanguage(text string) string { return detect(normalize(text)) }

func detect(normalized string) string {
	if len(normalized) > 4000 {
		normalized = normalized[:4000]
	}
	de, en := 0, 0
	for _, w := range strings.Fields(normalized) {
		if germanWords[w] {
			de++
		}
		if englishWords[w] {
			en++
		}
	}
	if de > en {
		return "de"
	}
	return "en"
}

func (m *Matcher) salary(top int) Reason {
	switch {
	case top == 0 || m.minPay == 0:
		return Reason{}
	case top < m.minPay:
		return Reason{Code: "salary.below", Hard: true, Params: map[string]string{"top": strconv.Itoa(top),
			"min": strconv.Itoa(m.minPay)}}
	default:
		return Reason{Code: "salary.fit", Points: maxSalary, Params: map[string]string{"top": strconv.Itoa(top)}}
	}
}

const blanks = " \t\xc2\xa0" // includes the no-break space

// salaryTop returns the highest yearly amount in EUR that a description names next to a euro sign or "EUR", 0
// if none. Amounts below 20,000 are monthly or hourly and ignored.
func salaryTop(s string) int {
	if len(s) > maxText {
		s = s[:maxText]
	}
	s = strings.ToLower(s)
	top, lastEuro := 0, -100
	for i := 0; i < len(s); {
		if s[i] < '0' || s[i] > '9' {
			i++
			continue
		}
		j := i
		for j < len(s) && (s[j] >= '0' && s[j] <= '9' || s[j] == '.' || s[j] == ',' || s[j] == '\'') {
			j++
		}
		n := amount(s[i:j])
		rest := strings.TrimLeft(s[j:min(len(s), j+10)], blanks)
		if k, ok := strings.CutPrefix(rest, "k"); ok {
			n, rest = n*1000, strings.TrimLeft(k, blanks)
		} else if k, ok := strings.CutPrefix(rest, "tsd"); ok {
			n, rest = n*1000, strings.TrimLeft(strings.TrimPrefix(k, "."), blanks)
		}
		before := strings.TrimRight(s[max(0, i-6):i], blanks)
		euro := strings.HasPrefix(rest, "€") || strings.HasPrefix(rest, "eur") ||
			strings.HasSuffix(before, "€") || strings.HasSuffix(before, "eur") ||
			i-lastEuro <= 6 // the upper end of "€60k - 80k"
		if euro && n >= 20_000 && n <= 1_000_000 {
			top, lastEuro = max(top, n), j
		}
		i = j
	}
	return top
}

// amount reads "65.000", "65,000", "65'000" or "65"; decimals ("65.000,50") are dropped.
func amount(s string) int {
	s = strings.TrimRight(s, ".,'")
	if k := strings.LastIndexAny(s, ".,"); k >= 0 && len(s)-k-1 != 3 {
		s = s[:k]
	}
	n := 0
	for _, r := range s {
		if r >= '0' && r <= '9' {
			if n = n*10 + int(r-'0'); n > 10_000_000 {
				return 0
			}
		}
	}
	return n
}

func recency(j Job, now time.Time) Reason {
	at := j.Posted
	if at.IsZero() || (!j.FirstSeen.IsZero() && j.FirstSeen.Before(at)) {
		at = j.FirstSeen
	}
	if at.IsZero() {
		return Reason{}
	}
	days := max(int(now.Sub(at).Hours()/24), 0)
	p := map[string]string{"days": strconv.Itoa(days)}
	switch {
	case days <= 3:
		return Reason{Code: "recency.new", Points: maxRecency, Params: p}
	case days <= 14:
		return Reason{Code: "recency.recent", Points: 3, Params: p}
	case days <= 30:
		return Reason{Code: "recency.month", Points: 1, Params: p}
	default:
		return Reason{Code: "recency.old", Params: p}
	}
}
