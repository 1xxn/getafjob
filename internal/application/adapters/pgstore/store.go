// Package pgstore implements application.Store on PostgreSQL; every call runs in the user's own transaction.
package pgstore

//go:generate go tool sqlc generate -f ../../../../sqlc.yaml

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/1xxn/getafjob/internal/application"
	"github.com/1xxn/getafjob/internal/application/adapters/pgstore/q"
	"github.com/1xxn/getafjob/internal/platform/db"
)

// Store is the PostgreSQL application store.
type Store struct{ pool *pgxpool.Pool }

var _ application.Store = (*Store)(nil)

// New returns a store on pool (application role).
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) user(ctx context.Context, userID uuid.UUID, fn func(*q.Queries) error) error {
	return db.InUserTx(ctx, s.pool, userID, func(tx pgx.Tx) error { return fn(q.New(tx)) })
}

// Create returns the application of the user for the job, creating it if needed.
func (s *Store) Create(ctx context.Context, userID uuid.UUID, jobID int64, state application.State, at time.Time) (
	id uuid.UUID, err error,
) {
	err = s.user(ctx, userID, func(qs *q.Queries) error {
		id, err = qs.CreateApplication(ctx, q.CreateApplicationParams{UserID: userID, JobID: jobID, State: string(state), CreatedAt: at})
		return err
	})
	return id, err
}

// Get loads an application of the user.
func (s *Store) Get(ctx context.Context, userID, id uuid.UUID) (a application.App, err error) {
	err = s.user(ctx, userID, func(qs *q.Queries) error {
		r, err := qs.GetApplication(ctx, q.GetApplicationParams{ID: id, UserID: userID})
		if errors.Is(err, pgx.ErrNoRows) {
			return application.ErrNotFound
		}
		var reasons []application.Reason
		for _, re := range r.ApprovalReasons {
			reasons = append(reasons, application.Reason(re))
		}
		a = application.App{
			ID:              r.ID,
			JobID:           r.JobID,
			State:           application.State(r.State),
			Version:         r.Version,
			UpdatedAt:       r.UpdatedAt,
			ApprovalAsked:   r.ApprovalAskedAt,
			ApprovalReasons: reasons,
			Approved:        r.ApprovedAt,
		}
		return err
	})
	return a, err
}

// Questions returns the questions from the latest ask event of the application.
func (s *Store) Questions(ctx context.Context, userID, id uuid.UUID) (out []application.Question, err error) {
	err = s.user(ctx, userID, func(qs *q.Queries) error {
		evs, err := qs.Events(ctx, q.EventsParams{ApplicationID: id, UserID: userID})
		if err != nil {
			return err
		}
		for i := len(evs) - 1; i >= 0; i-- {
			if evs[i].Event != string(application.EvAsk) {
				continue
			}
			var d struct {
				Questions []application.Question `json:"questions"`
			}
			if err := json.Unmarshal(evs[i].Detail, &d); err != nil {
				return err
			}
			out = d.Questions
			return nil
		}
		return nil
	})
	return out, err
}

