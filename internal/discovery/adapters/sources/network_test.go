package sources_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/1xxn/getafjob/internal/discovery"
	"github.com/1xxn/getafjob/internal/discovery/adapters/sources"
	"github.com/1xxn/getafjob/internal/platform/netguard"
)

// TestNetwork reads one public board per source from the real APIs: read-only, one source at a time, opt-in with
// GETAFJOB_NETWORK_TESTS=1 (nightly). It catches API changes the recorded responses cannot show.
func TestNetwork(t *testing.T) {
	if os.Getenv("GETAFJOB_NETWORK_TESTS") != "1" {
		t.Skip("set GETAFJOB_NETWORK_TESTS=1 to read the real APIs")
	}
	c := sources.NewClient(netguard.NewClient(netguard.ClientOptions{Timeout: 60 * time.Second}))
	cases := map[string]func(*testing.T) discovery.Source{
		"greenhouse": func(t *testing.T) discovery.Source { return board(t, c, "greenhouse", "gitlab", "", "") },
		"ashby":      func(t *testing.T) discovery.Source { return board(t, c, "ashby", "linear", "", "") },
		"lever":      func(t *testing.T) discovery.Source { return board(t, c, "lever", "palantir", "", "") },
		"smartrecruiters": func(t *testing.T) discovery.Source {
			return board(t, c, "smartrecruiters", "BoschGroup", "", `{"max":100}`)
		},
		"workday": func(t *testing.T) discovery.Source {
			return board(t, c, "workday", "nvidia", "NVIDIA", `{"host":"nvidia.wd5.myworkdayjobs.com","site":"NVIDIAExternalCareerSite","max":40}`)
		},
		"arbeitnow": func(*testing.T) discovery.Source { return sources.NewArbeitnow(c, 1) },
		"himalayas": func(*testing.T) discovery.Source {
			s := sources.NewHimalayas(c, []string{"golang"}, "Germany")
			s.MaxPages = 1
			return s
		},
		"arbeitsagentur": func(*testing.T) discovery.Source {
			s := sources.NewArbeitsagentur(c, []string{"Softwareentwickler"}, []string{"Berlin"})
			s.MaxPages = 1
			return s
		},
		"amazon": func(*testing.T) discovery.Source {
			s := sources.NewAmazon(c, []string{"software"}, "DEU")
			s.MaxPerQuery = 100
			return s
		},
	}
	if slug := os.Getenv("GETAFJOB_NETWORK_PERSONIO"); slug != "" {
		cases["personio"] = func(t *testing.T) discovery.Source { return board(t, c, "personio", slug, "", "") }
	}
	if slug := os.Getenv("GETAFJOB_NETWORK_RECRUITEE"); slug != "" {
		cases["recruitee"] = func(t *testing.T) discovery.Source { return board(t, c, "recruitee", slug, "", "") }
	}
	if slug := os.Getenv("GETAFJOB_NETWORK_WORKABLE"); slug != "" {
		cases["workable"] = func(t *testing.T) discovery.Source { return board(t, c, "workable", slug, "", "") }
	}
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			src := mk(t)
			b, err := src.Fetch(ctx)
			if err != nil {
				t.Fatalf("fetch: %v", err)
			}
			valid := 0
			for _, p := range b.Postings {
				if discovery.ValidPosting(p) {
					valid++
				}
			}
			if valid == 0 {
				t.Fatalf("no valid postings among %d", len(b.Postings))
			}
			t.Logf("%d postings (%d valid), complete %v", len(b.Postings), valid, b.Complete)
			if d, ok := src.(discovery.Detailer); ok {
				p := b.Postings[0]
				if err := d.Details(ctx, &p); err != nil || p.Description == "" {
					t.Fatalf("details: %v (description %d bytes)", err, len(p.Description))
				}
			}
		})
	}
}
