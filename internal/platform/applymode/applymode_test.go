package applymode

import "testing"

func TestOf(t *testing.T) {
	cases := []struct {
		page, apply string
		want        Mode
	}{
		{"https://boards.greenhouse.io/acme/jobs/1", "", Form},
		{"https://www.arbeitsagentur.de/jobsuche/jobdetail/10000-1", "", Portal},
		{"https://www.arbeitsagentur.de/jobsuche/jobdetail/10000-1", "https://jobs.lever.co/acme/1", Form},
		{"https://www.arbeitnow.com/jobs/companies/acme/go-dev", "", Portal},
		{"https://himalayas.app/companies/acme/jobs/go", "https://himalayas.app/companies/acme/jobs/go/apply", Portal},
		{"https://example.org/jobs/1", "mailto:jobs@example.org", Email},
		{"https://example.org/jobs/1", " MAILTO:Jobs@Example.org ", Email},
		{"https://www.linkedin.com/jobs/view/1", "", Form}, // Easy Apply belongs to the portals module
		{"https://notarbeitsagentur.de/x", "", Form},
		{"", "", Form},
	}
	for _, c := range cases {
		if got := Of(c.page, c.apply); got != c.want {
			t.Errorf("Of(%q, %q) = %s, want %s", c.page, c.apply, got, c.want)
		}
	}
}
