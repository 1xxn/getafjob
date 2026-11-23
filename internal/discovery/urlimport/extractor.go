package urlimport

import (
	"bytes"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	xhtml "golang.org/x/net/html"

	"github.com/1xxn/getafjob/internal/discovery"
	"github.com/1xxn/getafjob/internal/discovery/adapters/sources"
	"github.com/1xxn/getafjob/internal/platform/htmltext"
)

// ExtractedJob holds normalized metadata extracted from a job posting web page.
type ExtractedJob struct {
	Source         string
	ExternalID     string
	Company        string
	Title          string
	Location       string
	Workplace      discovery.Workplace
	URL            string
	ApplyURL       string
	Description    string
	Posted         string
	SalaryMin      int
	SalaryMax      int
	SalaryCurrency string
	ATS            string
}

// ToPosting converts ExtractedJob to a discovery.Posting for indexing.
func (e ExtractedJob) ToPosting() discovery.Posting {
	return discovery.Posting{
		Source:         e.Source,
		ExternalID:     e.ExternalID,
		Company:        e.Company,
		Title:          e.Title,
		Location:       e.Location,
		Workplace:      e.Workplace,
		URL:            e.URL,
		ApplyURL:       e.ApplyURL,
		Description:    e.Description,
		Posted:         e.Posted,
		SalaryMin:      e.SalaryMin,
		SalaryMax:      e.SalaryMax,
		SalaryCurrency: e.SalaryCurrency,
	}
}

var (
	titleSeparators = []string{" | ", " - ", " — ", " – ", " · ", " : "}
	salaryRegex     = regexp.MustCompile(`(?i)(€|\$|£|EUR|USD|GBP)\s*([0-9]{1,3}(?:[.,][0-9]{3})*k?)\s*(?:-|–|—|to)\s*(?:€|\$|£|EUR|USD|GBP)?\s*([0-9]{1,3}(?:[.,][0-9]{3})*k?)`)
	singleSalRegex  = regexp.MustCompile(`(?i)(€|\$|£|EUR|USD|GBP)\s*([0-9]{1,3}(?:[.,][0-9]{3})*k?)\s*(?:/yr|/year|p\.a\.|per year)`)
	jobIDPattern    = regexp.MustCompile(`/(?:jobs?|positions?|opening|postings?|detail)/([A-Za-z0-9_-]+)`)
)

type metaTags struct {
	title       string
	siteName    string
	description string
	url         string
	location    string
	pageTitle   string
	h1          string
}

