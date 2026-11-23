package matching

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Profiles supplies the criteria of users (the profile module, opened with the user's data key).
type Profiles interface {
	// Users lists the enabled users with a profile.
	Users(ctx context.Context) ([]uuid.UUID, error)
	Criteria(ctx context.Context, userID uuid.UUID) (Criteria, error)
}

// Jobs pages through the open jobs of the global index by ID.
type Jobs interface {
	OpenJobs(ctx context.Context, afterID int64, limit int) ([]Job, error)
}

// Store persists results per user (row-level security).
type Store interface {
	SaveMatches(ctx context.Context, userID uuid.UUID, rs []Result, at time.Time) error
}

// Feedback supplies the jobs a user rated by an own action (row-level security).
type Feedback interface {
	Rated(ctx context.Context, userID uuid.UUID, limit int) ([]Rated, error)
}

// Rated is a job the user liked (bookmarked, queued, approved, applied) or skipped.
type Rated struct {
	Job   Job
	Liked bool
}
