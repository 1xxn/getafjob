package matching

import (
	"strconv"
	"strings"
)

// Embedder turns a text into a unit vector (embed.Model, ADR 0029); nil leaves the similarity out.
type Embedder interface {
	Embed(text string) ([]float32, error)
}

// Prepared is what matching reads from one job, worked out once for all users: normalized text, the language,
// the salary named, the level asked and the job's vector.
type Prepared struct {
	Job       Job
	title     string // normalized
	text      string // normalized title and description
	preferred string // normalized sentences that name nice-to-have skills
	skills    jobSkills
	lang      string
	salaryTop int
	level     level
	years     int // years of experience asked, 0 when not named
	vec       []float32
}

// Prepare works out the parts of j that do not depend on the user. emb may be nil.
func Prepare(j Job, emb Embedder) *Prepared {
	p := &Prepared{Job: j, title: normalize(j.Title)}
	p.text = p.title + normalize(j.Description)
	p.preferred = preferredText(j.Description)
	p.skills = findSkills(p.text, p.preferred)
	p.lang = detect(p.text)
	if englishWelcome(p.text) {
		p.lang = "en"
	}
	p.salaryTop = salaryTop(j.Description)
	p.years = yearsAsked(p.text)
	p.level = levelOf(p.title)
	if p.level == levelUnknown && p.years > 0 {
		p.level = levelForYears(strconv.Itoa(p.years))
	}
	if emb != nil {
		p.vec, _ = emb.Embed(embedText(j))
	}
	return p
}

// embedText is what a job's vector is made of: its title and description.
func embedText(j Job) string { return j.Title + ". " + j.Description }

// preferredMarks introduce skills a job would like but does not require.
var preferredMarks = terms([]string{"nice to have", "is a plus", "are a plus", "a plus", "bonus", "preferred",
	"von vorteil", "wuenschenswert", "ist ein plus", "sind ein plus", "gerne gesehen", "idealerweise", "optional",
	"welcome", "willkommen"})

// preferredText returns the normalized sentences of a description that carry a nice-to-have mark, together with
// the lines after a "nice to have" heading up to the next empty line.
func preferredText(desc string) string {
	if len(desc) > maxText {
		desc = desc[:maxText]
	}
	var b strings.Builder
	inList := false
	for _, line := range strings.Split(desc, "\n") {
		n := normalize(line)
		if strings.TrimSpace(n) == "" {
			inList = false
			continue
		}
		if inList {
			b.WriteString(n)
			continue
		}
		marked := false
		for _, s := range strings.FieldsFunc(line, func(r rune) bool { return r == '.' || r == ';' || r == '!' }) {
			if ns := normalize(s); hasMark(ns) {
				b.WriteString(ns)
				marked = true
			}
		}
		// "Nice to have:" as a heading: the list below it is preferred too.
		inList = marked && strings.HasSuffix(strings.TrimSpace(line), ":")
	}
	return b.String()
}

func hasMark(normalized string) bool {
	_, ok := anyIn(preferredMarks, normalized)
	return ok
}

// englishMarks say that a German posting works in English.
var englishMarks = terms([]string{"englisch reicht", "english is enough", "english is sufficient", "english only",
	"working language is english", "arbeitssprache ist englisch", "unternehmenssprache ist englisch",
	"unternehmenssprache englisch", "no german required", "german is not required", "deutsch ist ein plus",
	"deutschkenntnisse sind ein plus", "german is a plus"})

func englishWelcome(text string) bool {
	_, ok := anyIn(englishMarks, text)
	return ok
}

// yearsAsked reads the years of experience a posting asks for ("5+ years", "3-5 years", "mindestens 3 Jahre",
// "mehrjaehrige" counts as 3); the smallest number named wins, 0 when none.
func yearsAsked(text string) int {
	words := strings.Fields(text)
	best := 0
	take := func(n int) {
		if n > 0 && n <= 25 && (best == 0 || n < best) {
			best = n
		}
	}
	for i, w := range words {
		if w == "mehrjaehrige" || w == "mehrjaehriger" || w == "several" && i+1 < len(words) && words[i+1] == "years" {
			take(3)
		}
		n, err := strconv.Atoi(w)
		if err != nil {
			continue
		}
		// "5 years", "5 jahre", "3 5 years" (from "3-5"), "5 years of" ...
		for k := i + 1; k < min(len(words), i+3); k++ {
			switch words[k] {
			case "years", "year", "jahre", "jahren", "yrs":
				take(n)
			}
			if _, err := strconv.Atoi(words[k]); err != nil {
				break
			}
		}
	}
	return best
}
