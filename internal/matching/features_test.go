package matching

import (
	"slices"
	"strings"
	"testing"
)

func TestYearsAsked(t *testing.T) {
	for text, want := range map[string]int{
		"You bring 5+ years of experience with Go":                     5,
		"3-5 years of experience":                                      3,
		"mindestens 3 Jahre Berufserfahrung":                           3,
		"Du hast mehrjährige Erfahrung im Betrieb":                     3,
		"12+ years of experience, of which 5 in leadership":            12,
		"We offer 30 days of holidays and 20 Stunden pro Woche":        0,
		"Founded 1998, 250 employees":                                  0,
		"several years as DevOps engineer, 10 years experience a plus": 3,
	} {
		if got := yearsAsked(normalize(text)); got != want {
			t.Errorf("yearsAsked(%q) = %d, want %d", text, got, want)
		}
	}
}

func TestPreferredText(t *testing.T) {
	desc := "Requirements: Go and Kubernetes.\nNice to have: Rust. You know Terraform.\n\nVon Vorteil:\nKafka\nSpark\n\nWir bieten Obst."
	got := preferredText(desc)
	for _, want := range []string{" rust ", " kafka ", " spark "} {
		if !strings.Contains(got, want) {
			t.Errorf("preferred text %q lacks %q", got, want)
		}
	}
	for _, not := range []string{" kubernetes ", " terraform ", " obst "} {
		if strings.Contains(got, not) {
			t.Errorf("preferred text %q holds %q", got, not)
		}
	}
	// A skill named only as nice to have is not missing; one named as required too is.
	m := NewMatcher(Criteria{Skills: []string{"Go"}}, nil)
	_, missing, _, _ := m.skillFit(Prepare(Job{Description: desc}, nil))
	if strings.Contains(strings.Join(missing, ","), "Rust") || !strings.Contains(strings.Join(missing, ","), "Kubernetes") {
		t.Errorf("missing = %v", missing)
	}
}

func TestPrepare(t *testing.T) {
	p := Prepare(Job{Title: "Engineer", Description: "Wir suchen dich. Du hast mindestens 6 Jahre Erfahrung. Englisch reicht."}, nil)
	if p.lang != "en" || p.years != 6 || p.level != levelSenior || p.vec != nil {
		t.Errorf("prepared %+v", p)
	}
	// The title's level wins over the years asked.
	if p := Prepare(Job{Title: "Junior Developer", Description: "2+ years"}, nil); p.level != levelJunior {
		t.Errorf("level %v", p.level)
	}
}

// fixedEmbedder returns a fixed unit vector per text.
type fixedEmbedder map[string][]float32

func (f fixedEmbedder) Embed(text string) ([]float32, error) { return f[text], nil }

func TestSimilarityInWords(t *testing.T) {
	c := Criteria{Titles: []string{"Go Developer"}}
	for _, tt := range []struct {
		job  []float32
		code string
	}{{[]float32{1, 0}, "similarity.high"}, {[]float32{0.5, 0.866}, "similarity.some"}, {[]float32{0, 1}, "similarity.low"}} {
		j := Job{Title: "Go Developer", Description: "Go"}
		emb := fixedEmbedder{ProfileText(c): {1, 0}, embedText(j): tt.job}
		r := NewMatcher(c, emb).Evaluate(j, now)
		if !slices.ContainsFunc(r.Reasons, func(rs Reason) bool { return rs.Code == tt.code && len(rs.Params) == 0 }) {
			t.Errorf("%v: %+v, want %s", tt.job, r.Reasons, tt.code)
		}
	}
}
