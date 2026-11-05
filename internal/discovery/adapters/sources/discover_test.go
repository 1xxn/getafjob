package sources_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/1xxn/getafjob/internal/discovery"
	"github.com/1xxn/getafjob/internal/discovery/adapters/sources"
)

func candidate(company string, hits ...discovery.Hit) discovery.Candidate {
	for i := range hits {
		hits[i].Company = company
		hits[i].MatchKey = discovery.MatchKey(company, hits[i].Title)
	}
	return discovery.Candidate{Key: discovery.CompanyKey(company), Company: company, Hits: hits}
}

func TestProbes(t *testing.T) {
	c := candidate("Beispiel Labs GmbH",
		discovery.Hit{Title: "Go Developer", URL: "https://www.arbeitnow.com/jobs/x", ApplyURL: "https://boards.greenhouse.io/Beispiel/jobs/1"},
		discovery.Hit{Title: "Designer", URL: "https://jobs.lever.co/beispiel/2", ApplyURL: "https://job-boards.greenhouse.io/beispiel/jobs/3"})
	var got []string
	for _, b := range sources.Probes(c) {
		if b.Company != "Beispiel Labs GmbH" {
			t.Errorf("company = %q", b.Company)
		}
		got = append(got, b.ATS+":"+b.Slug)
	}
	want := "greenhouse:Beispiel lever:beispiel " +
		"greenhouse:beispiellabs ashby:beispiellabs lever:beispiellabs recruitee:beispiellabs workable:beispiellabs personio:beispiellabs " +
		"greenhouse:beispiel-labs ashby:beispiel-labs lever:beispiel-labs recruitee:beispiel-labs workable:beispiel-labs personio:beispiel-labs"
	if strings.Join(got, " ") != want {
		t.Errorf("probes = %v", got)
	}
	if one := sources.Probes(candidate("Muster")); len(one) != 6 || one[0].Slug != "muster" {
		t.Errorf("single word: %v", one)
	}
}

func TestFindBoard(t *testing.T) {
	c := candidate("Beispiel Labs GmbH",
		discovery.Hit{Title: "Go Developer (m/w/d)", URL: "https://www.arbeitnow.com/jobs/x", ApplyURL: "https://boards.greenhouse.io/beispiel/jobs/1"})

	cl, r := client(t, "ashby", map[string][]string{
		"GET https://api.ashbyhq.com/posting-api/job-board/beispiellabs": {"board.json"},
	})
	b, ok, err := sources.NewFinder(cl).FindBoard(t.Context(), c)
	if err != nil || !ok || b.ATS != "ashby" || b.Slug != "beispiellabs" || b.Company != "Beispiel Labs GmbH" {
		t.Fatalf("board = %+v, %v, %v", b, ok, err)
	}
	if len(r.seen) != 3 || !strings.Contains(r.seen[0], "/boards/beispiel/") {
		t.Errorf("requests = %v", r.seen) // linked board first, retried never
	}

	// The board answers but lists none of the hits.
	other := candidate("Beispiel Labs GmbH", discovery.Hit{Title: "Chef"})
	cl, _ = client(t, "ashby", map[string][]string{
		"GET https://api.ashbyhq.com/posting-api/job-board/beispiellabs": {"board.json"},
	})
	if b, ok, err := sources.NewFinder(cl).FindBoard(t.Context(), other); ok || err != nil {
		t.Errorf("no match: %+v, %v, %v", b, ok, err)
	}

	// Nothing answers at all: inconclusive.
	down := sources.NewClient(&http.Client{Transport: failing{}}).WithoutWaiting()
	if _, ok, err := sources.NewFinder(down).FindBoard(t.Context(), c); ok || err == nil {
		t.Errorf("network down: %v, %v", ok, err)
	}
}

type failing struct{}

func (failing) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("dial failed")
}
