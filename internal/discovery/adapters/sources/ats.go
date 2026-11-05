package sources

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/1xxn/getafjob/internal/discovery"
	"github.com/1xxn/getafjob/internal/platform/htmltext"
)

// conditional keeps the validators of a source's last answer (discovery.Conditional).
type conditional struct{ since discovery.Validators }

// SetValidators implements discovery.Conditional.
func (c *conditional) SetValidators(v discovery.Validators) { c.since = v }

var (
	_ discovery.Conditional = (*Greenhouse)(nil)
	_ discovery.Conditional = (*Ashby)(nil)
	_ discovery.Conditional = (*Lever)(nil)
	_ discovery.Conditional = (*Personio)(nil)
)

// Greenhouse reads boards-api.greenhouse.io.
type Greenhouse struct {
	c *Client
	conditional
	board, company string
}

// Name implements discovery.Source.
func (s *Greenhouse) Name() string { return "greenhouse:" + s.board }

// Fetch implements discovery.Source.
func (s *Greenhouse) Fetch(ctx context.Context) (discovery.Batch, error) {
	base := "https://boards-api.greenhouse.io/v1/boards/" + url.PathEscape(s.board)
	company := s.company
	if company == "" {
		var meta struct {
			Name string `json:"name"`
		}
		_ = s.c.GetJSON(ctx, s.Name(), base, nil, nil, &meta) // the name is optional
		company = or(meta.Name, pretty(s.board))
	}
	var data struct {
		Jobs []struct {
			ID       json.RawMessage `json:"id"`
			Title    string          `json:"title"`
			Company  string          `json:"company_name"`
			Location struct {
				Name string `json:"name"`
			} `json:"location"`
			Offices []struct {
				Name string `json:"name"`
			} `json:"offices"`
			URL       string `json:"absolute_url"`
			Content   string `json:"content"`
			Published string `json:"first_published"`
			Updated   string `json:"updated_at"`
		} `json:"jobs"`
	}
	v, unchanged, err := s.c.GetJSONConditional(ctx, s.Name(), base+"/jobs", url.Values{"content": {"true"}}, s.since, &data)
	if err != nil || unchanged {
		return discovery.Batch{NotModified: unchanged, Validators: v}, err
	}
	out := make([]discovery.Posting, 0, len(data.Jobs))
	for _, j := range data.Jobs {
		location := j.Location.Name
		var offices []string
		for _, o := range j.Offices {
			offices = append(offices, o.Name)
		}
		if o := join(", ", offices...); o != "" && !strings.Contains(location, o) {
			location = join("; ", location, o)
		}
		out = append(out, discovery.Posting{
			Source: s.Name(), ExternalID: id(j.ID), Company: or(j.Company, company), Title: strings.TrimSpace(j.Title),
			Location: location, URL: j.URL, Description: htmltext.Text(j.Content), Posted: day(or(j.Published, j.Updated)),
		})
	}
	return discovery.Batch{Postings: out, Complete: true, Validators: v}, nil
}

// Ashby reads the public Ashby job board API.
type Ashby struct {
	c *Client
	conditional
	board, company string
}

// Name implements discovery.Source.
func (s *Ashby) Name() string { return "ashby:" + s.board }

var workplaces = map[string]discovery.Workplace{
	"remote": discovery.WorkplaceRemote, "hybrid": discovery.WorkplaceHybrid,
	"onsite": discovery.WorkplaceOnsite, "on-site": discovery.WorkplaceOnsite, "on_site": discovery.WorkplaceOnsite,
}

func workplace(s string) discovery.Workplace {
	return workplaces[strings.ToLower(strings.TrimSpace(s))]
}

