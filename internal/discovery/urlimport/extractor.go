package urlimport

import (
	"bytes"
	"encoding/json"
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
	salaryRegex     = regexp.MustCompile(`(?i)(?:€|\$|£|EUR|USD|GBP)\s*([0-9]{1,3}(?:[.,][0-9]{3})*k?)\s*(?:-|–|—|to)\s*(?:€|\$|£|EUR|USD|GBP)?\s*([0-9]{1,3}(?:[.,][0-9]{3})*k?)`)
	singleSalRegex  = regexp.MustCompile(`(?i)(?:€|\$|£|EUR|USD|GBP)\s*([0-9]{1,3}(?:[.,][0-9]{3})*k?)\s*(?:/yr|/year|p\.a\.|per year)?`)
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
	}

	// 3b. Extract salary from JSON-LD if present
	extractSalaryJSONLD(page, &out)

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

func extractSalaryJSONLD(page []byte, out *ExtractedJob) {
	z := xhtml.NewTokenizer(bytes.NewReader(page))
	inLD := false
	for {
		switch z.Next() {
		case xhtml.ErrorToken:
			return
		case xhtml.StartTagToken:
			name, hasAttr := z.TagName()
			inLD = false
			if string(name) != "script" {
				continue
			}
			for hasAttr {
				var k, v []byte
				k, v, hasAttr = z.TagAttr()
				if string(k) == "type" && strings.Contains(strings.ToLower(string(v)), "ld+json") {
					inLD = true
				}
			}
		case xhtml.TextToken:
			if inLD {
				var val any
				if err := json.Unmarshal(z.Text(), &val); err == nil {
					findBaseSalary(val, out)
				}
				inLD = false
			}
		case xhtml.EndTagToken, xhtml.SelfClosingTagToken:
			inLD = false
		case xhtml.CommentToken, xhtml.DoctypeToken:
		}
	}
}

func findBaseSalary(v any, out *ExtractedJob) {
	if out.SalaryMin > 0 || out.SalaryMax > 0 {
		return
	}
	switch x := v.(type) {
	case []any:
		for _, e := range x {
			findBaseSalary(e, out)
		}
	case map[string]any:
		if bs, ok := x["baseSalary"].(map[string]any); ok {
			if curr, ok := bs["currency"].(string); ok && curr != "" {
				out.SalaryCurrency = curr
			}
			if valObj, ok := bs["value"].(map[string]any); ok {
				if minV := numVal(valObj["minValue"]); minV > 0 {
					out.SalaryMin = minV
				}
				if maxV := numVal(valObj["maxValue"]); maxV > 0 {
					out.SalaryMax = maxV
				}
				if exactV := numVal(valObj["value"]); exactV > 0 && out.SalaryMin == 0 {
					out.SalaryMin = exactV
					out.SalaryMax = exactV
				}
			} else if exactV := numVal(bs["value"]); exactV > 0 {
				out.SalaryMin = exactV
				out.SalaryMax = exactV
			}
			return
		}
		for _, val := range x {
			findBaseSalary(val, out)
		}
	}
}

func numVal(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case string:
		clean := strings.ReplaceAll(strings.ReplaceAll(n, ",", ""), ".", "")
		val, _ := strconv.Atoi(clean)
		return val
	}
	return 0
}

func extractSalaryRegex(text string, out *ExtractedJob) {
	if m := salaryRegex.FindStringSubmatch(text); len(m) > 2 {
		out.SalaryMin = parseKVal(m[1])
		out.SalaryMax = parseKVal(m[2])
		return
	}
	if m := singleSalRegex.FindStringSubmatch(text); len(m) > 1 {
		val := parseKVal(m[1])
		out.SalaryMin = val
		out.SalaryMax = val
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
