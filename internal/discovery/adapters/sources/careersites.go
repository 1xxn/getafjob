package sources

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/1xxn/getafjob/internal/discovery"
	"github.com/1xxn/getafjob/internal/platform/htmltext"
)

// Career sites of large companies that run their own systems.

// Google reads the public jobs feed of Google, YouTube and DeepMind (every open job, with descriptions).
type Google struct{ c *Client }

// NewGoogle returns the Google careers source.
func NewGoogle(c *Client) *Google { return &Google{c: c} }

// googleFeedMax bounds the feed (about 20 MB in 2026).
const googleFeedMax = 96 << 20

// Name implements discovery.Source.
func (s *Google) Name() string { return "google" }

// Fetch implements discovery.Source.
func (s *Google) Fetch(ctx context.Context) (discovery.Batch, error) {
	b, err := s.c.do(ctx, s.Name(), request{method: http.MethodGet, accept: "application/xml",
		url: "https://www.google.com/about/careers/applications/jobs/feed.xml", maxBody: googleFeedMax})
	if err != nil {
		return discovery.Batch{}, err
	}
	var feed struct {
		Jobs []struct {
			ID          string `xml:"jobid"`
			Published   string `xml:"published"`
			Title       string `xml:"title"`
			Description string `xml:"description"`
			URL         string `xml:"url"`
			Employer    string `xml:"employer"`
			Remote      string `xml:"remote"`
			Hybrid      string `xml:"isHybrid"`
			Locations   []struct {
				City    string `xml:"city"`
				State   string `xml:"state"`
				Country string `xml:"country"`
			} `xml:"locations>location"`
		} `xml:"job"`
	}
	if err := xml.Unmarshal(b, &feed); err != nil {
		return discovery.Batch{}, &discovery.SourceError{Source: s.Name(), Code: "decode"}
	}
	out := make([]discovery.Posting, 0, len(feed.Jobs))
	for _, j := range feed.Jobs {
		var locs []string
		for _, l := range j.Locations {
			locs = append(locs, join(", ", l.City, l.State, l.Country))
		}
		wp := workplace(j.Remote)
		if strings.EqualFold(j.Hybrid, "yes") {
			wp = discovery.WorkplaceHybrid
		}
		out = append(out, discovery.Posting{
			Source: s.Name(), ExternalID: strings.TrimSpace(j.ID), Company: or(j.Employer, "Google"),
			Title: strings.TrimSpace(j.Title), Location: join("; ", locs...), URL: strings.TrimSpace(j.URL),
			Description: htmltext.Text(j.Description), Posted: day(j.Published), Workplace: wp,
		})
	}
	return discovery.Batch{Postings: out, Complete: true}, nil
}

// Apple reads the job search of jobs.apple.com for one location (search results are embedded in the page).
type Apple struct {
	c        *Client
	Location string // as in the site's URLs, e.g. germany-DEU
	MaxPages int
}

// NewApple returns the Apple careers source for a location.
func NewApple(c *Client, location string) *Apple {
	return &Apple{c: c, Location: location, MaxPages: 50}
}

// Name implements discovery.Source.
func (s *Apple) Name() string { return "apple:" + s.Location }

var appleData = regexp.MustCompile(`window\.__staticRouterHydrationData\s*=\s*JSON\.parse\("((?:[^"\\]|\\.)*)"\)`)

// Fetch implements discovery.Source.
func (s *Apple) Fetch(ctx context.Context) (discovery.Batch, error) {
	var out []discovery.Posting
	for page := 1; page <= s.MaxPages; page++ {
		b, err := s.c.GetBytes(ctx, s.Name(), "https://jobs.apple.com/en-us/search",
			url.Values{"location": {s.Location}, "page": {strconv.Itoa(page)}}, "text/html")
		if err != nil {
			return discovery.Batch{}, err
		}
		m := appleData.FindSubmatch(b)
		var raw string
		if m == nil || json.Unmarshal(append(append([]byte{'"'}, m[1]...), '"'), &raw) != nil {
			return discovery.Batch{}, &discovery.SourceError{Source: s.Name(), Code: "decode"}
		}
		var data struct {
			LoaderData struct {
				Search struct {
					Total   int `json:"totalRecords"`
					Results []struct {
						PositionID string `json:"positionId"`
						Title      string `json:"postingTitle"`
						Slug       string `json:"transformedPostingTitle"`
						Summary    string `json:"jobSummary"`
						Posted     string `json:"postDateInGMT"`
						HomeOffice any    `json:"homeOffice"`
						Locations  []struct {
							City    string `json:"city"`
							State   string `json:"stateProvince"`
							Country string `json:"countryName"`
							Name    string `json:"name"`
						} `json:"locations"`
					} `json:"searchResults"`
				} `json:"search"`
			} `json:"loaderData"`
		}
		if err := json.Unmarshal([]byte(raw), &data); err != nil {
			return discovery.Batch{}, &discovery.SourceError{Source: s.Name(), Code: "decode"}
		}
		res := data.LoaderData.Search
		for _, j := range res.Results {
			var locs []string
			for _, l := range j.Locations {
				locs = append(locs, or(join(", ", l.City, l.State, l.Country), l.Name))
			}
			wp := discovery.WorkplaceUnknown
			if h, ok := j.HomeOffice.(bool); (ok && h) || j.HomeOffice == "true" {
				wp = discovery.WorkplaceRemote
			}
			pid := strings.TrimSpace(j.PositionID)
			out = append(out, discovery.Posting{
				Source: s.Name(), ExternalID: pid, Company: "Apple", Title: strings.TrimSpace(j.Title),
				Location: join("; ", locs...), Description: strings.TrimSpace(j.Summary), Posted: day(j.Posted), Workplace: wp,
				URL: "https://jobs.apple.com/en-us/details/" + url.PathEscape(pid) + "/" + url.PathEscape(j.Slug),
			})
		}
		if len(res.Results) == 0 || len(out) >= res.Total {
			return discovery.Batch{Postings: out, Complete: true}, nil
		}
	}
	return discovery.Batch{Postings: out}, nil
}

