package application_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/application"
)

func TestPacingInterval(t *testing.T) {
	cases := []struct {
		limit int
		want  time.Duration
	}{
		{limit: -1, want: 0},
		{limit: 0, want: 0},
		{limit: 1, want: 24 * time.Hour},
		{limit: 5, want: 4*time.Hour + 48*time.Minute},
		{limit: 24, want: time.Hour},
		{limit: 48, want: 30 * time.Minute},
	}
	for _, tc := range cases {
		got := application.PacingInterval(tc.limit)
		if got != tc.want {
			t.Errorf("PacingInterval(%d) = %v, want %v", tc.limit, got, tc.want)
		}
	}
}

type fakePlannerStore struct {
	users      []uuid.UUID
	settings   map[uuid.UUID]application.Settings
	submits    map[uuid.UUID]int
	active     map[uuid.UUID]int
	lastPaced  map[uuid.UUID]time.Time
	candidates map[uuid.UUID][]application.PlannerCandidate
	guard      map[int64]error
	apps       map[int64]application.App
	queued     []int64
}

func newFakePlannerStore() *fakePlannerStore {
	return &fakePlannerStore{
		settings:   make(map[uuid.UUID]application.Settings),
		submits:    make(map[uuid.UUID]int),
		active:     make(map[uuid.UUID]int),
		lastPaced:  make(map[uuid.UUID]time.Time),
		candidates: make(map[uuid.UUID][]application.PlannerCandidate),
		guard:      make(map[int64]error),
		apps:       make(map[int64]application.App),
	}
}

func (f *fakePlannerStore) Create(_ context.Context, userID uuid.UUID, jobID int64, state application.State, at time.Time) (uuid.UUID, error) {
	id := uuid.New()
	f.apps[jobID] = application.App{ID: id, JobID: jobID, State: state, Version: 1, UpdatedAt: at}
	return id, nil
}

func (f *fakePlannerStore) Get(_ context.Context, _ uuid.UUID, id uuid.UUID) (application.App, error) {
	for _, a := range f.apps {
		if a.ID == id {
			return a, nil
		}
	}
	return application.App{}, application.ErrNotFound
}

func (f *fakePlannerStore) Questions(context.Context, uuid.UUID, uuid.UUID) ([]application.Question, error) {
	return nil, nil
}

func (f *fakePlannerStore) Evidence(context.Context, uuid.UUID, uuid.UUID) (application.Evidence, error) {
	return application.Evidence{}, nil
}

func (f *fakePlannerStore) CheckpointScreenshot(context.Context, uuid.UUID, uuid.UUID, int64) (string, error) {
	return "", nil
}

func (f *fakePlannerStore) Apply(_ context.Context, _ uuid.UUID, app application.App, c application.Change) error {
	a, ok := f.apps[app.JobID]
	if !ok || a.Version != app.Version {
		return application.ErrConflict
	}
	a.State = c.To
	a.Version++
	a.UpdatedAt = c.At
	f.apps[app.JobID] = a
	if c.To == application.Queued {
		f.queued = append(f.queued, app.JobID)
	}
	return nil
}

func (f *fakePlannerStore) Candidate(_ context.Context, _ uuid.UUID, jobID int64) (application.Candidate, error) {
	if err, ok := f.guard[jobID]; ok && err != nil {
		if errors.Is(err, application.ErrExcluded) {
			return application.Candidate{Excluded: true}, nil
		}
		if errors.Is(err, application.ErrDuplicate) {
			return application.Candidate{SameJob: []application.State{application.Submitted}}, nil
		}
	}
	return application.Candidate{}, nil
}

func (f *fakePlannerStore) StaleSubmitting(context.Context, time.Time) ([]application.Ref, error) {
	return nil, nil
}

func (f *fakePlannerStore) HandoffsDue(context.Context, time.Time) ([]application.Handoff, error) {
	return nil, nil
}

func (f *fakePlannerStore) MarkWarned(context.Context, application.Ref, time.Time) (bool, error) {
	return false, nil
}

func (f *fakePlannerStore) ExpiredScreenshots(context.Context, time.Time) ([]application.ExpiredScreenshot, error) {
	return nil, nil
}

func (f *fakePlannerStore) ClearScreenshot(context.Context, uuid.UUID, int64) error {
	return nil
}

func (f *fakePlannerStore) Settings(_ context.Context, userID uuid.UUID) (application.Settings, error) {
	if s, ok := f.settings[userID]; ok {
		return s, nil
	}
	return application.DefaultSettings, nil
}

