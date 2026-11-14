package matching

import (
	"encoding/json"
	"math"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/1xxn/getafjob/internal/matching/embed"
)

// rankingSet is a fixed set of fictional postings with graded relevance (0 to 3) per profile. It measures how well
// the rules order jobs, so a rule change cannot quietly make the ranking worse (#485).
type rankingSet struct {
	Profiles []struct {
		Name     string         `json:"name"`
		Criteria Criteria       `json:"criteria"`
		Grades   map[string]int `json:"grades"`
	} `json:"profiles"`
	Jobs []struct {
		ID          string `json:"id"`
		Title       string `json:"title"`
		Company     string `json:"company"`
		Location    string `json:"location"`
		Workplace   string `json:"workplace"`
		Description string `json:"description"`
		Days        int    `json:"days"`
	} `json:"jobs"`
}

// Floors of the ranking with the model (the token subset in testdata/embed) and of the rules alone; raise them
// when the ranking gets better, never lower them.
const (
	minNDCGModel     = 0.96
	minPairsModel    = 0.88
	minNDCGRules     = 0.91
	minPairsRules    = 0.76
	rankingModelDims = 256
)

func loadRankingSet(t *testing.T) rankingSet {
	t.Helper()
	b, err := os.ReadFile("testdata/ranking.json")
	if err != nil {
		t.Fatal(err)
	}
	var set rankingSet
	if err := json.Unmarshal(b, &set); err != nil {
		t.Fatal(err)
	}
	return set
}

func (set rankingSet) jobs() map[string]Job {
	jobs := map[string]Job{}
	for i, j := range set.Jobs {
		jobs[j.ID] = Job{ID: int64(i + 1), Company: j.Company, Title: j.Title, Location: j.Location, Workplace: j.Workplace,
			Description: j.Description, FirstSeen: now.Add(-time.Duration(j.Days) * 24 * time.Hour)}
	}
	return jobs
}

func TestRankingQuality(t *testing.T) {
	set := loadRankingSet(t)
	model, err := embed.Load("testdata/embed", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []struct {
		name            string
		emb             Embedder
		minNDCG, minPrs float64
	}{{"model", model, minNDCGModel, minPairsModel}, {"rules", nil, minNDCGRules, minPairsRules}} {
		t.Run(v.name, func(t *testing.T) {
			ndcg, pairs := rank(t, set, v.emb)
			if ndcg < v.minNDCG || pairs < v.minPrs {
				t.Errorf("ranking got worse: NDCG@10 %.3f (floor %.3f), pairs %.3f (floor %.3f)", ndcg, v.minNDCG, pairs, v.minPrs)
			}
		})
	}
}

// rank scores the ranking set with emb (nil: rules alone) and returns the mean NDCG@10 and pairwise accuracy.
func rank(t *testing.T, set rankingSet, emb Embedder) (ndcgMean, pairsMean float64) {
	t.Helper()
	jobs := set.jobs()
	prepared := map[string]*Prepared{}
	for id, j := range jobs {
		prepared[id] = Prepare(j, emb)
	}
	for _, p := range set.Profiles {
		m := NewMatcher(p.Criteria, emb)
		var ranked []graded
		for id, g := range p.Grades {
			pj, ok := prepared[id]
			if !ok {
				t.Fatalf("%s: unknown job %s", p.Name, id)
			}
			r := m.EvaluatePrepared(pj, now)
			key := r.Score
			if r.Decision == Skip {
				key = -1 // filtered out: below everything shown
			}
			ranked = append(ranked, graded{id: id, grade: g, key: key})
		}
		ndcg, pairs := ndcgAt(ranked, 10), pairwise(ranked)
		t.Logf("%-8s NDCG@10 %.3f  pairs %.3f", p.Name, ndcg, pairs)
		ndcgMean += ndcg
		pairsMean += pairs
	}
	n := float64(len(set.Profiles))
	t.Logf("mean     NDCG@10 %.3f  pairs %.3f", ndcgMean/n, pairsMean/n)
	return ndcgMean / n, pairsMean / n
}

// TestWriteRankingModel rebuilds testdata/embed from the full model: only the tokens of the ranking set, so the
// tests rank with the real vectors. Run it after changing the ranking set or the texts that are embedded:
// GETAFJOB_EMBED_FULL=<dir with tokenizer.json and model.safetensors> go test -run TestWriteRankingModel.
func TestWriteRankingModel(t *testing.T) {
	full := os.Getenv("GETAFJOB_EMBED_FULL")
	if full == "" {
		t.Skip("GETAFJOB_EMBED_FULL not set")
	}
	set := loadRankingSet(t)
	var texts []string
	for _, j := range set.Jobs {
		texts = append(texts, embedText(Job{Title: j.Title, Description: j.Description}))
	}
	for _, p := range set.Profiles {
		texts = append(texts, ProfileText(p.Criteria))
	}
	if err := embed.Subset(full, "testdata/embed", texts, rankingModelDims); err != nil {
		t.Fatal(err)
	}
}

// graded is one job of a profile: its relevance and the key the rules sort it by.
type graded struct {
	id    string
	grade int
	key   int
}

// ndcgAt is the normalized discounted cumulative gain of the first k jobs in the rules' order. Equal keys sort the
// worse job first, so ties never earn credit.
func ndcgAt(jobs []graded, k int) float64 {
	byRules := slices.Clone(jobs)
	slices.SortFunc(byRules, func(a, b graded) int {
		if a.key != b.key {
			return b.key - a.key
		}
		return a.grade - b.grade
	})
	ideal := slices.Clone(jobs)
	slices.SortFunc(ideal, func(a, b graded) int { return b.grade - a.grade })
	if best := dcg(ideal, k); best > 0 {
		return dcg(byRules, k) / best
	}
	return 1
}

func dcg(jobs []graded, k int) float64 {
	var s float64
	for i, j := range jobs[:min(k, len(jobs))] {
		s += (math.Pow(2, float64(j.grade)) - 1) / math.Log2(float64(i)+2)
	}
	return s
}

// pairwise is the share of job pairs with different grades that the rules order strictly right.
func pairwise(jobs []graded) float64 {
	var right, all int
	for i, a := range jobs {
		for _, b := range jobs[i+1:] {
			if a.grade == b.grade {
				continue
			}
			all++
			if (a.grade > b.grade) == (a.key > b.key) && a.key != b.key {
				right++
			}
		}
	}
	if all == 0 {
		return 1
	}
	return float64(right) / float64(all)
}
