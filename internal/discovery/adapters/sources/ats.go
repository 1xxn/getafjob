package sources

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
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

// conditional keeps the validators of a source's last answer (discovery.Conditional).
type conditional struct{ since discovery.Validators }

// SetValidators implements discovery.Conditional.
func (c *conditional) SetValidators(v discovery.Validators) { c.since = v }

var (
	_ discovery.Conditional = (*Greenhouse)(nil)
	_ discovery.Conditional = (*Ashby)(nil)
	_ discovery.Conditional = (*Lever)(nil)
	_ discovery.Conditional = (*Personio)(nil)

	_ discovery.Detailer = (*Softgarden)(nil)
	_ discovery.Detailer = (*Join)(nil)
	_ discovery.Detailer = (*SuccessFactors)(nil)
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
			URL       string          `json:"absolute_url"`
			Content   string          `json:"content"`
			Published string          `json:"first_published"`
			Updated   string          `json:"updated_at"`
			Pay       []greenhousePay `json:"pay_input_ranges"`
		} `json:"jobs"`
	}
	v, unchanged, err := s.c.GetJSONConditional(ctx, s.Name(), base+"/jobs",
		url.Values{"content": {"true"}, "pay_transparency": {"true"}}, s.since, &data)
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
		p := discovery.Posting{
			Source: s.Name(), ExternalID: id(j.ID), Company: boardCompany(s.company, j.Company, s.board, company), Title: strings.TrimSpace(j.Title),
			Location: location, URL: j.URL, Description: htmltext.Text(j.Content), Posted: day(or(j.Published, j.Updated)),
		}
		if sal, ok := greenhouseSalary(j.Pay); ok {
			sal.apply(&p)
		}
		out = append(out, p)
	}
	return discovery.Batch{Postings: out, Complete: true, Validators: v}, nil
}

// greenhousePay is a pay range of a Greenhouse posting (pay transparency), one per country or region.
type greenhousePay struct {
	MinCents json.Number `json:"min_cents"`
	MaxCents json.Number `json:"max_cents"`
	Currency string      `json:"currency_type"`
}

// greenhouseSalary takes the EUR range if there is one, else the first. Greenhouse names no period; only
// yearly-sized amounts count (see yearly).
func greenhouseSalary(ranges []greenhousePay) (Salary, bool) {
	if len(ranges) == 0 {
		return Salary{}, false
	}
	r := ranges[0]
	for _, x := range ranges {
		if strings.EqualFold(x.Currency, "EUR") {
			r = x
			break
		}
	}
	return yearly(amount(r.MinCents)/100, amount(r.MaxCents)/100, "", r.Currency)
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

// ashbyPay is one part of an Ashby compensation summary, e.g. a salary range per "1 YEAR".
type ashbyPay struct {
	Type     string      `json:"compensationType"`
	Interval string      `json:"interval"`
	Currency string      `json:"currencyCode"`
	Min      json.Number `json:"minValue"`
	Max      json.Number `json:"maxValue"`
}

// salary returns the yearly salary of a "Salary" part; equity and other parts give none.
func (c ashbyPay) salary() (Salary, bool) {
	if !strings.EqualFold(c.Type, "Salary") {
		return Salary{}, false
	}
	unit := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(c.Interval), "1 "))
	return yearly(amount(c.Min), amount(c.Max), unit, c.Currency)
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
			Pay         struct {
				Components []ashbyPay `json:"summaryComponents"`
			} `json:"compensation"`
		} `json:"jobs"`
	}
	u := "https://api.ashbyhq.com/posting-api/job-board/" + url.PathEscape(s.board)
	v, unchanged, err := s.c.GetJSONConditional(ctx, s.Name(), u, url.Values{"includeCompensation": {"true"}}, s.since, &data)
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
		p := discovery.Posting{
			Source: s.Name(), ExternalID: j.ID, Company: s.company, Title: strings.TrimSpace(j.Title),
			Location: join("; ", locs...), URL: j.URL, ApplyURL: j.ApplyURL, Description: desc,
			Posted: day(j.PublishedAt), Workplace: wp,
		}
		for _, c := range j.Pay.Components {
			if sal, ok := c.salary(); ok {
				sal.apply(&p)
				break
			}
		}
		out = append(out, p)
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
				Source: s.Name(), ExternalID: j.ID, Company: boardCompany(s.company, j.Company.Name, s.id, s.id),
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
			Source: s.Name(), ExternalID: pid, Company: boardCompany(s.company, p.Subcompany, s.slug, pretty(s.slug)),
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
			Source: s.Name(), ExternalID: id(j.ID), Company: boardCompany(s.company, j.Company, s.slug, pretty(s.slug)),
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
			Source: s.Name(), ExternalID: or(j.Shortcode, or(j.Code, j.URL)), Company: boardCompany(s.company, data.Name, s.slug, pretty(s.slug)),
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

