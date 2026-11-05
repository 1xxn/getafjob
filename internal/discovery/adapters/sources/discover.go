package sources

import (
	"context"
	"errors"
	"strings"

	"github.com/1xxn/getafjob/internal/discovery"
)

// guessSystems are the systems whose boards are tried by company name, most common first.
var guessSystems = []string{"greenhouse", "ashby", "lever", "recruitee", "workable", "personio"}

// maxProbes bounds the boards fetched for one company.
const maxProbes = 14

// Finder implements discovery.BoardFinder: boards named by the hits' links first, then boards guessed from the
// company name. A board counts only if it lists one of the hits (same MatchKey).
type Finder struct{ c *Client }

// NewFinder returns a finder on c; guessed boards are fetched without retries.
func NewFinder(c *Client) *Finder { return &Finder{c: c.Once()} }

var _ discovery.BoardFinder = (*Finder)(nil)

// FindBoard implements discovery.BoardFinder. err is set only when no board answered at all (network down).
func (f *Finder) FindBoard(ctx context.Context, cand discovery.Candidate) (discovery.Board, bool, error) {
	keys := cand.MatchKeys()
	answered := false
	var last error
	for _, b := range Probes(cand) {
		if err := ctx.Err(); err != nil {
			return discovery.Board{}, false, err
		}
		src, err := FromBoard(f.c, b)
		if err != nil {
			continue
		}
		batch, err := src.Fetch(ctx)
		if err != nil {
			last = err
			var se *discovery.SourceError
			if errors.As(err, &se) && (strings.HasPrefix(se.Code, "http_") || se.Code == "decode") {
				answered = true
			}
			continue
		}
		answered = true
		for _, p := range batch.Postings {
			if keys[discovery.MatchKey(cand.Company, p.Title)] {
				return discovery.Board{ATS: b.ATS, Slug: b.Slug, Company: b.Company, Config: b.Config}, true, nil
			}
		}
	}
	if !answered && last != nil {
		return discovery.Board{}, false, last
	}
	return discovery.Board{}, false, nil
}

// Probes lists the boards to try for a candidate: linked boards, then guessed slugs, without repeats and at most
// maxProbes. The company name of the hits becomes the board's name, so its jobs match the hits later.
func Probes(cand discovery.Candidate) []Board {
	var out []Board
	seen := map[string]bool{}
	add := func(b Board) {
		key := b.ATS + ":" + strings.ToLower(b.Slug)
		if seen[key] || len(out) >= maxProbes {
			return
		}
		if _, err := FromBoard(nil, b); err != nil {
			return
		}
		seen[key] = true
		out = append(out, b)
	}
	for _, h := range cand.Hits {
		for _, u := range []string{h.URL, h.ApplyURL} {
			if b, ok := BoardFromURL(u); ok {
				b.Company = cand.Company
				add(b)
			}
		}
	}
	words := strings.Fields(cand.Key)
	if len(words) == 0 {
		return out
	}
	slugs := []string{strings.Join(words, "")}
	if len(words) > 1 {
		slugs = append(slugs, strings.Join(words, "-"))
	}
	for _, slug := range slugs {
		for _, ats := range guessSystems {
			add(Board{ATS: ats, Slug: slug, Company: cand.Company})
		}
	}
	return out
}
