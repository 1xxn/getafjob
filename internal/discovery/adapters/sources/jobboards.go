package sources

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/1xxn/getafjob/internal/discovery"
	"github.com/1xxn/getafjob/internal/platform/htmltext"
)

// Job boards list jobs of many companies. Their searches are capped, so a missing job is not a closed one: they
// report Complete only when every page was read.

// Arbeitnow reads the free arbeitnow.com job board API (Germany and neighboring countries).
type Arbeitnow struct {
	c        *Client
	MaxPages int
	Pause    time.Duration // the API throttles fast page sequences
}

// NewArbeitnow returns the Arbeitnow source.
func NewArbeitnow(c *Client, maxPages int) *Arbeitnow {
	return &Arbeitnow{c: c, MaxPages: maxPages, Pause: 2 * time.Second}
}

// Name implements discovery.Source.
func (s *Arbeitnow) Name() string { return "arbeitnow" }

// arbeitnowCountries maps the country site in a job URL to the country, which locations often leave out.
var arbeitnowCountries = map[string]struct {
	name  string
	known *regexp.Regexp
}{
	"arbeitnow.com":   {"Germany", regexp.MustCompile(`(?i)germany|deutschland`)},
	"arbeitnow.de":    {"Germany", regexp.MustCompile(`(?i)germany|deutschland`)},
	"arbeitnow.at":    {"Austria", regexp.MustCompile(`(?i)austria|österreich`)},
	"arbeitnow.ch":    {"Switzerland", regexp.MustCompile(`(?i)switzerland|schweiz|suisse`)},
	"arbeitnow.nl":    {"Netherlands", regexp.MustCompile(`(?i)netherlands|nederland`)},
	"arbeitnow.fr":    {"France", regexp.MustCompile(`(?i)france|frankreich`)},
	"arbeitnow.co.uk": {"United Kingdom", regexp.MustCompile(`(?i)united kingdom|\buk\b|england|scotland|wales`)},
}

func arbeitnowLocation(location, raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return location
	}
	c, ok := arbeitnowCountries[strings.TrimPrefix(u.Hostname(), "www.")]
	if !ok || c.known.MatchString(location) {
		return location
	}
	return join(", ", location, c.name)
}

// Fetch implements discovery.Source.
func (s *Arbeitnow) Fetch(ctx context.Context) (discovery.Batch, error) {
	seen := map[string]int{}
	var out []discovery.Posting
	for page := 1; page <= s.MaxPages; page++ {
		if page > 1 {
			if err := s.c.sleep(ctx, s.Pause); err != nil {
				return discovery.Batch{}, err
			}
		}
		var data struct {
			Data []struct {
				Slug        string      `json:"slug"`
				Company     string      `json:"company_name"`
				Title       string      `json:"title"`
				Description string      `json:"description"`
				Remote      bool        `json:"remote"`
				URL         string      `json:"url"`
				Types       []string    `json:"job_types"`
				Location    string      `json:"location"`
				Created     json.Number `json:"created_at"`
			} `json:"data"`
			Links struct {
				Next string `json:"next"`
			} `json:"links"`
		}
		err := s.c.GetJSON(ctx, s.Name(), "https://www.arbeitnow.com/api/job-board-api",
			url.Values{"page": {strconv.Itoa(page)}}, nil, &data)
		if Status(err) == http.StatusTooManyRequests && len(out) > 0 {
			return discovery.Batch{Postings: out}, nil // throttled: keep what we have
		}
		if err != nil {
			return discovery.Batch{}, err
		}
		for _, j := range data.Data {
			wp := discovery.WorkplaceUnknown
			switch {
			case j.Remote:
				wp = discovery.WorkplaceRemote
			case strings.Contains(strings.ToLower(strings.Join(j.Types, " ")), "hybrid"):
				wp = discovery.WorkplaceHybrid
			}
			p := discovery.Posting{
				Source: s.Name(), ExternalID: or(j.Slug, j.URL), Company: strings.TrimSpace(j.Company),
				Title: strings.TrimSpace(j.Title), Location: arbeitnowLocation(strings.TrimSpace(j.Location), j.URL),
				URL: j.URL, Description: htmltext.Text(j.Description), Posted: epochDay(j.Created), Workplace: wp,
			}
			if i, dup := seen[p.ExternalID]; dup {
				out[i] = p
				continue
			}
			seen[p.ExternalID] = len(out)
			out = append(out, p)
		}
		if data.Links.Next == "" {
			return discovery.Batch{Postings: out, Complete: true}, nil
		}
	}
	return discovery.Batch{Postings: out}, nil // page limit reached
}

