package web

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/application"
	"github.com/1xxn/getafjob/internal/discovery"
	"github.com/1xxn/getafjob/internal/discovery/urlimport"
	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/tracking"
)

type fakeTrackerStore struct {
	fakeAppStore
	pipeline []application.PipelineApplication
	notes    map[uuid.UUID]application.NoteData
	fired    []struct {
		appID uuid.UUID
		ev    application.Event
	}
	bookmarked      []int64
	externalApplied []int64
}

func (f *fakeTrackerStore) PipelineApplications(_ context.Context, _ uuid.UUID) ([]application.PipelineApplication, error) {
	return f.pipeline, nil
}

func (f *fakeTrackerStore) Get(ctx context.Context, userID, id uuid.UUID) (application.App, error) {
	for _, p := range f.pipeline {
		if p.ID == id {
			return application.App{
				ID:      p.ID,
				JobID:   p.JobID,
				State:   p.State,
				Version: p.Version,
			}, nil
		}
	}
	return f.fakeAppStore.Get(ctx, userID, id)
}

func (f *fakeTrackerStore) Apply(_ context.Context, _ uuid.UUID, app application.App, c application.Change) error {
	for i := range f.pipeline {
		if f.pipeline[i].ID == app.ID {
			f.pipeline[i].State = c.To
			f.pipeline[i].Version = app.Version + 1
			f.pipeline[i].UpdatedAt = c.At
			break
		}
	}
	f.fired = append(f.fired, struct {
		appID uuid.UUID
		ev    application.Event
	}{appID: app.ID, ev: c.Event})
	return nil
}

func (f *fakeTrackerStore) GetNotes(_ context.Context, _, appID uuid.UUID) (application.NoteData, error) {
	if n, ok := f.notes[appID]; ok {
		return n, nil
	}
	return application.NoteData{ApplicationID: appID}, nil
}

func (f *fakeTrackerStore) SaveNotes(_ context.Context, _, appID uuid.UUID, notes, salary string, interviewAt, followUpAt *time.Time, now time.Time) error {
	if f.notes == nil {
		f.notes = make(map[uuid.UUID]application.NoteData)
	}
	f.notes[appID] = application.NoteData{
		ApplicationID:   appID,
		Notes:           notes,
		SalaryDiscussed: salary,
		InterviewAt:     interviewAt,
		FollowUpAt:      followUpAt,
		UpdatedAt:       now,
	}
	for i := range f.pipeline {
		if f.pipeline[i].ID == appID {
			f.pipeline[i].Notes = notes
			f.pipeline[i].SalaryDiscussed = salary
			f.pipeline[i].InterviewAt = interviewAt
			f.pipeline[i].FollowUpAt = followUpAt
			f.pipeline[i].UpdatedAt = now
			break
		}
	}
	return nil
}

func (f *fakeTrackerStore) BookmarkJob(_ context.Context, _ uuid.UUID, jobID int64, now time.Time) (uuid.UUID, error) {
	f.bookmarked = append(f.bookmarked, jobID)
	id := uuid.New()
	f.pipeline = append(f.pipeline, application.PipelineApplication{
		ID:        id,
		JobID:     jobID,
		State:     application.Matched,
		Company:   "Bookmarked Inc",
		Title:     "Software Engineer",
		CreatedAt: now,
		UpdatedAt: now,
	})
	return id, nil
}

func (f *fakeTrackerStore) MarkExternalApplied(_ context.Context, _ uuid.UUID, jobID int64, now time.Time) (uuid.UUID, error) {
	f.externalApplied = append(f.externalApplied, jobID)
	id := uuid.New()
	f.pipeline = append(f.pipeline, application.PipelineApplication{
		ID:        id,
		JobID:     jobID,
		State:     application.Submitted,
		Company:   "External Inc",
		Title:     "DevOps Specialist",
		CreatedAt: now,
		UpdatedAt: now,
	})
	return id, nil
}

type fakeTrackingSvc struct {
	suggestions map[uuid.UUID][]tracking.StatusSuggestion
}

func (t *fakeTrackingSvc) GetMailbox(context.Context, uuid.UUID) (tracking.MailboxAccount, error) {
	return tracking.MailboxAccount{}, nil
}

func (t *fakeTrackingSvc) SaveMailbox(context.Context, uuid.UUID, string, int, bool, string, string, bool) error {
	return nil
}

func (t *fakeTrackingSvc) SuggestionsForApplication(_ context.Context, _, appID uuid.UUID) ([]tracking.StatusSuggestion, error) {
	return t.suggestions[appID], nil
}

func (t *fakeTrackingSvc) AcceptSuggestion(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}

func (t *fakeTrackingSvc) DismissSuggestion(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}

type fakeDiscoverySvc struct {
	importedURL string
	nextJobID   int64
}

func (d *fakeDiscoverySvc) ImportJob(_ context.Context, _ uuid.UUID, rawURL string) (int64, error) {
	d.importedURL = rawURL
	if d.nextJobID == 0 {
		d.nextJobID = 99
	}
	return d.nextJobID, nil
}

