// Package matching ranks open jobs per user: hard filters, a score from 0 to 100 with a reason for every part,
// and a decision. Rules only, no AI; the same inputs always give the same result.
package matching

import (
	"slices"
	"strings"
	"time"
)

// RulesVersion changes whenever the rules do, so stored results can be recomputed.
const RulesVersion = 1

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
}

// Language is a language the user speaks with its level as typed ("C1", "native", "fließend").
type Language struct {
	Name, Level string
}

// Job is the part of a job that matching reads.
type Job struct {
	ID          int64
	Company     string
	Title       string
	Location    string
	Workplace   string // remote, hybrid, onsite or empty
	Description string
	Posted      time.Time // zero: unknown
	FirstSeen   time.Time
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

// Reason explains one part of a result: Code is an i18n key below match.reason., Params fill it in.
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

// Maximum points of each part; they add up to 100.
const (
	maxTitle    = 35
	maxSkills   = 25
	maxLevel    = 15
	maxLocation = 10
	maxLanguage = 5
	maxSalary   = 5
	maxRecency  = 5
)

// Matcher evaluates jobs for one user; build it once per user, it prepares the terms.
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
}

// NewMatcher prepares c.
func NewMatcher(c Criteria) *Matcher {
	m := &Matcher{titles: terms(c.Titles), exclude: terms(c.ExcludeTitles), cities: terms(c.Locations),
		remote: c.Remote, level: levelForYears(c.YearsTotal), langs: spoken(c.Languages), minPay: c.MinSalary,
		skills: userSkills(c.Skills)}
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
	return m
}

// name returns a user term as typed; dictionary terms come back normalized.
func (m *Matcher) name(t term) string {
	if n, ok := m.names[t]; ok {
		return n
	}
	return string(t)
}

// Evaluate scores one job.
func (m *Matcher) Evaluate(j Job, now time.Time) Result {
	r := Result{JobID: j.ID, Decision: Skip}
	title := normalize(j.Title)
	if ex, ok := anyIn(m.exclude, title); ok {
		r.Reasons = []Reason{{Code: "title.excluded", Hard: true, Params: map[string]string{"term": m.name(ex)}}}
		return r
	}
	text := title + normalize(j.Description)
	hard := false
	for _, p := range []Reason{m.title(title), m.skillFit(text), m.seniority(title), m.location(j),
		m.language(text), m.salary(j.Description), recency(j, now)} {
		if p.Code == "" {
			continue
		}
		r.Score += p.Points
		hard = hard || p.Hard
		r.Reasons = append(r.Reasons, p)
	}
	r.Score = min(max(r.Score, 0), 100)
	switch {
	case hard:
	case r.Score >= matchFrom:
		r.Decision = Match
	case r.Score >= maybeFrom:
		r.Decision = Maybe
	}
	return r
}

// FitInspection is a granular breakdown of how a candidate matches a job.
type FitInspection struct {
	Score              int               `json:"score"`
	Decision           Decision          `json:"decision"`
	MatchingSkills     []string          `json:"matching_skills"`
	MissingSkills      []string          `json:"missing_skills"`
	SkillPoints        int               `json:"skill_points"`
	MaxSkillPoints     int               `json:"max_skill_points"`
	TitlePoints        int               `json:"title_points"`
	MaxTitlePoints     int               `json:"max_title_points"`
	SeniorityPoints    int               `json:"seniority_points"`
	MaxSeniorityPoints int               `json:"max_seniority_points"`
	CandidateLevel     string            `json:"candidate_level"`
	JobLevel           string            `json:"job_level"`
	LocationPoints     int               `json:"location_points"`
	MaxLocationPoints  int               `json:"max_location_points"`
	LocationReason     string            `json:"location_reason"`
	LocationParams     map[string]string `json:"location_params,omitempty"`
	Workplace          string            `json:"workplace"`
	LanguagePoints     int               `json:"language_points"`
	SalaryPoints       int               `json:"salary_points"`
	RecencyPoints      int               `json:"recency_points"`
	Reasons            []Reason          `json:"reasons"`
}

// InspectFit provides a detailed fit breakdown including all matching skills and missing keywords.
func (m *Matcher) InspectFit(j Job, now time.Time) FitInspection {
	title := normalize(j.Title)
	text := title + normalize(j.Description)

	hits, missing, skillPts, _ := m.skillHitsAndMissing(text)
	titleReason := m.title(title)
	seniorityReason := m.seniority(title)
	locationReason := m.location(j)
	langReason := m.language(text)
	salReason := m.salary(j.Description)
	recReason := recency(j, now)

	res := m.Evaluate(j, now)

	candLevel := levelNames[m.level]
	jobLvl := levelNames[levelOf(title)]

	return FitInspection{
		Score:              res.Score,
		Decision:           res.Decision,
		MatchingSkills:     hits,
		MissingSkills:      missing,
		SkillPoints:        skillPts,
		MaxSkillPoints:     maxSkills,
		TitlePoints:        titleReason.Points,
		MaxTitlePoints:     maxTitle,
		SeniorityPoints:    seniorityReason.Points,
		MaxSeniorityPoints: maxLevel,
		CandidateLevel:     candLevel,
		JobLevel:           jobLvl,
		LocationPoints:     locationReason.Points,
		MaxLocationPoints:  maxLocation,
		LocationReason:     locationReason.Code,
		LocationParams:     locationReason.Params,
		Workplace:          j.Workplace,
		LanguagePoints:     langReason.Points,
		SalaryPoints:       salReason.Points,
		RecencyPoints:      recReason.Points,
		Reasons:            res.Reasons,
	}
}

// InspectFit evaluates criteria against a job with granular breakdown.
func InspectFit(c Criteria, j Job, now time.Time) FitInspection {
	m := NewMatcher(c)
	return m.InspectFit(j, now)
}
