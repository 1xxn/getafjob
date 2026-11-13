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
	store     Store
	now       func() time.Time
	unlimited bool
}

// NewService returns an application service.
func NewService(s Store) *Service { return &Service{store: s, now: time.Now} }

// WithoutLimits turns off the throughput limits for a self-hosted instance: no daily quota and no per-host gap.
// Guard rails (exclusions, duplicates, company window, approval rule, submit register) stay.
func (s *Service) WithoutLimits() *Service { s.unlimited = true; return s }

// Unlimited reports whether the throughput limits are off.
func (s *Service) Unlimited() bool { return s.unlimited }

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

// Evidence returns the audit evidence of an application.
func (s *Service) Evidence(ctx context.Context, userID, id uuid.UUID) (Evidence, error) {
	return s.store.Evidence(ctx, userID, id)
}

// CheckpointScreenshot returns the blob key of a checkpoint screenshot of an application.
func (s *Service) CheckpointScreenshot(ctx context.Context, userID, appID uuid.UUID, checkpointID int64) (string, error) {
	return s.store.CheckpointScreenshot(ctx, userID, appID, checkpointID)
}

// UserApplications returns all applications of the user with their timeline events.
func (s *Service) UserApplications(ctx context.Context, userID uuid.UUID) ([]ApplicationSummary, error) {
	return s.store.UserApplications(ctx, userID)
}

// UserScreenshots returns all distinct screenshot blob keys for the user.
func (s *Service) UserScreenshots(ctx context.Context, userID uuid.UUID) ([]string, error) {
	return s.store.UserScreenshots(ctx, userID)
}

// PurgeExpiredScreenshots deletes run screenshots older than ScreenshotRetention from the blob store and clears
// their checkpoint references.
func (s *Service) PurgeExpiredScreenshots(ctx context.Context, blobs ScreenshotDeleter) (int, error) {
	expired, err := s.store.ExpiredScreenshots(ctx, s.now().Add(-ScreenshotRetention))
	if err != nil {
		return 0, err
	}
	purged := 0
	var errs []error
	for _, sc := range expired {
		if blobs != nil {
			if err := blobs.Delete(ctx, sc.UserID, sc.Key); err != nil {
				errs = append(errs, err)
				continue
			}
		}
		if err := s.store.ClearScreenshot(ctx, sc.UserID, sc.CheckpointID); err != nil {
			errs = append(errs, err)
			continue
		}
		purged++
	}
	return purged, errors.Join(errs...)
}

// Create returns the user's application for a job, creating it as matched.
func (s *Service) Create(ctx context.Context, userID uuid.UUID, jobID int64) (uuid.UUID, error) {
	return s.store.Create(ctx, userID, jobID, Matched, s.now())
}

// ByJob returns the user's application for a job if one exists.
func (s *Service) ByJob(ctx context.Context, userID uuid.UUID, jobID int64) (App, error) {
	return s.store.ByJob(ctx, userID, jobID)
}

// SetCV sets or clears the chosen CV variant for an existing application.
func (s *Service) SetCV(ctx context.Context, userID, appID uuid.UUID, cvID *uuid.UUID) error {
	return s.store.SetCvDocument(ctx, userID, appID, cvID, s.now())
}

// SetJobCV sets or creates an application for a job with the chosen CV variant.
func (s *Service) SetJobCV(ctx context.Context, userID uuid.UUID, jobID int64, cvID *uuid.UUID) (uuid.UUID, error) {
	return s.store.SetJobCvDocument(ctx, userID, jobID, cvID, s.now())
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
	return s.applyChange(ctx, userID, id, Change{Event: ev, Actor: actor, Detail: detail, BeginSubmit: submit})
}