// Apply writes state, event and submit register in one transaction.
func (s *Store) Apply(ctx context.Context, userID uuid.UUID, a application.App, c application.Change) error {
	detail := []byte("{}")
	if len(c.Detail) > 0 {
		var err error
		if detail, err = json.Marshal(c.Detail); err != nil {
			return err
		}
	}
	return s.user(ctx, userID, func(qs *q.Queries) error {
		n, err := qs.SetState(ctx, q.SetStateParams{ToState: string(c.To), At: c.At, ID: a.ID, UserID: userID,
			FromState: string(c.From), Version: a.Version, HandoffUntil: c.HandoffUntil})
		if err != nil {
			return err
		}
		if n != 1 {
			return application.ErrConflict
		}
		if err := qs.InsertEvent(ctx, q.InsertEventParams{ApplicationID: a.ID, UserID: userID, Event: string(c.Event),
			FromState: string(c.From), ToState: string(c.To), Actor: string(c.Actor), Detail: detail, At: c.At}); err != nil {
			return err
		}
		if len(c.Approval) > 0 {
			reasons := make([]string, len(c.Approval))
			for i, r := range c.Approval {
				reasons[i] = string(r)
			}
			if err := qs.AskApproval(ctx, q.AskApprovalParams{ID: a.ID, UserID: userID, At: &c.At, Reasons: reasons}); err != nil {
				return err
			}
		}
		if c.BeginSubmit {
			err := qs.InsertSubmitAttempt(ctx, q.InsertSubmitAttemptParams{ApplicationID: a.ID, UserID: userID, StartedAt: c.At})
			var pe *pgconn.PgError
			if errors.As(err, &pe) && pe.Code == "23505" {
				return application.ErrAlreadySubmitted
			}
			if err != nil {
				return err
			}
		}
		if c.Outcome != "" {
			return qs.FinishSubmitAttempt(ctx, q.FinishSubmitAttemptParams{ApplicationID: a.ID, UserID: userID,
				Outcome: &c.Outcome, FinishedAt: &c.At})
		}
		return nil
	})
}

// Candidate collects the guard inputs for a job of the user.
func (s *Store) Candidate(ctx context.Context, userID uuid.UUID, jobID int64) (c application.Candidate, err error) {
	err = s.user(ctx, userID, func(qs *q.Queries) error {
		if c.Excluded, err = qs.HardSkipped(ctx, q.HardSkippedParams{UserID: userID, JobID: jobID}); err != nil {
			return err
		}
		states, err := qs.SameJobStates(ctx, q.SameJobStatesParams{UserID: userID, JobID: jobID})
		if err != nil {
			return err
		}
		for _, st := range states {
			c.SameJob = append(c.SameJob, application.State(st))
		}
		c.CompanyApplied, err = qs.CompanySubmits(ctx, q.CompanySubmitsParams{UserID: userID, JobID: jobID})
		return err
	})
	return c, err
}

// StaleSubmitting lists applications stuck in submitting (system role).
func (s *Store) StaleSubmitting(ctx context.Context, before time.Time) (refs []application.Ref, err error) {
	err = db.InSystemTx(ctx, s.pool, func(tx pgx.Tx) error {
		rows, err := q.New(tx).StaleSubmitting(ctx, before)
		for _, r := range rows {
			refs = append(refs, application.Ref{ID: r.ID, UserID: r.UserID})
		}
		return err
	})
	return refs, err
}

// HandoffsDue lists waiting applications whose window ends before the given time (system role).
func (s *Store) HandoffsDue(ctx context.Context, before time.Time) (hs []application.Handoff, err error) {
	err = db.InSystemTx(ctx, s.pool, func(tx pgx.Tx) error {
		rows, err := q.New(tx).HandoffsDue(ctx, &before)
		for _, r := range rows {
			if r.HandoffUntil != nil {
				hs = append(hs, application.Handoff{Ref: application.Ref{ID: r.ID, UserID: r.UserID}, Until: *r.HandoffUntil,
					Warned: r.HandoffWarned})
			}
		}
		return err
	})
	return hs, err
}

// MarkWarned records the handoff notice once, with an event.
func (s *Store) MarkWarned(ctx context.Context, r application.Ref, at time.Time) (ok bool, err error) {
	err = s.user(ctx, r.UserID, func(qs *q.Queries) error {
		n, err := qs.MarkHandoffWarned(ctx, q.MarkHandoffWarnedParams{ID: r.ID, UserID: r.UserID})
		if err != nil || n != 1 {
			return err
		}
		ok = true
		return qs.InsertEvent(ctx, q.InsertEventParams{ApplicationID: r.ID, UserID: r.UserID, Event: "handoff_warning",
			FromState: string(application.NeedsInput), ToState: string(application.NeedsInput), Actor: string(application.ActorSystem),
			Detail: []byte("{}"), At: at})
	})
	return ok, err
}

