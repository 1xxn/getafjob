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
	// ApprovalAsked is when a run asked for the user's approval before the submit, with the rule's reasons.
	ApprovalAsked   *time.Time
	ApprovalReasons []Reason
	// Approved is when the user approved the submit; the next submit of the application needs no green rule.
	Approved     *time.Time
	CvDocumentID *uuid.UUID
}

// AwaitingApproval reports an application on the approval list: a run asked for the approval, the user has not
// given it yet, and the application has not been sent or dropped.
func (a App) AwaitingApproval() bool {
	return a.ApprovalAsked != nil && a.Approved == nil && (a.State == NeedsInput || a.State == Parked)
}

// Approval is an application on the user's approval list.
type Approval struct {
	ID           uuid.UUID
	JobID        int64
	State        State
	Title        string
	Company      string
	Score        int
	Reasons      []Reason
	Asked        time.Time
	CvDocumentID *uuid.UUID
}

// ApplicationSummary is a concise summary of an application for export or listing.
type ApplicationSummary struct {
	ID              uuid.UUID
	JobID           int64
	State           State
	Version         int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
	ApprovalAsked   *time.Time
	ApprovalReasons []Reason
	Approved        *time.Time
	Company         string
	Title           string
	Location        string
	URL             string
	ApplyURL        string
	Events          []EventRecord
}

// EventRecord records a state machine transition event.
type EventRecord struct {
	Event    Event
	From, To State
	Actor    Actor
	Detail   map[string]any
	At       time.Time
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
	// Approval, when not nil, puts the application on the approval list with these reasons (set with EvAsk).
	Approval []Reason
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

// PlannerCandidate is an open job matching the user's criteria.
type PlannerCandidate struct {
	JobID int64
	Score int
}

// PlanStats summarizes what a run of the planner queued.
type PlanStats struct {
	UsersEligible int
	JobsPlanned   int
}

// Store persists applications per user (row-level security).
type Store interface {
	// Create returns the application of the user for the job, creating it in state if there is none.
	Create(ctx context.Context, userID uuid.UUID, jobID int64, state State, at time.Time) (uuid.UUID, error)
	Get(ctx context.Context, userID, id uuid.UUID) (App, error)
	// ByJob returns the user's application for a specific job if one exists.
	ByJob(ctx context.Context, userID uuid.UUID, jobID int64) (App, error)
	// SetCvDocument sets or clears the chosen CV variant for an application.
	SetCvDocument(ctx context.Context, userID, appID uuid.UUID, cvID *uuid.UUID, at time.Time) error
	// SetJobCvDocument sets or creates an application for the job with the chosen CV variant.
	SetJobCvDocument(ctx context.Context, userID uuid.UUID, jobID int64, cvID *uuid.UUID, at time.Time) (uuid.UUID, error)
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

	// Settings returns the user's autonomy settings, DefaultSettings if they saved none.
	Settings(ctx context.Context, userID uuid.UUID) (Settings, error)
	SaveSettings(ctx context.Context, userID uuid.UUID, st Settings, at time.Time) error
	// SaveHomeRunnerToken records a home runner token hash for the user.
	SaveHomeRunnerToken(ctx context.Context, userID uuid.UUID, hash []byte, at time.Time) error
	// UserByHomeRunnerToken looks up the user an active home runner token belongs to (system role).
	UserByHomeRunnerToken(ctx context.Context, hash []byte) (uuid.UUID, error)
	// RuleInput collects what the rule needs before the submit of a run: settings, match score, guard inputs,
	// the run's filled fields and final page, the user's recent submits and the maturity of the run's adapter.
	RuleInput(ctx context.Context, userID uuid.UUID, app App, runID uuid.UUID, now time.Time) (RuleInput, error)
	// Approve records the user's approval if the application is still at app.Version; ErrConflict otherwise.
	Approve(ctx context.Context, userID uuid.UUID, app App, at time.Time) error
	// Approvals lists the applications on the user's approval list, oldest request first.
	Approvals(ctx context.Context, userID uuid.UUID) ([]Approval, error)

	// PlanUsers lists active users whose autonomy level enables auto-planning and who have daily quota > 0 (system role).
	PlanUsers(ctx context.Context) ([]uuid.UUID, error)
	// ActiveApplicationsCount counts applications in queued, running, or submitting for the user.
	ActiveApplicationsCount(ctx context.Context, userID uuid.UUID) (int, error)
	// UserLastPacedAt returns the most recent timestamp an application was queued, run leased, or submit started for pacing.
	UserLastPacedAt(ctx context.Context, userID uuid.UUID) (time.Time, error)
	// PlannerCandidates returns open matching jobs for the user ordered by match score descending.
	PlannerCandidates(ctx context.Context, userID uuid.UUID, minScore int) ([]PlannerCandidate, error)
	// SubmitsSince counts the submit attempts of the user started since at.
	SubmitsSince(ctx context.Context, userID uuid.UUID, since time.Time) (int, error)
	// UserApplications returns all applications of the user, ordered by creation descending.
	UserApplications(ctx context.Context, userID uuid.UUID) ([]ApplicationSummary, error)
	// UserScreenshots returns all distinct screenshot blob keys belonging to the user's run checkpoints.
	UserScreenshots(ctx context.Context, userID uuid.UUID) ([]string, error)
}
