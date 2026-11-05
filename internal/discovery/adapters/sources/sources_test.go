package sources_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/1xxn/getafjob/internal/discovery"
	"github.com/1xxn/getafjob/internal/discovery/adapters/sources"
)

var update = flag.Bool("update", false, "rewrite golden files")

// replay answers requests from recorded (fictional) responses: "METHOD URL" → files served in order. Unknown
// requests get 404.
type replay struct {
	t      *testing.T
	dir    string
	mu     sync.Mutex
	routes map[string][]string
	seen   []string
}

func (r *replay) RoundTrip(req *http.Request) (*http.Response, error) {
	if ua := req.Header.Get("User-Agent"); !strings.HasPrefix(ua, "getafjob/") {
		r.t.Errorf("User-Agent = %q", ua)
	}
	key := req.Method + " " + req.URL.String()
	r.mu.Lock()
	r.seen = append(r.seen, key)
	files := r.routes[key]
	var file string
	if len(files) > 0 {
		file, r.routes[key] = files[0], files[1:]
	}
	r.mu.Unlock()
	if file == "" {
		return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
	}
	if strings.HasPrefix(file, "status:") {
		code := map[string]int{"status:429": 429, "status:500": 500}[file]
		return &http.Response{StatusCode: code, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
	}
	b, err := os.ReadFile(filepath.Join(r.dir, file))
	if err != nil {
		r.t.Fatal(err)
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(b)), Request: req}, nil
}

func client(t *testing.T, dir string, routes map[string][]string) (*sources.Client, *replay) {
	t.Helper()
	r := &replay{t: t, dir: filepath.Join("testdata", dir), routes: routes}
	return sources.NewClient(&http.Client{Transport: r}).WithoutWaiting(), r
}

// result is what a golden file holds: the batch and, for sources with details, the postings after Details.
type result struct {
	Complete bool                `json:"complete"`
	Postings []discovery.Posting `json:"postings"`
}

func golden(t *testing.T, dir string, got result) {
	t.Helper()
	b, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	b = append(b, '\n')
	path := filepath.Join("testdata", dir, "golden.json")
	if *update {
		if err := os.WriteFile(path, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update)", err)
	}
	if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), b) {
		t.Errorf("%s differs from the golden file (run with -update and review the diff):\n%s", dir, b)
	}
}

func run(t *testing.T, src discovery.Source) result {
	t.Helper()
	ctx := context.Background()
	batch, err := src.Fetch(ctx)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if d, ok := src.(discovery.Detailer); ok {
		for i := range batch.Postings {
			if !discovery.ValidStub(batch.Postings[i]) && batch.Postings[i].Stub {
				t.Errorf("invalid stub %+v", batch.Postings[i])
			}
			if err := d.Details(ctx, &batch.Postings[i]); err != nil {
				t.Fatalf("details %s: %v", batch.Postings[i].ExternalID, err)
			}
			batch.Postings[i].Stub = false // as the crawl does after details
		}
	}
	for _, p := range batch.Postings {
		if !discovery.ValidPosting(p) || p.Source != src.Name() {
			t.Errorf("invalid posting %+v", p)
		}
	}
	return result{Complete: batch.Complete, Postings: batch.Postings}
}

func board(t *testing.T, c *sources.Client, ats, slug, company, cfg string) discovery.Source {
	t.Helper()
	b := sources.Board{ATS: ats, Slug: slug, Company: company}
	if cfg != "" {
		b.Config = json.RawMessage(cfg)
	}
	src, err := sources.FromBoard(c, b)
	if err != nil {
		t.Fatal(err)
	}
	return src
}