// Evidence assembles the filled fields, screenshots, confirmation page, and timeline of an application.
func (s *Store) Evidence(ctx context.Context, userID, id uuid.UUID) (ev application.Evidence, err error) {
	err = s.user(ctx, userID, func(qs *q.Queries) error {
		evs, err := qs.Events(ctx, q.EventsParams{ApplicationID: id, UserID: userID})
		if err != nil {
			return err
		}
		cps, err := qs.ApplicationCheckpoints(ctx, q.ApplicationCheckpointsParams{ApplicationID: id, UserID: userID})
		if err != nil {
			return err
		}
		sawSubmit := false
		for _, c := range cps {
			var d struct {
				Filled []struct {
					ID     string `json:"id"`
					Label  string `json:"label"`
					Shown  string `json:"shown"`
					Source string `json:"source"`
				} `json:"filled"`
			}
			_ = json.Unmarshal(c.Data, &d)
			for _, f := range d.Filled {
				ef := application.EvidenceField{
					Page:   int(c.Page),
					ID:     f.ID,
					Label:  cmp.Or(f.Label, f.ID),
					Value:  f.Shown,
					Source: f.Source,
				}
				idx := slices.IndexFunc(ev.Fields, func(existing application.EvidenceField) bool {
					return existing.Page == ef.Page && ef.ID != "" && existing.ID == ef.ID
				})
				if idx >= 0 {
					ev.Fields[idx] = ef
				} else {
					ev.Fields = append(ev.Fields, ef)
				}
			}
			if c.Step == "submit" {
				sawSubmit = true
				if c.Screenshot != "" {
					ev.BeforeSubmitCheckpointID = c.ID
				}
			}
			if c.Step == "verify" && sawSubmit {
				if c.Screenshot != "" {
					ev.ConfirmationCheckpointID = c.ID
				}
				if c.Url != "" {
					ev.ConfirmationURL = c.Url
				}
			}
			var shotID int64
			if c.Screenshot != "" {
				shotID = c.ID
			}
			ev.Timeline = append(ev.Timeline, application.TimelineEntry{
				At:           c.At,
				Kind:         "checkpoint",
				Step:         c.Step,
				Page:         int(c.Page),
				URL:          c.Url,
				CheckpointID: shotID,
			})
		}
		for _, e := range evs {
			if e.Event == string(application.EvConfirm) || e.Event == string(application.EvReconciled) ||
				e.ToState == string(application.Confirmed) {
				ev.Confirmed = true
			}
			var d struct {
				Reason string `json:"reason"`
				Guard  string `json:"guard"`
				Via    string `json:"via"`
			}
			_ = json.Unmarshal(e.Detail, &d)
			ev.Timeline = append(ev.Timeline, application.TimelineEntry{
				At:        e.At,
				Kind:      "event",
				Step:      e.Event,
				FromState: application.State(e.FromState),
				ToState:   application.State(e.ToState),
				Actor:     application.Actor(e.Actor),
				Detail:    cmp.Or(d.Reason, d.Guard, d.Via),
			})
		}
		slices.SortStableFunc(ev.Timeline, func(a, b application.TimelineEntry) int {
			return a.At.Compare(b.At)
		})
		return nil
	})
	return ev, err
}

// CheckpointScreenshot returns the blob key of a checkpoint screenshot belonging to an application of the user.
func (s *Store) CheckpointScreenshot(ctx context.Context, userID, appID uuid.UUID, checkpointID int64) (key string, err error) {
	err = s.user(ctx, userID, func(qs *q.Queries) error {
		shot, err := qs.CheckpointScreenshot(ctx, q.CheckpointScreenshotParams{
			ApplicationID: appID,
			ID:            checkpointID,
			UserID:        userID,
		})
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && shot == "") {
			return application.ErrNotFound
		}
		key = shot
		return err
	})
	return key, err
}