// Softgarden reads the public vacancy list of a <slug>.softgarden.io site; descriptions come per job JSON-LD.
type Softgarden struct {
	c             *Client
	slug, company string
}

// Name implements discovery.Source.
func (s *Softgarden) Name() string { return "softgarden:" + s.slug }

var softgardenJobLink = regexp.MustCompile(`/job/([0-9]+)/([^/?#]+)`)

func dotDay(s string) string {
	s = strings.TrimSpace(s)
	for _, layout := range []string{"02.01.06", "02.01.2006"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format(time.DateOnly)
		}
	}
	return ""
}

func hasToken(attrVal, want string) bool {
	for tok := range strings.FieldsSeq(attrVal) {
		if tok == want {
			return true
		}
	}
	return false
}

func attrMap(z *xhtml.Tokenizer, hasAttr bool) map[string]string {
	if !hasAttr {
		return nil
	}
	attrs := map[string]string{}
	for hasAttr {
		var k, v []byte
		k, v, hasAttr = z.TagAttr()
		attrs[string(k)] = string(v)
	}
	return attrs
}

// Fetch implements discovery.Source.
func (s *Softgarden) Fetch(ctx context.Context) (discovery.Batch, error) {
	host := "https://" + s.slug + ".softgarden.io"
	b, err := s.c.GetBytes(ctx, s.Name(), host+"/de/vacancies", nil, "text/html")
	if err != nil {
		return discovery.Batch{}, err
	}
	out := parseSoftgardenVacancies(b, s.Name(), host, or(s.company, pretty(s.slug)))
	return discovery.Batch{Postings: out, Complete: true}, nil
}

func parseSoftgardenVacancies(b []byte, source, host, company string) []discovery.Posting {
	const maxRows = 2000
	if len(b) > MaxBody {
		b = b[:MaxBody]
	}
	z := xhtml.NewTokenizer(bytes.NewReader(b))
	var out []discovery.Posting
	seen := map[string]bool{}
	var cur *discovery.Posting
	var locs []string
	var jobSlug string
	inTitle, inDate, inLoc := false, false, false
	var titleBuf, dateBuf, locBuf strings.Builder

	flush := func() {
		if cur == nil {
			return
		}
		if cur.ExternalID != "" && !seen[cur.ExternalID] && cur.Title != "" {
			seen[cur.ExternalID] = true
			if cur.URL == "" {
				cur.URL = host + "/job/" + url.PathEscape(cur.ExternalID)
				if jobSlug != "" {
					cur.URL += "/" + url.PathEscape(jobSlug)
				}
			}
			cur.Location = join("; ", locs...)
			out = append(out, *cur)
		}
		cur, locs, jobSlug = nil, nil, ""
		inTitle, inDate, inLoc = false, false, false
		titleBuf.Reset()
		dateBuf.Reset()
		locBuf.Reset()
	}

	for len(out) < maxRows {
		switch z.Next() {
		case xhtml.ErrorToken:
			flush()
			return out
		case xhtml.StartTagToken:
			name, hasAttr := z.TagName()
			attrs := attrMap(z, hasAttr)
			tag := string(name)
			if idVal := attrs["id"]; strings.HasPrefix(idVal, "job_id_") {
				flush()
				ext := strings.TrimPrefix(idVal, "job_id_")
				cur = &discovery.Posting{Source: source, ExternalID: ext, Company: company}
				continue
			}
			if cur == nil {
				continue
			}
			switch {
			case tag == "a":
				if m := softgardenJobLink.FindStringSubmatch(attrs["href"]); m != nil {
					if m[1] != "" {
						cur.ExternalID = m[1]
					}
					jobSlug = m[2]
					inTitle = true
					titleBuf.Reset()
				}
			case hasToken(attrs["class"], "date") && hasToken(attrs["class"], "matchValue"):
				inDate = true
				dateBuf.Reset()
			case hasToken(attrs["class"], "location-view-item"):
				inLoc = true
				locBuf.Reset()
			}
		case xhtml.TextToken:
			if cur == nil {
				continue
			}
			txt := string(z.Text())
			if inTitle {
				titleBuf.WriteString(txt)
			}
			if inDate {
				dateBuf.WriteString(txt)
			}
			if inLoc {
				locBuf.WriteString(txt)
			}
		case xhtml.EndTagToken:
			if cur == nil {
				continue
			}
			name, _ := z.TagName()
			switch string(name) {
			case "a":
				if inTitle {
					cur.Title = strings.Join(strings.Fields(titleBuf.String()), " ")
					inTitle = false
				}
			case "div":
				if inDate {
					if d := dotDay(dateBuf.String()); d != "" {
						cur.Posted = d
					}
					inDate = false
				}
			case "span":
				if inLoc {
					if l := strings.Join(strings.Fields(locBuf.String()), " "); l != "" {
						locs = append(locs, l)
					}
					inLoc = false
				}
			}
		case xhtml.SelfClosingTagToken, xhtml.CommentToken, xhtml.DoctypeToken:
		}
	}
	flush()
	return out
}