// Extract parses metadata from HTML and the source URL using Schema.org JSON-LD, OpenGraph, and HTML tags.
func Extract(rawURL string, page []byte) (ExtractedJob, error) {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return ExtractedJob{}, err
	}

	out := ExtractedJob{
		URL:            rawURL,
		ApplyURL:       rawURL,
		SalaryCurrency: "EUR",
	}

	// 1. Detect ATS and slug from URL
	var boardSlug string
	if b, ok := sources.BoardFromURL(rawURL); ok {
		out.ATS = b.ATS
		boardSlug = b.Slug
		out.Source = b.ATS + ":" + b.Slug
	} else {
		host := parsedURL.Hostname()
		out.Source = "import:" + strings.TrimPrefix(host, "www.")
	}

	// 2. Extract External ID
	if m := jobIDPattern.FindStringSubmatch(parsedURL.Path); len(m) > 1 {
		out.ExternalID = m[1]
	} else {
		pathParts := strings.FieldsFunc(parsedURL.Path, func(r rune) bool { return r == '/' })
		if len(pathParts) > 0 {
			out.ExternalID = pathParts[len(pathParts)-1]
		} else {
			out.ExternalID = discovery.Fingerprint(out.Source, rawURL)
		}
	}

	// 3. Schema.org JobPosting JSON-LD
	postings := sources.JobPostings(page)
	if len(postings) > 0 {
		jp := postings[0]
		if jp.Title != "" {
			out.Title = jp.Title
		}
		if jp.Company != "" {
			out.Company = jp.Company
		}
		if len(jp.Locations) > 0 {
			out.Location = strings.Join(jp.Locations, "; ")
		}
		if jp.Description != "" {
			out.Description = jp.Description
		}
		if jp.Posted != "" {
			out.Posted = jp.Posted
		}
		if jp.Remote {
			out.Workplace = discovery.WorkplaceRemote
		}
		if jp.URL != "" && strings.HasPrefix(jp.URL, "http") {
			out.ApplyURL = jp.URL
		}
		if jp.ID != "" {
			out.ExternalID = jp.ID
		}
		if jp.Salary.Max > 0 {
			out.SalaryMin, out.SalaryMax, out.SalaryCurrency = jp.Salary.Min, jp.Salary.Max, jp.Salary.Currency
		}
	}

	// 4. HTML OpenGraph and Meta fallback
	meta := extractMeta(page)
	if out.Title == "" {
		out.Title = or(meta.title, meta.h1)
		if out.Title == "" && meta.pageTitle != "" {
			out.Title, _ = splitTitleCompany(meta.pageTitle)
		}
	}
	if out.Company == "" {
		out.Company = meta.siteName
		if out.Company == "" && meta.pageTitle != "" {
			_, out.Company = splitTitleCompany(meta.pageTitle)
		}
		if out.Company == "" && boardSlug != "" {
			out.Company = formatSlug(boardSlug)
		}
		if out.Company == "" {
			out.Company = cleanHost(parsedURL.Hostname())
		}
	}
	if out.Description == "" {
		out.Description = meta.description
		if out.Description == "" {
			out.Description = htmltext.Text(string(page))
			if len(out.Description) > 10000 {
				out.Description = out.Description[:10000]
			}
		}
	}
	if out.Location == "" && meta.location != "" {
		out.Location = meta.location
	}

	// 5. Workplace detection
	if out.Workplace == "" {
		fullText := strings.ToLower(out.Title + " " + out.Location + " " + out.Description)
		switch {
		case containsWord(fullText, "remote") || containsWord(fullText, "telecommute") || containsWord(fullText, "homeoffice"):
			out.Workplace = discovery.WorkplaceRemote
		case containsWord(fullText, "hybrid"):
			out.Workplace = discovery.WorkplaceHybrid
		case out.Location != "":
			out.Workplace = discovery.WorkplaceOnsite
		}
	}

	// 6. Salary regex fallback if not found in JSON-LD
	if out.SalaryMin == 0 && out.SalaryMax == 0 {
		extractSalaryRegex(out.Description, &out)
	}

	// Normalize spaces
	out.Title = strings.TrimSpace(out.Title)
	out.Company = strings.TrimSpace(out.Company)
	out.Location = strings.TrimSpace(out.Location)
	out.Description = strings.TrimSpace(out.Description)

	return out, nil
}

