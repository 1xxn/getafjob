package sources

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/1xxn/getafjob/internal/discovery"
)

// Board is a company board of an applicant tracking system, as stored in the boards table.
type Board struct {
	ATS     string
	Slug    string
	Company string // display name; empty: from the API or the slug
	Config  json.RawMessage
}

// ErrBadBoard reports a board that cannot be crawled (unknown system, unsafe slug, missing config).
var ErrBadBoard = errors.New("invalid board")

var (
	// hostLabel is a slug that becomes part of a host name (acme.jobs.personio.de): one DNS label.
	hostLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	// pathSlug is a slug that becomes one path segment.
	pathSlug = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)
	// workdayHost limits Workday sites to Workday's own domains.
	workdayHost = regexp.MustCompile(`^[a-z0-9-]{1,63}\.wd[0-9]{1,3}\.(myworkdayjobs|myworkdaysite)\.com$`)
)

// FromBoard returns the source for a board.
func FromBoard(c *Client, b Board) (discovery.Source, error) {
	slug := strings.TrimSpace(b.Slug)
	bad := func(why string) error { return fmt.Errorf("%w: %s %q: %s", ErrBadBoard, b.ATS, slug, why) }
	switch b.ATS {
	case "personio", "recruitee":
		slug = strings.ToLower(slug)
		if !hostLabel.MatchString(slug) {
			return nil, bad("slug")
		}
	default:
		if !pathSlug.MatchString(slug) {
			return nil, bad("slug")
		}
	}
	company := strings.TrimSpace(b.Company)
	switch b.ATS {
	case "greenhouse":
		return &Greenhouse{c: c, board: slug, company: company}, nil
	case "ashby":
		return &Ashby{c: c, board: slug, company: or(company, pretty(slug))}, nil
	case "lever":
		return &Lever{c: c, slug: slug, company: or(company, pretty(slug))}, nil
	case "smartrecruiters":
		var cfg struct {
			Country string `json:"country"`
			Max     int    `json:"max"`
		}
		if err := config(b.Config, &cfg); err != nil {
			return nil, bad("config")
		}
		return &SmartRecruiters{c: c, id: slug, company: company, country: cfg.Country, max: cfg.Max}, nil
	case "personio":
		return &Personio{c: c, slug: slug, company: company}, nil
	case "recruitee":
		return &Recruitee{c: c, slug: slug, company: company}, nil
	case "workable":
		return &Workable{c: c, slug: slug, company: company}, nil
	case "workday":
		var cfg struct {
			Host   string `json:"host"`
			Site   string `json:"site"`
			Search string `json:"search"`
			Max    int    `json:"max"`
		}
		if err := config(b.Config, &cfg); err != nil || !workdayHost.MatchString(cfg.Host) || !pathSlug.MatchString(cfg.Site) {
			return nil, bad("config")
		}
		return &Workday{c: c, host: cfg.Host, tenant: slug, site: cfg.Site, search: cfg.Search,
			company: or(company, pretty(slug)), max: cfg.Max}, nil
	}
	return nil, bad("unknown system")
}

func config(raw json.RawMessage, out any) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

func or(s, fallback string) string {
	if s = strings.TrimSpace(s); s != "" {
		return s
	}
	return fallback
}

// pretty turns a slug into a company name ("acme-labs" → "Acme Labs").
func pretty(slug string) string {
	words := strings.Fields(strings.NewReplacer("-", " ", "_", " ").Replace(slug))
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

// join joins the non-empty, trimmed parts.
func join(sep string, parts ...string) string {
	out := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}

// day returns the date at the start of an ISO timestamp, or "".
func day(s string) string {
	if len(s) < 10 {
		return ""
	}
	if _, err := time.Parse(time.DateOnly, s[:10]); err != nil {
		return ""
	}
	return s[:10]
}

// epochDay returns the UTC date of a Unix timestamp in seconds (or milliseconds).
func epochDay(v json.Number) string {
	n, err := strconv.ParseInt(string(v), 10, 64)
	if err != nil || n <= 0 {
		return ""
	}
	if n > 1e11 {
		n /= 1000
	}
	return time.Unix(n, 0).UTC().Format(time.DateOnly)
}

// id renders a JSON id (number or string).
func id(v json.RawMessage) string {
	var s string
	if json.Unmarshal(v, &s) == nil {
		return strings.TrimSpace(s)
	}
	var n json.Number
	if json.Unmarshal(v, &n) == nil {
		return n.String()
	}
	return ""
}