func (f *fakePlannerStore) SaveSettings(_ context.Context, userID uuid.UUID, st application.Settings, _ time.Time) error {
	f.settings[userID] = st
	return nil
}

func (f *fakePlannerStore) SaveHomeRunnerToken(context.Context, uuid.UUID, []byte, time.Time) error {
	return nil
}

func (f *fakePlannerStore) UserByHomeRunnerToken(context.Context, []byte) (uuid.UUID, error) {
	return uuid.Nil, application.ErrNotFound
}

func (f *fakePlannerStore) RuleInput(context.Context, uuid.UUID, application.App, uuid.UUID, time.Time) (application.RuleInput, error) {
	return application.RuleInput{}, nil
}

func (f *fakePlannerStore) Approve(context.Context, uuid.UUID, application.App, time.Time) error {
	return nil
}

func (f *fakePlannerStore) Approvals(context.Context, uuid.UUID) ([]application.Approval, error) {
	return nil, nil
}

func (f *fakePlannerStore) PlanUsers(context.Context, bool) ([]uuid.UUID, error) {
	return f.users, nil
}

func (f *fakePlannerStore) ActiveApplicationsCount(_ context.Context, userID uuid.UUID) (int, error) {
	return f.active[userID], nil
}

func (f *fakePlannerStore) UserLastPacedAt(_ context.Context, userID uuid.UUID) (time.Time, error) {
	return f.lastPaced[userID], nil
}

func (f *fakePlannerStore) PlannerCandidates(_ context.Context, userID uuid.UUID, _ int) ([]application.PlannerCandidate, error) {
	return f.candidates[userID], nil
}

func (f *fakePlannerStore) SubmitsSince(_ context.Context, userID uuid.UUID, _ time.Time) (int, error) {
	return f.submits[userID], nil
}

func (f *fakePlannerStore) UserApplications(_ context.Context, _ uuid.UUID) ([]application.ApplicationSummary, error) {
	return nil, nil
}

func (f *fakePlannerStore) ByJob(_ context.Context, _ uuid.UUID, _ int64) (application.App, error) {
	return application.App{}, application.ErrNotFound
}

func (f *fakePlannerStore) SetCvDocument(_ context.Context, _, _ uuid.UUID, _ *uuid.UUID, _ time.Time) error {
	return nil
}

func (f *fakePlannerStore) SetJobCvDocument(_ context.Context, _ uuid.UUID, _ int64, _ *uuid.UUID, _ time.Time) (uuid.UUID, error) {
	return uuid.New(), nil
}

func (f *fakePlannerStore) UserScreenshots(_ context.Context, _ uuid.UUID) ([]string, error) {
	return nil, nil
}

func (f *fakePlannerStore) GetNotes(_ context.Context, _, appID uuid.UUID) (application.NoteData, error) {
	return application.NoteData{ApplicationID: appID}, nil
}

func (f *fakePlannerStore) SaveNotes(_ context.Context, _, _ uuid.UUID, _, _ string, _, _ *time.Time, _ time.Time) error {
	return nil
}

func (f *fakePlannerStore) PipelineApplications(_ context.Context, _ uuid.UUID) ([]application.PipelineApplication, error) {
	return nil, nil
}

func (f *fakePlannerStore) QueueItems(context.Context, uuid.UUID) ([]application.QueueItem, error) {
	return nil, nil
}

func (f *fakePlannerStore) MoveQueued(context.Context, uuid.UUID, uuid.UUID, bool) (bool, error) {
	return false, nil
}

func (f *fakePlannerStore) PauseQueued(context.Context, uuid.UUID, uuid.UUID, bool) (bool, error) {
	return false, nil
}

func (f *fakePlannerStore) BookmarkJob(_ context.Context, _ uuid.UUID, _ int64, _ time.Time) (uuid.UUID, error) {
	return uuid.New(), nil
}

func (f *fakePlannerStore) MarkExternalApplied(_ context.Context, _ uuid.UUID, _ int64, _ time.Time) (uuid.UUID, error) {
	return uuid.New(), nil
}