// Fetch implements discovery.Source.
func (s *Ashby) Fetch(ctx context.Context) (discovery.Batch, error) {
	var data struct {
		Jobs []struct {
			ID          string                      `json:"id"`
			Title       string                      `json:"title"`
			Location    string                      `json:"location"`
			Secondary   []struct{ Location string } `json:"secondaryLocations"`
			Workplace   string                      `json:"workplaceType"`
			Remote      bool                        `json:"isRemote"`
			Listed      *bool                       `json:"isListed"`
			URL         string                      `json:"jobUrl"`
			ApplyURL    string                      `json:"applyUrl"`
			Plain       string                      `json:"descriptionPlain"`
			HTML        string                      `json:"descriptionHtml"`
			PublishedAt string                      `json:"publishedAt"`
		} `json:"jobs"`
	}
	u := "https://api.ashbyhq.com/posting-api/job-board/" + url.PathEscape(s.board)
	v, unchanged, err := s.c.GetJSONConditional(ctx, s.Name(), u, nil, s.since, &data)
	if err != nil || unchanged {
		return discovery.Batch{NotModified: unchanged, Validators: v}, err
	}
	out := make([]discovery.Posting, 0, len(data.Jobs))
	for _, j := range data.Jobs {
		if j.Listed != nil && !*j.Listed {
			continue
		}
		locs := []string{j.Location}
		for _, l := range j.Secondary {
			locs = append(locs, l.Location)
		}
		wp := workplace(j.Workplace)
		if wp == "" && j.Remote {
			wp = discovery.WorkplaceRemote
		}
		desc := strings.TrimSpace(j.Plain)
		if desc == "" {
			desc = htmltext.Text(j.HTML)
		}
		out = append(out, discovery.Posting{
			Source: s.Name(), ExternalID: j.ID, Company: s.company, Title: strings.TrimSpace(j.Title),
			Location: join("; ", locs...), URL: j.URL, ApplyURL: j.ApplyURL, Description: desc,
			Posted: day(j.PublishedAt), Workplace: wp,
		})
	}
	return discovery.Batch{Postings: out, Complete: true, Validators: v}, nil
}

// Lever reads the Lever postings API (global and EU hosts).
type Lever struct {
	c *Client
	conditional
	slug, company string
}

// Name implements discovery.Source.
func (s *Lever) Name() string { return "lever:" + s.slug }

// Fetch implements discovery.Source.
func (s *Lever) Fetch(ctx context.Context) (discovery.Batch, error) {
	type posting struct {
		ID         string `json:"id"`
		Text       string `json:"text"`
		Categories struct {
			Location     string   `json:"location"`
			AllLocations []string `json:"allLocations"`
		} `json:"categories"`
		HostedURL string      `json:"hostedUrl"`
		ApplyURL  string      `json:"applyUrl"`
		Plain     string      `json:"descriptionPlain"`
		Workplace string      `json:"workplaceType"`
		CreatedAt json.Number `json:"createdAt"`
		Lists     []struct {
			Text    string `json:"text"`
			Content string `json:"content"`
		} `json:"lists"`
	}
	var data []posting
	var err error
	var v discovery.Validators
	var unchanged bool
	for _, host := range []string{"api.lever.co", "api.eu.lever.co"} {
		u := "https://" + host + "/v0/postings/" + url.PathEscape(s.slug)
		v, unchanged, err = s.c.GetJSONConditional(ctx, s.Name(), u, url.Values{"mode": {"json"}}, s.since, &data)
		if Status(err) != http.StatusNotFound {
			break
		}
	}
	if err != nil || unchanged {
		return discovery.Batch{NotModified: unchanged, Validators: v}, err
	}
	out := make([]discovery.Posting, 0, len(data))
	for _, j := range data {
		locs := j.Categories.AllLocations
		if len(locs) == 0 {
			locs = []string{j.Categories.Location}
		}
		parts := []string{strings.TrimSpace(j.Plain)}
		for _, l := range j.Lists {
			parts = append(parts, strings.TrimSpace(l.Text+"\n"+htmltext.Text(l.Content)))
		}
		out = append(out, discovery.Posting{
			Source: s.Name(), ExternalID: j.ID, Company: s.company, Title: strings.TrimSpace(j.Text),
			Location: join("; ", locs...), URL: j.HostedURL, ApplyURL: j.ApplyURL, Description: join("\n\n", parts...),
			Posted: epochDay(j.CreatedAt), Workplace: workplace(j.Workplace),
		})
	}
	return discovery.Batch{Postings: out, Complete: true, Validators: v}, nil
}

// SmartRecruiters reads the SmartRecruiters posting API; descriptions come per posting (Details).
type SmartRecruiters struct {
	c                    *Client
	id, company, country string
	max                  int
}

// smartRecruitersMax caps one company's list unless the board config sets "max".
const smartRecruitersMax = 2000

// Name implements discovery.Source.
func (s *SmartRecruiters) Name() string { return "smartrecruiters:" + s.id }

func (s *SmartRecruiters) api() string {
	return "https://api.smartrecruiters.com/v1/companies/" + url.PathEscape(s.id) + "/postings"
}

