package discovery

import (
	"testing"
	"time"
)

func TestMatchKey(t *testing.T) {
	cases := []struct{ c1, t1, c2, t2 string }{
		{"Beispiel GmbH", "Platform Engineer (m/w/d)", "Beispiel", "Platform Engineer"},
		{"Example Cloud Inc.", "Site Reliability Engineer (all genders)", "example cloud", "site reliability engineer"},
		{"Zürich Versicherung AG", "DevOps-Engineer", "Zurich Versicherung", "DevOps Engineer"},
	}
	for _, c := range cases {
		if a, b := MatchKey(c.c1, c.t1), MatchKey(c.c2, c.t2); a != b {
			t.Errorf("MatchKey(%q,%q)=%q != MatchKey(%q,%q)=%q", c.c1, c.t1, a, c.c2, c.t2, b)
		}
	}
	if MatchKey("A", "Backend Engineer") == MatchKey("A", "Frontend Engineer") {
		t.Error("different titles share a key")
	}
}

func TestKeys(t *testing.T) {
	if Fingerprint("greenhouse:acme", "1") == Fingerprint("greenhouse:acme", "2") || len(Fingerprint("a", "b")) != 40 {
		t.Error("Fingerprint")
	}
	if ContentHash("T", "L", "D") != ContentHash(" T ", "L", "D ") || ContentHash("T", "L", "D") == ContentHash("T", "L", "E") {
		t.Error("ContentHash")
	}
	if Fold("  Ärztin   in   München ") != "arztin in munchen" {
		t.Errorf("Fold = %q", Fold("  Ärztin   in   München "))
	}
	if !IsAggregator("arbeitsagentur") || !IsAggregator("portal:linkedin") || IsAggregator("greenhouse:acme") {
		t.Error("IsAggregator")
	}
}

func TestShouldClose(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	runs := map[string]SourceRun{
		"greenhouse:acme": {Name: "greenhouse:acme", OK: true, Complete: true},
		"arbeitsagentur":  {Name: "arbeitsagentur", OK: true, Complete: false},
		"lever:down":      {Name: "lever:down", OK: false},
	}
	listed := map[string]bool{"seen": true}
	cases := []struct {
		name string
		job  OpenJob
		want bool
	}{
		{"listed again", OpenJob{Fingerprint: "seen", Source: "greenhouse:acme"}, false},
		{"missing from complete board", OpenJob{Fingerprint: "x", Source: "greenhouse:acme", LastSeen: now}, true},
		{"missing from capped search, recent", OpenJob{Fingerprint: "x", Source: "arbeitsagentur", LastSeen: now.Add(-20 * 24 * time.Hour)}, false},
		{"missing from capped search, stale", OpenJob{Fingerprint: "x", Source: "arbeitsagentur", LastSeen: now.Add(-StaleAfter)}, true},
		{"failing source", OpenJob{Fingerprint: "x", Source: "lever:down", LastSeen: now.Add(-60 * 24 * time.Hour)}, false},
		{"source not crawled", OpenJob{Fingerprint: "x", Source: "ashby:other", LastSeen: now.Add(-60 * 24 * time.Hour)}, false},
	}
	for _, c := range cases {
		if got := ShouldClose(c.job, runs, listed, now); got != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
	}
}

func FuzzMatchKey(f *testing.F) {
	f.Add("Beispiel GmbH", "Platform Engineer (m/w/d)")
	f.Fuzz(func(t *testing.T, company, title string) {
		k := MatchKey(company, title)
		if k != MatchKey(company, title) {
			t.Fatal("not deterministic")
		}
		for _, r := range k {
			if r > 127 {
				t.Fatalf("non-ASCII in key %q", k)
			}
		}
	})
}