// Himalayas searches remote jobs on himalayas.app that are open to a country.
type Himalayas struct {
	c        *Client
	Queries  []string
	Country  string
	MaxPages int
}

// NewHimalayas returns the Himalayas source.
func NewHimalayas(c *Client, queries []string, country string) *Himalayas {
	return &Himalayas{c: c, Queries: queries, Country: country, MaxPages: 5}
}

// Name implements discovery.Source.
func (s *Himalayas) Name() string { return "himalayas" }

// Fetch implements discovery.Source.
func (s *Himalayas) Fetch(ctx context.Context) (discovery.Batch, error) {
	seen := map[string]bool{}
	var out []discovery.Posting
	for _, q := range s.Queries {
		for page := 1; page <= s.MaxPages; page++ {
			var data struct {
				Jobs []struct {
					GUID         string      `json:"guid"`
					Title        string      `json:"title"`
					Company      string      `json:"companyName"`
					Link         string      `json:"applicationLink"`
					Description  string      `json:"description"`
					PubDate      json.Number `json:"pubDate"`
					Restrictions []string    `json:"locationRestrictions"`
				} `json:"jobs"`
			}
			err := s.c.GetJSON(ctx, s.Name(), "https://himalayas.app/jobs/api/search",
				url.Values{"q": {q}, "country": {s.Country}, "page": {strconv.Itoa(page)}}, nil, &data)
			if err != nil {
				return discovery.Batch{}, err
			}
			for _, j := range data.Jobs {
				ext := or(j.GUID, j.Link)
				if seen[ext] {
					continue
				}
				seen[ext] = true
				region := join(", ", s.Country, "Worldwide")
				if len(j.Restrictions) > 0 {
					region = join(", ", j.Restrictions[:min(6, len(j.Restrictions))]...)
				}
				out = append(out, discovery.Posting{
					Source: s.Name(), ExternalID: ext, Company: strings.TrimSpace(j.Company),
					Title: strings.TrimSpace(j.Title), Location: "Remote (" + region + ")", URL: or(j.Link, j.GUID),
					Description: htmltext.Text(j.Description), Posted: epochDay(j.PubDate), Workplace: discovery.WorkplaceRemote,
				})
			}
			if len(data.Jobs) < 20 {
				break
			}
		}
	}
	return discovery.Batch{Postings: out}, nil // search results are capped
}

// Arbeitsagentur searches the job board of the German Federal Employment Agency; descriptions come per job
// (Details).
type Arbeitsagentur struct {
	c            *Client
	Queries      []string
	Cities       []string
	Remote       bool // also search jobs with home office
	RadiusKm     int
	SkipAgencies bool // leave out temporary work and placement agencies
	MaxPages     int
	Days         int
}

// NewArbeitsagentur returns the Arbeitsagentur source.
func NewArbeitsagentur(c *Client, queries, cities []string) *Arbeitsagentur {
	return &Arbeitsagentur{c: c, Queries: queries, Cities: cities, RadiusKm: 30, MaxPages: 2, Days: 28}
}

const arbeitsagenturAPI = "https://rest.arbeitsagentur.de/jobboerse/jobsuche-service"

// arbeitsagenturKey is the public client id of the job search API, documented at jobsuche.api.bund.dev.
var arbeitsagenturKey = http.Header{"X-Api-Key": {"jobboerse-jobsuche"}}

// Name implements discovery.Source.
func (s *Arbeitsagentur) Name() string { return "arbeitsagentur" }

