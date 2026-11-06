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

// Ref names an application of a user.
type Ref struct {
	ID, UserID uuid.UUID
}

// Store persists applications per user (row-level security).
type Store interface {
	// Create returns the application of the user for the job, creating it in state if there is none.
	Create(ctx context.Context, userID uuid.UUID, jobID int64, state State, at time.Time) (uuid.UUID, error)
	Get(ctx context.Context, userID, id uuid.UUID) (App, error)
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
}
