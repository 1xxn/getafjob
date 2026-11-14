package matching

import (
	"os"
	"testing"

	"github.com/1xxn/getafjob/internal/matching/embed"
)

// TestRankingFullModel checks that the token subset in testdata/embed embeds the ranking set exactly like the full
// model and logs the full model's ranking. It needs the model files, so it only runs with GETAFJOB_EMBED_FULL set.
func TestRankingFullModel(t *testing.T) {
	dir := os.Getenv("GETAFJOB_EMBED_FULL")
	if dir == "" {
		t.Skip("GETAFJOB_EMBED_FULL not set")
	}
	full, err := embed.Load(dir, rankingModelDims)
	if err != nil {
		t.Fatal(err)
	}
	sub, err := embed.Load("testdata/embed", 0)
	if err != nil {
		t.Fatal(err)
	}
	set := loadRankingSet(t)
	var texts []string
	for _, j := range set.Jobs {
		texts = append(texts, embedText(Job{Title: j.Title, Description: j.Description}))
	}
	for _, p := range set.Profiles {
		texts = append(texts, ProfileText(p.Criteria))
	}
	for _, s := range texts {
		a, _ := full.Embed(s)
		b, _ := sub.Embed(s)
		for i := range a {
			if a[i] != b[i] {
				t.Fatalf("subset differs for %.40q: run TestWriteRankingModel", s)
			}
		}
	}
	rank(t, set, full)
}