// Details implements discovery.Detailer.
func (s *Softgarden) Details(ctx context.Context, p *discovery.Posting) error {
	b, err := s.c.GetBytes(ctx, s.Name(), p.URL, nil, "text/html")
	if err != nil {
		return err
	}
	jobs := JobPostings(b)
	if len(jobs) == 0 {
		return &discovery.SourceError{Source: s.Name(), Code: "no_jobposting"}
	}
	j := jobs[0]
	p.Description = j.Description
	p.Posted = or(p.Posted, j.Posted)
	j.Salary.apply(p)
	if p.Location == "" {
		p.Location = join("; ", j.Locations...)
	}
	if j.Remote {
		p.Workplace = discovery.WorkplaceRemote
	}
	return nil
}

// Join reads the public company job page on join.com (__NEXT_DATA__ SSR state); descriptions come from JSON-LD.
type Join struct {
	c             *Client
	slug, company string
}

const joinMaxPages = 10

// Name implements discovery.Source.
func (s *Join) Name() string { return "join:" + s.slug }

func nextDataJSON(page []byte) []byte {
	if len(page) > MaxBody {
		page = page[:MaxBody]
	}
	z := xhtml.NewTokenizer(bytes.NewReader(page))
	inNext := false
	for {
		switch z.Next() {
		case xhtml.ErrorToken:
			return nil
		case xhtml.StartTagToken:
			name, hasAttr := z.TagName()
			attrs := attrMap(z, hasAttr)
			inNext = string(name) == "script" && attrs["id"] == "__NEXT_DATA__"
		case xhtml.TextToken:
			if inNext {
				return bytes.Clone(z.Text())
			}
		case xhtml.EndTagToken, xhtml.SelfClosingTagToken, xhtml.CommentToken, xhtml.DoctypeToken:
			inNext = false
		}
	}
}

// Fetch implements discovery.Source.
func (s *Join) Fetch(ctx context.Context) (discovery.Batch, error) {
	base := "https://join.com/companies/" + url.PathEscape(s.slug)
	var out []discovery.Posting
	seen := map[string]bool{}
	pageCount := 1
	for page := 1; page <= joinMaxPages && page <= pageCount; page++ {
		var vals url.Values
		if page > 1 {
			vals = url.Values{"page": {strconv.Itoa(page)}}
		}
		b, err := s.c.GetBytes(ctx, s.Name(), base, vals, "text/html")
		if err != nil {
			return discovery.Batch{}, err
		}
		raw := nextDataJSON(b)
		if len(raw) == 0 {
			return discovery.Batch{}, &discovery.SourceError{Source: s.Name(), Code: "decode"}
		}
		var data struct {
			Props struct {
				PageProps struct {
					PageData struct {
						Company struct {
							Name string `json:"name"`
						} `json:"company"`
						Jobs struct {
							Items []struct {
								ID            json.RawMessage `json:"id"`
								IDParam       string          `json:"idParam"`
								Title         string          `json:"title"`
								CreatedAt     string          `json:"createdAt"`
								WorkplaceType string          `json:"workplaceType"`
								RemoteType    string          `json:"remoteType"`
								City          struct {
									CityName    string `json:"cityName"`
									CountryName string `json:"countryName"`
								} `json:"city"`
							} `json:"items"`
							Pagination struct {
								PageCount int `json:"pageCount"`
							} `json:"pagination"`
						} `json:"jobs"`
					} `json:"pageData"`
				} `json:"pageProps"`
			} `json:"props"`
		}
		if err := json.Unmarshal(raw, &data); err != nil {
			return discovery.Batch{}, &discovery.SourceError{Source: s.Name(), Code: "decode"}
		}
		pd := data.Props.PageProps.PageData
		if pd.Jobs.Pagination.PageCount > 0 {
			pageCount = pd.Jobs.Pagination.PageCount
		}
		comp := boardCompany(s.company, pd.Company.Name, s.slug, pretty(s.slug))
		added := 0
		for _, j := range pd.Jobs.Items {
			ext := or(j.IDParam, id(j.ID))
			if ext == "" || seen[ext] {
				continue
			}
			seen[ext] = true
			wp := workplace(j.WorkplaceType)
			if wp == discovery.WorkplaceUnknown {
				wp = workplace(j.RemoteType)
			}
			out = append(out, discovery.Posting{
				Source: s.Name(), ExternalID: ext, Company: comp, Title: strings.TrimSpace(j.Title),
				Location: join(", ", j.City.CityName, j.City.CountryName), URL: base + "/" + url.PathEscape(ext),
				Posted: day(j.CreatedAt), Workplace: wp,
			})
			added++
		}
		if added == 0 {
			break
		}
	}
	return discovery.Batch{Postings: out, Complete: pageCount <= joinMaxPages}, nil
}