// Fetch implements discovery.Source.
func (s *SmartRecruiters) Fetch(ctx context.Context) (discovery.Batch, error) {
	var out []discovery.Posting
	for offset := 0; ; {
		var data struct {
			Total   int `json:"totalFound"`
			Content []struct {
				ID       string `json:"id"`
				Name     string `json:"name"`
				Released string `json:"releasedDate"`
				Company  struct {
					Name string `json:"name"`
				} `json:"company"`
				Location struct {
					Full    string `json:"fullLocation"`
					City    string `json:"city"`
					Country string `json:"country"`
					Remote  bool   `json:"remote"`
					Hybrid  bool   `json:"hybrid"`
				} `json:"location"`
			} `json:"content"`
		}
		q := url.Values{"limit": {"100"}, "offset": {strconv.Itoa(offset)}}
		if s.country != "" {
			q.Set("country", s.country)
		}
		if err := s.c.GetJSON(ctx, s.Name(), s.api(), q, nil, &data); err != nil {
			return discovery.Batch{}, err
		}
		for _, j := range data.Content {
			wp := discovery.WorkplaceUnknown
			switch {
			case j.Location.Remote:
				wp = discovery.WorkplaceRemote
			case j.Location.Hybrid:
				wp = discovery.WorkplaceHybrid
			}
			out = append(out, discovery.Posting{
				Source: s.Name(), ExternalID: j.ID, Company: or(s.company, or(j.Company.Name, s.id)),
				Title: strings.TrimSpace(j.Name), Location: or(j.Location.Full, join(", ", j.Location.City, j.Location.Country)),
				URL:    "https://jobs.smartrecruiters.com/" + url.PathEscape(s.id) + "/" + url.PathEscape(j.ID),
				Posted: day(j.Released), Workplace: wp,
			})
		}
		offset += len(data.Content)
		if len(data.Content) == 0 || offset >= data.Total {
			return discovery.Batch{Postings: out, Complete: true}, nil
		}
		if limit := s.max; offset >= smartRecruitersMax || (limit > 0 && offset >= limit) {
			return discovery.Batch{Postings: out}, nil
		}
	}
}

// Details implements discovery.Detailer.
func (s *SmartRecruiters) Details(ctx context.Context, p *discovery.Posting) error {
	var data struct {
		ApplyURL string `json:"applyUrl"`
		JobAd    struct {
			Sections map[string]json.RawMessage `json:"sections"`
		} `json:"jobAd"`
	}
	if err := s.c.GetJSON(ctx, s.Name(), s.api()+"/"+url.PathEscape(p.ExternalID), nil, nil, &data); err != nil {
		return err
	}
	var parts []string
	for _, key := range []string{"companyDescription", "jobDescription", "qualifications", "additionalInformation"} {
		var sec struct{ Title, Text string }
		if raw, ok := data.JobAd.Sections[key]; ok && json.Unmarshal(raw, &sec) == nil {
			parts = append(parts, strings.TrimSpace(sec.Title+"\n"+htmltext.Text(sec.Text)))
		}
	}
	p.Description = join("\n\n", parts...)
	p.ApplyURL = or(data.ApplyURL, p.ApplyURL)
	return nil
}

// Personio reads the XML feed of a Personio career page.
type Personio struct {
	c *Client
	conditional
	slug, company string
}

// Name implements discovery.Source.
func (s *Personio) Name() string { return "personio:" + s.slug }

type personioFeed struct {
	Positions []struct {
		ID         string   `xml:"id"`
		Subcompany string   `xml:"subcompany"`
		Office     string   `xml:"office"`
		Offices    []string `xml:"additionalOffices>office"`
		Name       string   `xml:"name"`
		CreatedAt  string   `xml:"createdAt"`
		Seniority  string   `xml:"seniority"`
		Years      string   `xml:"yearsOfExperience"`
		Texts      []struct {
			Name  string `xml:"name"`
			Value string `xml:"value"`
		} `xml:"jobDescriptions>jobDescription"`
	} `xml:"position"`
}