// ExpiredScreenshots lists checkpoint screenshots older than before (system role).
func (s *Store) ExpiredScreenshots(ctx context.Context, before time.Time) (out []application.ExpiredScreenshot, err error) {
	err = db.InSystemTx(ctx, s.pool, func(tx pgx.Tx) error {
		rows, err := q.New(tx).ExpiredCheckpointScreenshots(ctx, before)
		for _, r := range rows {
			out = append(out, application.ExpiredScreenshot{
				CheckpointID: r.ID,
				UserID:       r.UserID,
				Key:          r.Screenshot,
			})
		}
		return err
	})
	return out, err
}

// ClearScreenshot clears the screenshot blob reference on a checkpoint after its blob was deleted.
func (s *Store) ClearScreenshot(ctx context.Context, userID uuid.UUID, checkpointID int64) error {
	return s.user(ctx, userID, func(qs *q.Queries) error {
		return qs.ClearCheckpointScreenshot(ctx, q.ClearCheckpointScreenshotParams{ID: checkpointID, UserID: userID})
	})
}

var _ application.RunStore = (*Store)(nil)

// LeaseNext leases the next application in the system role: one lease at a time (advisory lock), so the
// per-host check holds across runners.
func (s *Store) LeaseNext(ctx context.Context, runnerID string, tokenHash []byte, runnerChoice string, userID *uuid.UUID, now, expires, gapStart time.Time) (
	l application.Lease, ok bool, err error,
) {
	err = db.InSystemTx(ctx, s.pool, func(tx pgx.Tx) error {
		qs := q.New(tx)
		if err := qs.LockLeases(ctx); err != nil {
			return err
		}
		c, err := qs.LeaseCandidate(ctx, q.LeaseCandidateParams{Now: now, GapStart: gapStart, RunnerChoice: runnerChoice, UserID: userID})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		id, err := qs.InsertRun(ctx, q.InsertRunParams{ApplicationID: c.ID, UserID: c.UserID, RunnerID: runnerID,
			Host: c.Host, TokenHash: tokenHash, LeasedAt: now, ExpiresAt: expires})
		if err != nil {
			return err
		}
		l = application.Lease{RunID: id, AppID: c.ID, UserID: c.UserID, JobID: c.JobID, Host: c.Host, ExpiresAt: expires}
		ok = true
		return nil
	})
	return l, ok, err
}

// RunByToken finds the run of a token (system role: the caller is a runner, not a user).
func (s *Store) RunByToken(ctx context.Context, tokenHash []byte) (ref application.RunRef, expires time.Time, finished *time.Time, err error) {
	err = db.InSystemTx(ctx, s.pool, func(tx pgx.Tx) error {
		r, err := q.New(tx).RunByToken(ctx, tokenHash)
		if errors.Is(err, pgx.ErrNoRows) {
			return application.ErrNoRun
		}
		if err != nil {
			return err
		}
		ref = application.RunRef{RunID: r.ID, AppID: r.ApplicationID, UserID: r.UserID}
		expires, finished = r.ExpiresAt, r.FinishedAt
		return nil
	})
	return ref, expires, finished, err
}

// FinishRun ends a run with its outcome.
func (s *Store) FinishRun(ctx context.Context, r application.RunRef, outcome string, at time.Time) error {
	return s.user(ctx, r.UserID, func(qs *q.Queries) error {
		return qs.FinishRun(ctx, q.FinishRunParams{ID: r.RunID, UserID: r.UserID, FinishedAt: &at, Outcome: &outcome})
	})
}