// Details implements discovery.Detailer.
func (s *Join) Details(ctx context.Context, p *discovery.Posting) error {
	b, err := s.c.GetBytes(ctx, s.Name(), p.URL, nil, "text/html")
	if err != nil {
		return err
	}
	jobs := JobPostings(b)
	if len(jobs) == 0 {
		return &discovery.SourceError{Source: s.Name(), Code: "no_jobposting"}
	}
	p.Description = jobs[0].Description
	p.Posted = or(p.Posted, jobs[0].Posted)
	jobs[0].Salary.apply(p)
	return nil
}

// SuccessFactors reads SAP SuccessFactors Career Site Builder /search/ listings; descriptions come from item details.
type SuccessFactors struct {
	c                   *Client
	host, slug, company string
	location, search    string
	max                 int
}

const sfDefaultMax = 100

var sfJobPathID = regexp.MustCompile(`/job/[^?#]*/([0-9]+)/?(?:[?#].*)?$`)

// Name implements discovery.Source.
func (s *SuccessFactors) Name() string { return "successfactors:" + s.slug }

func sfDate(s string) string {
	s = strings.TrimSpace(s)
	if d := day(s); d != "" {
		return d
	}
	for _, layout := range []string{time.UnixDate, time.RFC1123, time.RFC1123Z, "Mon Jan 2 15:04:05 MST 2006"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format(time.DateOnly)
		}
	}
	return ""
}

// Fetch implements discovery.Source.
func (s *SuccessFactors) Fetch(ctx context.Context) (discovery.Batch, error) {
	limit := s.max
	if limit <= 0 {
		limit = sfDefaultMax
	}
	var out []discovery.Posting
	seen := map[string]bool{}
	for start := 0; len(out) < limit; start += 25 {
		vals := url.Values{
			"q":              {s.search},
			"locationsearch": {s.location},
			"startrow":       {strconv.Itoa(start)},
		}
		b, err := s.c.GetBytes(ctx, s.Name(), "https://"+s.host+"/search/", vals, "text/html")
		if err != nil {
			return discovery.Batch{}, err
		}
		rows := parseSFSearch(b)
		added := 0
		for _, r := range rows {
			if seen[r.extID] {
				continue
			}
			seen[r.extID] = true
			out = append(out, discovery.Posting{
				Source: s.Name(), ExternalID: r.extID, Company: s.company, Title: r.title,
				Location: or(r.location, s.location), URL: "https://" + s.host + r.path,
				Extra: map[string]string{"path": r.path},
			})
			added++
			if len(out) >= limit {
				return discovery.Batch{Postings: out, Complete: false}, nil
			}
		}
		if added == 0 {
			return discovery.Batch{Postings: out, Complete: true}, nil
		}
	}
	return discovery.Batch{Postings: out, Complete: len(out) < limit}, nil
}

type sfRow struct {
	extID, path, title, location string
}

