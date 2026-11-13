package tracking_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/tracking"
)

type fakeStore struct {
	mailbox     tracking.MailboxAccount
	mailboxErr  error
	apps        []tracking.ApplicationSummary
	suggestions map[uuid.UUID]tracking.StatusSuggestion
	cursorUID   uint32
	cursorVal   uint32
	cursorErr   string
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		suggestions: make(map[uuid.UUID]tracking.StatusSuggestion),
	}
}

func (f *fakeStore) GetMailbox(_ context.Context, userID uuid.UUID) (tracking.MailboxAccount, error) {
	if f.mailboxErr != nil {
		return tracking.MailboxAccount{}, f.mailboxErr
	}
	if f.mailbox.UserID == uuid.Nil {
		return tracking.MailboxAccount{}, tracking.ErrNotFound
	}
	return f.mailbox, nil
}

func (f *fakeStore) SaveMailbox(_ context.Context, acc tracking.MailboxAccount) error {
	f.mailbox = acc
	return nil
}

func (f *fakeStore) UpdateMailboxCursor(_ context.Context, _ uuid.UUID, lastUID, uidVal uint32, _ time.Time, lastErr string) error {
	f.cursorUID = lastUID
	f.cursorVal = uidVal
	f.cursorErr = lastErr
	return nil
}

func (f *fakeStore) ListEnabledMailboxes(_ context.Context) ([]tracking.MailboxAccount, error) {
	if f.mailbox.Enabled {
		return []tracking.MailboxAccount{f.mailbox}, nil
	}
	return nil, nil
}

func (f *fakeStore) ListActiveApplications(_ context.Context, _ uuid.UUID) ([]tracking.ApplicationSummary, error) {
	return f.apps, nil
}

func (f *fakeStore) CreateSuggestion(_ context.Context, s tracking.StatusSuggestion) error {
	f.suggestions[s.ID] = s
	return nil
}

func (f *fakeStore) Suggestions(_ context.Context, _ uuid.UUID, status string) ([]tracking.StatusSuggestion, error) {
	var list []tracking.StatusSuggestion
	for _, s := range f.suggestions {
		if status == "" || s.Status == status {
			list = append(list, s)
		}
	}
	return list, nil
}

func (f *fakeStore) SuggestionsForApplication(_ context.Context, _ uuid.UUID, appID uuid.UUID) ([]tracking.StatusSuggestion, error) {
	var list []tracking.StatusSuggestion
	for _, s := range f.suggestions {
		if s.ApplicationID == appID {
			list = append(list, s)
		}
	}
	return list, nil
}

func (f *fakeStore) Suggestion(_ context.Context, _ uuid.UUID, id uuid.UUID) (tracking.StatusSuggestion, error) {
	s, ok := f.suggestions[id]
	if !ok {
		return tracking.StatusSuggestion{}, tracking.ErrNotFound
	}
	return s, nil
}

func (f *fakeStore) SetSuggestionStatus(_ context.Context, _ uuid.UUID, id uuid.UUID, status string) error {
	s, ok := f.suggestions[id]
	if !ok {
		return tracking.ErrNotFound
	}
	s.Status = status
	f.suggestions[id] = s
	return nil
}

type fakeClient struct {
	messages []tracking.EmailMessage
	nextUID  uint32
	validity uint32
	testErr  error
	fetchErr error
}

func (f *fakeClient) FetchNewMessages(_ context.Context, _ tracking.MailboxAccount, _ string) ([]tracking.EmailMessage, uint32, uint32, error) {
	return f.messages, f.nextUID, f.validity, f.fetchErr
}

func (f *fakeClient) TestConnection(_ context.Context, _ tracking.MailboxAccount, _ string) error {
	return f.testErr
}

type fakeCipher struct{}

func (f *fakeCipher) SealPassword(_ context.Context, _ uuid.UUID, password string) ([]byte, error) {
	return []byte("enc:" + password), nil
}

func (f *fakeCipher) OpenPassword(_ context.Context, _ uuid.UUID, ciphertext []byte) (string, error) {
	s := string(ciphertext)
	if len(s) > 4 && s[:4] == "enc:" {
		return s[4:], nil
	}
	return "", errors.New("bad ciphertext")
}

type fakeAppUpdater struct {
	transitions []string
	firedAppID  uuid.UUID
}

func (f *fakeAppUpdater) Transition(_ context.Context, _, appID uuid.UUID, state string, _ map[string]any) error {
	f.transitions = append(f.transitions, state)
	f.firedAppID = appID
	return nil
}

// Allowed follows the application state machine for the suggestion states.
func (f *fakeAppUpdater) Allowed(from, to string) bool {
	next := map[string][]string{"submitted": {"confirmed", "rejected", "interview"}, "confirmed": {"rejected", "interview"},
		"interview": {"rejected"}, "submit_unknown": {"confirmed"}}
	return slices.Contains(next[from], to)
}