// AddCheckpoint stores a step of a run.
func (s *Store) AddCheckpoint(ctx context.Context, r application.RunRef, c application.Checkpoint) error {
	data := c.Data
	if len(data) == 0 {
		data = []byte("{}")
	}
	return s.user(ctx, r.UserID, func(qs *q.Queries) error {
		return qs.InsertCheckpoint(ctx, q.InsertCheckpointParams{RunID: r.RunID, UserID: r.UserID, Step: c.Step,
			Page: int32(min(max(c.Page, 0), 100)), Url: c.URL, Data: data, Screenshot: c.Screenshot, At: c.At})
	})
}

// Job reads a job of the global index.
func (s *Store) Job(ctx context.Context, id int64) (application.Job, error) {
	r, err := q.New(s.pool).GetJob(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return application.Job{}, application.ErrNotFound
	}
	return application.Job{ID: r.ID, Source: r.Source, Company: r.Company, Title: r.Title, Location: r.Location,
		URL: r.Url, Apply: r.ApplyUrl, Description: r.Description}, err
}

// Settings returns the user's autonomy settings, DefaultSettings if they saved none.
func (s *Store) Settings(ctx context.Context, userID uuid.UUID) (st application.Settings, err error) {
	err = s.user(ctx, userID, func(qs *q.Queries) error {
		r, err := qs.GetSettings(ctx, userID)
		if errors.Is(err, pgx.ErrNoRows) {
			st = application.DefaultSettings
			return nil
		}
		if err != nil {
			return err
		}
		choice := application.RunnerChoice(r.RunnerChoice)
		if choice == "" {
			choice = application.RunnerServer
		}
		st = application.Settings{
			Level:        application.Level(r.Level),
			MinScore:     int(r.MinScore),
			DailyLimit:   int(r.DailyLimit),
			RunnerChoice: choice,
		}
		return nil
	})
	return st, err
}

// SaveSettings validates and stores the user's autonomy settings.
func (s *Store) SaveSettings(ctx context.Context, userID uuid.UUID, st application.Settings, at time.Time) error {
	if err := st.Validate(); err != nil {
		return err
	}
	if st.MinScore < 0 || st.MinScore > 100 {
		return fmt.Errorf("%w: min_score out of range", application.ErrInvalidSettings)
	}
	if st.DailyLimit < 0 || st.DailyLimit > 50 {
		return fmt.Errorf("%w: daily_limit out of range", application.ErrInvalidSettings)
	}
	choice := string(st.RunnerChoice)
	if choice == "" {
		choice = string(application.RunnerServer)
	}
	minScore := int16(st.MinScore)
	dailyLimit := int16(st.DailyLimit)
	return s.user(ctx, userID, func(qs *q.Queries) error {
		return qs.SaveSettings(ctx, q.SaveSettingsParams{
			UserID:       userID,
			Level:        string(st.Level),
			MinScore:     minScore,
			DailyLimit:   dailyLimit,
			RunnerChoice: choice,
			UpdatedAt:    at,
		})
	})
}

// SaveHomeRunnerToken stores the home runner token hash for the user.
func (s *Store) SaveHomeRunnerToken(ctx context.Context, userID uuid.UUID, hash []byte, at time.Time) error {
	return s.user(ctx, userID, func(qs *q.Queries) error {
		return qs.SaveHomeRunnerToken(ctx, q.SaveHomeRunnerTokenParams{UserID: userID, HomeRunnerTokenHash: hash, UpdatedAt: at})
	})
}

// UserByHomeRunnerToken finds the user ID associated with a home runner token hash (system role).
func (s *Store) UserByHomeRunnerToken(ctx context.Context, hash []byte) (userID uuid.UUID, err error) {
	err = db.InSystemTx(ctx, s.pool, func(tx pgx.Tx) error {
		id, err := q.New(tx).UserByHomeRunnerToken(ctx, hash)
		if errors.Is(err, pgx.ErrNoRows) {
			return application.ErrNotFound
		}
		if err != nil {
			return err
		}
		userID = id
		return nil
	})
	return userID, err
}

