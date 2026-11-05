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

// Stats summarizes a crawl.
type Stats struct {
	Fetched    int            `json:"fetched"`
	Invalid    int            `json:"invalid"`
	Duplicates int            `json:"duplicates"`
	Changes    map[Change]int `json:"changes"`
	Closed     int            `json:"closed"`
}

// Store persists the global job index.
type Store interface {
	StartRun(ctx context.Context, sources []string, at time.Time) (int64, error)
	// Upsert records one posting; Duplicate reports an aggregator copy of a job already in the index.
	Upsert(ctx context.Context, runID int64, p Posting, at time.Time) (change Change, duplicate bool, err error)
	OpenJobs(ctx context.Context, sources []string) ([]OpenJob, error)
	Close(ctx context.Context, runID int64, ids []int64, at time.Time) error
	FinishRun(ctx context.Context, runID int64, s Stats, errs map[string]string, at time.Time) error
}
