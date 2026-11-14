package matching

import (
	"encoding/json"
	"math"
	"os"
	"slices"
	"testing"
	"time"
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

// Floors of the current rules; raise them when the ranking gets better, never lower them.
const (
	minNDCG     = 0.86
	minPairwise = 0.72
)

func TestRankingQuality(t *testing.T) {
	b, err := os.ReadFile("testdata/ranking.json")
	if err != nil {
		t.Fatal(err)
	}
	var set rankingSet
	if err := json.Unmarshal(b, &set); err != nil {
		t.Fatal(err)
	}
	jobs := map[string]Job{}
	for i, j := range set.Jobs {
		jobs[j.ID] = Job{ID: int64(i + 1), Company: j.Company, Title: j.Title, Location: j.Location, Workplace: j.Workplace,
			Description: j.Description, FirstSeen: now.Add(-time.Duration(j.Days) * 24 * time.Hour)}
	}
	var sumNDCG, sumPairs float64
	for _, p := range set.Profiles {
		m := NewMatcher(p.Criteria)
		var ranked []graded
		for id, g := range p.Grades {
			j, ok := jobs[id]
			if !ok {
				t.Fatalf("%s: unknown job %s", p.Name, id)
			}
			r := m.Evaluate(j, now)
			key := r.Score
			if r.Decision == Skip {
				key = -1 // filtered out: below everything shown
			}
			ranked = append(ranked, graded{id: id, grade: g, key: key})
		}
		ndcg, pairs := ndcgAt(ranked, 10), pairwise(ranked)
		t.Logf("%-8s NDCG@10 %.3f  pairs %.3f", p.Name, ndcg, pairs)
		sumNDCG += ndcg
		sumPairs += pairs
	}
	n := float64(len(set.Profiles))
	t.Logf("mean     NDCG@10 %.3f  pairs %.3f", sumNDCG/n, sumPairs/n)
	if sumNDCG/n < minNDCG || sumPairs/n < minPairwise {
		t.Errorf("ranking got worse: NDCG@10 %.3f (floor %.3f), pairs %.3f (floor %.3f)", sumNDCG/n, minNDCG, sumPairs/n, minPairwise)
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