func (d *fakeDiscoverySvc) TriggerCrawl(context.Context) (bool, error) {
	return false, nil
}

func (d *fakeDiscoverySvc) CrawlStatus(context.Context) (urlimport.CrawlStatus, error) {
	return urlimport.CrawlStatus{}, nil
}

func (d *fakeDiscoverySvc) AddBoard(context.Context, string) (discovery.Board, error) {
	return discovery.Board{}, nil
}

func TestTrackerPage_Unauthenticated(t *testing.T) {
	srv := &Server{Logger: slog.Default()}
	mux := http.NewServeMux()
	srv.Register(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/tracker", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Fatalf("Location = %q, want /login", loc)
	}
}

func TestTrackerPage_RendersFunnel(t *testing.T) {
	max := uuid.MustParse("0192f3a4-5b6c-7d8e-9f01-23456789abcd")
	app1 := uuid.MustParse("0192f3a4-5b6c-7d8e-9f01-000000000001")
	app2 := uuid.MustParse("0192f3a4-5b6c-7d8e-9f01-000000000002")
	app3 := uuid.MustParse("0192f3a4-5b6c-7d8e-9f01-000000000003")
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)

	st := &fakeTrackerStore{
		fakeAppStore: fakeAppStore{userID: max},
		pipeline: []application.PipelineApplication{
			{
				ID:        app1,
				JobID:     101,
				State:     application.Matched,
				Company:   "Acme Systems",
				Title:     "Senior Go Engineer",
				Score:     92,
				Workplace: "remote",
				UpdatedAt: now,
			},
			{
				ID:        app2,
				JobID:     102,
				State:     application.Submitted,
				Company:   "Globex Corp",
				Title:     "Cloud Platform Architect",
				Score:     88,
				Workplace: "hybrid",
				UpdatedAt: now,
			},
			{
				ID:              app3,
				JobID:           103,
				State:           application.Interview,
				Company:         "Initech",
				Title:           "Staff Engineer",
				Score:           95,
				Workplace:       "onsite",
				Notes:           "Passed technical screening with lead architect.",
				SalaryDiscussed: "95.000 €",
				InterviewAt:     &now,
				UpdatedAt:       now,
			},
		},
	}

	trackSvc := &fakeTrackingSvc{
		suggestions: map[uuid.UUID][]tracking.StatusSuggestion{
			app2: {
				{
					ID:             uuid.New(),
					Status:         tracking.SuggestionPending,
					Category:       tracking.CategoryInvitation,
					SuggestedState: "interview",
					MessageSubject: "Interview Invitation: Cloud Platform Architect",
				},
			},
		},
	}

	srv := &Server{
		Apps:     application.NewService(st),
		Tracking: trackSvc,
		Logger:   slog.Default(),
	}

	u := &identity.User{ID: max, Email: "max@example.org"}

	// 1. Full page request
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/tracker", nil)
	srv.trackerPage(rec, req, u)

	if rec.Code != http.StatusOK {
		t.Fatalf("trackerPage status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, expected := range []string{
		"Senior Go Engineer",
		"Acme Systems",
		"Cloud Platform Architect",
		"Globex Corp",
		"Staff Engineer",
		"Initech",
		"95.000 €",
		"Passed technical screening",
		"tracker-board",
		"tracker-column-saved",
		"tracker-column-submitted",
		"tracker-column-interview",
		"badge-email-activity",
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("body missing expected snippet %q", expected)
		}
	}

	// 2. HTMX partial request
	recPartial := httptest.NewRecorder()
	reqPartial := httptest.NewRequest(http.MethodGet, "/tracker", nil)
	reqPartial.Header.Set("HX-Request", "true")
	srv.trackerPage(recPartial, reqPartial, u)
	if recPartial.Code != http.StatusOK {
		t.Fatalf("partial status = %d, want 200", recPartial.Code)
	}
}

func TestTrackerSetStage(t *testing.T) {
	max := uuid.MustParse("0192f3a4-5b6c-7d8e-9f01-23456789abcd")
	appID := uuid.MustParse("0192f3a4-5b6c-7d8e-9f01-000000000001")
	now := time.Now()

	st := &fakeTrackerStore{
		fakeAppStore: fakeAppStore{userID: max},
		pipeline: []application.PipelineApplication{
			{
				ID:        appID,
				JobID:     101,
				State:     application.Submitted,
				Company:   "Acme Systems",
				Title:     "Senior Go Engineer",
				UpdatedAt: now,
			},
		},
	}

	srv := &Server{
		Apps:   application.NewService(st),
		Logger: slog.Default(),
	}
	u := &identity.User{ID: max, Email: "max@example.org"}

	form := url.Values{}
	form.Set("stage", "interview")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/tracker/"+appID.String()+"/stage", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	req.SetPathValue("id", appID.String())

	srv.trackerSetStage(rec, req, u)

	if rec.Code != http.StatusOK {
		t.Fatalf("trackerSetStage status = %d, want 200", rec.Code)
	}
	if len(st.fired) != 1 || st.fired[0].ev != application.EvInterview {
		t.Fatalf("fired = %+v, want EvInterview", st.fired)
	}
	if st.pipeline[0].State != application.Interview {
		t.Fatalf("pipeline item state = %v, want Interview", st.pipeline[0].State)
	}
}

