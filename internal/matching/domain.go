// Package matching ranks open jobs per user: hard filters, a score from 0 to 100 with a reason for every part,
// and a decision. Rules plus a static embedding model (ADR 0029), no LLM; the same inputs always give the same
// result.
package matching

import (
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
)

// RulesVersion changes whenever the rules do, so stored results can be recomputed.
const RulesVersion = 6

// Criteria is what a user wants and brings. Empty fields do not filter.
type Criteria struct {
	Titles        []string
	ExcludeTitles []string
	Locations     []string
	Remote        bool
	RemoteRegions []string
	MinSalary     int // yearly gross in EUR
	Skills        []string
	Languages     []Language
	YearsTotal    string // as typed in the profile, e.g. "7" or "5+"
	// Weights are learned from the user's own choices (#502); nil: the default weights.
	Weights *Weights
}

// Language is a language the user speaks with its level as typed ("C1", "native", "fließend").
type Language struct {
	Name, Level string
}

// Job is the part of a job that matching reads.
type Job struct {
	ID             int64
	Company        string
	Title          string
	Location       string
	Workplace      string // remote, hybrid, onsite or empty
	Description    string
	SalaryMin      int
	SalaryMax      int
	SalaryCurrency string
	Posted         time.Time // zero: unknown
	FirstSeen      time.Time
}

// Decision is what getafjob suggests for a job.
type Decision string

// Decisions.
const (
	Match Decision = "match" // worth applying
	Maybe Decision = "maybe" // the user should look
	Skip  Decision = "skip"  // filtered out or a poor fit
)

// Thresholds of the decision.
const (
	matchFrom = 60
	maybeFrom = 35
)

// Reason explains one part of a result: Code is an i18n key below match.reason., Params fill it in. The points of
// all reasons add up to the score.
type Reason struct {
	Code   string            `json:"code"`
	Points int               `json:"points"`
	Params map[string]string `json:"params,omitempty"`
	Hard   bool              `json:"hard,omitempty"` // a filter that decides on its own
}

// Result is the outcome for one user and job.
type Result struct {
	JobID    int64
	Score    int // 0 to 100
	Decision Decision
	Reasons  []Reason
}

// Raw points of the parts; the score weighs them (weights below).
const (
	maxTitle    = 35
	maxSkills   = 25
	maxLevel    = 15
	maxLocation = 10
	maxLanguage = 5
	maxSalary   = 5
	maxRecency  = 5
)

// Weights of the core parts in the score, with and without the embedding model. Text similarity carries most:
// on the ranking set it orders jobs best (ADR 0029).
var (
	weightsWithModel = Weights{Similarity: 0.40, Title: 0.20, Skills: 0.25, Level: 0.15}
	weightsRulesOnly = Weights{Title: 0.40, Skills: 0.35, Level: 0.25}
)

// Weights are the shares of the core factors in the score; they add up to 1.
type Weights struct {
	Similarity float64 `json:"similarity"`
	Title      float64 `json:"title"`
	Skills     float64 `json:"skills"`
	Level      float64 `json:"level"`
	Samples    int     `json:"samples,omitempty"` // choices they were learned from; 0 for the defaults
}

// DefaultWeights are the weights with the embedding model (ADR 0029).
func DefaultWeights() Weights { return weightsWithModel }

// withoutSimilarity spreads the similarity share over the other factors (no model, or no profile vector).
func (w Weights) withoutSimilarity() Weights {
	rest := w.Title + w.Skills + w.Level
	if rest <= 0 {
		return weightsRulesOnly
	}
	return Weights{Title: w.Title / rest, Skills: w.Skills / rest, Level: w.Level / rest, Samples: w.Samples}
}

// Matcher evaluates jobs for one user; build it once per user, it prepares the terms and the profile's vector.
type Matcher struct {
	titles   []term
	synonyms []term
	tokens   []string
	exclude  []term
	cities   []term
	remote   bool
	regions  []term
	skills   []userSkill
	level    level
	langs    map[string]bool
	minPay   int
	names    map[term]string // user terms as typed, for reasons
	emb      Embedder
	vec      []float32 // the profile's vector; nil without a model or profile text
	learned  *Weights  // the user's own weights; nil for the defaults
}

