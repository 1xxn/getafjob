// Package discovery collects job postings into one global index, crawled once for all users: sources, boards,
// snapshots, deduplication and closings. Ranking per user is the matching module's job.
package discovery

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// Workplace of a posting.
type Workplace string

// Workplaces.
const (
	WorkplaceUnknown Workplace = ""
	WorkplaceRemote  Workplace = "remote"
	WorkplaceHybrid  Workplace = "hybrid"
	WorkplaceOnsite  Workplace = "onsite"
)

// Posting is one job as a source delivers it.
type Posting struct {
	Source      string // e.g. greenhouse:acme, arbeitsagentur
	ExternalID  string
	Company     string
	Title       string
	Location    string
	URL         string
	ApplyURL    string
	Description string
	Posted      string // YYYY-MM-DD or empty
	Workplace   Workplace
	Extra       map[string]string
	// Stub marks a posting that only names a job (sitemaps): source, ID and URL. A known open job is just seen
	// again; a new one gets its details before it counts.
	Stub bool `json:",omitempty"`
}

// Change describes what a crawl did to a job.
type Change string

// Changes recorded as snapshots.
const (
	ChangeNew       Change = "new"
	ChangeUpdated   Change = "updated"
	ChangeReopened  Change = "reopened"
	ChangeMerged    Change = "merged" // a company board replaced a job-board entry
	ChangeClosed    Change = "closed"
	ChangeUnchanged Change = "unchanged"
)

// StaleAfter closes jobs from capped sources (job-board searches) that were not seen for this long.
const StaleAfter = 21 * 24 * time.Hour

// aggregators are job boards that list jobs of many companies; company boards win over them.
var aggregators = map[string]bool{
	"arbeitnow":      true,
	"himalayas":      true,
	"arbeitsagentur": true,
	"portal":         true,
	"linkedin":       true,
	"indeed":         true,
	"glassdoor":      true,
}

// IsAggregator reports whether source is a job board rather than a company's own board.
func IsAggregator(source string) bool {
	kind, _, _ := strings.Cut(source, ":")
	return aggregators[kind]
}

// Fingerprint identifies a posting within its source.
func Fingerprint(source, externalID string) string {
	h := sha256.Sum256([]byte(source + "\x1f" + externalID))
	return hex.EncodeToString(h[:20])
}

// ContentHash changes when title, location or description change.
func ContentHash(title, location, description string) string {
	h := sha256.Sum256([]byte(strings.TrimSpace(title) + "\x1f" + strings.TrimSpace(location) + "\x1f" + strings.TrimSpace(description)))
	return hex.EncodeToString(h[:16])
}

var (
	legalForms  = regexp.MustCompile(`\b(gmbh|ag|se|kg|co|inc|ltd|llc|group|gruppe|deutschland|germany)\b`)
	genderTags  = regexp.MustCompile(`\((m|w|f|d|x|gn|all|all genders|w/m/d|m/w/d|f/m/d|m/f/d|d/f/m|w/d/m|m/f/x|x/f/m)[^)]*\)`)
	nonTitle    = regexp.MustCompile(`[^a-z0-9/ ]`)
	nonCompany  = regexp.MustCompile(`[^a-z0-9 ]`)
	whitespaces = regexp.MustCompile(`\s+`)
)

// Fold lower-cases, removes diacritics ("Zürich" → "zurich") and collapses whitespace.
func Fold(s string) string {
	t := transform.Chain(norm.NFKD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	out, _, err := transform.String(t, s)
	if err != nil {
		out = s
	}
	out = strings.Map(func(r rune) rune {
		if r > unicode.MaxASCII {
			return -1
		}
		return unicode.ToLower(r)
	}, out)
	return strings.TrimSpace(whitespaces.ReplaceAllString(out, " "))
}

// MatchKey finds the same job across sources: company without legal form plus title without gender tags.
func MatchKey(company, title string) string {
	c := legalForms.ReplaceAllString(Fold(company), " ")
	c = nonCompany.ReplaceAllString(c, " ")
	t := genderTags.ReplaceAllString(Fold(title), " ")
	t = nonTitle.ReplaceAllString(t, " ")
	return strings.Join(strings.Fields(c), " ") + "|" + strings.Join(strings.Fields(t), " ")
}

// CompanyKey compares company names across sources.
func CompanyKey(name string) string {
	c := legalForms.ReplaceAllString(Fold(name), " ")
	return strings.Join(strings.Fields(nonCompany.ReplaceAllString(c, " ")), " ")
}

// SourceRun is the outcome of fetching one source in a crawl.
type SourceRun struct {
	Name     string
	OK       bool // the source answered
	Complete bool // every job of the source was listed (not capped)
}

// OpenJob is the part of a stored job the closing rule needs.
type OpenJob struct {
	ID          int64
	Fingerprint string
	Source      string
	LastSeen    time.Time
}

// ShouldClose decides whether an open job that the crawl did not list is gone. A failing source closes nothing; a
// complete source closes at once; a capped source only after StaleAfter without a sighting.
func ShouldClose(j OpenJob, runs map[string]SourceRun, listed map[string]bool, now time.Time) bool {
	if listed[j.Fingerprint] {
		return false
	}
	r, ok := runs[j.Source]
	if !ok || !r.OK {
		return false
	}
	if r.Complete {
		return true
	}
	return now.Sub(j.LastSeen) >= StaleAfter
}

// ValidStub reports whether a stub names a job.
func ValidStub(p Posting) bool {
	return p.Stub && p.Source != "" && p.ExternalID != "" && p.URL != "" && len(p.URL) <= 2048 && len(p.ExternalID) <= 500
}

// ValidPosting reports whether a posting has what the index needs.
func ValidPosting(p Posting) bool {
	return !p.Stub && p.Source != "" && p.ExternalID != "" && strings.TrimSpace(p.Title) != "" && p.URL != "" &&
		len(p.Title) <= 500 && len(p.Company) <= 300 && len(p.Location) <= 1000 && len(p.URL) <= 2048
}