func TestSources(t *testing.T) {
	type tc struct {
		dir    string
		routes map[string][]string
		source func(*testing.T, *sources.Client) discovery.Source
	}
	cases := []tc{
		{"greenhouse", map[string][]string{
			"GET https://boards-api.greenhouse.io/v1/boards/acme":                   {"board.json"},
			"GET https://boards-api.greenhouse.io/v1/boards/acme/jobs?content=true": {"jobs.json"},
		}, func(t *testing.T, c *sources.Client) discovery.Source {
			return board(t, c, "greenhouse", "acme", "", "")
		}},
		{"ashby", map[string][]string{
			"GET https://api.ashbyhq.com/posting-api/job-board/beispiel": {"board.json"},
		}, func(t *testing.T, c *sources.Client) discovery.Source {
			return board(t, c, "ashby", "beispiel", "Beispiel Labs", "")
		}},
		{"lever", map[string][]string{
			// the global host does not know the board (404), the EU host does
			"GET https://api.eu.lever.co/v0/postings/musterfirma?mode=json": {"postings.json"},
		}, func(t *testing.T, c *sources.Client) discovery.Source {
			return board(t, c, "lever", "musterfirma", "", "")
		}},
		{"smartrecruiters", map[string][]string{
			"GET https://api.smartrecruiters.com/v1/companies/Musterwerk/postings?limit=100&offset=0": {"page1.json"},
			"GET https://api.smartrecruiters.com/v1/companies/Musterwerk/postings?limit=100&offset=2": {"page2.json"},
			"GET https://api.smartrecruiters.com/v1/companies/Musterwerk/postings/744000000000001":    {"detail1.json"},
			"GET https://api.smartrecruiters.com/v1/companies/Musterwerk/postings/744000000000002":    {"detail2.json"},
			"GET https://api.smartrecruiters.com/v1/companies/Musterwerk/postings/744000000000003":    {"detail1.json"},
		}, func(t *testing.T, c *sources.Client) discovery.Source {
			return board(t, c, "smartrecruiters", "Musterwerk", "", "")
		}},
		{"personio", map[string][]string{
			"GET https://musterhaus.jobs.personio.de/xml?language=de": {"feed.xml"},
		}, func(t *testing.T, c *sources.Client) discovery.Source {
			return board(t, c, "personio", "musterhaus", "", "")
		}},
		{"recruitee", map[string][]string{
			"GET https://beispielbau.recruitee.com/api/offers/": {"offers.json"},
		}, func(t *testing.T, c *sources.Client) discovery.Source {
			return board(t, c, "recruitee", "beispielbau", "", "")
		}},
		{"workable", map[string][]string{
			"GET https://apply.workable.com/api/v1/widget/accounts/musterlabs?details=true": {"account.json"},
		}, func(t *testing.T, c *sources.Client) discovery.Source {
			return board(t, c, "workable", "musterlabs", "", "")
		}},
		{"workday", map[string][]string{
			"POST https://muster.wd3.myworkdayjobs.com/wday/cxs/muster/External/jobs":                                {"page1.json", "page2.json"},
			"GET https://muster.wd3.myworkdayjobs.com/wday/cxs/muster/External/job/Berlin/Backend-Engineer_R-1001":   {"detail1.json"},
			"GET https://muster.wd3.myworkdayjobs.com/wday/cxs/muster/External/job/Remote/Data-Engineer_R-1002":      {"detail2.json"},
			"GET https://muster.wd3.myworkdayjobs.com/wday/cxs/muster/External/job/Hamburg/Platform-Engineer_R-1003": {"detail2.json"},
		}, func(t *testing.T, c *sources.Client) discovery.Source {
			return board(t, c, "workday", "muster", "Muster AG", `{"host":"muster.wd3.myworkdayjobs.com","site":"External"}`)
		}},
		{"arbeitnow", map[string][]string{
			"GET https://www.arbeitnow.com/api/job-board-api?page=1": {"page1.json"},
			"GET https://www.arbeitnow.com/api/job-board-api?page=2": {"page2.json"},
		}, func(_ *testing.T, c *sources.Client) discovery.Source { return sources.NewArbeitnow(c, 5) }},
		{"himalayas", map[string][]string{
			"GET https://himalayas.app/jobs/api/search?country=Germany&page=1&q=golang": {"search.json"},
		}, func(_ *testing.T, c *sources.Client) discovery.Source {
			return sources.NewHimalayas(c, []string{"golang"}, "Germany")
		}},
		{"arbeitsagentur", map[string][]string{
			"GET https://rest.arbeitsagentur.de/jobboerse/jobsuche-service/pc/v6/jobs?angebotsart=1&page=1&size=100&umkreis=30&veroeffentlichtseit=28&was=Softwareentwickler&wo=Berlin": {"search.json"},
			"GET https://rest.arbeitsagentur.de/jobboerse/jobsuche-service/pc/v4/jobdetails/MTIzNDUtMTAwMDAwMDAwMS1T":                                                                   {"detail.json"},
			"GET https://rest.arbeitsagentur.de/jobboerse/jobsuche-service/pc/v4/jobdetails/MTIzNDUtMTAwMDAwMDAwMi1T":                                                                   {"detail.json"},
		}, func(_ *testing.T, c *sources.Client) discovery.Source {
			return sources.NewArbeitsagentur(c, []string{"Softwareentwickler"}, []string{"Berlin"})
		}},
		{"amazon", map[string][]string{
			"GET https://www.amazon.jobs/en/search.json?base_query=software&country=DEU&offset=0&result_limit=100&sort=recent": {"search.json"},
		}, func(_ *testing.T, c *sources.Client) discovery.Source {
			return sources.NewAmazon(c, []string{"software"}, "DEU")
		}},
		{"google", map[string][]string{
			"GET https://www.google.com/about/careers/applications/jobs/feed.xml": {"feed.xml"},
		}, func(_ *testing.T, c *sources.Client) discovery.Source { return sources.NewGoogle(c) }},
		{"apple", map[string][]string{
			"GET https://jobs.apple.com/en-us/search?location=germany-DEU&page=1": {"page1.html"},
			"GET https://jobs.apple.com/en-us/search?location=germany-DEU&page=2": {"page2.html"},
		}, func(_ *testing.T, c *sources.Client) discovery.Source { return sources.NewApple(c, "germany-DEU") }},
		{"sitemap", map[string][]string{
			"GET https://careers.example.com/sitemap.xml":      {"index.xml"},
			"GET https://careers.example.com/sitemap-jobs.xml": {"jobs.xml"},
			"GET https://careers.example.com/jobs/1001/":       {"job1001.html"},
			"GET https://careers.example.com/jobs/1002/":       {"job1002.html"},
		}, func(t *testing.T, c *sources.Client) discovery.Source {
			return board(t, c, "sitemap", "example", "Example Social", `{"url":"https://careers.example.com/sitemap.xml","pattern":"/jobs/(\\d+)/$"}`)
		}},
		{"careers", map[string][]string{
			"GET https://www.beispielwerk.example/karriere": {"page.html"},
		}, func(t *testing.T, c *sources.Client) discovery.Source {
			return board(t, c, "careers", "beispielwerk", "Beispielwerk", `{"url":"https://www.beispielwerk.example/karriere"}`)
		}},
		{"eightfold-v2", map[string][]string{
			"GET https://careers.example.org/api/apply/v2/jobs?domain=example.org&location=Germany&num=100&start=0": {"jobs.json"},
			"GET https://careers.example.org/api/apply/v2/jobs/800000000001?domain=example.org":                     {"detail1.json"},
			"GET https://careers.example.org/api/apply/v2/jobs/800000000002?domain=example.org":                     {"detail2.json"},
		}, func(t *testing.T, c *sources.Client) discovery.Source {
			return board(t, c, "eightfold", "example.org", "Example Streaming", `{"host":"careers.example.org","api":"v2","location":"Germany"}`)
		}},
		{"eightfold-pcsx", map[string][]string{
			"GET https://apply.careers.example.com/api/pcsx/search?domain=example.com&location=Germany&start=0":                     {"page1.json"},
			"GET https://apply.careers.example.com/api/pcsx/search?domain=example.com&location=Germany&start=2":                     {"page2.json"},
			"GET https://apply.careers.example.com/api/pcsx/position_details?domain=example.com&hl=en&position_id=1970000000000001": {"detail.json"},
			"GET https://apply.careers.example.com/api/pcsx/position_details?domain=example.com&hl=en&position_id=1970000000000002": {"detail.json"},
			"GET https://apply.careers.example.com/api/pcsx/position_details?domain=example.com&hl=en&position_id=1970000000000003": {"detail.json"},
		}, func(t *testing.T, c *sources.Client) discovery.Source {
			return board(t, c, "eightfold", "example.com", "Example Software", `{"host":"apply.careers.example.com","api":"pcsx","location":"Germany"}`)
		}},
	}
	for _, c := range cases {
		t.Run(c.dir, func(t *testing.T) {
			cl, r := client(t, c.dir, c.routes)
			got := run(t, c.source(t, cl))
			golden(t, c.dir, got)
			for key, left := range r.routes {
				if len(left) > 0 {
					t.Errorf("route not requested: %s %v (requests: %v)", key, left, r.seen)
				}
			}
		})
	}
}

