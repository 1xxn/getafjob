package privacy_test

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/application"
	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/matching"
	"github.com/1xxn/getafjob/internal/privacy"
	"github.com/1xxn/getafjob/internal/profile"
)

type fakeIdentity struct {
	user     identity.User
	passkeys []identity.PasskeyInfo
	sessions []identity.SessionInfo
	events   []identity.AuditEvent
	deleted  uuid.UUID
	cleaned  bool
}

func (f *fakeIdentity) UserByID(_ context.Context, id uuid.UUID) (identity.User, error) {
	if f.user.ID == id {
		return f.user, nil
	}
	return identity.User{}, identity.ErrNotFound
}

func (f *fakeIdentity) Passkeys(_ context.Context, _ uuid.UUID) ([]identity.PasskeyInfo, error) {
	return f.passkeys, nil
}

func (f *fakeIdentity) Sessions(_ context.Context, _ uuid.UUID, _ string) ([]identity.SessionInfo, error) {
	return f.sessions, nil
}

func (f *fakeIdentity) AuditEvents(_ context.Context, _ uuid.UUID) ([]identity.AuditEvent, error) {
	return f.events, nil
}

func (f *fakeIdentity) DeleteUser(_ context.Context, id uuid.UUID) error {
	f.deleted = id
	return nil
}

func (f *fakeIdentity) Cleanup(_ context.Context) error {
	f.cleaned = true
	return nil
}

type fakeProfile struct {
	snap profile.Snapshot
}

func (f *fakeProfile) Get(_ context.Context, _ uuid.UUID) (profile.Snapshot, error) {
	return f.snap, nil
}

type fakeItems struct {
	answers []profile.Answer
	texts   []profile.TextBlock
}

func (f *fakeItems) Answers(_ context.Context, _ uuid.UUID) ([]profile.Answer, error) {
	return f.answers, nil
}

func (f *fakeItems) TextBlocks(_ context.Context, _ uuid.UUID) ([]profile.TextBlock, error) {
	return f.texts, nil
}

type fakeDocs struct {
	docs  []profile.Document
	files map[uuid.UUID][]byte
}

func (f *fakeDocs) List(_ context.Context, _ uuid.UUID) ([]profile.Document, error) {
	return f.docs, nil
}

func (f *fakeDocs) Read(_ context.Context, _ uuid.UUID, id uuid.UUID) (profile.Document, []byte, error) {
	for _, d := range f.docs {
		if d.ID == id {
			return d, f.files[id], nil
		}
	}
	return profile.Document{}, nil, profile.ErrNotFound
}

type fakeApps struct {
	apps        []application.ApplicationSummary
	screenshots []string
	settings    application.Settings
	purged      bool
}

func (f *fakeApps) UserApplications(_ context.Context, _ uuid.UUID) ([]application.ApplicationSummary, error) {
	return f.apps, nil
}

func (f *fakeApps) UserScreenshots(_ context.Context, _ uuid.UUID) ([]string, error) {
	return f.screenshots, nil
}

func (f *fakeApps) Settings(_ context.Context, _ uuid.UUID) (application.Settings, error) {
	return f.settings, nil
}

func (f *fakeApps) PurgeExpiredScreenshots(_ context.Context, _ application.ScreenshotDeleter) (int, error) {
	f.purged = true
	return 2, nil
}

type fakeMatching struct {
	matches []matching.MatchSummary
}

func (f *fakeMatching) UserMatches(_ context.Context, _ uuid.UUID) ([]matching.MatchSummary, error) {
	return f.matches, nil
}

type fakeKeys struct {
	forgotten uuid.UUID
}

func (f *fakeKeys) Forget(_ context.Context, id uuid.UUID) (bool, error) {
	f.forgotten = id
	return true, nil
}

type fakeBlobs struct {
	deleted []string
}

func (f *fakeBlobs) Delete(_ context.Context, _ uuid.UUID, key string) error {
	f.deleted = append(f.deleted, key)
	return nil
}

