package sources_test

import (
	"strings"
	"testing"

	"github.com/1xxn/getafjob/internal/discovery/adapters/sources"
)

func page(blocks ...string) []byte {
	var b strings.Builder
	b.WriteString("<html><head>")
	for _, x := range blocks {
		b.WriteString(`<script type="application/ld+json">` + x + `</script>`)
	}
	b.WriteString("</head><body></body></html>")
	return []byte(b.String())
}

func TestJobPostings(t *testing.T) {
	cases := []struct {
		name   string
		page   []byte
		titles string
	}{
		{"single", page(`{"@type":"JobPosting","title":"Go Developer"}`), "Go Developer"},
		{"array and graph", page(`[{"@type":"JobPosting","title":"A"},{"@graph":[{"@type":"Organization"},{"@type":["Thing","JobPosting"],"title":"B"}]}]`), "A|B"},
		{"full type URL", page(`{"@type":"http://schema.org/JobPosting","title":"C"}`), "C"},
		{"broken block skipped", page(`{"@type":"JobPosting",`, `{"@type":"JobPosting","title":"D"}`), "D"},
		{"no title", page(`{"@type":"JobPosting","description":"x"}`), ""},
		{"other script ignored", []byte(`<script>{"@type":"JobPosting","title":"E"}</script>`), ""},
		{"title as list", page(`{"@type":"JobPosting","title":["F","ignored"]}`), "F"},
	}
	for _, c := range cases {
		var got []string
		for _, j := range sources.JobPostings(c.page) {
			got = append(got, j.Title)
		}
		if strings.Join(got, "|") != c.titles {
			t.Errorf("%s: titles = %q, want %q", c.name, strings.Join(got, "|"), c.titles)
		}
	}
	j := sources.JobPostings(page(`{"@type":"JobPosting","title":"Remote SRE","identifier":{"@type":"PropertyValue","name":"Muster","value":"R-7"},
		"jobLocationType":"TELECOMMUTE","applicantLocationRequirements":{"@type":"Country","name":"Germany"},
		"jobLocation":{"@type":"Place","address":"Berlin"},"hiringOrganization":"Muster GmbH","datePosted":"2026-11-02T08:00:00+01:00"}`))
	if len(j) != 1 || j[0].ID != "R-7" || !j[0].Remote || strings.Join(j[0].Locations, "; ") != "Berlin; Remote (Germany)" ||
		j[0].Company != "Muster GmbH" || j[0].Posted != "2026-11-02" {
		t.Fatalf("posting = %+v", j)
	}
}

func TestJobPostingsLimits(t *testing.T) {
	deep := strings.Repeat(`{"@graph":[`, 100) + `{"@type":"JobPosting","title":"deep"}` + strings.Repeat(`]}`, 100)
	if got := sources.JobPostings(page(deep)); len(got) != 0 {
		t.Fatalf("nesting beyond the limit was read: %d", len(got))
	}
	many := "[" + strings.Repeat(`{"@type":"JobPosting","title":"x"},`, 1500) + `{"@type":"JobPosting","title":"x"}]`
	if got := sources.JobPostings(page(many)); len(got) != 1000 {
		t.Fatalf("got %d postings, want the cap of 1000", len(got))
	}
}

func TestBoardFromURL(t *testing.T) {
	cases := map[string]string{
		"https://boards.greenhouse.io/acme/jobs/4000001":                         "greenhouse:acme",
		"https://job-boards.eu.greenhouse.io/acme-gmbh":                          "greenhouse:acme-gmbh",
		"https://boards.greenhouse.io/embed/job_board?for=acme&b=https://x":      "greenhouse:acme",
		"https://boards-api.greenhouse.io/v1/boards/acme/jobs":                   "greenhouse:acme",
		"https://jobs.ashbyhq.com/beispiel/1b6c4a0e-0000":                        "ashby:beispiel",
		"https://jobs.eu.lever.co/musterfirma/aa00-1/apply":                      "lever:musterfirma",
		"https://jobs.smartrecruiters.com/Musterwerk/744000000000001":            "smartrecruiters:Musterwerk",
		"https://musterhaus.jobs.personio.de/job/1200001":                        "personio:musterhaus",
		"https://beispielbau.recruitee.com/o/bauleiter":                          "recruitee:beispielbau",
		"https://apply.workable.com/musterlabs/j/A1B2C3":                         "workable:musterlabs",
		"https://muster.wd3.myworkdayjobs.com/en-US/External/job/Berlin/Dev_R-1": "workday:muster",
		"https://apply.workable.com/j/A1B2C3":                                    "",
		"https://boards.greenhouse.io/embed/job_app?token=1":                     "",
		"https://www.jobs.personio.de/":                                          "",
		"https://evil.example.org/boards.greenhouse.io/acme":                     "",
		"https://boards.greenhouse.io.evil.example.org/acme":                     "",
		"javascript:alert(1)":                                                    "",
		"https://muster.wd3.myworkdayjobs.com/wday/cxs/muster/External/jobs":     "",
	}
	for raw, want := range cases {
		b, ok := sources.BoardFromURL(raw)
		got := ""
		if ok {
			got = b.ATS + ":" + b.Slug
		}
		if got != want {
			t.Errorf("BoardFromURL(%q) = %q, want %q", raw, got, want)
		}
	}
	b, _ := sources.BoardFromURL("https://muster.wd3.myworkdayjobs.com/en-US/External/job/Berlin/Dev_R-1")
	if string(b.Config) != `{"host":"muster.wd3.myworkdayjobs.com","site":"External"}` {
		t.Fatalf("workday config = %s", b.Config)
	}
}

func TestDetectBoards(t *testing.T) {
	html := `<html><body>
		<a href="https://boards.greenhouse.io/acme">Jobs</a>
		<a href="https://boards.greenhouse.io/acme/jobs/1">Job</a>
		<iframe src="https://jobs.ashbyhq.com/acme/embed"></iframe>
		<script>var board = "https://acme.recruitee.com/api/offers/";</script>
		<a href="https://example.org/careers">Other</a>
	</body></html>`
	var got []string
	for _, b := range sources.DetectBoards([]byte(html)) {
		got = append(got, b.ATS+":"+b.Slug)
	}
	if strings.Join(got, " ") != "greenhouse:acme ashby:acme recruitee:acme" {
		t.Fatalf("boards = %v", got)
	}
}

func FuzzJobPostings(f *testing.F) {
	f.Add(page(`{"@type":"JobPosting","title":"Go","jobLocation":[{"address":{"addressLocality":"Berlin"}}]}`))
	f.Add([]byte(`<script type="application/ld+json">[[[[{"@graph":{"@type":"JobPosting","title":1}}]]]]</script>`))
	f.Fuzz(func(t *testing.T, b []byte) {
		for _, j := range sources.JobPostings(b) {
			if j.Title == "" || len(j.Title) > 500 {
				t.Fatalf("title %q", j.Title)
			}
		}
		for _, bd := range sources.DetectBoards(b) {
			if _, err := sources.FromBoard(nil, bd); err != nil {
				t.Fatalf("detected invalid board %+v: %v", bd, err)
			}
		}
	})
}