func TestFromBoardRejectsUnsafeSlugs(t *testing.T) {
	c := sources.NewClient(http.DefaultClient)
	for _, b := range []sources.Board{
		{ATS: "personio", Slug: "evil.example.org/x"},
		{ATS: "personio", Slug: "a.b"},
		{ATS: "recruitee", Slug: "-x"},
		{ATS: "greenhouse", Slug: "../admin"},
		{ATS: "greenhouse", Slug: "a/b"},
		{ATS: "lever", Slug: ""},
		{ATS: "workday", Slug: "muster", Config: json.RawMessage(`{"host":"evil.example.org","site":"External"}`)},
		{ATS: "workday", Slug: "muster", Config: json.RawMessage(`{"host":"muster.wd3.myworkdayjobs.com.evil.org","site":"x"}`)},
		{ATS: "workday", Slug: "muster", Config: json.RawMessage(`{"host":"muster.wd3.myworkdayjobs.com","site":"../x"}`)},
		{ATS: "taleo", Slug: "muster"},
	} {
		if _, err := sources.FromBoard(c, b); !errors.Is(err, sources.ErrBadBoard) {
			t.Errorf("FromBoard(%+v) = %v, want ErrBadBoard", b, err)
		}
	}
}

func TestClientErrors(t *testing.T) {
	ctx := context.Background()
	cl, r := client(t, "client", map[string][]string{
		"GET https://boards-api.greenhouse.io/v1/boards/flaky/jobs?content=true": {"status:500", "status:429", "../greenhouse/jobs.json"},
		"GET https://boards-api.greenhouse.io/v1/boards/down/jobs?content=true":  {"status:500", "status:500", "status:500"},
		"GET https://boards-api.greenhouse.io/v1/boards/junk/jobs?content=true":  {"junk.txt"},
	})
	if _, err := board(t, cl, "greenhouse", "flaky", "Flaky", "").Fetch(ctx); err != nil {
		t.Fatalf("retries did not recover: %v", err)
	}
	for slug, want := range map[string]string{"down": "http_500", "junk": "decode", "gone": "http_404"} {
		_, err := board(t, cl, "greenhouse", slug, "X", "").Fetch(ctx)
		var se *discovery.SourceError
		if !errors.As(err, &se) || se.Code != want || se.Source != "greenhouse:"+slug {
			t.Errorf("%s: err = %v, want code %s", slug, err, want)
		}
	}
	// 404 is final: no retries.
	n := 0
	for _, k := range r.seen {
		if strings.Contains(k, "/gone/") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("404 requested %d times", n)
	}
}

