package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Run limits: a lease is valid for RunTTL; one host gets no second run while one is active and at most one lease
// per HostGap.
const (
	RunTTL  = 60 * time.Minute
	HostGap = 5 * time.Minute
)

// ErrNoRun reports a token that names no active run.
var ErrNoRun = errors.New("no active run for this token")

// Lease is a leased run.
type Lease struct {
	RunID, AppID, UserID uuid.UUID
	JobID                int64
	Host                 string
	Token                string // only in the lease answer; the store keeps its hash
	ExpiresAt            time.Time
}

// RunRef names an active run.
type RunRef struct {
	RunID, AppID, UserID uuid.UUID
}

// Checkpoint is a stored step of a run.
type Checkpoint struct {
	Step       string
	Page       int
	URL        string
	Data       []byte // JSON
	Screenshot string // blob key, empty if none
	At         time.Time
}

// RunStore persists runs (row-level security; leasing in the system role).
type RunStore interface {
	// LeaseNext leases the oldest queued application that passes the per-host limits.
	LeaseNext(ctx context.Context, runnerID string, tokenHash []byte, runnerChoice string, userID *uuid.UUID, now, expires, gapStart time.Time) (Lease, bool, error)
	RunByToken(ctx context.Context, tokenHash []byte) (RunRef, time.Time, *time.Time, error)
	FinishRun(ctx context.Context, r RunRef, outcome string, at time.Time) error
	AddCheckpoint(ctx context.Context, r RunRef, c Checkpoint) error
	// SetAdapter records the ATS adapter of a run.
	SetAdapter(ctx context.Context, r RunRef, adapter string) error
	UserByHomeRunnerToken(ctx context.Context, tokenHash []byte) (uuid.UUID, error)
}

