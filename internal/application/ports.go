package application

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Store errors.
var (
	ErrNotFound         = errors.New("application not found")
	ErrConflict         = errors.New("application changed meanwhile")
	ErrAlreadySubmitted = errors.New("application has a submit attempt already")
)

// App is the stored state of an application.
type App struct {
	ID        uuid.UUID
	JobID     int64
	State     State
	Version   int64
	UpdatedAt time.Time
}

// Change is one transition as the store writes it: state, event and, for the submit, the register row.
type Change struct {
	Event       Event
	From, To    State
	Actor       Actor
	Detail      map[string]any
	At          time.Time
	BeginSubmit bool   // insert the submit attempt (unique per application) in the same transaction
	Outcome     string // finish the submit attempt: submitted, unknown or not_sent
	// HandoffUntil is when the user's turn ends (set with EvAsk); every other transition clears it.
	HandoffUntil *time.Time
}

// Handoff is an application waiting for the user.
type Handoff struct {
	Ref
	Until  time.Time
	Warned bool
}

// Notifier tells the user that a handoff window is about to close (Web Push, mail; #31).
type Notifier interface {
	HandoffClosing(ctx context.Context, r Ref, until time.Time) error
}

// Job is what a run needs to know about the job.
type Job struct {
	ID                                           int64
	Source, Company, Title, Location, URL, Apply string
	Description                                  string
}

// Ref names an application of a user.
type Ref struct {
	ID, UserID uuid.UUID
}

// Question is an open question or checkpoint asked during a run.
type Question struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
	Draft string `json:"draft,omitempty"`
}

// ScreenshotRetention is how long encrypted run screenshots are kept in the blob store.
const ScreenshotRetention = 90 * 24 * time.Hour

// EvidenceField is a form field filled during an application run, with its value and provenance source.
type EvidenceField struct {
	Page   int
	ID     string
	Label  string
	Value  string
	Source string
}

// TimelineEntry is a chronological state transition event or run checkpoint of an application.
type TimelineEntry struct {
	At           time.Time
	Kind         string // "event" or "checkpoint"
	Step         string // event name or checkpoint step
	FromState    State
	ToState      State
	Actor        Actor
	Page         int
	URL          string
	Detail       string
	CheckpointID int64 // > 0 when this entry is a checkpoint with a stored screenshot
}

// Evidence is the audit record of an application: every filled field with its value and source, the screenshot
// before submitting, the confirmation page, and the chronological timeline.
type Evidence struct {
	Fields                   []EvidenceField
	BeforeSubmitCheckpointID int64
	ConfirmationCheckpointID int64
	ConfirmationURL          string
	Confirmed                bool
	Timeline                 []TimelineEntry
}

// ExpiredScreenshot names a checkpoint screenshot older than ScreenshotRetention.
type ExpiredScreenshot struct {
	CheckpointID int64
	UserID       uuid.UUID
	Key          string
}

// ScreenshotDeleter deletes a sealed screenshot blob of a user.
type ScreenshotDeleter interface {
	Delete(ctx context.Context, userID uuid.UUID, key string) error
}

// Store persists applications per user (row-level security).
type Store interface {
	// Create returns the application of the user for the job, creating it in state if there is none.
	Create(ctx context.Context, userID uuid.UUID, jobID int64, state State, at time.Time) (uuid.UUID, error)
	Get(ctx context.Context, userID, id uuid.UUID) (App, error)
	// Questions returns the questions of the last ask event of an application.
	Questions(ctx context.Context, userID, id uuid.UUID) ([]Question, error)
	// Evidence returns the filled fields, screenshots, confirmation page, and timeline of an application.
	Evidence(ctx context.Context, userID, id uuid.UUID) (Evidence, error)
	// CheckpointScreenshot returns the blob key of a checkpoint screenshot belonging to an application of the user.
	CheckpointScreenshot(ctx context.Context, userID, appID uuid.UUID, checkpointID int64) (string, error)
	// Apply writes a change if the application is still at app.Version; ErrConflict otherwise.
	Apply(ctx context.Context, userID uuid.UUID, app App, c Change) error
	// Candidate collects what the guards need for a job of the user.
	Candidate(ctx context.Context, userID uuid.UUID, jobID int64) (Candidate, error)
	// StaleSubmitting lists applications in submitting since before (system role).
	StaleSubmitting(ctx context.Context, before time.Time) ([]Ref, error)
	// HandoffsDue lists waiting applications whose window ends before the given time (system role).
	HandoffsDue(ctx context.Context, before time.Time) ([]Handoff, error)
	// MarkWarned records the notice once; false when it was recorded before or the application moved on.
	MarkWarned(ctx context.Context, r Ref, at time.Time) (bool, error)
	// ExpiredScreenshots lists checkpoint screenshots older than before (system role).
	ExpiredScreenshots(ctx context.Context, before time.Time) ([]ExpiredScreenshot, error)
	// ClearScreenshot clears the screenshot blob reference on a checkpoint after its blob was deleted.
	ClearScreenshot(ctx context.Context, userID uuid.UUID, checkpointID int64) error
}