func TestArbeitnowKeepsPagesWhenThrottled(t *testing.T) {
	cl, _ := client(t, "arbeitnow", map[string][]string{
		"GET https://www.arbeitnow.com/api/job-board-api?page=1": {"page1.json"},
		"GET https://www.arbeitnow.com/api/job-board-api?page=2": {"status:429", "status:429", "status:429"},
	})
	b, err := sources.NewArbeitnow(cl, 5).Fetch(context.Background())
	if err != nil || b.Complete || len(b.Postings) == 0 {
		t.Fatalf("batch = %d postings, complete %v, err %v", len(b.Postings), b.Complete, err)
	}
}

func TestSourceNames(t *testing.T) {
	cl := sources.NewClient(http.DefaultClient)
	var names []string
	for _, ats := range []string{"greenhouse", "ashby", "lever", "smartrecruiters", "personio", "recruitee", "workable"} {
		names = append(names, board(t, cl, ats, "acme", "", "").Name())
	}
	sort.Strings(names)
	want := "ashby:acme greenhouse:acme lever:acme personio:acme recruitee:acme smartrecruiters:acme workable:acme"
	if got := strings.Join(names, " "); got != want {
		t.Fatalf("names = %s", got)
	}
}

// TestPagingIsBounded serves a page that never ends: every source stops at its cap.
func TestPagingIsBounded(t *testing.T) {
	body := anyBody(`{"total": 1000000, "totalFound": 1000000, "hits": "1000000",
		"jobPostings": [{"externalPath": "/job/x_1", "title": "t"}], "content": [{"id": "1", "name": "t"}],
		"jobs": [{"id_icims": "1", "title": "t"}], "data": [{"slug": "a", "title": "t", "url": "https://example.org/a"}],
		"links": {"next": "more"}, "ergebnisliste": [{"referenznummer": "1", "stellenangebotsTitel": "t"}]}`)
	c := sources.NewClient(&http.Client{Transport: body}).WithoutWaiting()
	for _, s := range []discovery.Source{
		board(t, c, "smartrecruiters", "x", "", ""),
		board(t, c, "workday", "x", "", `{"host":"x.wd1.myworkdayjobs.com","site":"x"}`),
		sources.NewAmazon(c, []string{"go"}, ""), sources.NewArbeitnow(c, 3),
		sources.NewArbeitsagentur(c, []string{"go"}, []string{"Berlin"}),
	} {
		b, err := s.Fetch(context.Background())
		if err != nil || b.Complete {
			t.Errorf("%s: complete %v, err %v", s.Name(), b.Complete, err)
		}
	}
}

