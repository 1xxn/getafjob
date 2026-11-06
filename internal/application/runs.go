package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
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
	LeaseNext(ctx context.Context, runnerID string, tokenHash []byte, now, expires, gapStart time.Time) (Lease, bool, error)
	RunByToken(ctx context.Context, tokenHash []byte) (RunRef, time.Time, *time.Time, error)
	FinishRun(ctx context.Context, r RunRef, outcome string, at time.Time) error
	AddCheckpoint(ctx context.Context, r RunRef, c Checkpoint) error
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
func (r *Runs) Lease(ctx context.Context, runnerID string) (Lease, bool, error) {
	for range 5 {
		b := make([]byte, 32)
		_, _ = rand.Read(b)
		token := base64.RawURLEncoding.EncodeToString(b)
		now := r.apps.now()
		l, ok, err := r.store.LeaseNext(ctx, runnerID, tokenHash(token), now, now.Add(RunTTL), now.Add(-HostGap))
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

// Ask hands the run to the user and ends it (the answers start a new run).
func (r *Runs) Ask(ctx context.Context, ref RunRef, questions any) error {
	if _, err := r.apps.Fire(ctx, ref.UserID, ref.AppID, EvAsk, ActorRunner, map[string]any{"questions": questions}); err != nil {
		return err
	}
	return r.store.FinishRun(ctx, ref, "asked", r.apps.now())
}

// BeginSubmit registers the submit of the run's application.
func (r *Runs) BeginSubmit(ctx context.Context, ref RunRef) error {
	return r.apps.BeginSubmit(ctx, ref.UserID, ref.AppID)
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
		map[string]any{"reason": reason, "screenshot": screenshot}); err != nil {
		return err
	}
	return r.store.FinishRun(ctx, ref, "parked", r.apps.now())
}
