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
	"github.com/1xxn/getafjob/internal/portals"
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

func TestApprovalCVVariantAndSetCV(t *testing.T) {
	max := uuid.MustParse("0192f3a4-5b6c-7d8e-9f01-23456789abcd")
	appID := uuid.MustParse("0192f3a4-5b6c-7d8e-9f01-000000000001")
	cvID := uuid.MustParse("0192f3a4-5b6c-7d8e-9f01-000000000002")
	u := &identity.User{ID: max, DisplayName: "Max Mustermann", Email: "max@example.org"}

	st := &fakeAutonomyStore{
		fakeAppStore: fakeAppStore{
			userID: max,
			app: application.App{
				ID:           appID,
				JobID:        42,
				State:        application.NeedsInput,
				Version:      1,
				CvDocumentID: &cvID,
			},
		},
		approvals: []application.Approval{
			{
				ID:           appID,
				JobID:        42,
				State:        application.NeedsInput,
				Title:        "Site Reliability Engineer",
				Company:      "CloudCorp",
				Score:        85,
				Reasons:      []application.Reason{application.ReasonLevel},
				Asked:        time.Now().Add(-10 * time.Minute),
				CvDocumentID: &cvID,
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

	// 2. POST /approvals/{id}/cv changes CV variant
	newCVID := uuid.MustParse("0192f3a4-5b6c-7d8e-9f01-000000000003")
	form := url.Values{"cv_id": {newCVID.String()}}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/approvals/"+appID.String()+"/cv", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", appID.String())
	srv.approvalSetCV(rec, req, u)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("approvalSetCV status = %d, want 303", rec.Code)
	}
	if st.app.CvDocumentID == nil || *st.app.CvDocumentID != newCVID {
		t.Fatalf("st.app.CvDocumentID = %v, want %v", st.app.CvDocumentID, newCVID)
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
	req := httptest.NewRequest(http.MethodGet, "/settings/automation", nil)
	req.SetPathValue("section", "automation")
	srv.preferencesPage(rec, req, u)
	if rec.Code != http.StatusOK {
		t.Fatalf("preferencesPage status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `name="autonomy_level"`) || !strings.Contains(body, `name="min_score"`) || !strings.Contains(body, `name="runner_choice"`) {
		t.Fatalf("preferencesPage missing autonomy inputs:\n%s", body)
	}
	// The own runner's token lives with the integrations.
	recInt := httptest.NewRecorder()
	reqInt := httptest.NewRequest(http.MethodGet, "/settings/integrations", nil)
	reqInt.SetPathValue("section", "integrations")
	srv.preferencesPage(recInt, reqInt, u)
	if !strings.Contains(recInt.Body.String(), `/preferences/runner/token`) {
		t.Fatalf("integrations missing runner token action:\n%s", recInt.Body.String())
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
	// The token is shown in the answer itself, never in a URL, and the answer is not cached.
	if rec.Code != http.StatusOK || rec.Header().Get("Location") != "" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("runnerToken = %d, location %q, cache %q; want 200 without redirect, no-store",
			rec.Code, rec.Header().Get("Location"), rec.Header().Get("Cache-Control"))
	}
	if len(st.tokenHash) != 32 {
		t.Fatalf("expected 32-byte token hash, got %d bytes", len(st.tokenHash))
	}
}

type fakePortalsService struct {
	adminEnabled bool
	accounts     []portals.AccountView
	disconnected []string
	dailyLimits  map[string]int
}

func (f *fakePortalsService) AdminEnabled() bool { return f.adminEnabled }
func (f *fakePortalsService) ListAccounts(context.Context, uuid.UUID) ([]portals.AccountView, error) {
	return f.accounts, nil
}
func (f *fakePortalsService) SaveCookies(context.Context, uuid.UUID, string, []byte, int) error {
	return nil
}
func (f *fakePortalsService) Disconnect(_ context.Context, _ uuid.UUID, portal string) error {
	f.disconnected = append(f.disconnected, portal)
	return nil
}
func (f *fakePortalsService) SetDailyLimit(_ context.Context, _ uuid.UUID, portal string, limit int) error {
	if f.dailyLimits == nil {
		f.dailyLimits = make(map[string]int)
	}
	f.dailyLimits[portal] = limit
	return nil
}

func TestPreferencesEasyApply(t *testing.T) {
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
	portalsSvc := &fakePortalsService{
		adminEnabled: true,
		accounts: []portals.AccountView{
			{Portal: portals.PortalLinkedIn, Status: portals.StatusActive, DailyLimit: 5},
			{Portal: portals.PortalIndeed, Status: portals.StatusDisconnected, DailyLimit: 5},
		},
	}

	srv := &Server{
		Profile: profSvc,
		Apps:    application.NewService(st),
		Portals: portalsSvc,
		Logger:  slog.Default(),
	}

	// 1. GET /preferences renders Easy Apply and notice warning
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/settings/automation", nil)
	req.SetPathValue("section", "automation")
	srv.preferencesPage(rec, req, u)
	if rec.Code != http.StatusOK {
		t.Fatalf("preferencesPage status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `name="easy_apply_enabled"`) || !strings.Contains(body, `name="easy_apply_terms_accepted"`) {
		t.Fatalf("preferencesPage missing easy apply checkboxes:\n%s", body)
	}
	if !strings.Contains(body, "LinkedIn") || !strings.Contains(body, "Indeed") {
		t.Fatalf("preferencesPage missing portal accounts list:\n%s", body)
	}

	// 2. POST /preferences enabling Easy Apply WITHOUT accepting terms is rejected with 422
	form := url.Values{
		"easy_apply_enabled": {"on"},
		// terms not accepted
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/preferences", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	srv.preferencesSave(rec, req, u)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("preferencesSave without terms status = %d, want 422", rec.Code)
	}
	if st.settings.EasyApplyEnabled {
		t.Fatal("expected EasyApplyEnabled to remain false")
	}

	// 3. POST /preferences enabling Easy Apply WITH accepting terms succeeds
	form = url.Values{
		"easy_apply_enabled":        {"on"},
		"easy_apply_terms_accepted": {"on"},
		"portal_limit_linkedin":     {"7"},
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/preferences", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	srv.preferencesSave(rec, req, u)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("preferencesSave with terms status = %d, want 303, body: %s", rec.Code, rec.Body.String())
	}
	if !st.settings.EasyApplyEnabled || st.settings.EasyApplyTermsAcceptedAt == nil {
		t.Fatalf("st.settings EasyApplyEnabled = %v, TermsAcceptedAt = %v", st.settings.EasyApplyEnabled, st.settings.EasyApplyTermsAcceptedAt)
	}
	if portalsSvc.dailyLimits[portals.PortalLinkedIn] != 7 {
		t.Fatalf("portal limit for linkedin = %d, want 7", portalsSvc.dailyLimits[portals.PortalLinkedIn])
	}

	// 4. POST /preferences with disconnect_portal disconnects account
	form = url.Values{
		"disconnect_portal": {"linkedin"},
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/preferences", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	srv.preferencesSave(rec, req, u)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("disconnect_portal status = %d, want 303", rec.Code)
	}
	if len(portalsSvc.disconnected) == 0 || portalsSvc.disconnected[0] != "linkedin" {
		t.Fatalf("expected linkedin disconnected, got %+v", portalsSvc.disconnected)
	}
}

// A section form carries only its own fields: saving the search must not reset the automation, and the reverse.
func TestSettingsSectionsSaveOnlyTheirFields(t *testing.T) {
	u := &identity.User{ID: uuid.New(), DisplayName: "Max Mustermann", Email: "max@example.org"}
	profSvc := profile.NewService(&fakeProfileStore{}, nil)
	st := &fakeAutonomyStore{settings: application.Settings{Level: application.LevelAutonomous, RunnerChoice: application.RunnerHomeRunner,
		MinScore: 85, DailyLimit: 10}}
	srv := &Server{Profile: profSvc, Apps: application.NewService(st), Logger: slog.Default()}
	post := func(form url.Values) string {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/preferences", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		srv.preferencesSave(rec, req, u)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("save %v: status %d", form, rec.Code)
		}
		return rec.Header().Get("Location")
	}

	if loc := post(url.Values{"section": {"search"}, "titles": {"Go Engineer"}}); loc != "/settings/search?saved=1" {
		t.Errorf("search saved to %q", loc)
	}
	if st.settings.Level != application.LevelAutonomous || st.settings.MinScore != 85 || st.settings.DailyLimit != 10 {
		t.Fatalf("saving the search changed the automation: %+v", st.settings)
	}
	snap, err := profSvc.Get(t.Context(), u.ID)
	if err != nil || len(snap.Preferences.Titles) != 1 {
		t.Fatalf("search not saved: %+v %v", snap.Preferences, err)
	}

	post(url.Values{"section": {"automation"}, "autonomy_level": {"approve"}, "min_score": {"70"}, "daily_limit": {"3"}})
	if st.settings.Level != application.LevelApprove || st.settings.MinScore != 70 {
		t.Fatalf("automation not saved: %+v", st.settings)
	}
	if snap, _ := profSvc.Get(t.Context(), u.ID); len(snap.Preferences.Titles) != 1 || snap.Preferences.Titles[0] != "Go Engineer" {
		t.Fatalf("saving the automation changed the search: %+v", snap.Preferences)
	}
}