func tokenHash(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// Runs leases applications to runners and authorizes the run calls.
type Runs struct {
	apps  *Service
	store RunStore
}

// NewRuns returns the run service on top of the application service.
func NewRuns(apps *Service, store RunStore) *Runs { return &Runs{apps: apps, store: store} }

// Lease hands out the next run: the application is started (guards run first; a failing guard parks it and the
// next one is tried). ok is false when there is nothing to do.
func (r *Runs) Lease(ctx context.Context, runnerID string, choice RunnerChoice, userID *uuid.UUID) (Lease, bool, error) {
	runnerChoice := string(choice)
	if runnerChoice == "" {
		runnerChoice = string(RunnerServer)
	}
	for range 5 {
		b := make([]byte, 32)
		_, _ = rand.Read(b)
		token := base64.RawURLEncoding.EncodeToString(b)
		now := r.apps.now()
		l, ok, err := r.store.LeaseNext(ctx, runnerID, tokenHash(token), runnerChoice, userID, now, now.Add(RunTTL), now.Add(-HostGap))
		if err != nil || !ok {
			return Lease{}, false, err
		}
		ref := RunRef{RunID: l.RunID, AppID: l.AppID, UserID: l.UserID}
		if err := r.apps.Start(ctx, l.UserID, l.AppID, ActorRunner); err != nil {
			_ = r.store.FinishRun(ctx, ref, "guard", now)
			if errors.Is(err, ErrExcluded) || errors.Is(err, ErrDuplicate) || errors.Is(err, ErrCompanyWindow) ||
				errors.Is(err, ErrConflict) || errors.Is(err, ErrTransition) {
				continue
			}
			return Lease{}, false, err
		}
		l.Token = token
		return l, true, nil
	}
	return Lease{}, false, nil
}

// UserByHomeRunnerToken finds the user of a home runner token hash.
func (r *Runs) UserByHomeRunnerToken(ctx context.Context, tokenHash []byte) (uuid.UUID, error) {
	return r.store.UserByHomeRunnerToken(ctx, tokenHash)
}

// Authorize returns the active run of a token.
func (r *Runs) Authorize(ctx context.Context, token string) (RunRef, error) {
	if len(token) < 40 || len(token) > 100 {
		return RunRef{}, ErrNoRun
	}
	ref, expires, finished, err := r.store.RunByToken(ctx, tokenHash(token))
	if err != nil {
		return RunRef{}, err
	}
	if finished != nil || !r.apps.now().Before(expires) {
		return RunRef{}, ErrNoRun
	}
	return ref, nil
}

// Checkpoint stores a step.
func (r *Runs) Checkpoint(ctx context.Context, ref RunRef, c Checkpoint) error {
	c.At = r.apps.now()
	return r.store.AddCheckpoint(ctx, ref, c)
}

// Ask hands the run to the user; the run stays active during the handoff so the runner can stream the browser
// and continue once the user answers.
func (r *Runs) Ask(ctx context.Context, ref RunRef, questions any) error {
	_, err := r.apps.Fire(ctx, ref.UserID, ref.AppID, EvAsk, ActorRunner, map[string]any{"questions": questions})
	return err
}

// BeginSubmit checks the guards and the autonomy rule (ADR 0016), then registers the submit of the run's
// application. A failing guard parks the application and returns the guard's error. Without the user's approval
// and a green rule the run asks for the approval instead: the application waits on the approval list and an
// *ApprovalError (ErrNeedsApproval) is returned. Only a nil answer allows the click.
func (r *Runs) BeginSubmit(ctx context.Context, ref RunRef) error {
	s := r.apps
	app, err := s.store.Get(ctx, ref.UserID, ref.AppID)
	if err != nil {
		return err
	}
	if app.State != Running {
		return fmt.Errorf("%w: %s on %s", ErrTransition, EvBeginSubmit, app.State)
	}
	c, err := s.store.Candidate(ctx, ref.UserID, app.JobID)
	if err != nil {
		return err
	}
	now := s.now()
	if gerr := Guard(c, now); gerr != nil {
		if _, err := s.apply(ctx, ref.UserID, ref.AppID, EvPark, ActorSystem, map[string]any{"guard": gerr.Error()}, false); err != nil {
			return err
		}
		return gerr
	}
	via := "approval"
	if app.Approved == nil {
		in, err := s.store.RuleInput(ctx, ref.UserID, app, ref.RunID, now)
		if err != nil {
			return err
		}
		if reasons := Rule(in, now); len(reasons) > 0 {
			qs := []Question{{Kind: QuestionApproval, Label: QuestionApproval}}
			if _, err := s.applyChange(ctx, ref.UserID, ref.AppID, Change{Event: EvAsk, Actor: ActorRunner,
				Detail: map[string]any{"questions": qs, "reasons": reasons}, Approval: reasons}); err != nil {
				return err
			}
			return &ApprovalError{Reasons: reasons}
		}
		via = "rule"
	}
	_, err = s.applyChange(ctx, ref.UserID, ref.AppID, Change{Event: EvBeginSubmit, Actor: ActorRunner,
		Detail: map[string]any{"via": via}, BeginSubmit: true})
	return err
}

// SetAdapter records the ATS adapter of a run; finished runs per adapter measure its maturity.
func (r *Runs) SetAdapter(ctx context.Context, ref RunRef, adapter string) error {
	return r.store.SetAdapter(ctx, ref, adapter)
}

// Submitted records the submit and, if the page confirmed it, the confirmation; the run ends.
func (r *Runs) Submitted(ctx context.Context, ref RunRef, confirmed bool, effect any) error {
	if _, err := r.apps.Fire(ctx, ref.UserID, ref.AppID, EvSubmitted, ActorRunner, map[string]any{"effect": effect}); err != nil {
		return err
	}
	if confirmed {
		if _, err := r.apps.Fire(ctx, ref.UserID, ref.AppID, EvConfirm, ActorRunner, map[string]any{"via": "page"}); err != nil {
			return err
		}
	}
	return r.store.FinishRun(ctx, ref, "submitted", r.apps.now())
}

// Park stops the run for a human look.
func (r *Runs) Park(ctx context.Context, ref RunRef, reason, screenshot string) error {
	if _, err := r.apps.Fire(ctx, ref.UserID, ref.AppID, EvPark, ActorRunner,
		map[string]any{"reason": reason, "screenshot": screenshot}); err != nil && !errors.Is(err, ErrTransition) {
		return err
	}
	return r.store.FinishRun(ctx, ref, "parked", r.apps.now())
}