// Fetch implements discovery.Source.
func (s *Arbeitsagentur) Fetch(ctx context.Context) (discovery.Batch, error) {
	type search struct {
		query  string
		where  url.Values
		remote bool
	}
	var searches []search
	for _, q := range s.Queries {
		for _, city := range s.Cities {
			searches = append(searches, search{q, url.Values{"wo": {city}, "umkreis": {strconv.Itoa(s.RadiusKm)}}, false})
		}
		if s.Remote {
			searches = append(searches, search{q, url.Values{"arbeitszeit": {"ho"}}, true})
		}
	}
	seen := map[string]bool{}
	var out []discovery.Posting
	for _, se := range searches {
		for page := 1; page <= s.MaxPages; page++ {
			q := url.Values{"was": {se.query}, "angebotsart": {"1"}, "veroeffentlichtseit": {strconv.Itoa(s.Days)},
				"size": {"100"}, "page": {strconv.Itoa(page)}}
			for k, v := range se.where {
				q[k] = v
			}
			if s.SkipAgencies {
				q.Set("zeitarbeit", "false")
				q.Set("pav", "false")
			}
			var data struct {
				List   []arbeitsagenturJob `json:"ergebnisliste"`
				Offers []arbeitsagenturJob `json:"stellenangebote"`
			}
			if err := s.c.GetJSON(ctx, s.Name(), arbeitsagenturAPI+"/pc/v6/jobs", q, arbeitsagenturKey, &data); err != nil {
				return discovery.Batch{}, err
			}
			results := data.List
			if len(results) == 0 {
				results = data.Offers
			}
			for _, j := range results {
				p := j.posting(s.Name(), se.remote)
				if p.ExternalID == "" || seen[p.ExternalID] {
					continue
				}
				seen[p.ExternalID] = true
				out = append(out, p)
			}
			if len(results) < 100 {
				break
			}
		}
	}
	return discovery.Batch{Postings: out}, nil // search results are capped
}

// arbeitsagenturJob accepts both field sets the API has used (v6 names first).
type arbeitsagenturJob struct {
	Reference string `json:"referenznummer"`
	Ref       string `json:"refnr"`
	Company   string `json:"firma"`
	Employer  string `json:"arbeitgeber"`
	FullTitle string `json:"stellenangebotsTitel"`
	Title     string `json:"titel"`
	First     string `json:"datumErsteVeroeffentlichung"`
	Published string `json:"aktuelleVeroeffentlichungsdatum"`
	Places    []struct {
		Address struct {
			City string `json:"ort"`
		} `json:"adresse"`
	} `json:"stellenlokationen"`
	Place struct {
		City string `json:"ort"`
	} `json:"arbeitsort"`
}

func (j arbeitsagenturJob) posting(source string, remote bool) discovery.Posting {
	ref := or(j.Reference, j.Ref)
	cities := []string{j.Place.City}
	if len(j.Places) > 0 {
		cities = cities[:0]
		for _, pl := range j.Places {
			cities = append(cities, pl.Address.City)
		}
	}
	location := "Deutschland"
	if c := join("; ", cities...); c != "" {
		location = c + ", Deutschland"
	}
	p := discovery.Posting{
		Source: source, ExternalID: ref, Company: or(j.Company, j.Employer), Title: or(j.FullTitle, j.Title),
		Location: location, URL: "https://www.arbeitsagentur.de/jobsuche/jobdetail/" + url.PathEscape(ref),
		Posted: day(or(j.First, j.Published)),
	}
	if remote {
		p.Workplace = discovery.WorkplaceRemote
	}
	return p
}

// Details implements discovery.Detailer.
func (s *Arbeitsagentur) Details(ctx context.Context, p *discovery.Posting) error {
	ref := base64.StdEncoding.EncodeToString([]byte(p.ExternalID))
	var data struct {
		Description string `json:"stellenangebotsBeschreibung"`
		Text        string `json:"stellenbeschreibung"`
		External    string `json:"externeUrl"`
		HomeOffice  bool   `json:"homeofficemoeglich"`
	}
	if err := s.c.GetJSON(ctx, s.Name(), arbeitsagenturAPI+"/pc/v4/jobdetails/"+url.PathEscape(ref), nil, arbeitsagenturKey, &data); err != nil {
		return err
	}
	p.Description = htmltext.Text(or(data.Description, data.Text))
	p.ApplyURL = or(data.External, p.ApplyURL)
	if data.HomeOffice && p.Workplace == discovery.WorkplaceUnknown {
		p.Workplace = discovery.WorkplaceHybrid
	}
	return nil
}