func TestPlanner(t *testing.T) {
	u1 := uuid.New()
	u2 := uuid.New()
	u3 := uuid.New()
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

	store := newFakePlannerStore()
	store.users = []uuid.UUID{u1, u2, u3}

	// u1: autonomous, daily_limit = 5. Both matching jobs (101, 102) should be queued immediately (#156)
	store.settings[u1] = application.Settings{Level: application.LevelAutonomous, MinScore: 75, DailyLimit: 5}
	store.candidates[u1] = []application.PlannerCandidate{
		{JobID: 101, Score: 95},
		{JobID: 102, Score: 85},
	}

	// u2: suggest level -> AutoQueue is false -> skipped
	store.settings[u2] = application.Settings{Level: application.LevelSuggest, MinScore: 75, DailyLimit: 5}
	store.candidates[u2] = []application.PlannerCandidate{
		{JobID: 201, Score: 90},
	}

	// u3: approve level, daily_limit = 5, but submits = 5 -> quota full, skipped
	store.settings[u3] = application.Settings{Level: application.LevelApprove, MinScore: 80, DailyLimit: 5}
	store.submits[u3] = 5
	store.candidates[u3] = []application.PlannerCandidate{
		{JobID: 301, Score: 92},
	}

	svc := application.NewService(store).WithClock(func() time.Time { return now })
	st, err := svc.Plan(t.Context())
	if err != nil {
		t.Fatalf("Plan() unexpected error: %v", err)
	}

	if st.UsersEligible != 3 {
		t.Errorf("UsersEligible = %d, want 3", st.UsersEligible)
	}
	if st.JobsPlanned != 2 {
		t.Errorf("JobsPlanned = %d, want 2", st.JobsPlanned)
	}
	if !slices.Contains(store.queued, 101) || !slices.Contains(store.queued, 102) {
		t.Errorf("queued jobs = %v, want to contain 101 and 102", store.queued)
	}

	// Test candidate guards: if job 101 is excluded, planner falls back to job 102
	t.Run("skips guarded candidate", func(t *testing.T) {
		store2 := newFakePlannerStore()
		store2.users = []uuid.UUID{u1}
		store2.settings[u1] = application.Settings{Level: application.LevelAutonomous, MinScore: 75, DailyLimit: 5}
		store2.candidates[u1] = []application.PlannerCandidate{
			{JobID: 101, Score: 95},
			{JobID: 102, Score: 85},
		}
		store2.guard[101] = application.ErrExcluded // job 101 excluded by hard filter

		svc2 := application.NewService(store2).WithClock(func() time.Time { return now })
		st2, err := svc2.Plan(t.Context())
		if err != nil {
			t.Fatalf("Plan() unexpected error: %v", err)
		}
		if st2.JobsPlanned != 1 {
			t.Errorf("JobsPlanned = %d, want 1", st2.JobsPlanned)
		}
		if !slices.Contains(store2.queued, 102) {
			t.Errorf("queued jobs = %v, want to contain 102", store2.queued)
		}
	})

	// Test quota full: if submits + active >= daily_limit, no job planned
	t.Run("quota full", func(t *testing.T) {
		store3 := newFakePlannerStore()
		store3.users = []uuid.UUID{u1}
		store3.settings[u1] = application.Settings{Level: application.LevelAutonomous, MinScore: 75, DailyLimit: 5}
		store3.submits[u1] = 3
		store3.active[u1] = 2 // 3 + 2 = 5 == daily_limit
		store3.candidates[u1] = []application.PlannerCandidate{{JobID: 101, Score: 95}}

		svc3 := application.NewService(store3).WithClock(func() time.Time { return now })
		st3, err := svc3.Plan(t.Context())
		if err != nil {
			t.Fatalf("Plan() unexpected error: %v", err)
		}
		if st3.JobsPlanned != 0 {
			t.Errorf("JobsPlanned = %d, want 0 when quota is full", st3.JobsPlanned)
		}
	})
}

func (f *fakePlannerStore) NavCounts(context.Context, uuid.UUID) (application.NavCounts, error) {
	return application.NavCounts{}, nil
}

func (f *fakePlannerStore) ParkReasons(context.Context, uuid.UUID) (map[uuid.UUID]string, error) {
	return nil, nil
}

func (f *fakePlannerStore) SubmitsPerDay(context.Context, uuid.UUID, time.Time) (map[time.Time]int, error) {
	return nil, nil
}

func (f *fakePlannerStore) Briefs(context.Context, uuid.UUID, []uuid.UUID) ([]application.Brief, error) {
	return nil, nil
}

func (f *fakePlannerStore) StatesByJob(context.Context, uuid.UUID, []int64) (map[int64]application.State, error) {
	return nil, nil
}

func (f *fakePlannerStore) AnswerUsage(context.Context, uuid.UUID) (map[string]int, error) {
	return nil, nil
}
