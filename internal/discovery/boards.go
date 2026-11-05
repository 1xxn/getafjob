package discovery

import (
	"context"
	"maps"
	"slices"
	"strings"
	"time"
)

// Board discovery: companies that appear only on job boards may have their own board in a supported system. A board
// found that way is added only if it lists one of the company's jobs.

// Limits of one discovery run and how long a result holds.
const (
	MaxBoardChecks   = 30   // companies per run
	maxCandidateRows = 3000 // job-board hits read per run
	maxHitsPerCheck  = 5    // hits per company that a board may confirm
	BoardRecheck     = 30 * 24 * time.Hour
	BoardRetry       = 24 * time.Hour // after an inconclusive check (no answer at all)
)

// Hit is an open job of a company that so far comes only from job boards.
type Hit struct {
	Company, Title, URL, ApplyURL, MatchKey string
}

// Candidate is one company to look for, with up to maxHitsPerCheck of its hits.
type Candidate struct {
	Key     string // CompanyKey, the part of MatchKey before "|"
	Company string
	Hits    []Hit
}

// MatchKeys returns the match keys of the candidate's hits.
func (c Candidate) MatchKeys() map[string]bool {
	out := make(map[string]bool, len(c.Hits))
	for _, h := range c.Hits {
		out[h.MatchKey] = true
	}
	return out
}

// CheckResult is the outcome of looking for one company's board.
type CheckResult string

// Check results.
const (
	CheckFound CheckResult = "found"
	CheckNone  CheckResult = "none"
	CheckError CheckResult = "error" // no system answered; try again after BoardRetry
)

// BoardCheck records one lookup; Board is set when a new board was confirmed.
type BoardCheck struct {
	CompanyKey string
	Result     CheckResult
	Board      *Board
}

// RecheckAfter is how long a result holds.
func (r CheckResult) RecheckAfter() time.Duration {
	if r == CheckError {
		return BoardRetry
	}
	return BoardRecheck
}

// Aggregators lists the source kinds that are job boards.
func Aggregators() []string { return slices.Sorted(maps.Keys(aggregators)) }

// GroupCandidates groups hits by company in the order they come (newest first), skipping known companies, at most
// maxCompanies.
func GroupCandidates(hits []Hit, known map[string]bool, maxCompanies int) []Candidate {
	idx := map[string]int{}
	var out []Candidate
	for _, h := range hits {
		key, _, _ := strings.Cut(h.MatchKey, "|")
		if key == "" || known[key] {
			continue
		}
		i, ok := idx[key]
		if !ok {
			if len(out) >= maxCompanies {
				continue
			}
			i = len(out)
			idx[key] = i
			out = append(out, Candidate{Key: key, Company: strings.TrimSpace(h.Company)})
		}
		if len(out[i].Hits) < maxHitsPerCheck {
			out[i].Hits = append(out[i].Hits, h)
		}
	}
	return out
}

// BoardChecks stores the candidates and outcomes of board discovery.
type BoardChecks interface {
	// BoardCandidates returns open job-board hits of companies without a check that still holds at now.
	BoardCandidates(ctx context.Context, now time.Time, maxRows int) ([]Hit, error)
	// RecordBoardChecks stores the outcomes and adds confirmed boards (origin "discovered"); a board that exists
	// already stays as it is. It returns the number of boards added.
	RecordBoardChecks(ctx context.Context, checks []BoardCheck, at time.Time) (added int, err error)
}

// BoardFinder looks for a candidate's own board. found is false when no system lists one of its hits; err means no
// system answered at all.
type BoardFinder interface {
	FindBoard(ctx context.Context, c Candidate) (b Board, found bool, err error)
}

// DiscoveryStats summarizes a discovery run.
type DiscoveryStats struct {
	Checked int `json:"checked"`
	Found   int `json:"found"`
	Added   int `json:"added"`
	Errors  int `json:"errors"`
}

// BoardDiscovery finds the company boards behind job-board hits.
type BoardDiscovery struct {
	checks      BoardChecks
	boards      Boards
	finder      BoardFinder
	now         func() time.Time
	parallelism int
}

// NewBoardDiscovery returns a discovery over the stored hits, the known boards and finder.
func NewBoardDiscovery(checks BoardChecks, boards Boards, finder BoardFinder) *BoardDiscovery {
	return &BoardDiscovery{checks: checks, boards: boards, finder: finder, now: time.Now, parallelism: 4}
}

// WithClock replaces the clock (tests).
func (d *BoardDiscovery) WithClock(now func() time.Time) *BoardDiscovery { d.now = now; return d }

// Run checks up to MaxBoardChecks companies. Companies that have a board already are recorded as found without a
// request.
func (d *BoardDiscovery) Run(ctx context.Context) (DiscoveryStats, error) {
	var st DiscoveryStats
	hits, err := d.checks.BoardCandidates(ctx, d.now(), maxCandidateRows)
	if err != nil {
		return st, err
	}
	list, err := d.boards.EnabledBoards(ctx)
	if err != nil {
		return st, err
	}
	known := map[string]bool{}
	for _, b := range list {
		if k := CompanyKey(b.Company); k != "" {
			known[k] = true
		}
	}
	var checks []BoardCheck
	seen := map[string]bool{}
	for _, h := range hits {
		key, _, _ := strings.Cut(h.MatchKey, "|")
		if known[key] && !seen[key] {
			seen[key] = true
			checks = append(checks, BoardCheck{CompanyKey: key, Result: CheckFound})
		}
	}
	cands := GroupCandidates(hits, known, MaxBoardChecks)
	found := make([]BoardCheck, len(cands))
	parallel(len(cands), d.parallelism, func(i int) {
		c := cands[i]
		b, ok, err := d.finder.FindBoard(ctx, c)
		switch {
		case ok:
			found[i] = BoardCheck{CompanyKey: c.Key, Result: CheckFound, Board: &b}
		case err != nil:
			found[i] = BoardCheck{CompanyKey: c.Key, Result: CheckError}
		default:
			found[i] = BoardCheck{CompanyKey: c.Key, Result: CheckNone}
		}
	})
	if err := ctx.Err(); err != nil {
		return st, err // canceled: results may be wrong, record nothing
	}
	for _, c := range found {
		st.Checked++
		switch c.Result {
		case CheckFound:
			st.Found++
		case CheckError:
			st.Errors++
		case CheckNone:
		}
	}
	checks = append(checks, found...)
	st.Added, err = d.checks.RecordBoardChecks(ctx, checks, d.now())
	return st, err
}