func TestTrackingService(t *testing.T) {
	ctx := context.Background()
	u := uuid.New()
	st := newFakeStore()
	cl := &fakeClient{}
	ci := &fakeCipher{}
	apps := &fakeAppUpdater{}

	svc := tracking.NewService(st, cl, ci, apps)
	now := time.Date(2026, 11, 8, 15, 0, 0, 0, time.UTC)
	svc.SetNow(func() time.Time { return now })

	// 1. Configure mailbox
	if err := svc.SaveMailbox(ctx, u, "imap.example.com", 993, true, "user@example.com", "app-pass-123", true); err != nil {
		t.Fatalf("SaveMailbox: %v", err)
	}

	acc, err := svc.GetMailbox(ctx, u)
	if err != nil {
		t.Fatalf("GetMailbox: %v", err)
	}
	if acc.Host != "imap.example.com" || string(acc.EncryptedPassword) != "enc:app-pass-123" {
		t.Errorf("GetMailbox = %+v", acc)
	}

	// 2. Setup candidate applications
	targetAppID := uuid.New()
	st.apps = []tracking.ApplicationSummary{
		{
			ID:           targetAppID,
			UserID:       u,
			JobID:        42,
			CurrentState: "submitted",
			Company:      "Acme Cloud",
			Title:        "Go Engineer",
			URL:          "https://jobs.acmecloud.example.org/1",
		},
	}

	// 3. Incoming emails:
	// - 1 security code (MUST BE IGNORED)
	// - 1 interview invitation for Acme Cloud (MUST GENERATE SUGGESTION)
	cl.nextUID = 202
	cl.validity = 12345
	cl.messages = []tracking.EmailMessage{
		{
			UID:       201,
			MessageID: "msg-sec-code",
			From:      "security@acmecloud.example.org",
			Subject:   "Your verification code",
			Body:      "Your security code is 881920. Do not share it.",
			Date:      now,
		},
		{
			UID:       202,
			MessageID: "msg-invite",
			From:      "hiring@acmecloud.example.org",
			Subject:   "Invitation to interview: Go Engineer",
			Body:      "We would like to invite you to an interview for the Go Engineer position.",
			Date:      now,
		},
	}

	suggestions, err := svc.CheckMailbox(ctx, u)
	if err != nil {
		t.Fatalf("CheckMailbox: %v", err)
	}

	// Must have generated exactly 1 suggestion (for interview), NOT for security code!
	if len(suggestions) != 1 {
		t.Fatalf("len(suggestions) = %d, want 1", len(suggestions))
	}
	sug := suggestions[0]
	if sug.ApplicationID != targetAppID {
		t.Errorf("sug.ApplicationID = %v, want %v", sug.ApplicationID, targetAppID)
	}
	if sug.Category != tracking.CategoryInvitation {
		t.Errorf("sug.Category = %v, want %v", sug.Category, tracking.CategoryInvitation)
	}
	if sug.SuggestedState != tracking.StateInterview {
		t.Errorf("sug.SuggestedState = %v, want %v", sug.SuggestedState, tracking.StateInterview)
	}

	// Verify cursor updated
	if st.cursorUID != 202 || st.cursorVal != 12345 {
		t.Errorf("cursorUID = %d, cursorVal = %d", st.cursorUID, st.cursorVal)
	}

	// 4. Accept suggestion
	if err := svc.AcceptSuggestion(ctx, u, sug.ID); err != nil {
		t.Fatalf("AcceptSuggestion: %v", err)
	}
	if len(apps.transitions) != 1 || apps.transitions[0] != tracking.StateInterview {
		t.Errorf("transitions = %v, want [interview]", apps.transitions)
	}
	if apps.firedAppID != targetAppID {
		t.Errorf("firedAppID = %v, want %v", apps.firedAppID, targetAppID)
	}

	// Verify suggestion status is accepted
	updatedSug, err := st.Suggestion(ctx, u, sug.ID)
	if err != nil || updatedSug.Status != tracking.SuggestionAccepted {
		t.Errorf("updatedSug.Status = %v, want accepted", updatedSug.Status)
	}
}

// A mail never suggests a step back: "thanks for your application" after the interview stays without suggestion.
func TestNoSuggestionBackwards(t *testing.T) {
	ctx := context.Background()
	u := uuid.New()
	st := newFakeStore()
	cl := &fakeClient{}
	svc := tracking.NewService(st, cl, &fakeCipher{}, &fakeAppUpdater{})
	if err := svc.SaveMailbox(ctx, u, "imap.example.com", 993, true, "user@example.com", "app-pass-123", true); err != nil {
		t.Fatal(err)
	}
	st.apps = []tracking.ApplicationSummary{{ID: uuid.New(), UserID: u, CurrentState: "interview", Company: "Acme Cloud",
		Title: "Go Engineer", URL: "https://jobs.acmecloud.example.org/1"}}
	cl.nextUID, cl.validity = 2, 1
	cl.messages = []tracking.EmailMessage{{UID: 2, From: "hiring@acmecloud.example.org",
		Subject: "Thank you for your application", Body: "We have received your application."}}
	sugs, err := svc.CheckMailbox(ctx, u)
	if err != nil || len(sugs) != 0 {
		t.Fatalf("suggestions = %+v, %v; want none", sugs, err)
	}
}
