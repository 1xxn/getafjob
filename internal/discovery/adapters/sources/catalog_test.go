package sources_test

import (
	"testing"

	"github.com/1xxn/getafjob/internal/discovery"
	"github.com/1xxn/getafjob/internal/discovery/adapters/sources"
)

func TestForGermany(t *testing.T) {
	cases := []struct {
		location string
		wp       discovery.Workplace
		want     bool
	}{
		{"Berlin", "", true},
		{"München, Bayern", "", true},
		{"Germany, Bavaria, Munich", "", true},
		{"Berlin,Germany", "", true},
		{"Köln; Düsseldorf", "", true},
		{"Frankfurt am Main, DE", "", true},
		{"Remote", "", true},
		{"Remote (Europe)", "", true},
		{"Remote - EMEA", "", true},
		{"Anywhere", discovery.WorkplaceRemote, true},
		{"Remote - US", "", false},
		{"Remote, Spain", "", false},
		{"Poland (Remote)", "", false},
		{"Remote-first", "", true},
		{"", discovery.WorkplaceRemote, true},
		{"Remote, Europe", "", true},
		{"EMEA", discovery.WorkplaceRemote, true},
		{"Remote, Canada", "", false},
		{"San Francisco, CA", "", false},
		{"London, UK", "", false},
		{"Dublin, Ireland", discovery.WorkplaceHybrid, false},
		{"Delaware", "", false},
	}
	for _, c := range cases {
		if got := sources.ForGermany(discovery.Posting{Location: c.location, Workplace: c.wp}); got != c.want {
			t.Errorf("ForGermany(%q, %q) = %v, want %v", c.location, c.wp, got, c.want)
		}
	}
}

func TestCatalogIsValid(t *testing.T) {
	boards, err := sources.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	c := sources.NewClient(nil)
	seen := map[string]bool{}
	for _, b := range boards {
		if _, err := sources.FromBoard(c, b); err != nil {
			t.Error(err)
		}
		if b.Company == "" {
			t.Errorf("%s %s: no company name", b.ATS, b.Slug)
		}
		key := b.ATS + ":" + b.Slug
		if seen[key] {
			t.Errorf("duplicate %s", key)
		}
		seen[key] = true
	}
}