// Fetch implements discovery.Source.
func (s *Personio) Fetch(ctx context.Context) (discovery.Batch, error) {
	host := "https://" + s.slug + ".jobs.personio.de"
	b, v, unchanged, err := s.c.GetConditional(ctx, s.Name(), host+"/xml", url.Values{"language": {"de"}}, "application/xml", s.since)
	if err != nil || unchanged {
		return discovery.Batch{NotModified: unchanged, Validators: v}, err
	}
	var feed personioFeed
	// encoding/xml expands no external entities and no DTD entities.
	if err := xml.Unmarshal(b, &feed); err != nil {
		return discovery.Batch{}, &discovery.SourceError{Source: s.Name(), Code: "decode"}
	}
	out := make([]discovery.Posting, 0, len(feed.Positions))
	for _, p := range feed.Positions {
		pid := strings.TrimSpace(p.ID)
		var texts []string
		for _, t := range p.Texts {
			texts = append(texts, strings.TrimSpace(t.Name+"\n"+htmltext.Text(t.Value)))
		}
		extra := map[string]string{}
		if v := strings.TrimSpace(p.Seniority); v != "" {
			extra["seniority"] = v
		}
		if v := strings.TrimSpace(p.Years); v != "" {
			extra["years"] = v
		}
		if len(extra) == 0 {
			extra = nil
		}
		out = append(out, discovery.Posting{
			Source: s.Name(), ExternalID: pid, Company: or(s.company, or(p.Subcompany, pretty(s.slug))),
			Title: strings.TrimSpace(p.Name), Location: join("; ", append([]string{p.Office}, p.Offices...)...),
			URL: host + "/job/" + url.PathEscape(pid), Description: join("\n\n", texts...), Posted: day(p.CreatedAt),
			Extra: extra,
		})
	}
	return discovery.Batch{Postings: out, Complete: true, Validators: v}, nil
}

// Recruitee reads the offers API of a Recruitee career site.
type Recruitee struct {
	c             *Client
	slug, company string
}

// Name implements discovery.Source.
func (s *Recruitee) Name() string { return "recruitee:" + s.slug }

// Fetch implements discovery.Source.
func (s *Recruitee) Fetch(ctx context.Context) (discovery.Batch, error) {
	var data struct {
		Offers []struct {
			ID           json.RawMessage `json:"id"`
			Status       string          `json:"status"`
			Title        string          `json:"title"`
			Company      string          `json:"company_name"`
			City         string          `json:"city"`
			Country      string          `json:"country"`
			Location     string          `json:"location"`
			URL          string          `json:"careers_url"`
			ApplyURL     string          `json:"careers_apply_url"`
			Description  string          `json:"description"`
			Requirements string          `json:"requirements"`
			Published    string          `json:"published_at"`
			Remote       bool            `json:"remote"`
			Hybrid       bool            `json:"hybrid"`
		} `json:"offers"`
	}
	if err := s.c.GetJSON(ctx, s.Name(), "https://"+s.slug+".recruitee.com/api/offers/", nil, nil, &data); err != nil {
		return discovery.Batch{}, err
	}
	out := make([]discovery.Posting, 0, len(data.Offers))
	for _, j := range data.Offers {
		if j.Status != "" && j.Status != "published" {
			continue
		}
		wp := discovery.WorkplaceUnknown
		switch {
		case j.Remote:
			wp = discovery.WorkplaceRemote
		case j.Hybrid:
			wp = discovery.WorkplaceHybrid
		}
		out = append(out, discovery.Posting{
			Source: s.Name(), ExternalID: id(j.ID), Company: or(s.company, or(j.Company, pretty(s.slug))),
			Title: strings.TrimSpace(j.Title), Location: or(join(", ", j.City, j.Country), j.Location),
			URL: j.URL, ApplyURL: j.ApplyURL, Description: htmltext.Text(j.Description + "\n" + j.Requirements),
			Posted: day(j.Published), Workplace: wp,
		})
	}
	return discovery.Batch{Postings: out, Complete: true}, nil
}

// Workable reads the Workable widget API.
type Workable struct {
	c             *Client
	slug, company string
}

// Name implements discovery.Source.
func (s *Workable) Name() string { return "workable:" + s.slug }

