package matching

import (
	"math"

	"github.com/1xxn/getafjob/internal/platform/vocab"
)

// skillCounts counts how often each dictionary skill is named in a normalized text.
func skillCounts(normalized string) []uint16 {
	return vocab.SkillCounts(normalized)
}

// jobSkills are the dictionary skills a job names: asked for, or named only in nice-to-have sentences.
type jobSkills struct {
	named     []bool
	preferred []bool // every mention is a nice-to-have one
}

func findSkills(text, preferred string) jobSkills {
	all, pref := skillCounts(text), skillCounts(preferred)
	js := jobSkills{named: make([]bool, len(skills)), preferred: make([]bool, len(skills))}
	for i := range all {
		js.named[i] = all[i] > 0
		js.preferred[i] = all[i] > 0 && pref[i] >= all[i]
	}
	return js
}

// KnownSkills extracts canonical skill names from free text using the shared skill dictionary (#506).
func KnownSkills(text string) []string {
	return vocab.KnownSkills(text)
}

// weight is the weight of dictionary skill i; -1 (a skill the dictionary does not know) weighs 1.
func weight(i int) float64 {
	if i < 0 {
		return 1
	}
	return float64(skills[i].Weight)
}

// typicalWeight is the weight of an average skill: the unit for how many skills a job needs before coverage counts.
const typicalWeight = 2

// skillFit compares the user's skills with the skills a job names. Skills named only as nice to have count when
// the user has them and are not missing when not. The points are the weighted share of the job's skills the user
// has; a job naming fewer than three typical skills is judged as if it named three.
func (m *Matcher) skillFit(p *Prepared) (hits []string, missing []string, points int, code string) {
	var have, want float64
	mine := make([]bool, len(skills))
	for _, s := range m.skills {
		found := false
		if s.idx >= 0 {
			mine[s.idx] = true
			found = p.skills.named[s.idx]
		} else {
			_, found = anyIn(s.terms, p.text)
		}
		if found {
			have += weight(s.idx)
			hits = append(hits, s.name)
		}
	}
	want = have
	for i, named := range p.skills.named {
		if named && !mine[i] && !p.skills.preferred[i] {
			want += weight(i)
			missing = append(missing, skills[i].Name)
		}
	}
	if want == 0 {
		return nil, nil, maxSkills * 2 / 5, "skills.unknown"
	}
	switch {
	case have == 0:
		code = "skills.none"
	case len(missing) == 0:
		code = "skills.all" // every known skill the job asks for is in the profile
	default:
		code = "skills.fit"
	}
	t := float64(typicalWeight)
	points = min(int(math.Round(maxSkills*have/min(max(want, 3*t), 6*t))), maxSkills)
	return hits, missing, points, code
}
