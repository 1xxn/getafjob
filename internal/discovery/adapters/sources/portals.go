package sources

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	xhtml "golang.org/x/net/html"

	"github.com/1xxn/getafjob/internal/discovery"
	"github.com/1xxn/getafjob/internal/platform/htmltext"
)

// Public search on major portals (LinkedIn, Indeed, Glassdoor) ported from the JobSpy approaches.
// Requests are strictly unauthenticated and public (no login required, no private session cookies).

var (
	_ discovery.Source      = (*LinkedIn)(nil)
	_ discovery.Detailer    = (*LinkedIn)(nil)
	_ discovery.Conditional = (*LinkedIn)(nil)

	_ discovery.Source      = (*Indeed)(nil)
	_ discovery.Detailer    = (*Indeed)(nil)
	_ discovery.Conditional = (*Indeed)(nil)

	_ discovery.Source      = (*Glassdoor)(nil)
	_ discovery.Detailer    = (*Glassdoor)(nil)
	_ discovery.Conditional = (*Glassdoor)(nil)
)

var (
	reLinkedInID        = regexp.MustCompile(`(?:urn:li:jobPosting:|/jobs/view/(?:[^\/]*?-)?|currentJobId=)(\d+)`)
	reLinkedInURN       = regexp.MustCompile(`urn:li:jobPosting:(\d+)`)
	reIndeedMosaicVar   = regexp.MustCompile(`window\.mosaic\.providerData\["mosaic-provider-jobcards"\]\s*=\s*(\{[\s\S]*?\});`)
	reIndeedMosaicTag   = regexp.MustCompile(`<script[^>]*id=["']mosaic-provider-jobcards["'][^>]*>([\s\S]*?)</script>`)
	reIndeedMosaicData  = regexp.MustCompile(`<script[^>]*id=["']mosaic-data["'][^>]*>([\s\S]*?)</script>`)
	reGlassdoorNextData = regexp.MustCompile(`<script[^>]*id=["']__NEXT_DATA__["'][^>]*>([\s\S]*?)</script>`)
	reGlassdoorApollo   = regexp.MustCompile(`window\.__APOLLO_STATE__\s*=\s*(\{[\s\S]*?\});`)
)

func portalWorkplace(s string) discovery.Workplace {
	lower := strings.ToLower(s)
	switch {
	case strings.Contains(lower, "remote") || strings.Contains(lower, "home office") || strings.Contains(lower, "homeoffice"):
		return discovery.WorkplaceRemote
	case strings.Contains(lower, "hybrid"):
		return discovery.WorkplaceHybrid
	case strings.Contains(lower, "onsite") || strings.Contains(lower, "on-site") || strings.Contains(lower, "vor ort"):
		return discovery.WorkplaceOnsite
	default:
		return discovery.WorkplaceUnknown
	}
}

// LinkedIn searches public job listings via LinkedIn's guest search endpoint.
type LinkedIn struct {
	c *Client
	conditional
	Queries  []string
	Location string
	MaxPages int
	Pause    time.Duration
}

// NewLinkedIn returns the LinkedIn source.
func NewLinkedIn(c *Client, queries []string, location string) *LinkedIn {
	return &LinkedIn{
		c:        c,
		Queries:  queries,
		Location: or(location, "Germany"),
		MaxPages: 3,
		Pause:    1500 * time.Millisecond,
	}
}

// Name implements discovery.Source.
func (s *LinkedIn) Name() string { return "linkedin" }