// Fetch implements discovery.Source.
func (s *Workable) Fetch(ctx context.Context) (discovery.Batch, error) {
	var data struct {
		Name string `json:"name"`
		Jobs []struct {
			Shortcode   string `json:"shortcode"`
			Code        string `json:"code"`
			Title       string `json:"title"`
			City        string `json:"city"`
			Country     string `json:"country"`
			URL         string `json:"url"`
			ApplyURL    string `json:"application_url"`
			Description string `json:"description"`
			Published   string `json:"published_on"`
			Remote      any    `json:"telecommuting"`
		} `json:"jobs"`
	}
	u := "https://apply.workable.com/api/v1/widget/accounts/" + url.PathEscape(s.slug)
	if err := s.c.GetJSON(ctx, s.Name(), u, url.Values{"details": {"true"}}, nil, &data); err != nil {
		return discovery.Batch{}, err
	}
	out := make([]discovery.Posting, 0, len(data.Jobs))
	for _, j := range data.Jobs {
		wp := discovery.WorkplaceUnknown
		if r, ok := j.Remote.(bool); (ok && r) || j.Remote == "true" {
			wp = discovery.WorkplaceRemote
		}
		out = append(out, discovery.Posting{
			Source: s.Name(), ExternalID: or(j.Shortcode, or(j.Code, j.URL)), Company: or(s.company, or(data.Name, pretty(s.slug))),
			Title: strings.TrimSpace(j.Title), Location: join(", ", j.City, j.Country), URL: j.URL, ApplyURL: j.ApplyURL,
			Description: htmltext.Text(j.Description), Posted: day(j.Published), Workplace: wp,
		})
	}
	return discovery.Batch{Postings: out, Complete: true}, nil
}

// Workday reads the public job search of a Workday site; descriptions come per posting (Details).
type Workday struct {
	c                                   *Client
	host, tenant, site, search, company string
	max                                 int
}

// workdayMax caps one site's list unless the board config sets "max".
const workdayMax = 400

// Name implements discovery.Source.
func (s *Workday) Name() string { return "workday:" + s.tenant + "/" + s.site }

func (s *Workday) api() string {
	return "https://" + s.host + "/wday/cxs/" + url.PathEscape(s.tenant) + "/" + url.PathEscape(s.site)
}

// Fetch implements discovery.Source.
func (s *Workday) Fetch(ctx context.Context) (discovery.Batch, error) {
	limit := s.max
	if limit <= 0 {
		limit = workdayMax
	}
	var out []discovery.Posting
	total := -1
	for offset := 0; total < 0 || offset < total; {
		var data struct {
			Total    int `json:"total"`
			Postings []struct {
				Title     string `json:"title"`
				Path      string `json:"externalPath"`
				Locations string `json:"locationsText"`
				PostedOn  string `json:"postedOn"`
			} `json:"jobPostings"`
		}
		body := map[string]any{"appliedFacets": map[string]any{}, "limit": 20, "offset": offset, "searchText": s.search}
		if err := s.c.PostJSON(ctx, s.Name(), s.api()+"/jobs", body, &data); err != nil {
			return discovery.Batch{}, err
		}
		if total < 0 {
			total = data.Total // later pages report 0
		}
		if len(data.Postings) == 0 {
			break
		}
		for _, j := range data.Postings {
			if !strings.HasPrefix(j.Path, "/") {
				continue
			}
			ext := j.Path[strings.LastIndex(j.Path, "_")+1:]
			out = append(out, discovery.Posting{
				Source: s.Name(), ExternalID: or(ext, j.Path), Company: s.company, Title: strings.TrimSpace(j.Title),
				Location: j.Locations, URL: "https://" + s.host + "/" + s.site + j.Path,
				Extra: map[string]string{"path": j.Path},
			})
		}
		offset += len(data.Postings)
		if offset >= limit {
			return discovery.Batch{Postings: out, Complete: offset >= total}, nil
		}
	}
	return discovery.Batch{Postings: out, Complete: true}, nil
}

// Details implements discovery.Detailer.
func (s *Workday) Details(ctx context.Context, p *discovery.Posting) error {
	path := p.Extra["path"]
	if !strings.HasPrefix(path, "/") || strings.Contains(path, "..") {
		return &discovery.SourceError{Source: s.Name(), Code: "path"}
	}
	var data struct {
		Info struct {
			Description string `json:"jobDescription"`
			RemoteType  string `json:"remoteType"`
		} `json:"jobPostingInfo"`
	}
	if err := s.c.GetJSON(ctx, s.Name(), s.api()+path, nil, nil, &data); err != nil {
		return err
	}
	// The location stays the one from the list: later crawls only see that one and would report a change.
	p.Description = htmltext.Text(data.Info.Description)
	switch rt := strings.ToLower(data.Info.RemoteType); {
	case strings.Contains(rt, "remote"):
		p.Workplace = discovery.WorkplaceRemote
	case strings.Contains(rt, "hybrid"):
		p.Workplace = discovery.WorkplaceHybrid
	}
	return nil
}
