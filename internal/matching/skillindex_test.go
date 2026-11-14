package matching

import (
	"slices"
	"testing"
)

func TestSkillIndex(t *testing.T) {
	named := func(text string) []string {
		var out []string
		for i, c := range skillCounts(normalize(text)) {
			if c > 0 {
				out = append(out, skills[i].name)
			}
		}
		return out
	}
	got := named("Google Cloud, Spring Boot and Node.js; we deploy with GitHub Actions on Kubernetes (k8s).")
	for _, want := range []string{"GCP", "Spring", "Node.js", "GitHub Actions", "Git", "Kubernetes"} {
		if !slices.Contains(got, want) {
			t.Errorf("%v lacks %s", got, want)
		}
	}
	// Whole words only: "go" is not in "Google", "chef" alone is the boss, "spring" alone is a season.
	for _, text := range []string{"Google Ads", "Dein Chef ist nett", "Start im Frühling (spring 2027)"} {
		if got := named(text); len(got) != 0 {
			t.Errorf("%q names %v", text, got)
		}
	}
	// Every alias of the dictionary is found by the index.
	for i, k := range skills {
		for _, a := range k.aliases {
			if c := skillCounts(" " + string(newTerm(a)) + " "); c[i] == 0 {
				t.Errorf("alias %q of %s not found", a, k.name)
			}
		}
	}
}

func TestImpliedSkills(t *testing.T) {
	var names []string
	for _, s := range userSkills([]string{"PyTorch", "Next.js"}) {
		names = append(names, s.name)
	}
	for _, want := range []string{"PyTorch", "Machine Learning", "Python", "Next.js", "React", "JavaScript"} {
		if !slices.Contains(names, want) {
			t.Errorf("%v lacks %s", names, want)
		}
	}
	for name, list := range implied {
		for _, n := range append(list, name) {
			if !slices.ContainsFunc(skills, func(s skill) bool { return s.name == n }) {
				t.Errorf("implied names unknown skill %q", n)
			}
		}
	}
}