// applyChange fills in the states, time, outcome and handoff window of c and writes it.
func (s *Service) applyChange(ctx context.Context, userID, id uuid.UUID, c Change) (State, error) {
	app, err := s.store.Get(ctx, userID, id)
	if err != nil {
		return "", err
	}
	to, err := Next(app.State, c.Event)
	if err != nil {
		return app.State, err
	}
	c.From, c.To, c.At, c.Outcome = app.State, to, s.now(), outcome(app.State, c.Event)
	if c.Event == EvAsk {
		until := c.At.Add(HandoffWindow)
		c.HandoffUntil = &until
	} else {
		c.Approval = nil
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
// Runs reach it only through Runs.BeginSubmit, which checks the guards and the autonomy rule first.
func (s *Service) BeginSubmit(ctx context.Context, userID, id uuid.UUID) error {
	_, err := s.apply(ctx, userID, id, EvBeginSubmit, ActorRunner, nil, true)
	return err
}

// Settings returns the user's autonomy settings.
func (s *Service) Settings(ctx context.Context, userID uuid.UUID) (Settings, error) {
	st, err := s.store.Settings(ctx, userID)
	st.Unlimited = s.unlimited
	return st, err
}

// SaveSettings validates and stores the user's autonomy settings.
func (s *Service) SaveSettings(ctx context.Context, userID uuid.UUID, st Settings) error {
	if err := st.Validate(); err != nil {
		return err
	}
	return s.store.SaveSettings(ctx, userID, st, s.now())
}

// GenerateHomeRunnerToken creates and records a new home runner token for the user, returning the plaintext token.
func (s *Service) GenerateHomeRunnerToken(ctx context.Context, userID uuid.UUID) (string, error) {
	plain, hash, err := NewHomeRunnerToken()
	if err != nil {
		return "", err
	}
	now := s.now()
	if err := s.store.SaveHomeRunnerToken(ctx, userID, hash, now); err != nil {
		return "", err
	}
	return plain, nil
}

// Queue queues the user's application for a job, creating it if needed: a new application is queued, a parked or
// failed one is queued again. Anything else (already queued, in a run, sent or skipped) stays as it is.
func (s *Service) Queue(ctx context.Context, userID uuid.UUID, jobID int64) (uuid.UUID, State, error) {
	id, err := s.Create(ctx, userID, jobID)
	if err != nil {
		return id, "", err
	}
	app, err := s.store.Get(ctx, userID, id)
	if err != nil {
		return id, "", err
	}
	switch app.State {
	case Matched:
		st, err := s.apply(ctx, userID, id, EvQueue, ActorUser, nil, false)
		return id, st, err
	case Parked, Failed:
		st, err := s.apply(ctx, userID, id, EvRequeue, ActorUser, nil, false)
		return id, st, err
	default:
		return id, app.State, nil
	}
}

// Approvals lists the user's approval list.
func (s *Service) Approvals(ctx context.Context, userID uuid.UUID) ([]Approval, error) {
	return s.store.Approvals(ctx, userID)
}

// NavCounts counts the user's applications that wait for an answer or an approval, and the running ones.
func (s *Service) NavCounts(ctx context.Context, userID uuid.UUID) (NavCounts, error) {
	return s.store.NavCounts(ctx, userID)
}

// Approve records the user's approval of the submit. A parked or failed application is queued again so that the
// next run submits it; a waiting one (needs_input) stays for the caller to resume the run or queue it again.
func (s *Service) Approve(ctx context.Context, userID, id uuid.UUID) (State, error) {
	app, err := s.store.Get(ctx, userID, id)
	if err != nil {
		return "", err
	}
	if !app.State.Approvable() {
		return app.State, fmt.Errorf("%w: approve on %s", ErrTransition, app.State)
	}
	if err := s.store.Approve(ctx, userID, app, s.now()); err != nil {
		return app.State, err
	}
	switch app.State {
	case Parked, Failed:
		return s.apply(ctx, userID, id, EvRequeue, ActorUser, map[string]any{"via": "approval"}, false)
	case Matched:
		return s.apply(ctx, userID, id, EvQueue, ActorUser, map[string]any{"via": "approval"}, false)
	default:
		return app.State, nil
	}
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

// GetNotes returns private notes and interview dates for an application.
func (s *Service) GetNotes(ctx context.Context, userID, appID uuid.UUID) (NoteData, error) {
	return s.store.GetNotes(ctx, userID, appID)
}

// SaveNotes saves private notes, compensation discussed, and interview/follow-up dates for an application.
func (s *Service) SaveNotes(ctx context.Context, userID, appID uuid.UUID, notes, salary string, interviewAt, followUpAt *time.Time) error {
	return s.store.SaveNotes(ctx, userID, appID, notes, salary, interviewAt, followUpAt, s.now())
}

// Pipeline returns all applications of a user categorized for the pipeline tracker.
func (s *Service) Pipeline(ctx context.Context, userID uuid.UUID) ([]PipelineApplication, error) {
	return s.store.PipelineApplications(ctx, userID)
}

// Bookmark saves a job into the candidate's pipeline in matched state.
func (s *Service) Bookmark(ctx context.Context, userID uuid.UUID, jobID int64) (uuid.UUID, error) {
	return s.store.BookmarkJob(ctx, userID, jobID, s.now())
}

// ApplyExternal records that the user applied to the job outside getafjob: the application moves to submitted
// through the state machine with an event. One that is already sent stays as it is; one in a run is refused.
func (s *Service) ApplyExternal(ctx context.Context, userID uuid.UUID, jobID int64) (uuid.UUID, error) {
	id, err := s.store.BookmarkJob(ctx, userID, jobID, s.now())
	if err != nil {
		return uuid.Nil, err
	}
	app, err := s.store.Get(ctx, userID, id)
	if err != nil {
		return id, err
	}
	switch {
	case app.State.Sent():
		return id, nil
	case app.State.Open():
		return id, fmt.Errorf("%w: external submit on %s", ErrTransition, app.State)
	}
	_, err = s.Fire(ctx, userID, id, EvSubmitted, ActorUser, map[string]any{"external": true})
	return id, err
}

// Candidate collects guard inputs for a job of the user.
func (s *Service) Candidate(ctx context.Context, userID uuid.UUID, jobID int64) (Candidate, error) {
	return s.store.Candidate(ctx, userID, jobID)
}

// CheckGuard verifies whether an application can start for a job based on filters, duplicates, and company limit.
func (s *Service) CheckGuard(ctx context.Context, userID uuid.UUID, jobID int64) error {
	if app, err := s.store.ByJob(ctx, userID, jobID); err == nil {
		if app.State.Open() || app.State.Sent() {
			return ErrDuplicate
		}
	}
	c, err := s.store.Candidate(ctx, userID, jobID)
	if err != nil {
		return err
	}
	return Guard(c, s.now())
}

// Overview is what the dashboard shows of the user's applications: all of them with their pipeline data, why
// parked ones stopped, and the submits per day of the last 30 days.
type Overview struct {
	Apps         []PipelineApplication
	ParkReasons  map[uuid.UUID]string
	SubmitsByDay map[time.Time]int // UTC midnight -> count
	Since        time.Time
}

// Overview collects the dashboard data in three queries.
func (s *Service) Overview(ctx context.Context, userID uuid.UUID) (Overview, error) {
	apps, err := s.store.PipelineApplications(ctx, userID)
	if err != nil {
		return Overview{}, err
	}
	reasons, err := s.store.ParkReasons(ctx, userID)
	if err != nil {
		return Overview{}, err
	}
	since := s.now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -29)
	days, err := s.store.SubmitsPerDay(ctx, userID, since)
	if err != nil {
		return Overview{}, err
	}
	return Overview{Apps: apps, ParkReasons: reasons, SubmitsByDay: days, Since: since}, nil
}

// Brief is an application with its state and the job's title, for lists of live runs.
type Brief struct {
	ID      uuid.UUID
	JobID   int64
	State   State
	Title   string
	Company string
}

// QueueItem is one application waiting for a runner, in queue order; a paused one is skipped until resumed.
type QueueItem struct {
	ID      uuid.UUID
	JobID   int64
	Title   string
	Company string
	Paused  bool
}

// QueueItems lists the user's queued applications in the order their runs start.
func (s *Service) QueueItems(ctx context.Context, userID uuid.UUID) ([]QueueItem, error) {
	return s.store.QueueItems(ctx, userID)
}

// MoveQueued moves a queued application one place up or down; false when it is already at that end or no longer
// queued.
func (s *Service) MoveQueued(ctx context.Context, userID, id uuid.UUID, up bool) (bool, error) {
	return s.store.MoveQueued(ctx, userID, id, up)
}

// PauseQueued pauses or resumes a queued application: the lease skips a paused one.
func (s *Service) PauseQueued(ctx context.Context, userID, id uuid.UUID, paused bool) (bool, error) {
	return s.store.PauseQueued(ctx, userID, id, paused)
}

// Briefs returns the user's applications with the given IDs (unknown IDs are missing), in one query.
func (s *Service) Briefs(ctx context.Context, userID uuid.UUID, ids []uuid.UUID) ([]Brief, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	return s.store.Briefs(ctx, userID, ids)
}

// StatesByJob returns the state of the user's application per job (jobs without one are missing).
func (s *Service) StatesByJob(ctx context.Context, userID uuid.UUID, jobIDs []int64) (map[int64]State, error) {
	if len(jobIDs) == 0 {
		return map[int64]State{}, nil
	}
	return s.store.StatesByJob(ctx, userID, jobIDs)
}

// SkipJob marks a job as skipped: it gets an application in state skipped, so it leaves the to-do lists. A job
// that is already being applied to or was sent is not changed (the transition is refused).
func (s *Service) SkipJob(ctx context.Context, userID uuid.UUID, jobID int64) error {
	id, err := s.store.BookmarkJob(ctx, userID, jobID, s.now())
	if err != nil {
		return err
	}
	app, err := s.store.Get(ctx, userID, id)
	if err != nil {
		return err
	}
	if app.State == Skipped {
		return nil
	}
	_, err = s.Fire(ctx, userID, id, EvSkip, ActorUser, map[string]any{"reason": "skipped by user"})
	return err
}

// List returns the user's applications with the reason of every parked one (two queries).
func (s *Service) List(ctx context.Context, userID uuid.UUID) ([]PipelineApplication, map[uuid.UUID]string, error) {
	apps, err := s.store.PipelineApplications(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	reasons, err := s.store.ParkReasons(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	return apps, reasons, nil
}

// ParkReason returns why the application was parked last, or "" when it is not parked.
func (s *Service) ParkReason(ctx context.Context, userID, id uuid.UUID) (string, error) {
	reasons, err := s.store.ParkReasons(ctx, userID)
	if err != nil {
		return "", err
	}
	return reasons[id], nil
}

// Retry queues a parked or failed application again by the user's decision; sent ones are refused by the state
// machine, so nothing is ever sent twice.
func (s *Service) Retry(ctx context.Context, userID, id uuid.UUID) (State, error) {
	return s.Fire(ctx, userID, id, EvRequeue, ActorUser, map[string]any{"reason": "retried by user"})
}

// AnswerUsage counts per answer bank entry (by id) the applications that used it.
func (s *Service) AnswerUsage(ctx context.Context, userID uuid.UUID) (map[string]int, error) {
	return s.store.AnswerUsage(ctx, userID)
}