func TestTrackerSaveNotes(t *testing.T) {
	max := uuid.MustParse("0192f3a4-5b6c-7d8e-9f01-23456789abcd")
	appID := uuid.MustParse("0192f3a4-5b6c-7d8e-9f01-000000000001")
	now := time.Now()

	st := &fakeTrackerStore{
		fakeAppStore: fakeAppStore{userID: max},
		pipeline: []application.PipelineApplication{
			{
				ID:        appID,
				JobID:     101,
				State:     application.Interview,
				Company:   "Acme Systems",
				Title:     "Senior Go Engineer",
				UpdatedAt: now,
			},
		},
	}

	srv := &Server{
		Apps:   application.NewService(st),
		Logger: slog.Default(),
	}
	u := &identity.User{ID: max, Email: "max@example.org"}

	form := url.Values{}
	form.Set("notes", "Round 1 interview passed. Next: System Design.")
	form.Set("salary", "90.000 €")
	form.Set("interview_at", "2026-11-15T14:30")
	form.Set("follow_up_at", "2026-11-16")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/tracker/"+appID.String()+"/notes", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	req.SetPathValue("id", appID.String())

	srv.trackerSaveNotes(rec, req, u)

	if rec.Code != http.StatusOK {
		t.Fatalf("trackerSaveNotes status = %d, want 200", rec.Code)
	}

	savedNotes := st.notes[appID]
	if savedNotes.Notes != "Round 1 interview passed. Next: System Design." {
		t.Errorf("notes = %q", savedNotes.Notes)
	}
	if savedNotes.SalaryDiscussed != "90.000 €" {
		t.Errorf("salary = %q", savedNotes.SalaryDiscussed)
	}
	if savedNotes.InterviewAt == nil || savedNotes.InterviewAt.Format("2006-01-02T15:04") != "2026-11-15T14:30" {
		t.Errorf("interview_at = %v", savedNotes.InterviewAt)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "Round 1 interview passed") || !strings.Contains(body, "90.000 €") {
		t.Errorf("body missing updated notes: %s", body)
	}
}

func TestJobBookmarkAndApplyExternal(t *testing.T) {
	max := uuid.MustParse("0192f3a4-5b6c-7d8e-9f01-23456789abcd")
	st := &fakeTrackerStore{fakeAppStore: fakeAppStore{userID: max}}
	disc := &fakeDiscoverySvc{nextJobID: 123}

	srv := &Server{
		Apps:      application.NewService(st),
		Discovery: disc,
		Logger:    slog.Default(),
	}
	u := &identity.User{ID: max, Email: "max@example.org"}

	// 1. Bookmark a job
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/jobs/55/bookmark", nil)
	req.SetPathValue("id", "55")
	req.Header.Set("HX-Request", "true")
	srv.jobBookmark(rec, req, u)

	if rec.Code != http.StatusOK {
		t.Fatalf("jobBookmark status = %d, want 200", rec.Code)
	}
	if len(st.bookmarked) != 1 || st.bookmarked[0] != 55 {
		t.Fatalf("bookmarked = %v, want [55]", st.bookmarked)
	}

	// 2. Apply external directly on job
	recExt := httptest.NewRecorder()
	reqExt := httptest.NewRequest(http.MethodPost, "/jobs/56/apply-external", nil)
	reqExt.SetPathValue("id", "56")
	srv.jobApplyExternal(recExt, reqExt, u)

	if recExt.Code != http.StatusSeeOther {
		t.Fatalf("jobApplyExternal status = %d, want 303", recExt.Code)
	}
	if len(st.externalApplied) != 1 || st.externalApplied[0] != 56 {
		t.Fatalf("externalApplied = %v, want [56]", st.externalApplied)
	}

	// 3. Add external by posting job URL from tracker
	form := url.Values{}
	form.Set("url", "https://jobs.example.org/posting/123")
	recAdd := httptest.NewRecorder()
	reqAdd := httptest.NewRequest(http.MethodPost, "/tracker/external", strings.NewReader(form.Encode()))
	reqAdd.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqAdd.Header.Set("HX-Request", "true")
	srv.trackerAddExternal(recAdd, reqAdd, u)

	if recAdd.Code != http.StatusOK {
		t.Fatalf("trackerAddExternal status = %d, want 200", recAdd.Code)
	}
	if disc.importedURL != "https://jobs.example.org/posting/123" {
		t.Errorf("importedURL = %q", disc.importedURL)
	}
	if len(st.externalApplied) != 2 || st.externalApplied[1] != 123 {
		t.Errorf("externalApplied = %v, want second item 123", st.externalApplied)
	}
}