// Eightfold reads career sites run on Eightfold (Netflix, Microsoft and others). Two API generations exist: "v2"
// (/api/apply/v2) and "pcsx" (/api/pcsx). Descriptions come per job (Details).
type Eightfold struct {
	c                     *Client
	host, domain, company string
	api, location, query  string
	max                   int
}

// eightfoldMax caps one site's list unless the board config sets "max".
const eightfoldMax = 500

// Name implements discovery.Source.
func (s *Eightfold) Name() string { return "eightfold:" + s.domain }

type eightfoldJob struct {
	ID          json.RawMessage `json:"id"`
	Name        string          `json:"name"`
	Locations   []string        `json:"locations"`
	Created     json.Number     `json:"t_create"`
	Posted      json.Number     `json:"postedTs"`
	URL         string          `json:"canonicalPositionUrl"`
	Path        string          `json:"positionUrl"`
	Option      string          `json:"work_location_option"`
	OptionPCSX  string          `json:"workLocationOption"`
	Description string          `json:"job_description"`
	JobDesc     string          `json:"jobDescription"`
}

// Fetch implements discovery.Source.
func (s *Eightfold) Fetch(ctx context.Context) (discovery.Batch, error) {
	limit := s.max
	if limit <= 0 {
		limit = eightfoldMax
	}
	var out []discovery.Posting
	for start := 0; ; {
		q := url.Values{"domain": {s.domain}, "start": {strconv.Itoa(start)}}
		if s.location != "" {
			q.Set("location", s.location)
		}
		if s.query != "" {
			q.Set("query", s.query)
		}
		var jobs []eightfoldJob
		var count int
		if s.api == "pcsx" {
			var data struct {
				Data struct {
					Positions []eightfoldJob `json:"positions"`
					Count     int            `json:"count"`
				} `json:"data"`
			}
			if err := s.c.GetJSON(ctx, s.Name(), "https://"+s.host+"/api/pcsx/search", q, nil, &data); err != nil {
				return discovery.Batch{}, err
			}
			jobs, count = data.Data.Positions, data.Data.Count
		} else {
			q.Set("num", "100")
			var data struct {
				Positions []eightfoldJob `json:"positions"`
				Count     int            `json:"count"`
			}
			if err := s.c.GetJSON(ctx, s.Name(), "https://"+s.host+"/api/apply/v2/jobs", q, nil, &data); err != nil {
				return discovery.Batch{}, err
			}
			jobs, count = data.Positions, data.Count
		}
		for _, j := range jobs {
			ext := id(j.ID)
			u := j.URL
			if !strings.HasPrefix(u, "https://") {
				u = "https://" + s.host + "/careers/job/" + url.PathEscape(ext)
				if strings.HasPrefix(j.Path, "/") {
					u = "https://" + s.host + j.Path
				}
			}
			posted := epochDay(j.Created)
			if posted == "" {
				posted = epochDay(j.Posted)
			}
			out = append(out, discovery.Posting{
				Source: s.Name(), ExternalID: ext, Company: s.company, Title: strings.TrimSpace(j.Name),
				Location: join("; ", j.Locations...), URL: u, Posted: posted, Workplace: workplace(or(j.Option, j.OptionPCSX)),
				Description: htmltext.Text(or(j.Description, j.JobDesc)),
			})
		}
		start += len(jobs)
		if len(jobs) == 0 || start >= count {
			// A location filter is a stable attribute; a text query is a search.
			return discovery.Batch{Postings: out, Complete: s.query == ""}, nil
		}
		if start >= limit {
			return discovery.Batch{Postings: out}, nil
		}
	}
}

// Details implements discovery.Detailer.
func (s *Eightfold) Details(ctx context.Context, p *discovery.Posting) error {
	var desc string
	if s.api == "pcsx" {
		var data struct {
			Data eightfoldJob `json:"data"`
		}
		q := url.Values{"position_id": {p.ExternalID}, "domain": {s.domain}, "hl": {"en"}}
		if err := s.c.GetJSON(ctx, s.Name(), "https://"+s.host+"/api/pcsx/position_details", q, nil, &data); err != nil {
			return err
		}
		desc = data.Data.JobDesc
	} else {
		var data eightfoldJob
		u := "https://" + s.host + "/api/apply/v2/jobs/" + url.PathEscape(p.ExternalID)
		if err := s.c.GetJSON(ctx, s.Name(), u, url.Values{"domain": {s.domain}}, nil, &data); err != nil {
			return err
		}
		desc = data.Description
	}
	p.Description = htmltext.Text(desc)
	return nil
}