// Fetch implements discovery.Source.
func (s *LinkedIn) Fetch(ctx context.Context) (discovery.Batch, error) {
	if len(s.Queries) == 0 {
		return discovery.Batch{}, nil
	}
	seen := map[string]int{}
	var out []discovery.Posting
	var lastValidators discovery.Validators
	for qi, q := range s.Queries {
		for page := 0; page < s.MaxPages; page++ {
			if qi > 0 || page > 0 {
				if err := s.c.sleep(ctx, s.Pause); err != nil {
					return discovery.Batch{}, err
				}
			}
			start := page * 25
			vals := url.Values{"keywords": {q}, "location": {s.Location}, "start": {strconv.Itoa(start)}}
			endpoint := "https://www.linkedin.com/jobs-guest/jobs/api/seeMoreJobPostings/search"

			var b []byte
			var next discovery.Validators
			var notModified bool
			var err error
			if qi == 0 && page == 0 && (s.since.ETag != "" || s.since.LastModified != "") {
				b, next, notModified, err = s.c.GetConditional(ctx, s.Name(), endpoint, vals, "text/html", s.since)
				if notModified {
					return discovery.Batch{NotModified: true, Validators: next}, nil
				}
			} else {
				b, err = s.c.GetBytes(ctx, s.Name(), endpoint, vals, "text/html")
			}
			if Status(err) == http.StatusTooManyRequests && len(out) > 0 {
				return discovery.Batch{Postings: out, Validators: lastValidators}, nil
			}
			if err != nil {
				return discovery.Batch{}, err
			}
			if next.ETag != "" || next.LastModified != "" {
				lastValidators = next
			}
			postings := parseLinkedInSearch(b, s.Name())
			if len(postings) == 0 {
				break
			}
			for _, p := range postings {
				if i, dup := seen[p.ExternalID]; dup {
					out[i] = p
					continue
				}
				seen[p.ExternalID] = len(out)
				out = append(out, p)
			}
			if len(postings) < 25 {
				break
			}
		}
	}
	return discovery.Batch{Postings: out, Validators: lastValidators}, nil
}

// Details implements discovery.Detailer.
func (s *LinkedIn) Details(ctx context.Context, p *discovery.Posting) error {
	if p.Description != "" {
		return nil
	}
	endpoint := "https://www.linkedin.com/jobs-guest/jobs/api/jobPosting/" + url.PathEscape(p.ExternalID)
	b, err := s.c.GetBytes(ctx, s.Name(), endpoint, nil, "text/html")
	if err != nil {
		return err
	}
	doc, err := xhtml.Parse(bytes.NewReader(b))
	if err != nil {
		return &discovery.SourceError{Source: s.Name(), Code: "decode"}
	}
	descNode := findByClass(doc, "div", "show-more-less-html__markup")
	if descNode == nil {
		descNode = findByClass(doc, "div", "description__text")
	}
	if descNode == nil {
		descNode = findByClass(doc, "section", "show-more-less-html")
	}
	if descNode != nil {
		p.Description = htmltext.Text(innerHTML(descNode))
	} else {
		p.Description = htmltext.Text(string(b))
	}
	applyNode := findByClass(doc, "a", "apply-button")
	if applyNode != nil {
		if u := attr(applyNode, "href"); u != "" {
			p.ApplyURL = u
		}
	}
	return nil
}

