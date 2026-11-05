package discovery

import (
	"context"
	"time"
)

// Batch is what one source delivered.
type Batch struct {
	Postings []Posting
	Complete bool // false when the source caps its results (searches, page limits)
}

// Source fetches postings from one board or job board.
type Source interface {
	Name() string
	Fetch(ctx context.Context) (Batch, error)
}

// Detailer is a source whose list leaves out descriptions; Details loads them for one posting.
type Detailer interface {
	Details(ctx context.Context, p *Posting) error
}

// Stats summarizes a crawl.
type Stats struct {
	Fetched       int            `json:"fetched"`
	Seen          int            `json:"seen"` // stubs of known jobs
	Invalid       int            `json:"invalid"`
	Details       int            `json:"details"`
	DetailsFailed int            `json:"details_failed"`
	Duplicates    int            `json:"duplicates"`
	Changes       map[Change]int `json:"changes"`
	Closed        int            `json:"closed"`
}

// Store persists the global job index.
type Store interface {
	StartRun(ctx context.Context, sources []string, at time.Time) (int64, error)
	// Upsert records one posting; duplicate reports a job-board copy of a job already in the index. An empty
	// description keeps the stored one.
	Upsert(ctx context.Context, runID int64, p Posting, at time.Time) (change Change, duplicate bool, err error)
	OpenJobs(ctx context.Context, sources []string) ([]OpenJob, error)
	// Seen marks open jobs as seen at, by fingerprint, and returns the fingerprints it found.
	Seen(ctx context.Context, fingerprints []string, at time.Time) (map[string]bool, error)
	// Described returns the fingerprints of stored jobs that already have a description.
	Described(ctx context.Context, fingerprints []string) (map[string]bool, error)
	Close(ctx context.Context, runID int64, ids []int64, at time.Time) error
	FinishRun(ctx context.Context, runID int64, s Stats, errs map[string]string, at time.Time) error
}

// Board is a company board of an applicant tracking system; Config holds system-specific JSON (Workday host and
// site, Eightfold host and API).
type Board struct {
	ID      int64
	ATS     string
	Slug    string
	Company string
	Config  []byte
}

// Boards stores the boards to crawl.
type Boards interface {
	// ImportBoards adds new boards and refreshes name and config of known ones; enabled and crawl state stay.
	ImportBoards(ctx context.Context, boards []Board) (inserted, updated int, err error)
	EnabledBoards(ctx context.Context) ([]Board, error)
}
