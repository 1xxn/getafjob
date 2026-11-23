package matching

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
)

// samples builds n rated jobs: the user likes the ones whose skills fit, title and level say nothing.
func skillSamples(n int) []Sample {
	out := make([]Sample, n)
	for i := range out {
		skills := float64(i%10) / 10
		out[i] = Sample{F: Factors{Similarity: 0.5, Title: float64((i*7)%10) / 10, Skills: skills, Level: 0.5},
			Liked: skills >= 0.5}
	}
	return out
}

func sum(w Weights) float64 { return w.Similarity + w.Title + w.Skills + w.Level }

func TestLearnWeights(t *testing.T) {
	if w := LearnWeights(skillSamples(MinRated - 1)); w != nil {
		t.Fatalf("too few samples learned %+v", w)
	}
	oneSided := skillSamples(40)
	for i := range oneSided {
		oneSided[i].Liked = i < 3
	}
	if w := LearnWeights(oneSided); w != nil {
		t.Fatalf("one side under %d learned %+v", MinRatedPerSide, w)
	}

	w := LearnWeights(skillSamples(200))
	if w == nil {
		t.Fatal("no weights")
	}
	def := DefaultWeights()
	t.Logf("learned from 200: %+v", *w)
	if w.Samples != 200 || math.Abs(sum(*w)-1) > 0.005 {
		t.Fatalf("weights %+v: samples or sum", w)
	}
	if w.Skills <= def.Skills || w.Title >= def.Title {
		t.Fatalf("weights %+v: skills should gain, title lose against %+v", w, def)
	}
	for _, v := range []float64{w.Similarity, w.Title, w.Skills, w.Level} {
		if v < weightFloor {
			t.Fatalf("weights %+v: below the floor", w)
		}
	}
	// Deterministic: the same choices give the same weights.
	if again := LearnWeights(skillSamples(200)); *again != *w {
		t.Fatalf("not deterministic: %+v vs %+v", again, w)
	}
	// Fewer choices stay closer to the defaults.
	if few := LearnWeights(skillSamples(30)); few == nil || few.Skills >= w.Skills {
		t.Fatalf("30 samples %+v should move less than 200 %+v", few, w)
	}
}

func TestLearnWeightsWithoutModel(t *testing.T) {
	s := skillSamples(100)
	for i := range s {
		s[i].F.Similarity = 0
	}
	w := LearnWeights(s)
	if w == nil || w.Similarity != DefaultWeights().Similarity || math.Abs(sum(*w)-1) > 0.005 {
		t.Fatalf("weights without model %+v: similarity keeps its default share", w)
	}
}

func TestLearnedWeightsInScore(t *testing.T) {
	c := Criteria{Titles: []string{"Backend Engineer"}, Skills: []string{"Go", "PostgreSQL"}, YearsTotal: "6"}
	j := Job{Title: "Backend Engineer", Location: "Berlin", Description: "We use Java.", Posted: now}
	base := NewMatcher(c, nil).Evaluate(j, now)
	c.Weights = &Weights{Similarity: 0.1, Title: 0.7, Skills: 0.1, Level: 0.1, Samples: 42}
	learned := NewMatcher(c, nil).Evaluate(j, now)
	if learned.Score <= base.Score {
		t.Fatalf("a title-heavy weighting should raise a title-only fit: %d vs %d", learned.Score, base.Score)
	}
	last := learned.Reasons[len(learned.Reasons)-1]
	if last.Code != "weights.learned" || last.Params["n"] != "42" || last.Points != 0 {
		t.Fatalf("reasons %+v: want the learned-weights reason", learned.Reasons)
	}
	for _, rs := range base.Reasons {
		if rs.Code == "weights.learned" {
			t.Fatal("default weights name learned weights")
		}
	}
}

type fakeProfiles map[uuid.UUID]Criteria

func (p fakeProfiles) Users(context.Context) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, 0, len(p))
	for id := range p {
		out = append(out, id)
	}
	return out, nil
}

func (p fakeProfiles) Criteria(_ context.Context, id uuid.UUID) (Criteria, error) { return p[id], nil }

type fakeJobs []Job

func (f fakeJobs) OpenJobs(_ context.Context, after int64, _ int) ([]Job, error) {
	if after > 0 {
		return nil, nil
	}
	return f, nil
}

type fakeStore map[uuid.UUID][]Result

func (f fakeStore) SaveMatches(_ context.Context, u uuid.UUID, rs []Result, _ time.Time) error {
	f[u] = append(f[u], rs...)
	return nil
}

type fakeFeedback []Rated

func (f fakeFeedback) Rated(context.Context, uuid.UUID, int) ([]Rated, error) { return f, nil }

func TestRunLearnsFromFeedback(t *testing.T) {
	u := uuid.New()
	c := Criteria{Titles: []string{"Backend Engineer"}, Skills: []string{"Go", "PostgreSQL", "Kubernetes"}, YearsTotal: "6"}
	// The user likes jobs with their skills and skips the others, whatever the title says.
	var rated fakeFeedback
	for i := range 40 {
		title := "Backend Engineer"
		if i%2 == 0 {
			title = "Platform Engineer"
		}
		desc, liked := "We use Java and Spring.", false
		if i%4 < 2 {
			desc, liked = "We use Go, PostgreSQL and Kubernetes.", true
		}
		rated = append(rated, Rated{Job: Job{ID: int64(1000 + i), Title: title, Description: desc, Posted: now}, Liked: liked})
	}
	store := fakeStore{}
	jobs := fakeJobs{{ID: 1, Title: "Platform Engineer", Description: "Go, PostgreSQL and Kubernetes.", Posted: now}}
	svc := NewService(fakeProfiles{u: c}, jobs, store).WithFeedback(rated).WithClock(func() time.Time { return now })
	if _, err := svc.Run(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	plain := NewMatcher(c, nil).Evaluate(jobs[0], now)
	got := store[u][0]
	if got.Score <= plain.Score {
		t.Fatalf("learned score %d should beat the default %d for a skill fit with another title", got.Score, plain.Score)
	}
	if last := got.Reasons[len(got.Reasons)-1]; last.Code != "weights.learned" || last.Params["n"] != "40" {
		t.Fatalf("reasons %+v", got.Reasons)
	}
}