func TestExportGeneratesValidZip(t *testing.T) {
	ctx := context.Background()
	userID := uuid.MustParse("0192f3a4-5b6c-7d8e-9f01-000000000001")
	docID := uuid.MustParse("0192f3a4-5b6c-7d8e-9f01-000000000002")

	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	fIds := &fakeIdentity{
		user: identity.User{
			ID:          userID,
			Email:       "test@example.com",
			DisplayName: "Max Mustermann",
			Role:        identity.RoleUser,
			Locale:      "en",
		},
		passkeys: []identity.PasskeyInfo{
			{Name: "YubiKey 5", CreatedAt: now.Add(-10 * time.Hour)},
		},
		events: []identity.AuditEvent{
			{Actor: "user", Code: "identity.signed_in"},
		},
	}
	fProf := &fakeProfile{
		snap: profile.Snapshot{
			Profile: profile.Profile{
				Personal: profile.Personal{
					FirstName: "Max",
					LastName:  "Mustermann",
					Email:     "test@example.com",
				},
			},
		},
	}
	fItems := &fakeItems{
		answers: []profile.Answer{
			{Question: "Why work here?", Answer: "Great mission."},
		},
		texts: []profile.TextBlock{
			{Name: "Intro", Language: "en", Body: "Hello world."},
		},
	}
	fDocs := &fakeDocs{
		docs: []profile.Document{
			{ID: docID, Name: "cv_2026.pdf", Kind: "cv", Size: 12, CreatedAt: now},
		},
		files: map[uuid.UUID][]byte{
			docID: []byte("%PDF-1.4 test"),
		},
	}
	fApps := &fakeApps{
		apps: []application.ApplicationSummary{
			{
				ID:      uuid.New(),
				Company: "Acme Corp",
				Title:   "Go Engineer",
				State:   application.State("submitted"),
			},
		},
		settings: application.Settings{
			Level:      application.LevelAutonomous,
			DailyLimit: 5,
		},
	}
	fMatch := &fakeMatching{
		matches: []matching.MatchSummary{
			{JobID: 123, Title: "Go Engineer", Company: "Acme Corp", Score: 85, Decision: matching.Match},
		},
	}
	fKeys := &fakeKeys{}
	fBlobs := &fakeBlobs{}

	svc := privacy.NewService(fIds, fProf, fItems, fDocs, fApps, fMatch, fKeys, fBlobs).WithClock(func() time.Time { return now })

	var buf bytes.Buffer
	if err := svc.Export(ctx, userID, &buf); err != nil {
		t.Fatalf("export error: %v", err)
	}

	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("zip reader error: %v", err)
	}

	entries := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open entry %s: %v", f.Name, err)
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("read entry %s: %v", f.Name, err)
		}
		entries[f.Name] = string(b)
	}

	for _, expected := range []string{"profile.json", "applications.json", "matches.json", "settings.json", "audit_events.json", "documents/cv_2026.pdf"} {
		if _, ok := entries[expected]; !ok {
			t.Errorf("missing zip entry: %s", expected)
		}
	}

	if !strings.Contains(entries["profile.json"], "Max Mustermann") {
		t.Errorf("profile.json missing display name: %s", entries["profile.json"])
	}
	if !strings.Contains(entries["applications.json"], "Acme Corp") {
		t.Errorf("applications.json missing company: %s", entries["applications.json"])
	}
	if entries["documents/cv_2026.pdf"] != "%PDF-1.4 test" {
		t.Errorf("document content mismatch: %q", entries["documents/cv_2026.pdf"])
	}
}

func TestDeleteAccountPurgesBlobsAndKeys(t *testing.T) {
	ctx := context.Background()
	userID := uuid.MustParse("0192f3a4-5b6c-7d8e-9f01-000000000001")
	docID := uuid.MustParse("0192f3a4-5b6c-7d8e-9f01-000000000002")

	fIds := &fakeIdentity{
		user: identity.User{ID: userID},
	}
	fDocs := &fakeDocs{
		docs: []profile.Document{
			{ID: docID, Name: "resume.pdf"},
		},
	}
	fApps := &fakeApps{
		screenshots: []string{"runs/run1/shots/1.png", "runs/run1/shots/2.png"},
	}
	fKeys := &fakeKeys{}
	fBlobs := &fakeBlobs{}

	svc := privacy.NewService(fIds, nil, nil, fDocs, fApps, nil, fKeys, fBlobs)

	if err := svc.DeleteAccount(ctx, userID); err != nil {
		t.Fatalf("delete account error: %v", err)
	}

	if fKeys.forgotten != userID {
		t.Errorf("expected crypto key forgotten for %s, got %s", userID, fKeys.forgotten)
	}
	if fIds.deleted != userID {
		t.Errorf("expected user deleted for %s, got %s", userID, fIds.deleted)
	}
	if len(fBlobs.deleted) != 3 { // 1 document + 2 screenshots
		t.Errorf("expected 3 deleted blobs, got %d: %v", len(fBlobs.deleted), fBlobs.deleted)
	}
}

func TestCleanupRunsAllPruning(t *testing.T) {
	ctx := context.Background()
	fIds := &fakeIdentity{}
	fApps := &fakeApps{}
	fBlobs := &fakeBlobs{}

	svc := privacy.NewService(fIds, nil, nil, nil, fApps, nil, nil, fBlobs)

	if err := svc.Cleanup(ctx); err != nil {
		t.Fatalf("cleanup error: %v", err)
	}

	if !fIds.cleaned {
		t.Errorf("identity cleanup not called")
	}
	if !fApps.purged {
		t.Errorf("screenshots cleanup not called")
	}
}
