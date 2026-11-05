package sources

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/1xxn/getafjob/internal/discovery"
	"github.com/1xxn/getafjob/internal/platform/htmltext"
)

// Amazon searches amazon.jobs (Amazon and AWS).
type Amazon struct {
	c       *Client
	Queries []string
	Country string // ISO 3166 alpha-3 as amazon.jobs uses it, e.g. DEU
	// MaxPerQuery caps the results of one query; more make the source incomplete.
	MaxPerQuery int
}

// NewAmazon returns the amazon.jobs source.
func NewAmazon(c *Client, queries []string, country string) *Amazon {
	return &Amazon{c: c, Queries: queries, Country: country, MaxPerQuery: 500}
}

// Name implements discovery.Source.
func (s *Amazon) Name() string { return "amazon" }

// Fetch implements discovery.Source.
func (s *Amazon) Fetch(ctx context.Context) (discovery.Batch, error) {
	seen := map[string]bool{}
	var out []discovery.Posting
	for _, q := range s.Queries {
		offset := 0
		for {
			var data struct {
				Hits json.Number `json:"hits"`
				Jobs []struct {
					ICIMS       string `json:"id_icims"`
					ID          string `json:"id"`
					Company     string `json:"company_name"`
					Title       string `json:"title"`
					Normalized  string `json:"normalized_location"`
					Location    string `json:"location"`
					City        string `json:"city"`
					Path        string `json:"job_path"`
					Description string `json:"description"`
					Basic       string `json:"basic_qualifications"`
					Preferred   string `json:"preferred_qualifications"`
					PostedDate  string `json:"posted_date"`
					URLNextStep string `json:"url_next_step"`
				} `json:"jobs"`
			}
			params := url.Values{"base_query": {q}, "result_limit": {"100"}, "offset": {strconv.Itoa(offset)}, "sort": {"recent"}}
			if s.Country != "" {
				params.Set("country", s.Country)
			}
			if err := s.c.GetJSON(ctx, s.Name(), "https://www.amazon.jobs/en/search.json", params, nil, &data); err != nil {
				return discovery.Batch{}, err
			}
			for _, j := range data.Jobs {
				ext := or(j.ICIMS, j.ID)
				if ext == "" || seen[ext] {
					continue
				}
				seen[ext] = true
				path := j.Path
				if !strings.HasPrefix(path, "/") {
					path = "/en/jobs/" + url.PathEscape(ext)
				}
				company := "Amazon"
				if c := strings.ToLower(j.Company + " " + j.Title); strings.Contains(c, "aws") || strings.Contains(c, "web services") {
					company = "AWS"
				}
				out = append(out, discovery.Posting{
					Source: s.Name(), ExternalID: ext, Company: company, Title: strings.TrimSpace(j.Title),
					Location: or(j.Normalized, or(j.Location, j.City)), URL: "https://www.amazon.jobs" + path,
					ApplyURL:    j.URLNextStep,
					Description: join("\n\n", htmltext.Text(j.Description), htmltext.Text(j.Basic), htmltext.Text(j.Preferred)),
					Posted:      amazonDay(j.PostedDate),
				})
			}
			offset += len(data.Jobs)
			hits, _ := strconv.Atoi(data.Hits.String())
			if len(data.Jobs) == 0 || offset >= hits {
				break
			}
			if offset >= s.MaxPerQuery {
				break
			}
		}
	}
	// Searches list only what they match, so a missing job is not a closed one.
	return discovery.Batch{Postings: out}, nil
}

// amazonDay reads "November 3, 2026".
func amazonDay(s string) string {
	t, err := time.Parse("January 2, 2006", strings.TrimSpace(s))
	if err != nil {
		return day(s)
	}
	return t.Format(time.DateOnly)
}
