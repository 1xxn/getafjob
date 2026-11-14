package matching

import (
	"math"
	"slices"
	"strings"
)

// skillAlias is one spelling of a dictionary skill, as words.
type skillAlias struct {
	words []string
	skill int
}

// skillIndex finds every dictionary skill in a normalized text in one pass over its words: the aliases are keyed
// by their first word, so a text costs one map lookup per word whatever the size of the dictionary.
var skillIndex = func() map[string][]skillAlias {
	idx := map[string][]skillAlias{}
	for i, k := range skills {
		for _, t := range terms(k.aliases) { // not the name: "Spring", "Chef", "C" and "R" mean other things in a text
			words := strings.Fields(string(t))
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

// skillCounts counts how often each dictionary skill is named in a normalized text.
func skillCounts(normalized string) []uint16 {
	counts := make([]uint16, len(skills))
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

// weight is the weight of dictionary skill i; -1 (a skill the dictionary does not know) weighs 1.
func weight(i int) float64 {
	if i < 0 {
		return 1
	}
	return float64(skills[i].weight)
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
			missing = append(missing, skills[i].name)
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