func parseSFSearch(b []byte) []sfRow {
	if len(b) > MaxBody {
		b = b[:MaxBody]
	}
	z := xhtml.NewTokenizer(bytes.NewReader(b))
	var rows []sfRow
	var cur *sfRow
	inTitle, inLoc := false, false
	locDepth := 0
	var titleBuf, locBuf strings.Builder

	flush := func() {
		if cur != nil && cur.extID != "" && cur.title != "" {
			rows = append(rows, *cur)
		}
		cur = nil
		inTitle, inLoc = false, false
		locDepth = 0
		titleBuf.Reset()
		locBuf.Reset()
	}

	for len(rows) < 200 {
		switch z.Next() {
		case xhtml.ErrorToken:
			flush()
			return rows
		case xhtml.StartTagToken:
			name, hasAttr := z.TagName()
			attrs := attrMap(z, hasAttr)
			tag := string(name)
			cls := attrs["class"]
			if tag == "a" && hasToken(cls, "jobTitle-link") {
				if m := sfJobPathID.FindStringSubmatch(attrs["href"]); m != nil {
					cleanPath := strings.SplitN(attrs["href"], "?", 2)[0]
					cleanPath = strings.SplitN(cleanPath, "#", 2)[0]
					if strings.HasPrefix(cleanPath, "/") {
						flush()
						cur = &sfRow{extID: m[1], path: cleanPath}
						inTitle = true
						titleBuf.Reset()
						continue
					}
				}
			}
			if cur != nil && !inLoc && cur.location == "" {
				if (hasToken(cls, "jobLocation") && !hasToken(cls, "sort")) || strings.HasSuffix(attrs["id"], "-section-city-value") {
					inLoc = true
					locDepth = 1
					locBuf.Reset()
					continue
				}
			}
			if inLoc {
				locDepth++
			}
		case xhtml.TextToken:
			if inTitle {
				titleBuf.Write(z.Text())
			} else if inLoc {
				locBuf.Write(z.Text())
			}
		case xhtml.EndTagToken:
			name, _ := z.TagName()
			if inTitle && string(name) == "a" && cur != nil {
				cur.title = strings.Join(strings.Fields(titleBuf.String()), " ")
				inTitle = false
			}
			if inLoc {
				locDepth--
				if locDepth <= 0 {
					if cur != nil {
						cur.location = strings.Join(strings.Fields(locBuf.String()), " ")
					}
					inLoc = false
				}
			}
		case xhtml.SelfClosingTagToken, xhtml.CommentToken, xhtml.DoctypeToken:
		}
	}
	flush()
	return rows
}

// Details implements discovery.Detailer.
func (s *SuccessFactors) Details(ctx context.Context, p *discovery.Posting) error {
	path := p.Extra["path"]
	if !strings.HasPrefix(path, "/") || strings.Contains(path, "..") {
		return &discovery.SourceError{Source: s.Name(), Code: "path"}
	}
	b, err := s.c.GetBytes(ctx, s.Name(), "https://"+s.host+path, nil, "text/html")
	if err != nil {
		return err
	}
	if jobs := JobPostings(b); len(jobs) > 0 && jobs[0].Description != "" {
		p.Description = jobs[0].Description
		p.Posted = or(p.Posted, jobs[0].Posted)
		jobs[0].Salary.apply(p)
		return nil
	}
	desc, posted := parseSFDetail(b)
	if desc == "" {
		return &discovery.SourceError{Source: s.Name(), Code: "no_jobposting"}
	}
	p.Description = desc
	p.Posted = or(p.Posted, posted)
	return nil
}

func parseSFDetail(b []byte) (desc, posted string) {
	if len(b) > MaxBody {
		b = b[:MaxBody]
	}
	z := xhtml.NewTokenizer(bytes.NewReader(b))
	descDepth := 0
	var raw strings.Builder
	for {
		switch z.Next() {
		case xhtml.ErrorToken:
			return htmltext.Text(raw.String()), posted
		case xhtml.StartTagToken:
			name, hasAttr := z.TagName()
			attrs := attrMap(z, hasAttr)
			tag := string(name)
			if attrs["itemprop"] == "datePosted" && posted == "" {
				posted = sfDate(attrs["content"])
			}
			if descDepth == 0 && attrs["itemprop"] == "description" {
				if tag == "meta" {
					raw.WriteString(attrs["content"])
					continue
				}
				descDepth = 1
				continue
			}
			if descDepth > 0 {
				if tag != "meta" && tag != "br" && tag != "hr" && tag != "img" && tag != "input" {
					descDepth++
				}
				raw.Write(z.Raw())
			}
		case xhtml.SelfClosingTagToken:
			_, hasAttr := z.TagName()
			attrs := attrMap(z, hasAttr)
			if attrs["itemprop"] == "datePosted" && posted == "" {
				posted = sfDate(attrs["content"])
			}
			if descDepth == 0 && attrs["itemprop"] == "description" {
				raw.WriteString(attrs["content"])
			} else if descDepth > 0 {
				raw.Write(z.Raw())
			}
		case xhtml.TextToken:
			if descDepth > 0 {
				raw.Write(z.Raw())
			}
		case xhtml.EndTagToken:
			if descDepth > 0 {
				descDepth--
				if descDepth > 0 {
					raw.Write(z.Raw())
				} else if desc == "" {
					desc = htmltext.Text(raw.String())
				}
			}
		case xhtml.CommentToken, xhtml.DoctypeToken:
		}
	}
}