// RuleInput collects what the rule needs before the submit of a run.
func (s *Store) RuleInput(ctx context.Context, userID uuid.UUID, app application.App, runID uuid.UUID, now time.Time) (
	in application.RuleInput, err error,
) {
	st, err := s.Settings(ctx, userID)
	if err != nil {
		return in, err
	}
	in.Settings = st

	cand, err := s.Candidate(ctx, userID, app.JobID)
	if err != nil {
		return in, err
	}
	in.Candidate = cand

	err = s.user(ctx, userID, func(qs *q.Queries) error {
		score, err := qs.MatchScore(ctx, q.MatchScoreParams{UserID: userID, JobID: app.JobID})
		if err == nil {
			in.Matched = true
			in.Score = int(score)
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		submits, err := qs.SubmitsSince(ctx, q.SubmitsSinceParams{UserID: userID, StartedAt: now.Add(-application.QuotaWindow)})
		if err != nil {
			return err
		}
		in.Submits = int(submits)

		adapter, err := qs.RunAdapter(ctx, q.RunAdapterParams{ID: runID, UserID: userID})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		in.Adapter = adapter

		cps, err := qs.RunCheckpoints(ctx, q.RunCheckpointsParams{RunID: runID, UserID: userID})
		if err != nil {
			return err
		}
		fieldKind := make(map[string]string)
		filledMap := make(map[string]string)
		for _, cp := range cps {
			var d struct {
				Fields []struct {
					ID       string `json:"id"`
					Kind     string `json:"kind"`
					Required bool   `json:"required"`
					Value    string `json:"value"`
					Invalid  bool   `json:"invalid"`
				} `json:"fields"`
				Filled []struct {
					FieldID string `json:"field_id"`
					Source  string `json:"source"`
				} `json:"filled"`
				Errors []string `json:"errors"`
			}
			if err := json.Unmarshal(cp.Data, &d); err != nil {
				continue
			}
			for _, f := range d.Fields {
				if f.Kind != "" {
					fieldKind[f.ID] = f.Kind
				}
			}
			for _, f := range d.Filled {
				filledMap[f.FieldID] = f.Source
			}
			if cp.Step == "submit" {
				open := 0
				formErrors := len(d.Errors)
				for _, f := range d.Fields {
					if f.Required && strings.TrimSpace(f.Value) == "" {
						open++
					}
					if f.Invalid {
						formErrors++
					}
				}
				in.OpenFields = open
				in.FormErrors = formErrors
			}
		}
		for fieldID, src := range filledMap {
			in.Filled = append(in.Filled, application.SubmitField{
				Kind:   fieldKind[fieldID],
				Source: src,
			})
		}
		return nil
	})
	if err != nil {
		return in, err
	}

	if in.Adapter != "" {
		since := now.Add(-application.MaturityWindow)
		err = db.InSystemTx(ctx, s.pool, func(tx pgx.Tx) error {
			row, err := q.New(tx).AdapterRuns(ctx, q.AdapterRunsParams{Adapter: in.Adapter, FinishedAt: &since})
			if err != nil {
				return err
			}
			in.AdapterRuns = int(row.Runs)
			in.AdapterSubmitted = int(row.Submitted)
			return nil
		})
	}
	return in, err
}

// Approve records the user's approval if the application is still at app.Version; ErrConflict otherwise.
func (s *Store) Approve(ctx context.Context, userID uuid.UUID, app application.App, at time.Time) error {
	return s.user(ctx, userID, func(qs *q.Queries) error {
		n, err := qs.ApproveApplication(ctx, q.ApproveApplicationParams{
			ID:      app.ID,
			UserID:  userID,
			Version: app.Version,
			At:      &at,
		})
		if err != nil {
			return err
		}
		if n != 1 {
			return application.ErrConflict
		}
		return qs.InsertEvent(ctx, q.InsertEventParams{
			ApplicationID: app.ID,
			UserID:        userID,
			Event:         "approve",
			FromState:     string(app.State),
			ToState:       string(app.State),
			Actor:         string(application.ActorUser),
			Detail:        []byte("{}"),
			At:            at,
		})
	})
}

// Approvals lists the applications on the user's approval list, oldest request first.
func (s *Store) Approvals(ctx context.Context, userID uuid.UUID) (out []application.Approval, err error) {
	err = s.user(ctx, userID, func(qs *q.Queries) error {
		rows, err := qs.Approvals(ctx, userID)
		if err != nil {
			return err
		}
		for _, r := range rows {
			var reasons []application.Reason
			for _, re := range r.ApprovalReasons {
				reasons = append(reasons, application.Reason(re))
			}
			out = append(out, application.Approval{
				ID:      r.ID,
				JobID:   r.JobID,
				State:   application.State(r.State),
				Title:   r.Title,
				Company: r.Company,
				Score:   int(r.Score),
				Reasons: reasons,
				Asked:   r.AskedAt,
			})
		}
		return nil
	})
	return out, err
}

// SetAdapter records the ATS adapter of a run.
func (s *Store) SetAdapter(ctx context.Context, r application.RunRef, adapter string) error {
	return s.user(ctx, r.UserID, func(qs *q.Queries) error {
		return qs.SetRunAdapter(ctx, q.SetRunAdapterParams{
			ID:      r.RunID,
			UserID:  r.UserID,
			Adapter: adapter,
		})
	})
}

// PlanUsers lists active users whose autonomy level enables auto-planning and who have daily quota > 0 (system role).
func (s *Store) PlanUsers(ctx context.Context) (ids []uuid.UUID, err error) {
	err = db.InSystemTx(ctx, s.pool, func(tx pgx.Tx) error {
		ids, err = q.New(tx).PlanUsers(ctx)
		return err
	})
	return ids, err
}

// ActiveApplicationsCount counts applications in queued, running, or submitting for the user.
func (s *Store) ActiveApplicationsCount(ctx context.Context, userID uuid.UUID) (n int, err error) {
	err = s.user(ctx, userID, func(qs *q.Queries) error {
		c, err := qs.ActiveApplicationsCount(ctx, userID)
		if err != nil {
			return err
		}
		n = int(c)
		return nil
	})
	return n, err
}

// UserLastPacedAt returns the most recent timestamp an application was queued, run leased, or submit started for pacing.
func (s *Store) UserLastPacedAt(ctx context.Context, userID uuid.UUID) (at time.Time, err error) {
	err = s.user(ctx, userID, func(qs *q.Queries) error {
		at, err = qs.UserLastPacedAt(ctx, userID)
		return err
	})
	return at, err
}

// PlannerCandidates returns open matching jobs for the user ordered by match score descending.
func (s *Store) PlannerCandidates(ctx context.Context, userID uuid.UUID, minScore int) (out []application.PlannerCandidate, err error) {
	if minScore < 0 || minScore > 100 {
		return nil, fmt.Errorf("%w: min_score out of range", application.ErrInvalidSettings)
	}
	score := int16(minScore)
	err = s.user(ctx, userID, func(qs *q.Queries) error {
		rows, err := qs.PlannerCandidates(ctx, q.PlannerCandidatesParams{UserID: userID, MinScore: score})
		if err != nil {
			return err
		}
		out = make([]application.PlannerCandidate, len(rows))
		for i, r := range rows {
			out[i] = application.PlannerCandidate{JobID: r.ID, Score: int(r.Score)}
		}
		return nil
	})
	return out, err
}

// SubmitsSince counts the submit attempts of the user started since at.
func (s *Store) SubmitsSince(ctx context.Context, userID uuid.UUID, since time.Time) (n int, err error) {
	err = s.user(ctx, userID, func(qs *q.Queries) error {
		c, err := qs.SubmitsSince(ctx, q.SubmitsSinceParams{UserID: userID, StartedAt: since})
		if err != nil {
			return err
		}
		n = int(c)
		return nil
	})
	return n, err
}
