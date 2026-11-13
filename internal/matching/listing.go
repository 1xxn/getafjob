package matching

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// ErrNotFound: the job does not exist or has no result for the user.
var ErrNotFound = errors.New("matching: not found")

// PageSize is the number of jobs on one list page.
const PageSize = 50

// Bounds of a list request.
const (
	maxQuery = 200
	maxPage  = 200
)

// Filter selects the jobs of a list page. Empty fields do not filter.
type Filter struct {
	Query         string     // full-text search over title, company and description
	Decisions     []Decision // e.g. match and maybe
	Workplace     string     // remote, hybrid or onsite
	Location      string     // city or country substring
	Source        string     // ATS platform (e.g. greenhouse, lever, ashby, workday, personio)
	MinSalary     int        // minimum salary in EUR
	FreshnessDays int        // 1 (24h), 3, 7, 14, 30, or 0 (all)
	Sort          string     // "match", "newest", "salary", "company"
	Blocked       bool       // show only blocked jobs
	SeenAfter     time.Time  // only jobs first seen after it (saved search alerts); zero: all
	Page          int        // from 1
}

// Item is one job in a list.
type Item struct {
	JobID          int64
	Title          string
	Company        string
	Location       string
	Workplace      string
	Posted         time.Time // zero: unknown
	FirstSeen      time.Time
	Score          int
	Decision       Decision
	SalaryMin      int
	SalaryMax      int
	SalaryCurrency string
	Source         string
	Blocked        bool
}

// List is one page of results.
type List struct {
	Items        []Item
	Total        int // hits of the filter
	Counts       map[Decision]int
	BlockedCount int
}

// Detail is one job with its reasons.
type Detail struct {
	Item
	Description string
	URL         string
	ApplyURL    string
	Open        bool
	Reasons     []Reason
	ComputedAt  time.Time
}

// MatchSummary is one job match of the user for export or summary.
type MatchSummary struct {
	JobID      int64
	Title      string
	Company    string
	Location   string
	Workplace  string
	Score      int
	Decision   Decision
	Reasons    []Reason
	ComputedAt time.Time
}

// BlocklistItem represents a blocked employer or keyword.
type BlocklistItem struct {
	ID        int64     `json:"id"`
	UserID    uuid.UUID `json:"user_id"`
	Kind      string    `json:"kind"` // "company" or "keyword"
	Pattern   string    `json:"pattern"`
	CreatedAt time.Time `json:"created_at"`
}

// Reader reads results per user (row-level security).
type Reader interface {
	ListMatches(ctx context.Context, userID uuid.UUID, f Filter, offset, limit int) ([]Item, int, error)
	MatchCounts(ctx context.Context, userID uuid.UUID) (map[Decision]int, int, error)
	Match(ctx context.Context, userID uuid.UUID, jobID int64) (Detail, error) // ErrNotFound
	UserMatches(ctx context.Context, userID uuid.UUID) ([]MatchSummary, error)
	ListBlocklist(ctx context.Context, userID uuid.UUID) ([]BlocklistItem, error)
	AddBlocklist(ctx context.Context, userID uuid.UUID, kind, pattern string) error
	RemoveBlocklist(ctx context.Context, userID uuid.UUID, id int64) error
	RemoveBlocklistByPattern(ctx context.Context, userID uuid.UUID, kind, pattern string) error
}

// Listings serves the job list and detail pages.
type Listings struct{ r Reader }

// NewListings returns the read side of matching.
func NewListings(r Reader) *Listings { return &Listings{r: r} }

// Clean bounds a filter from user input: page 1 to maxPage, query of at most maxQuery characters, known values.
func (f Filter) Clean() Filter {
	f.Query = strings.Join(strings.Fields(f.Query), " ")
	if utf8.RuneCountInString(f.Query) > maxQuery {
		f.Query = string([]rune(f.Query)[:maxQuery])
	}
	f.Page = min(max(f.Page, 1), maxPage)
	if !slices.Contains([]string{"remote", "hybrid", "onsite"}, f.Workplace) {
		f.Workplace = ""
	}
	f.Location = strings.TrimSpace(f.Location)
	if utf8.RuneCountInString(f.Location) > 100 {
		f.Location = string([]rune(f.Location)[:100])
	}
	f.Source = strings.ToLower(strings.TrimSpace(f.Source))
	if utf8.RuneCountInString(f.Source) > 50 {
		f.Source = string([]rune(f.Source)[:50])
	}
	f.MinSalary = max(f.MinSalary, 0)
	if f.MinSalary > 10_000_000 {
		f.MinSalary = 10_000_000
	}
	if !slices.Contains([]int{1, 3, 7, 14, 30}, f.FreshnessDays) {
		f.FreshnessDays = 0
	}
	if !slices.Contains([]string{"match", "newest", "salary", "company"}, f.Sort) {
		f.Sort = "match"
	}
	var ds []Decision
	for _, d := range f.Decisions {
		if (d == Match || d == Maybe || d == Skip) && !slices.Contains(ds, d) {
			ds = append(ds, d)
		}
	}
	f.Decisions = ds
	return f
}

// List returns one page of the user's open jobs, ordered by criteria.
func (l *Listings) List(ctx context.Context, userID uuid.UUID, f Filter) (List, error) {
	f = f.Clean()
	items, total, err := l.r.ListMatches(ctx, userID, f, (f.Page-1)*PageSize, PageSize)
	if err != nil {
		return List{}, err
	}
	counts, blocked, err := l.r.MatchCounts(ctx, userID)
	if err != nil {
		return List{}, err
	}
	return List{Items: items, Total: total, Counts: counts, BlockedCount: blocked}, nil
}

// Detail returns one job with its reasons.
func (l *Listings) Detail(ctx context.Context, userID uuid.UUID, jobID int64) (Detail, error) {
	return l.r.Match(ctx, userID, jobID)
}

// UserMatches returns all matches computed for the user.
func (l *Listings) UserMatches(ctx context.Context, userID uuid.UUID) ([]MatchSummary, error) {
	return l.r.UserMatches(ctx, userID)
}

// Blocklist returns all blocked employers and keywords for the user.
func (l *Listings) Blocklist(ctx context.Context, userID uuid.UUID) ([]BlocklistItem, error) {
	return l.r.ListBlocklist(ctx, userID)
}

// AddBlocklist adds a company or keyword to the candidate's blocklist.
func (l *Listings) AddBlocklist(ctx context.Context, userID uuid.UUID, kind, pattern string) error {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" || (kind != "company" && kind != "keyword") {
		return errors.New("matching: invalid blocklist item")
	}
	if utf8.RuneCountInString(pattern) > 200 {
		pattern = string([]rune(pattern)[:200])
	}
	return l.r.AddBlocklist(ctx, userID, kind, pattern)
}

// RemoveBlocklist removes a blocklist item by ID.
func (l *Listings) RemoveBlocklist(ctx context.Context, userID uuid.UUID, id int64) error {
	return l.r.RemoveBlocklist(ctx, userID, id)
}

// RemoveBlocklistByPattern removes a blocklist item by kind and pattern.
func (l *Listings) RemoveBlocklistByPattern(ctx context.Context, userID uuid.UUID, kind, pattern string) error {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return errors.New("matching: empty pattern")
	}
	return l.r.RemoveBlocklistByPattern(ctx, userID, kind, pattern)
}