func extractMeta(page []byte) metaTags {
	var m metaTags
	z := xhtml.NewTokenizer(bytes.NewReader(page))
	inTitle := false
	inH1 := false

	for {
		tt := z.Next()
		if tt == xhtml.ErrorToken {
			break
		}
		switch tt {
		case xhtml.StartTagToken, xhtml.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			switch string(name) {
			case "title":
				inTitle = true
			case "h1":
				if m.h1 == "" {
					inH1 = true
				}
			case "meta":
				var prop, content, metaName string
				for hasAttr {
					var k, v []byte
					k, v, hasAttr = z.TagAttr()
					switch string(k) {
					case "property":
						prop = strings.ToLower(string(v))
					case "name":
						metaName = strings.ToLower(string(v))
					case "content":
						content = strings.TrimSpace(string(v))
					}
				}
				key := prop
				if key == "" {
					key = metaName
				}
				switch key {
				case "og:title", "twitter:title":
					if m.title == "" {
						m.title = content
					}
				case "og:site_name", "og:publisher", "publisher":
					if m.siteName == "" {
						m.siteName = content
					}
				case "og:description", "description", "twitter:description":
					if m.description == "" {
						m.description = content
					}
				case "og:url":
					if m.url == "" {
						m.url = content
					}
				case "job:location", "location":
					if m.location == "" {
						m.location = content
					}
				}
			}
		case xhtml.TextToken:
			if inTitle && m.pageTitle == "" {
				m.pageTitle = strings.TrimSpace(string(z.Text()))
			}
			if inH1 && m.h1 == "" {
				m.h1 = strings.TrimSpace(string(z.Text()))
			}
		case xhtml.EndTagToken:
			name, _ := z.TagName()
			if string(name) == "title" {
				inTitle = false
			} else if string(name) == "h1" {
				inH1 = false
			}
		case xhtml.ErrorToken, xhtml.CommentToken, xhtml.DoctypeToken:
		}
	}
	return m
}

// extractSalaryRegex reads a pay range like "€60k – €75k" or "EUR 70,000 p.a." from the text. Only yearly-sized
// figures count (10,000 and more): a monthly or hourly amount or a stray "€5" is no yearly salary.
func extractSalaryRegex(text string, out *ExtractedJob) {
	var cur string
	var lo, hi int
	if m := salaryRegex.FindStringSubmatch(text); len(m) > 3 {
		cur, lo, hi = m[1], parseKVal(m[2]), parseKVal(m[3])
	} else if m := singleSalRegex.FindStringSubmatch(text); len(m) > 2 {
		cur, lo = m[1], parseKVal(m[2])
		hi = lo
	}
	if lo > hi {
		lo, hi = hi, lo
	}
	if lo < 10_000 || hi > 10_000_000 {
		return
	}
	out.SalaryMin, out.SalaryMax = lo, hi
	out.SalaryCurrency = map[string]string{"€": "EUR", "$": "USD", "£": "GBP"}[cur]
	if out.SalaryCurrency == "" {
		out.SalaryCurrency = strings.ToUpper(cur)
	}
}

func parseKVal(s string) int {
	s = strings.TrimSpace(strings.ToLower(s))
	mult := 1
	if strings.HasSuffix(s, "k") {
		mult = 1000
		s = strings.TrimSuffix(s, "k")
	}
	s = strings.ReplaceAll(strings.ReplaceAll(s, ",", ""), ".", "")
	v, _ := strconv.Atoi(s)
	return v * mult
}

func splitTitleCompany(pageTitle string) (title, company string) {
	for _, sep := range titleSeparators {
		if parts := strings.Split(pageTitle, sep); len(parts) >= 2 {
			p0 := strings.TrimSpace(parts[0])
			p1 := strings.TrimSpace(parts[1])
			if len(parts) > 2 {
				p1 = strings.TrimSpace(parts[len(parts)-1])
			}
			return p0, p1
		}
	}
	return pageTitle, ""
}

func formatSlug(slug string) string {
	parts := strings.FieldsFunc(slug, func(r rune) bool { return r == '-' || r == '_' })
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

func cleanHost(host string) string {
	host = strings.TrimPrefix(host, "www.")
	if idx := strings.IndexByte(host, '.'); idx > 0 {
		name := host[:idx]
		if len(name) > 0 {
			return strings.ToUpper(name[:1]) + name[1:]
		}
	}
	return host
}

func containsWord(s, word string) bool {
	idx := strings.Index(s, word)
	for idx >= 0 {
		before := idx == 0 || !unicode.IsLetter(rune(s[idx-1]))
		end := idx + len(word)
		after := end >= len(s) || !unicode.IsLetter(rune(s[end]))
		if before && after {
			return true
		}
		next := strings.Index(s[end:], word)
		if next < 0 {
			break
		}
		idx = end + next
	}
	return false
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