// NewMatcher prepares c. emb may be nil: the score then comes from the rules alone.
func NewMatcher(c Criteria, emb Embedder) *Matcher {
	m := &Matcher{titles: terms(c.Titles), exclude: terms(c.ExcludeTitles), cities: terms(c.Locations),
		remote: c.Remote, level: levelForYears(c.YearsTotal), langs: spoken(c.Languages), minPay: c.MinSalary,
		skills: userSkills(c.Skills), emb: emb, learned: c.Weights}
	m.names = map[term]string{}
	for _, l := range [][]string{c.Titles, c.ExcludeTitles, c.Locations} {
		for _, s := range l {
			if t := newTerm(s); t != "" && m.names[t] == "" {
				m.names[t] = strings.Join(strings.Fields(s), " ")
			}
		}
	}
	m.synonyms = synonymsOf(m.titles)
	for _, t := range m.titles {
		for _, w := range strings.Fields(string(t)) {
			if len(w) >= 2 && !generic[w] && !slices.Contains(m.tokens, w) {
				m.tokens = append(m.tokens, w)
			}
		}
	}
	for _, t := range terms(c.RemoteRegions) {
		m.regions = append(m.regions, t)
		m.regions = append(m.regions, terms(regionAliases[string(t)])...)
	}
	if text := ProfileText(c); emb != nil && text != "" {
		m.vec, _ = emb.Embed(text)
	}
	return m
}

// ProfileText is what the profile's vector is made of: the wanted titles and the skills.
func ProfileText(c Criteria) string {
	var parts []string
	if len(c.Titles) > 0 {
		parts = append(parts, strings.Join(c.Titles, ", "))
	}
	if len(c.Skills) > 0 {
		parts = append(parts, strings.Join(c.Skills, ", "))
	}
	return strings.Join(parts, ". ")
}

// name returns a user term as typed; dictionary terms come back normalized.
func (m *Matcher) name(t term) string {
	if n, ok := m.names[t]; ok {
		return n
	}
	return string(t)
}

// Evaluate scores one job. To score many users against the same jobs, Prepare each job once and use
// EvaluatePrepared.
func (m *Matcher) Evaluate(j Job, now time.Time) Result {
	r, _ := m.evaluate(Prepare(j, m.emb), now)
	return r
}

// EvaluatePrepared scores a prepared job; p must come from the same embedding model as the matcher.
func (m *Matcher) EvaluatePrepared(p *Prepared, now time.Time) Result {
	r, _ := m.evaluate(p, now)
	return r
}

// scored keeps the parts of one evaluation; all zero when the title is excluded.
type scored struct {
	title, skills, level, location, language, salary, recency, similarity Reason
	hits, missing                                                         []string
	factors                                                               Factors
	excluded                                                              bool
}

// Factors are the core parts of a result from 0 to 1, before weighting: what the learned weights are fitted on.
type Factors struct {
	Similarity, Title, Skills, Level float64
}

// FactorsOf returns the core factors of a prepared job for this user; false when a hard filter decides on its own
// (an excluded title), so the job tells nothing about the weights.
func (m *Matcher) FactorsOf(p *Prepared, now time.Time) (Factors, bool) {
	_, sc := m.evaluate(p, now)
	return sc.factors, !sc.excluded
}

