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
	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/profile"
)

type fakeAutonomyStore struct {
	fakeAppStore
	settings  application.Settings
	approvals []application.Approval
	approved  uuid.UUID
	tokenHash []byte
}

func (f *fakeAutonomyStore) SaveHomeRunnerToken(_ context.Context, _ uuid.UUID, hash []byte, _ time.Time) error {
	f.tokenHash = hash
	return nil
}

func (f *fakeAutonomyStore) Settings(context.Context, uuid.UUID) (application.Settings, error) {
	return f.settings, nil
}

func (f *fakeAutonomyStore) SaveSettings(_ context.Context, _ uuid.UUID, st application.Settings, _ time.Time) error {
	f.settings = st
	return nil
}

func (f *fakeAutonomyStore) Approvals(context.Context, uuid.UUID) ([]application.Approval, error) {
	return f.approvals, nil
}

func (f *fakeAutonomyStore) Approve(_ context.Context, _ uuid.UUID, app application.App, _ time.Time) error {
	f.approved = app.ID
	return nil
}

func TestApprovalsPageAndApprove(t *testing.T) {
	max := uuid.MustParse("0192f3a4-5b6c-7d8e-9f01-23456789abcd")
	appID := uuid.MustParse("0192f3a4-5b6c-7d8e-9f01-000000000001")
	u := &identity.User{ID: max, DisplayName: "Max Mustermann", Email: "max@example.org"}

	st := &fakeAutonomyStore{
		fakeAppStore: fakeAppStore{
			userID: max,
			app: application.App{
				ID:      appID,
				JobID:   42,
				State:   application.NeedsInput,
				Version: 1,
			},
		},
		settings: application.DefaultSettings,
		approvals: []application.Approval{
			{
				ID:      appID,
				JobID:   42,
				State:   application.NeedsInput,
				Title:   "Site Reliability Engineer",
				Company: "CloudCorp",
				Score:   85,
				Reasons: []application.Reason{application.ReasonLevel},
				Asked:   time.Now().Add(-10 * time.Minute),
			},
		},
	}

	srv := &Server{
		Apps:   application.NewService(st),
		Logger: slog.Default(),
	}

	// 1. GET /approvals
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/approvals", nil)
	srv.approvalsPage(rec, req, u)
	if rec.Code != http.StatusOK {
		t.Fatalf("approvalsPage status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"CloudCorp",
		"Site Reliability Engineer",
		"85",
		"/applications/" + appID.String(),
		"/approvals/" + appID.String() + "/approve",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("approvalsPage missing %q in body:\n%s", want, body)
		}
	}

	// 2. POST /approvals/{id}/approve
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/approvals/"+appID.String()+"/approve", nil)
	req.SetPathValue("id", appID.String())
	srv.approvalApprove(rec, req, u)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("approvalApprove status = %d, want 303", rec.Code)
	}
	if st.approved != appID {
		t.Fatalf("st.approved = %s, want %s", st.approved, appID)
	}
}

type fakeProfileStore struct {
	pref []byte
}

func (f *fakeProfileStore) Load(context.Context, uuid.UUID) (profile.Stored, error) {
	return profile.Stored{Preferences: f.pref}, nil
}
func (f *fakeProfileStore) SaveProfile(context.Context, uuid.UUID, []byte, int64, time.Time) (bool, error) {
	return true, nil
}
func (f *fakeProfileStore) SavePreferences(_ context.Context, _ uuid.UUID, p []byte, _ time.Time) error {
	f.pref = p
	return nil
}

func TestPreferencesAutonomySettings(t *testing.T) {
	max := uuid.MustParse("0192f3a4-5b6c-7d8e-9f01-23456789abcd")
	u := &identity.User{ID: max, DisplayName: "Max Mustermann", Email: "max@example.org"}

	profStore := &fakeProfileStore{}
	profSvc := profile.NewService(profStore, nil)

	st := &fakeAutonomyStore{
		settings: application.Settings{
			Level:      application.LevelApprove,
			MinScore:   80,
			DailyLimit: 5,
		},
	}

	srv := &Server{
		Profile: profSvc,
		Apps:    application.NewService(st),
		Logger:  slog.Default(),
	}

	// 1. GET /preferences includes autonomy values
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/preferences", nil)
	srv.preferencesPage(rec, req, u)
	if rec.Code != http.StatusOK {
		t.Fatalf("preferencesPage status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `name="autonomy_level"`) || !strings.Contains(body, `name="min_score"`) || !strings.Contains(body, `name="runner_choice"`) {
		t.Fatalf("preferencesPage missing autonomy inputs:\n%s", body)
	}
	if !strings.Contains(body, `/preferences/runner/token`) {
		t.Fatalf("preferencesPage missing runner token action:\n%s", body)
	}

	// 2. POST /preferences saves autonomy values
	form := url.Values{
		"titles":         {"DevOps Engineer"},
		"autonomy_level": {"autonomous"},
		"runner_choice":  {"home_runner"},
		"min_score":      {"85"},
		"daily_limit":    {"10"},
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/preferences", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	srv.preferencesSave(rec, req, u)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("preferencesSave status = %d, want 303, body: %s", rec.Code, rec.Body.String())
	}
	if st.settings.Level != application.LevelAutonomous || st.settings.RunnerChoice != application.RunnerHomeRunner || st.settings.MinScore != 85 || st.settings.DailyLimit != 10 {
		t.Fatalf("st.settings = %+v, want autonomous / home_runner / 85 / 10", st.settings)
	}

	// 3. POST /preferences/runner/token generates a token
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/preferences/runner/token", nil)
	srv.runnerToken(rec, req, u)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("runnerToken status = %d, want 303", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/preferences?runner_token=") {
		t.Fatalf("runnerToken redirect = %q, want /preferences?runner_token=...", loc)
	}
	if len(st.tokenHash) != 32 {
		t.Fatalf("expected 32-byte token hash, got %d bytes", len(st.tokenHash))
	}
}
