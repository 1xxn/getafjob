package sources

import (
	"bytes"
	"context"
	"encoding/xml"
	"net/url"
	"regexp"
	"strings"

	xhtml "golang.org/x/net/html"

	"github.com/1xxn/getafjob/internal/discovery"
	"github.com/1xxn/getafjob/internal/platform/netguard"
)

// Career sites without an applicant tracking system API: a sitemap that lists job pages, or a careers page with
// schema.org JobPostings. Both read JSON-LD, never guess from page text.

// maxSitemapURLs caps the job URLs of one sitemap.
const maxSitemapURLs = 20000

// Sitemap lists the job pages of a sitemap as stubs; Details reads each new page's JobPosting.
type Sitemap struct {
	c                     *Client
	name, company, rawURL string
	pattern               *regexp.Regexp // job URLs; the first group, if any, is the job ID
	complete              bool
}

// Name implements discovery.Source.
func (s *Sitemap) Name() string { return "sitemap:" + s.name }

// Fetch implements discovery.Source.
func (s *Sitemap) Fetch(ctx context.Context) (discovery.Batch, error) {
	base, err := url.Parse(s.rawURL)
	if err != nil {
		return discovery.Batch{}, &discovery.SourceError{Source: s.Name(), Code: "config"}
	}
	locs, err := s.locs(ctx, s.rawURL, 0)
	if err != nil {
		return discovery.Batch{}, err
	}
	seen := map[string]bool{}
	var out []discovery.Posting
	for _, loc := range locs {
		u, err := url.Parse(loc)
		if err != nil || u.Scheme != "https" || u.Host != base.Host || !netguard.PublicURL(loc) {
			continue // job pages stay on the site of the sitemap
		}
		m := s.pattern.FindStringSubmatch(loc)
		if m == nil {
			continue
		}
		id := loc
		if len(m) > 1 && m[1] != "" {
			id = m[1]
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, discovery.Posting{Source: s.Name(), ExternalID: id, Company: s.company, URL: loc, Stub: true})
		if len(out) >= maxSitemapURLs {
			return discovery.Batch{Postings: out}, nil
		}
	}
	return discovery.Batch{Postings: out, Complete: s.complete}, nil
}

// locs reads a sitemap or a sitemap index (one level deep, children on the same host).
func (s *Sitemap) locs(ctx context.Context, raw string, depth int) ([]string, error) {
	b, err := s.c.GetBytes(ctx, s.Name(), raw, nil, "application/xml")
	if err != nil {
		return nil, err
	}
	var doc struct {
		XMLName  xml.Name
		URLs     []string `xml:"url>loc"`
		Sitemaps []string `xml:"sitemap>loc"`
	}
	if err := xml.Unmarshal(b, &doc); err != nil {
		return nil, &discovery.SourceError{Source: s.Name(), Code: "decode"}
	}
	out := doc.URLs
	if depth > 0 {
		return out, nil
	}
	parent, _ := url.Parse(raw)
	for i, child := range doc.Sitemaps {
		cu, err := url.Parse(strings.TrimSpace(child))
		if i >= 50 || err != nil || cu.Host != parent.Host {
			continue
		}
		more, err := s.locs(ctx, cu.String(), depth+1)
		if err != nil {
			return nil, err
		}
		out = append(out, more...)
		if len(out) >= maxSitemapURLs {
			break
		}
	}
	return out, nil
}

// Details implements discovery.Detailer: the job page's JobPosting fills the stub.
func (s *Sitemap) Details(ctx context.Context, p *discovery.Posting) error {
	b, err := s.c.GetBytes(ctx, s.Name(), p.URL, nil, "text/html")
	if err != nil {
		return err
	}
	jobs := JobPostings(b)
	if len(jobs) == 0 {
		return &discovery.SourceError{Source: s.Name(), Code: "no_jobposting"}
	}
	jobs[0].fill(p, s.company)
	return nil
}

// CareersPage reads the JobPostings embedded in one careers page.
type CareersPage struct {
	c                     *Client
	name, company, rawURL string
}

// Name implements discovery.Source.
func (s *CareersPage) Name() string { return "careers:" + s.name }

// Fetch implements discovery.Source.
func (s *CareersPage) Fetch(ctx context.Context) (discovery.Batch, error) {
	b, err := s.c.GetBytes(ctx, s.Name(), s.rawURL, nil, "text/html")
	if err != nil {
		return discovery.Batch{}, err
	}
	var out []discovery.Posting
	for _, j := range JobPostings(b) {
		if !strings.HasPrefix(j.URL, "https://") || !netguard.PublicURL(j.URL) {
			continue
		}
		p := discovery.Posting{Source: s.Name(), ExternalID: or(j.ID, j.URL), URL: j.URL}
		j.fill(&p, s.company)
		out = append(out, p)
	}
	// A page may show only some jobs (paging, filters), so missing ones are not closed at once.
	return discovery.Batch{Postings: out}, nil
}

// ATS detection: links and scripts on careers pages and the apply links of job boards name the system and the
// board of a company.

