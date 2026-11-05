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
	Query     string     // full-text search over title, company and description
	Decisions []Decision // e.g. match and maybe
	Workplace string     // remote, hybrid or onsite
	Page      int        // from 1
}

// Item is one job in a list.
type Item struct {
	JobID     int64
	Title     string
	Company   string
	Location  string
	Workplace string
	Posted    time.Time // zero: unknown
	FirstSeen time.Time
	Score     int
	Decision  Decision
}

// List is one page of results.
type List struct {
	Items  []Item
	Total  int // hits of the filter
	Counts map[Decision]int
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

// Reader reads results per user (row-level security).
type Reader interface {
	ListMatches(ctx context.Context, userID uuid.UUID, f Filter, offset, limit int) ([]Item, int, error)
	MatchCounts(ctx context.Context, userID uuid.UUID) (map[Decision]int, error)
	Match(ctx context.Context, userID uuid.UUID, jobID int64) (Detail, error) // ErrNotFound
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
	var ds []Decision
	for _, d := range f.Decisions {
		if (d == Match || d == Maybe || d == Skip) && !slices.Contains(ds, d) {
			ds = append(ds, d)
		}
	}
	f.Decisions = ds
	return f
}

// List returns one page of the user's open jobs, best score first.
func (l *Listings) List(ctx context.Context, userID uuid.UUID, f Filter) (List, error) {
	f = f.Clean()
	items, total, err := l.r.ListMatches(ctx, userID, f, (f.Page-1)*PageSize, PageSize)
	if err != nil {
		return List{}, err
	}
	counts, err := l.r.MatchCounts(ctx, userID)
	if err != nil {
		return List{}, err
	}
	return List{Items: items, Total: total, Counts: counts}, nil
}

// Detail returns one job with its reasons.
func (l *Listings) Detail(ctx context.Context, userID uuid.UUID, jobID int64) (Detail, error) {
	return l.r.Match(ctx, userID, jobID)
}