// evaluate scores one job once and returns the parts it added up.
func (m *Matcher) evaluate(p *Prepared, now time.Time) (Result, scored) {
	j := p.Job
	r := Result{JobID: j.ID, Decision: Skip}
	if ex, ok := anyIn(m.exclude, p.title); ok {
		r.Reasons = []Reason{{Code: "title.excluded", Hard: true, Params: map[string]string{"term": m.name(ex)}}}
		return r, scored{excluded: true}
	}
	var sc scored
	var skillPoints int
	var skillCode string
	sc.hits, sc.missing, skillPoints, skillCode = m.skillFit(p)
	sc.title, sc.skills, sc.level = m.title(p.title), skillReason(sc.hits, sc.missing, skillPoints, skillCode), m.seniority(p.level)
	sc.location, sc.language, sc.salary, sc.recency = m.location(j), m.language(p.lang), m.salary(p.salaryTop), recency(j, now)

	// Core: weighted factors from 0 to 1; each reason carries its share of the score in points.
	w := weightsRulesOnly
	sim, simFactor, hasSim := m.similarity(p.vec)
	switch {
	case hasSim && m.learned != nil:
		w = *m.learned
	case hasSim:
		w = weightsWithModel
	case m.learned != nil:
		w = m.learned.withoutSimilarity()
	}
	if hasSim {
		sc.similarity = sim
	} else if sc.title.Code == "title.none" {
		sc.title.Hard = true // without the model nothing else tells a related title from an unrelated one
	}
	share := func(rs *Reason, factor, weight float64) {
		rs.Points = int(math.Round(100 * weight * min(max(factor, 0), 1)))
	}
	f := Factors{Title: float64(sc.title.Points) / maxTitle, Skills: float64(sc.skills.Points) / maxSkills,
		Level: float64(sc.level.Points+maxLevel) / (2 * maxLevel)}
	if hasSim {
		f.Similarity = simFactor
		share(&sc.similarity, simFactor, w.Similarity)
	}
	sc.factors = f
	share(&sc.title, f.Title, w.Title)
	share(&sc.skills, f.Skills, w.Skills)
	share(&sc.level, f.Level, w.Level)
	parts := []*Reason{&sc.similarity, &sc.title, &sc.skills, &sc.level}
	score := 0
	for _, rs := range parts {
		score += rs.Points
	}
	// Soft parts lower the score by a share: place, language and age of the posting; salary only filters.
	for _, s := range []struct {
		rs     *Reason
		factor float64
	}{{&sc.location, locationFactor(sc.location)}, {&sc.language, languageFactor(sc.language)},
		{&sc.salary, 1}, {&sc.recency, recencyFactor(sc.recency)}} {
		delta := int(math.Round(float64(score) * (s.factor - 1)))
		s.rs.Points = delta
		score += delta
		parts = append(parts, s.rs)
	}
	if m.learned != nil && m.learned.Samples > 0 {
		parts = append(parts, &Reason{Code: "weights.learned", Params: map[string]string{"n": strconv.Itoa(m.learned.Samples)}})
	}
	hard := false
	for _, rs := range parts {
		if rs.Code == "" {
			continue
		}
		hard = hard || rs.Hard
		r.Reasons = append(r.Reasons, *rs)
	}
	r.Score = min(max(score, 0), 100)
	switch {
	case hard:
	case r.Score >= matchFrom:
		r.Decision = Match
	case r.Score >= maybeFrom:
		r.Decision = Maybe
	}
	return r, sc
}

// similarity compares the profile's vector with the job's: cosine 0.25 or less counts as no likeness, 0.70 or
// more as full (the range the model gives for unrelated and for the same kind of job). The reason names it in
// words; a percentage of a scaled cosine reads like a share and misleads.
func (m *Matcher) similarity(job []float32) (Reason, float64, bool) {
	if m.vec == nil || len(job) != len(m.vec) {
		return Reason{}, 0, false
	}
	var cos float64
	for i := range job {
		cos += float64(job[i]) * float64(m.vec[i])
	}
	f := min(max((cos-0.25)/(0.70-0.25), 0), 1)
	code := "similarity.low"
	switch {
	case f >= 0.6:
		code = "similarity.high"
	case f >= 0.3:
		code = "similarity.some"
	}
	return Reason{Code: code}, f, true
}

func locationFactor(r Reason) float64 {
	switch r.Code {
	case "location.city", "location.remote":
		return 1
	case "location.none":
		return 0.8
	}
	return 0.9 // unknown or no wish
}

func languageFactor(r Reason) float64 {
	switch r.Code {
	case "language.unknown":
		return 0.95
	case "language.missing":
		return 0.8
	}
	return 1
}

func recencyFactor(r Reason) float64 {
	switch r.Code {
	case "recency.recent":
		return 0.97
	case "recency.month":
		return 0.94
	case "recency.old":
		return 0.9
	}
	return 1
}

// SkillFit is the skill comparison of one job for the job page: the user's skills it names and the known skills
// it asks for that the profile lacks (nice-to-have ones are not missing). The score itself comes from the stored
// result, so the page and the list never disagree.
type SkillFit struct {
	Have    []string
	Missing []string
}

// SkillsFor compares c's skills with j.
func SkillsFor(c Criteria, j Job) SkillFit {
	have, missing, _, _ := NewMatcher(c, nil).skillFit(Prepare(j, nil))
	return SkillFit{Have: have, Missing: missing}
}