func parseLinkedInSearch(b []byte, src string) []discovery.Posting {
	doc, err := xhtml.Parse(bytes.NewReader(b))
	if err != nil {
		return nil
	}
	var out []discovery.Posting
	var walk func(*xhtml.Node)
	walk = func(n *xhtml.Node) {
		if n.Type == xhtml.ElementNode && (n.Data == "li" || n.Data == "div") {
			urn := attr(n, "data-entity-urn")
			if urn == "" {
				urn = attr(n, "data-urn")
			}
			m := reLinkedInURN.FindStringSubmatch(urn)
			id := ""
			if len(m) > 1 {
				id = m[1]
			}
			linkNode := findByClass(n, "a", "base-card__full-link")
			if id == "" && linkNode != nil {
				href := attr(linkNode, "href")
				if sm := reLinkedInID.FindStringSubmatch(href); len(sm) > 1 {
					id = sm[1]
				}
			}
			if id != "" {
				titleNode := findByClass(n, "h3", "base-search-card__title")
				title := ""
				if titleNode != nil {
					title = textContent(titleNode)
				} else if linkNode != nil {
					title = textContent(linkNode)
				}
				companyNode := findByClass(n, "h4", "base-search-card__subtitle")
				company := ""
				if companyNode != nil {
					company = textContent(companyNode)
				}
				locNode := findByClass(n, "span", "job-search-card__location")
				location := ""
				if locNode != nil {
					location = textContent(locNode)
				}
				timeNode := findTag(n, "time")
				posted := ""
				if timeNode != nil {
					posted = day(attr(timeNode, "datetime"))
					if posted == "" {
						posted = day(textContent(timeNode))
					}
				}
				jobURL := "https://www.linkedin.com/jobs/view/" + id
				if title != "" {
					out = append(out, discovery.Posting{
						Source:     src,
						ExternalID: id,
						Company:    company,
						Title:      title,
						Location:   location,
						URL:        jobURL,
						Posted:     posted,
						Workplace:  portalWorkplace(title + " " + location),
					})
					return
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return out
}

// Indeed searches public job listings via Indeed's search endpoint and embedded Mosaic / GraphQL data.
type Indeed struct {
	c *Client
	conditional
	Queries  []string
	Location string
	MaxPages int
	Pause    time.Duration
}

// NewIndeed returns the Indeed source.
func NewIndeed(c *Client, queries []string, location string) *Indeed {
	return &Indeed{
		c:        c,
		Queries:  queries,
		Location: or(location, "Germany"),
		MaxPages: 3,
		Pause:    1500 * time.Millisecond,
	}
}

// Name implements discovery.Source.
func (s *Indeed) Name() string { return "indeed" }

// Fetch implements discovery.Source.
func (s *Indeed) Fetch(ctx context.Context) (discovery.Batch, error) {
	if len(s.Queries) == 0 {
		return discovery.Batch{}, nil
	}
	seen := map[string]int{}
	var out []discovery.Posting
	var lastValidators discovery.Validators
	for qi, q := range s.Queries {
		for page := 0; page < s.MaxPages; page++ {
			if qi > 0 || page > 0 {
				if err := s.c.sleep(ctx, s.Pause); err != nil {
					return discovery.Batch{}, err
				}
			}
			start := page * 10
			vals := url.Values{"q": {q}, "l": {s.Location}, "start": {strconv.Itoa(start)}, "fromage": {"14"}}
			endpoint := "https://www.indeed.com/jobs"

			var b []byte
			var next discovery.Validators
			var notModified bool
			var err error
			if qi == 0 && page == 0 && (s.since.ETag != "" || s.since.LastModified != "") {
				b, next, notModified, err = s.c.GetConditional(ctx, s.Name(), endpoint, vals, "text/html,application/json", s.since)
				if notModified {
					return discovery.Batch{NotModified: true, Validators: next}, nil
				}
			} else {
				b, err = s.c.GetBytes(ctx, s.Name(), endpoint, vals, "text/html,application/json")
			}
			if Status(err) == http.StatusTooManyRequests && len(out) > 0 {
				return discovery.Batch{Postings: out, Validators: lastValidators}, nil
			}
			if err != nil {
				return discovery.Batch{}, err
			}
			if next.ETag != "" || next.LastModified != "" {
				lastValidators = next
			}
			postings := parseIndeedSearch(b, s.Name())
			if len(postings) == 0 {
				break
			}
			for _, p := range postings {
				if i, dup := seen[p.ExternalID]; dup {
					out[i] = p
					continue
				}
				seen[p.ExternalID] = len(out)
				out = append(out, p)
			}
			if len(postings) < 10 {
				break
			}
		}
	}
	return discovery.Batch{Postings: out, Validators: lastValidators}, nil
}

// Details implements discovery.Detailer.
func (s *Indeed) Details(ctx context.Context, p *discovery.Posting) error {
	if p.Description != "" {
		return nil
	}
	endpoint := "https://www.indeed.com/viewjob?jk=" + url.QueryEscape(p.ExternalID)
	b, err := s.c.GetBytes(ctx, s.Name(), endpoint, nil, "text/html")
	if err != nil {
		return err
	}
	doc, err := xhtml.Parse(bytes.NewReader(b))
	if err != nil {
		return &discovery.SourceError{Source: s.Name(), Code: "decode"}
	}
	descNode := findByID(doc, "jobDescriptionText")
	if descNode == nil {
		descNode = findByClass(doc, "div", "jobsearch-JobComponent-description")
	}
	if descNode != nil {
		p.Description = htmltext.Text(innerHTML(descNode))
	} else {
		p.Description = htmltext.Text(string(b))
	}
	return nil
}

type indeedItem struct {
	Jobkey             string      `json:"jobkey"`
	DisplayTitle       string      `json:"displayTitle"`
	Title              string      `json:"title"`
	Company            string      `json:"company"`
	FormattedLocation  string      `json:"formattedLocation"`
	Location           string      `json:"location"`
	PubDate            json.Number `json:"pubDate"`
	Date               string      `json:"date"`
	Snippet            string      `json:"snippet"`
	JobDescriptionText string      `json:"jobDescriptionText"`
	Description        string      `json:"description"`
	Link               string      `json:"link"`
}

type indeedPayload struct {
	Results                     []indeedItem `json:"results"`
	MosaicProviderJobCardsModel struct {
		Results []indeedItem `json:"results"`
	} `json:"mosaicProviderJobCardsModel"`
	Data struct {
		JobSearch struct {
			Results []indeedItem `json:"results"`
		} `json:"jobSearch"`
	} `json:"data"`
}

func (p indeedPayload) items() []indeedItem {
	if len(p.MosaicProviderJobCardsModel.Results) > 0 {
		return p.MosaicProviderJobCardsModel.Results
	}
	if len(p.Results) > 0 {
		return p.Results
	}
	return p.Data.JobSearch.Results
}

func parseIndeedSearch(b []byte, src string) []discovery.Posting {
	trimmed := bytes.TrimSpace(b)
	var payload indeedPayload
	if len(trimmed) > 0 && trimmed[0] == '{' {
		_ = json.Unmarshal(trimmed, &payload)
	}
	if len(payload.items()) == 0 {
		for _, re := range []*regexp.Regexp{reIndeedMosaicTag, reIndeedMosaicVar, reIndeedMosaicData} {
			if m := re.FindSubmatch(b); len(m) > 1 {
				var candidate indeedPayload
				if json.Unmarshal(m[1], &candidate) == nil && len(candidate.items()) > 0 {
					payload = candidate
					break
				}
			}
		}
	}
	var out []discovery.Posting
	for _, item := range payload.items() {
		jk := strings.TrimSpace(item.Jobkey)
		if jk == "" {
			continue
		}
		title := or(item.DisplayTitle, item.Title)
		company := strings.TrimSpace(item.Company)
		location := or(item.FormattedLocation, item.Location)
		desc := htmltext.Text(or(item.JobDescriptionText, item.Description))
		posted := day(item.Date)
		if posted == "" {
			posted = epochDay(item.PubDate)
		}
		jobURL := "https://www.indeed.com/viewjob?jk=" + url.QueryEscape(jk)
		out = append(out, discovery.Posting{
			Source:      src,
			ExternalID:  jk,
			Company:     company,
			Title:       title,
			Location:    location,
			URL:         jobURL,
			Description: desc,
			Posted:      posted,
			Workplace:   portalWorkplace(title + " " + location + " " + desc),
		})
	}
	if len(out) > 0 {
		return out
	}

	// HTML DOM fallback
	doc, err := xhtml.Parse(bytes.NewReader(b))
	if err != nil {
		return nil
	}
	var walk func(*xhtml.Node)
	walk = func(n *xhtml.Node) {
		if n.Type == xhtml.ElementNode {
			jk := attr(n, "data-jk")
			if jk != "" {
				titleNode := findByClass(n, "h2", "jobTitle")
				title := ""
				if titleNode != nil {
					title = textContent(titleNode)
				}
				companyNode := findByClass(n, "span", "companyName")
				if companyNode == nil {
					companyNode = findByAttr(n, "data-testid", "company-name")
				}
				company := ""
				if companyNode != nil {
					company = textContent(companyNode)
				}
				locNode := findByClass(n, "div", "companyLocation")
				if locNode == nil {
					locNode = findByAttr(n, "data-testid", "text-location")
				}
				location := ""
				if locNode != nil {
					location = textContent(locNode)
				}
				snippetNode := findByClass(n, "div", "job-snippet")
				desc := ""
				if snippetNode != nil {
					desc = htmltext.Text(innerHTML(snippetNode))
				}
				if title != "" {
					jobURL := "https://www.indeed.com/viewjob?jk=" + url.QueryEscape(jk)
					out = append(out, discovery.Posting{
						Source:      src,
						ExternalID:  jk,
						Company:     company,
						Title:       title,
						Location:    location,
						URL:         jobURL,
						Description: desc,
						Workplace:   portalWorkplace(title + " " + location + " " + desc),
					})
					return
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return out
}

// Glassdoor searches public job listings via Glassdoor's search endpoint and embedded Next.js / GraphQL data.
type Glassdoor struct {
	c *Client
	conditional
	Queries  []string
	Location string
	MaxPages int
	Pause    time.Duration
}

// NewGlassdoor returns the Glassdoor source.
func NewGlassdoor(c *Client, queries []string, location string) *Glassdoor {
	return &Glassdoor{
		c:        c,
		Queries:  queries,
		Location: or(location, "Germany"),
		MaxPages: 3,
		Pause:    1500 * time.Millisecond,
	}
}

// Name implements discovery.Source.
func (s *Glassdoor) Name() string { return "glassdoor" }

// Fetch implements discovery.Source.
func (s *Glassdoor) Fetch(ctx context.Context) (discovery.Batch, error) {
	if len(s.Queries) == 0 {
		return discovery.Batch{}, nil
	}
	seen := map[string]int{}
	var out []discovery.Posting
	var lastValidators discovery.Validators
	for qi, q := range s.Queries {
		for page := 1; page <= s.MaxPages; page++ {
			if qi > 0 || page > 1 {
				if err := s.c.sleep(ctx, s.Pause); err != nil {
					return discovery.Batch{}, err
				}
			}
			vals := url.Values{"sc.keyword": {q}, "locKeyword": {s.Location}, "page": {strconv.Itoa(page)}}
			endpoint := "https://www.glassdoor.com/Job/jobs.htm"

			var b []byte
			var next discovery.Validators
			var notModified bool
			var err error
			if qi == 0 && page == 1 && (s.since.ETag != "" || s.since.LastModified != "") {
				b, next, notModified, err = s.c.GetConditional(ctx, s.Name(), endpoint, vals, "text/html,application/json", s.since)
				if notModified {
					return discovery.Batch{NotModified: true, Validators: next}, nil
				}
			} else {
				b, err = s.c.GetBytes(ctx, s.Name(), endpoint, vals, "text/html,application/json")
			}
			if Status(err) == http.StatusTooManyRequests && len(out) > 0 {
				return discovery.Batch{Postings: out, Validators: lastValidators}, nil
			}
			if err != nil {
				return discovery.Batch{}, err
			}
			if next.ETag != "" || next.LastModified != "" {
				lastValidators = next
			}
			postings := parseGlassdoorSearch(b, s.Name())
			if len(postings) == 0 {
				break
			}
			for _, p := range postings {
				if i, dup := seen[p.ExternalID]; dup {
					out[i] = p
					continue
				}
				seen[p.ExternalID] = len(out)
				out = append(out, p)
			}
		}
	}
	return discovery.Batch{Postings: out, Validators: lastValidators}, nil
}

// Details implements discovery.Detailer.
func (s *Glassdoor) Details(ctx context.Context, p *discovery.Posting) error {
	if p.Description != "" {
		return nil
	}
	endpoint := "https://www.glassdoor.com/job-listing/jl.htm?jl=" + url.QueryEscape(p.ExternalID)
	b, err := s.c.GetBytes(ctx, s.Name(), endpoint, nil, "text/html")
	if err != nil {
		return err
	}
	doc, err := xhtml.Parse(bytes.NewReader(b))
	if err != nil {
		return &discovery.SourceError{Source: s.Name(), Code: "decode"}
	}
	descNode := findByClass(doc, "div", "jobDescriptionContent")
	if descNode == nil {
		descNode = findByClass(doc, "div", "jobDescription")
	}
	if descNode != nil {
		p.Description = htmltext.Text(innerHTML(descNode))
	} else {
		p.Description = htmltext.Text(string(b))
	}
	return nil
}

type glassdoorItem struct {
	ListingID     json.Number `json:"listingId"`
	JobTitle      string      `json:"jobTitle"`
	JobTitleText  string      `json:"jobTitleText"`
	Title         string      `json:"title"`
	EmployerName  string      `json:"employerName"`
	Employer      string      `json:"employer"`
	Location      string      `json:"location"`
	LocationName  string      `json:"locationName"`
	PostingDate   string      `json:"postingDate"`
	DatePosted    string      `json:"datePosted"`
	Description   string      `json:"description"`
	JobViewURL    string      `json:"jobViewUrl"`
	WorkplaceType string      `json:"workplaceType"`
}

type glassdoorPayload struct {
	Props struct {
		PageProps struct {
			JobListings []struct {
				Jobview struct {
					Job struct {
						ListingID   json.Number `json:"listingId"`
						JobTitle    string      `json:"jobTitleText"`
						Title       string      `json:"title"`
						Description string      `json:"description"`
						Date        string      `json:"postingDate"`
					} `json:"job"`
					Header struct {
						EmployerName string `json:"employerNameFromSearch"`
						Employer     string `json:"employerName"`
						Location     string `json:"locationName"`
					} `json:"header"`
					Overview struct {
						WorkplaceType string `json:"workplaceType"`
					} `json:"overview"`
				} `json:"jobview"`
			} `json:"jobListings"`
		} `json:"pageProps"`
	} `json:"props"`
	Data struct {
		JobListings []glassdoorItem `json:"jobListings"`
	} `json:"data"`
	JobListings []glassdoorItem `json:"jobListings"`
}

func parseGlassdoorSearch(b []byte, src string) []discovery.Posting {
	trimmed := bytes.TrimSpace(b)
	var payload glassdoorPayload
	if len(trimmed) > 0 && trimmed[0] == '{' {
		_ = json.Unmarshal(trimmed, &payload)
	}
	if len(payload.Props.PageProps.JobListings) == 0 && len(payload.Data.JobListings) == 0 && len(payload.JobListings) == 0 {
		for _, re := range []*regexp.Regexp{reGlassdoorNextData, reGlassdoorApollo} {
			if m := re.FindSubmatch(b); len(m) > 1 {
				var candidate glassdoorPayload
				if json.Unmarshal(m[1], &candidate) == nil {
					payload = candidate
					break
				}
			}
		}
	}
	var out []discovery.Posting
	for _, jv := range payload.Props.PageProps.JobListings {
		job := jv.Jobview.Job
		hdr := jv.Jobview.Header
		ov := jv.Jobview.Overview
		id := strings.TrimSpace(string(job.ListingID))
		if id == "" {
			continue
		}
		title := or(job.JobTitle, job.Title)
		company := or(hdr.EmployerName, hdr.Employer)
		location := hdr.Location
		posted := day(job.Date)
		desc := htmltext.Text(job.Description)
		jobURL := "https://www.glassdoor.com/job-listing/jl.htm?jl=" + url.QueryEscape(id)
		wp := portalWorkplace(title + " " + location + " " + ov.WorkplaceType)
		out = append(out, discovery.Posting{
			Source:      src,
			ExternalID:  id,
			Company:     company,
			Title:       title,
			Location:    location,
			URL:         jobURL,
			Description: desc,
			Posted:      posted,
			Workplace:   wp,
		})
	}
	items := payload.Data.JobListings
	if len(items) == 0 {
		items = payload.JobListings
	}
	for _, item := range items {
		id := strings.TrimSpace(string(item.ListingID))
		if id == "" {
			continue
		}
		title := or(item.JobTitle, or(item.JobTitleText, item.Title))
		company := or(item.EmployerName, item.Employer)
		location := or(item.LocationName, item.Location)
		posted := day(or(item.PostingDate, item.DatePosted))
		desc := htmltext.Text(item.Description)
		jobURL := or(item.JobViewURL, "https://www.glassdoor.com/job-listing/jl.htm?jl="+url.QueryEscape(id))
		wp := portalWorkplace(title + " " + location + " " + item.WorkplaceType)
		out = append(out, discovery.Posting{
			Source:      src,
			ExternalID:  id,
			Company:     company,
			Title:       title,
			Location:    location,
			URL:         jobURL,
			Description: desc,
			Posted:      posted,
			Workplace:   wp,
		})
	}
	if len(out) > 0 {
		return out
	}

	// HTML DOM fallback
	doc, err := xhtml.Parse(bytes.NewReader(b))
	if err != nil {
		return nil
	}
	var walk func(*xhtml.Node)
	walk = func(n *xhtml.Node) {
		if n.Type == xhtml.ElementNode {
			id := attr(n, "data-id")
			if id == "" {
				id = attr(n, "data-job-id")
			}
			if id != "" {
				titleNode := findByAttr(n, "data-test", "job-title")
				title := ""
				if titleNode != nil {
					title = textContent(titleNode)
				}
				companyNode := findByAttr(n, "data-test", "employer-name")
				company := ""
				if companyNode != nil {
					company = textContent(companyNode)
				}
				locNode := findByAttr(n, "data-test", "emp-location")
				location := ""
				if locNode != nil {
					location = textContent(locNode)
				}
				if title != "" {
					jobURL := "https://www.glassdoor.com/job-listing/jl.htm?jl=" + url.QueryEscape(id)
					out = append(out, discovery.Posting{
						Source:     src,
						ExternalID: id,
						Company:    company,
						Title:      title,
						Location:   location,
						URL:        jobURL,
						Workplace:  portalWorkplace(title + " " + location),
					})
					return
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return out
}

func attr(n *xhtml.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func textContent(n *xhtml.Node) string {
	var b strings.Builder
	var walk func(*xhtml.Node)
	walk = func(curr *xhtml.Node) {
		if curr.Type == xhtml.TextNode {
			b.WriteString(curr.Data)
		}
		for c := curr.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.TrimSpace(b.String())
}

func hasClass(n *xhtml.Node, class string) bool {
	classes := strings.Fields(attr(n, "class"))
	for _, c := range classes {
		if c == class || strings.Contains(c, class) {
			return true
		}
	}
	return false
}

func findByClass(n *xhtml.Node, tag, class string) *xhtml.Node {
	var found *xhtml.Node
	var walk func(*xhtml.Node)
	walk = func(curr *xhtml.Node) {
		if found != nil {
			return
		}
		if curr.Type == xhtml.ElementNode && (tag == "" || curr.Data == tag) && hasClass(curr, class) {
			found = curr
			return
		}
		for c := curr.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return found
}

func findByAttr(n *xhtml.Node, key, val string) *xhtml.Node {
	var found *xhtml.Node
	var walk func(*xhtml.Node)
	walk = func(curr *xhtml.Node) {
		if found != nil {
			return
		}
		if curr.Type == xhtml.ElementNode && attr(curr, key) == val {
			found = curr
			return
		}
		for c := curr.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return found
}

func findByID(n *xhtml.Node, id string) *xhtml.Node {
	return findByAttr(n, "id", id)
}

func findTag(n *xhtml.Node, tag string) *xhtml.Node {
	var found *xhtml.Node
	var walk func(*xhtml.Node)
	walk = func(curr *xhtml.Node) {
		if found != nil {
			return
		}
		if curr.Type == xhtml.ElementNode && curr.Data == tag {
			found = curr
			return
		}
		for c := curr.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return found
}

func innerHTML(n *xhtml.Node) string {
	var b bytes.Buffer
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		_ = xhtml.Render(&b, c)
	}
	return b.String()
}
