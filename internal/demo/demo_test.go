package demo

import (
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/1xxn/getafjob/internal/discovery"
)

func TestPeopleAreValid(t *testing.T) {
	for _, p := range People() {
		if errs := p.Profile.Validate(); len(errs) > 0 {
			t.Errorf("%s profile: %v", p.Email, errs)
		}
		if errs := p.Preferences.Validate(); len(errs) > 0 {
			t.Errorf("%s preferences: %v", p.Email, errs)
		}
		if !strings.HasSuffix(p.Email, "@example.org") {
			t.Errorf("%s is not an example address", p.Email)
		}
	}
}

func TestPostingsAreStableAndInvented(t *testing.T) {
	now := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)
	a, b := Postings(200, 7, now), Postings(200, 7, now)
	if !slices.EqualFunc(a, b, func(x, y discovery.Posting) bool { return x.Title == y.Title && x.Description == y.Description }) {
		t.Fatal("same seed, different postings")
	}
	if Postings(1, 8, now)[0].ExternalID == a[0].ExternalID {
		t.Fatal("seeds share IDs")
	}
	for _, p := range a {
		if !strings.HasPrefix(p.URL, "https://jobs.example.org/") || p.Title == "" || p.Description == "" || p.Source != Source {
			t.Fatalf("posting %+v", p)
		}
	}
}

func TestPointAtUsesOnlyTheFixtureSite(t *testing.T) {
	base, _ := url.Parse("http://fixtures:8080")
	ps := Postings(20, 1, time.Now())
	PointAt(ps, base)
	seen := map[string]bool{}
	for _, p := range ps {
		u, err := url.Parse(p.URL)
		if err != nil || u.Host != "fixtures:8080" {
			t.Fatalf("url %q", p.URL)
		}
		seen[p.URL] = true
	}
	if len(seen) != len(ps) {
		t.Fatalf("urls must stay unique: %d of %d", len(seen), len(ps))
	}
}
