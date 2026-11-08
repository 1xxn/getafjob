package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Actor is who caused a transition.
type Actor string

// Actors.
const (
	ActorUser   Actor = "user"
	ActorWorker Actor = "worker"
	ActorRunner Actor = "runner"
	ActorSystem Actor = "system"
)

// The user's turn in a run: the runner keeps the browser for HandoffWindow; a notice goes out HandoffNotice
// before the end, and an unanswered handoff is parked.
const (
	HandoffWindow = 20 * time.Minute
	HandoffNotice = 5 * time.Minute
)

// StuckAfter is how long an application may stay in submitting before it counts as crashed: a submit click with
// its verification takes seconds.
const StuckAfter = 15 * time.Minute

// Service changes application states.
type Service struct {
	store Store
	now   func() time.Time
}

// NewService returns an application service.
func NewService(s Store) *Service { return &Service{store: s, now: time.Now} }

// WithClock sets the clock (tests).
func (s *Service) WithClock(now func() time.Time) *Service { s.now = now; return s }

// Get returns an application of the user.
func (s *Service) Get(ctx context.Context, userID, id uuid.UUID) (App, error) {
	return s.store.Get(ctx, userID, id)
}

// Questions returns the questions of the last ask event of an application.
func (s *Service) Questions(ctx context.Context, userID, id uuid.UUID) ([]Question, error) {
	return s.store.Questions(ctx, userID, id)
}

// Create returns the user's application for a job, creating it as matched.
func (s *Service) Create(ctx context.Context, userID uuid.UUID, jobID int64) (uuid.UUID, error) {
	return s.store.Create(ctx, userID, jobID, Matched, s.now())
}

// outcomes finishes the submit attempt for transitions out of submitting and submit_unknown.
func outcome(from State, ev Event) string {
	switch {
	case from == Submitting && ev == EvSubmitted, from == SubmitUnknown && (ev == EvReconciled || ev == EvConfirm):
		return "submitted"
	case from == Submitting && (ev == EvCrash || ev == EvFail):
		return "unknown"
	case from == Submitting && ev == EvPark, from == SubmitUnknown && ev == EvNotSent:
		return "not_sent"
	}
	return ""
}

// Fire applies an event. Starting a run and the submit have their own methods (guards, register).
func (s *Service) Fire(ctx context.Context, userID, id uuid.UUID, ev Event, actor Actor, detail map[string]any) (State, error) {
	if ev == EvStart || ev == EvBeginSubmit {
		return "", fmt.Errorf("%w: %s needs its own call", ErrTransition, ev)
	}
	return s.apply(ctx, userID, id, ev, actor, detail, false)
}

func (s *Service) apply(ctx context.Context, userID, id uuid.UUID, ev Event, actor Actor, detail map[string]any,
	submit bool,
) (State, error) {
	app, err := s.store.Get(ctx, userID, id)
	if err != nil {
		return "", err
	}
	to, err := Next(app.State, ev)
	if err != nil {
		return app.State, err
	}
	c := Change{Event: ev, From: app.State, To: to, Actor: actor, Detail: detail, At: s.now(), BeginSubmit: submit,
		Outcome: outcome(app.State, ev)}
	if ev == EvAsk {
		until := c.At.Add(HandoffWindow)
		c.HandoffUntil = &until
	}
	if err := s.store.Apply(ctx, userID, app, c); err != nil {
		return app.State, err
	}
	return to, nil
}

// Start runs the guards and moves a queued application to running. A failing guard parks it with the reason.
func (s *Service) Start(ctx context.Context, userID, id uuid.UUID, actor Actor) error {
	app, err := s.store.Get(ctx, userID, id)
	if err != nil {
		return err
	}
	if app.State != Queued {
		return fmt.Errorf("%w: start on %s", ErrTransition, app.State)
	}
	c, err := s.store.Candidate(ctx, userID, app.JobID)
	if err != nil {
		return err
	}
	if gerr := Guard(c, s.now()); gerr != nil {
		if _, err := s.apply(ctx, userID, id, EvPark, ActorSystem, map[string]any{"guard": gerr.Error()}, false); err != nil {
			return err
		}
		return gerr
	}
	_, err = s.apply(ctx, userID, id, EvStart, actor, nil, false)
	return err
}

// BeginSubmit writes the submit attempt and the state submitting in one transaction, before the click. A second
// call for the same application fails with ErrAlreadySubmitted or ErrTransition; the click must not happen then.
func (s *Service) BeginSubmit(ctx context.Context, userID, id uuid.UUID) error {
	_, err := s.apply(ctx, userID, id, EvBeginSubmit, ActorRunner, nil, true)
	return err
}

// RecoverCrashed turns applications stuck in submitting into submit_unknown; they are reconciled through the
// confirmation page or the inbox and never retried automatically.
func (s *Service) RecoverCrashed(ctx context.Context) (int, error) {
	refs, err := s.store.StaleSubmitting(ctx, s.now().Add(-StuckAfter))
	if err != nil {
		return 0, err
	}
	n := 0
	var errs []error
	for _, r := range refs {
		_, err := s.apply(ctx, r.UserID, r.ID, EvCrash, ActorSystem, map[string]any{"reason": "stuck in submitting"}, false)
		switch {
		case err == nil:
			n++
		case errors.Is(err, ErrConflict), errors.Is(err, ErrTransition):
			// it moved on meanwhile
		default:
			errs = append(errs, err)
		}
	}
	return n, errors.Join(errs...)
}

// WatchHandoffs sends the notice for handoff windows that close within HandoffNotice (once) and parks
// applications whose window has closed. It returns how many it warned and parked.
func (s *Service) WatchHandoffs(ctx context.Context, n Notifier) (warned, parked int, err error) {
	now := s.now()
	due, err := s.store.HandoffsDue(ctx, now.Add(HandoffNotice))
	if err != nil {
		return 0, 0, err
	}
	var errs []error
	for _, h := range due {
		if !now.Before(h.Until) {
			_, err := s.apply(ctx, h.UserID, h.ID, EvPark, ActorSystem, map[string]any{"reason": "handoff window closed"}, false)
			switch {
			case err == nil:
				parked++
			case errors.Is(err, ErrConflict), errors.Is(err, ErrTransition):
			default:
				errs = append(errs, err)
			}
			continue
		}
		if h.Warned {
			continue
		}
		ok, err := s.store.MarkWarned(ctx, h.Ref, now)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if ok {
			warned++
			if err := n.HandoffClosing(ctx, h.Ref, h.Until); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return warned, parked, errors.Join(errs...)
}