// anyBody answers every request with the same body.
type anyBody []byte

func (b anyBody) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(b)), Request: req}, nil
}

// FuzzSources feeds arbitrary responses to every parser: no panics, no endless paging, only valid postings.
func FuzzSources(f *testing.F) {
	for _, seed := range []string{"testdata/greenhouse/jobs.json", "testdata/workday/page1.json", "testdata/personio/feed.xml",
		"testdata/arbeitsagentur/search.json", "testdata/amazon/search.json", "testdata/smartrecruiters/page1.json",
		"testdata/google/feed.xml", "testdata/apple/page1.html", "testdata/eightfold-pcsx/page1.json"} {
		b, err := os.ReadFile(seed)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		c := sources.NewClient(&http.Client{Transport: anyBody(body)}).WithoutWaiting()
		apple := sources.NewApple(c, "germany-DEU")
		apple.MaxPages = 3
		srcs := []discovery.Source{
			sources.NewArbeitnow(c, 3), sources.NewHimalayas(c, []string{"go"}, "Germany"),
			sources.NewArbeitsagentur(c, []string{"go"}, []string{"Berlin"}), sources.NewAmazon(c, []string{"go"}, "DEU"),
			sources.NewGoogle(c), apple,
		}
		for _, b := range []sources.Board{
			{ATS: "greenhouse", Slug: "x"}, {ATS: "ashby", Slug: "x"}, {ATS: "lever", Slug: "x"},
			{ATS: "smartrecruiters", Slug: "x"}, {ATS: "personio", Slug: "x"}, {ATS: "recruitee", Slug: "x"},
			{ATS: "workable", Slug: "x"},
			{ATS: "workday", Slug: "x", Config: json.RawMessage(`{"host":"x.wd1.myworkdayjobs.com","site":"x"}`)},
			{ATS: "eightfold", Slug: "x.com", Config: json.RawMessage(`{"host":"jobs.x.com","api":"v2"}`)},
			{ATS: "eightfold", Slug: "x.com", Config: json.RawMessage(`{"host":"jobs.x.com","api":"pcsx"}`)},
		} {
			s, err := sources.FromBoard(c, b)
			if err != nil {
				t.Fatal(err)
			}
			srcs = append(srcs, s)
		}
		for _, s := range srcs {
			batch, err := s.Fetch(context.Background())
			if err != nil {
				continue
			}
			if len(batch.Postings) > 5000 {
				t.Fatalf("%s: %d postings", s.Name(), len(batch.Postings))
			}
			if d, ok := s.(discovery.Detailer); ok {
				for i := range batch.Postings[:min(3, len(batch.Postings))] {
					_ = d.Details(context.Background(), &batch.Postings[i])
				}
			}
			for _, p := range batch.Postings {
				if p.Source != s.Name() {
					t.Fatalf("%s: posting from %q", s.Name(), p.Source)
				}
			}
		}
	})
}