var (
	slugPath = func(host string) *regexp.Regexp {
		return regexp.MustCompile(`^https?://` + regexp.QuoteMeta(host) + `/([A-Za-z0-9][A-Za-z0-9._-]*)`)
	}
	atsByPath = []struct {
		ats string
		rx  *regexp.Regexp
	}{
		{"greenhouse", slugPath("boards.greenhouse.io")},
		{"greenhouse", slugPath("job-boards.greenhouse.io")},
		{"greenhouse", slugPath("job-boards.eu.greenhouse.io")},
		{"greenhouse", slugPath("boards.eu.greenhouse.io")},
		{"greenhouse", regexp.MustCompile(`^https?://boards-api\.greenhouse\.io/v1/boards/([A-Za-z0-9._-]+)`)},
		{"ashby", slugPath("jobs.ashbyhq.com")},
		{"ashby", regexp.MustCompile(`^https?://api\.ashbyhq\.com/posting-api/job-board/([A-Za-z0-9._-]+)`)},
		{"lever", slugPath("jobs.lever.co")},
		{"lever", slugPath("jobs.eu.lever.co")},
		{"smartrecruiters", slugPath("jobs.smartrecruiters.com")},
		{"smartrecruiters", slugPath("careers.smartrecruiters.com")},
		{"workable", slugPath("apply.workable.com")},
	}
	atsByHost = []struct {
		ats string
		rx  *regexp.Regexp
	}{
		{"personio", regexp.MustCompile(`^https?://([a-z0-9-]+)\.jobs\.personio\.(?:de|com)(?:[/?#]|$)`)},
		{"recruitee", regexp.MustCompile(`^https?://([a-z0-9-]+)\.recruitee\.com(?:[/?#]|$)`)},
	}
	workdayURL  = regexp.MustCompile(`^https?://([a-z0-9-]+)\.(wd[0-9]{1,3}\.myworkday(?:jobs|site)\.com)/(?:[a-z]{2}-[A-Z]{2}/)?([A-Za-z0-9_-]+)`)
	greenhouseQ = regexp.MustCompile(`^https?://(?:job-)?boards(?:\.eu)?\.greenhouse\.io/embed/job_board(?:/js)?\?(?:.*&)?for=([A-Za-z0-9._-]+)`)
	urlInText   = regexp.MustCompile(`https?://[A-Za-z0-9.-]+\.(?:greenhouse\.io|ashbyhq\.com|lever\.co|smartrecruiters\.com|workable\.com|personio\.(?:de|com)|recruitee\.com|myworkdayjobs\.com|myworkdaysite\.com)[^\s"'<>\\)]*`)
	notSlugs    = map[string]bool{"embed": true, "api": true, "j": true, "jobs": true, "v1": true, "static": true, "assets": true}
)

// BoardFromURL names the board a link belongs to, if it points into a supported system.
func BoardFromURL(raw string) (Board, bool) {
	raw = strings.TrimSpace(raw)
	if m := greenhouseQ.FindStringSubmatch(raw); m != nil {
		return checked(Board{ATS: "greenhouse", Slug: m[1]})
	}
	if m := workdayURL.FindStringSubmatch(raw); m != nil && m[3] != "wday" {
		return checked(Board{ATS: "workday", Slug: m[1],
			Config: []byte(`{"host":"` + m[1] + "." + m[2] + `","site":"` + m[3] + `"}`)})
	}
	for _, a := range atsByHost {
		if m := a.rx.FindStringSubmatch(raw); m != nil && m[1] != "www" && m[1] != "api" {
			return checked(Board{ATS: a.ats, Slug: m[1]})
		}
	}
	for _, a := range atsByPath {
		if m := a.rx.FindStringSubmatch(raw); m != nil && !notSlugs[strings.ToLower(m[1])] {
			return checked(Board{ATS: a.ats, Slug: m[1]})
		}
	}
	return Board{}, false
}

// checked keeps only boards FromBoard would accept.
func checked(b Board) (Board, bool) {
	if _, err := FromBoard(nil, b); err != nil {
		return Board{}, false
	}
	return b, true
}

// DetectBoards finds the boards a careers page links to or embeds (links, scripts, iframes, inline URLs).
func DetectBoards(page []byte) []Board {
	if len(page) > MaxBody {
		page = page[:MaxBody]
	}
	var cands []string
	z := xhtml.NewTokenizer(bytes.NewReader(page))
	for done := false; !done; {
		switch z.Next() {
		case xhtml.ErrorToken:
			done = true
		case xhtml.StartTagToken, xhtml.SelfClosingTagToken:
			_, hasAttr := z.TagName()
			for hasAttr {
				var k, v []byte
				k, v, hasAttr = z.TagAttr()
				switch string(k) {
				case "href", "src", "action", "data-url", "data-src":
					cands = append(cands, string(v))
				}
			}
		case xhtml.TextToken:
			cands = append(cands, urlInText.FindAllString(string(z.Text()), 50)...)
		case xhtml.EndTagToken, xhtml.CommentToken, xhtml.DoctypeToken:
		}
	}
	seen := map[string]bool{}
	var out []Board
	for _, c := range cands {
		b, ok := BoardFromURL(c)
		key := b.ATS + ":" + strings.ToLower(b.Slug)
		if !ok || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, b)
	}
	return out
}
