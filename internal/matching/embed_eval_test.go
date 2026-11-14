package matching

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/1xxn/getafjob/internal/matching/embed"
)

// TestEmbeddingEval measures static embedding models on the ranking fixtures: cosine alone and blended with the
// rules, load time, memory and time per job. It needs real model files, so it only runs with
// GETAFJOB_EMBED_MODELS=<dir with one folder per model> (never in CI).
func TestEmbeddingEval(t *testing.T) {
	root := os.Getenv("GETAFJOB_EMBED_MODELS")
	if root == "" {
		t.Skip("GETAFJOB_EMBED_MODELS not set")
	}
	b, err := os.ReadFile("testdata/ranking.json")
	if err != nil {
		t.Fatal(err)
	}
	var set rankingSet
	if err := json.Unmarshal(b, &set); err != nil {
		t.Fatal(err)
	}
	dirs, _ := os.ReadDir(root)
	for _, d := range dirs {
		for _, dims := range []int{0, 256, 128} {
			runtime.GC()
			var before runtime.MemStats
			runtime.ReadMemStats(&before)
			start := time.Now()
			m, err := embed.Load(filepath.Join(root, d.Name()), dims)
			if err != nil {
				t.Fatalf("%s: %v", d.Name(), err)
			}
			load := time.Since(start)
			runtime.GC()
			var after runtime.MemStats
			runtime.ReadMemStats(&after)
			if dims > 0 && dims >= m.Dim() && dims != m.Dim() {
				continue
			}

			jobText := map[string]string{}
			jobs := map[string]Job{}
			for i, j := range set.Jobs {
				jobText[j.ID] = j.Title + ". " + j.Description
				jobs[j.ID] = Job{ID: int64(i + 1), Title: j.Title, Location: j.Location, Workplace: j.Workplace,
					Description: j.Description, FirstSeen: now.Add(-time.Duration(j.Days) * 24 * time.Hour)}
			}
			start = time.Now()
			vecs := map[string][]float32{}
			for id, s := range jobText {
				vecs[id], _ = m.Embed(s)
			}
			perJob := time.Since(start) / time.Duration(len(jobText))

			// w is the weight of the cosine (1 = cosine alone); gated keeps the hard filters except "no title".
			for _, v := range []struct {
				w     float64
				gated bool
			}{{1, false}, {0, false}, {1, true}, {0.7, true}, {0.5, true}} {
				w := v.w
				var nd, pr float64
				for _, p := range set.Profiles {
					c := p.Criteria
					pv, _ := m.Embed(strings.Join(c.Titles, ", ") + ". " + strings.Join(c.Skills, ", "))
					mt := NewMatcher(c)
					var ranked []graded
					for id, g := range p.Grades {
						r := mt.Evaluate(jobs[id], now)
						cos := float64(embed.Cosine(pv, vecs[id]))
						rules := 0
						for _, rs := range r.Reasons {
							if rs.Code != "title.none" {
								rules += rs.Points
							}
						}
						key := w*cos*100 + (1-w)*float64(rules)
						blocked := false
						for _, rs := range r.Reasons {
							blocked = blocked || rs.Hard && rs.Code != "title.none"
						}
						switch {
						case !v.gated && w < 1 && r.Decision == Skip, v.gated && blocked:
							key = -1
						}
						ranked = append(ranked, graded{id: id, grade: g, key: int(key * 10)})
					}
					nd += ndcgAt(ranked, 10)
					pr += pairwise(ranked)
				}
				n := float64(len(set.Profiles))
				t.Logf("%-12s dims %4d  w %.1f gated %-5v NDCG@10 %.3f  pairs %.3f  | load %v  heap %d MB  %v/job",
					d.Name(), m.Dim(), w, v.gated, nd/n, pr/n, load.Round(time.Millisecond),
					(after.HeapAlloc-before.HeapAlloc)>>20, perJob.Round(time.Microsecond))
			}
		}
	}
}
