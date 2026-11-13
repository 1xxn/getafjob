package notify_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/notify"
	"github.com/1xxn/getafjob/internal/platform/i18n"
)

type fakeStore struct {
	prefs     map[uuid.UUID]notify.Preferences
	emails    map[uuid.UUID]string
	summaries map[uuid.UUID]time.Time
	stats     notify.DailyStats
	locales   map[uuid.UUID]i18n.Locale
	seen      map[uuid.UUID]time.Time
	activity  []notify.ActivityItem // newest first
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		prefs:     make(map[uuid.UUID]notify.Preferences),
		emails:    make(map[uuid.UUID]string),
		summaries: make(map[uuid.UUID]time.Time),
		locales:   make(map[uuid.UUID]i18n.Locale),
		seen:      make(map[uuid.UUID]time.Time),
	}
}

func (f *fakeStore) GetPreferences(_ context.Context, userID uuid.UUID) (notify.Preferences, error) {
	p, ok := f.prefs[userID]
	if !ok {
		return notify.Preferences{}, notify.ErrNotFound
	}
	return p, nil
}

func (f *fakeStore) SavePreferences(_ context.Context, p notify.Preferences) error {
	f.prefs[p.UserID] = p
	return nil
}

func (f *fakeStore) ListDailySummaryCandidates(_ context.Context) ([]notify.Preferences, error) {
	var res []notify.Preferences
	for _, p := range f.prefs {
		if p.DailySummaryEnabled {
			res = append(res, p)
		}
	}
	return res, nil
}

func (f *fakeStore) UpdateLastSummaryAt(_ context.Context, userID uuid.UUID, at time.Time) error {
	f.summaries[userID] = at
	p := f.prefs[userID]
	p.LastSummaryAt = &at
	f.prefs[userID] = p
	return nil
}

func (f *fakeStore) GetUserEmail(_ context.Context, userID uuid.UUID) (string, error) {
	return f.emails[userID], nil
}

func (f *fakeStore) UserLocale(_ context.Context, userID uuid.UUID) (i18n.Locale, error) {
	return f.locales[userID], nil
}

func (f *fakeStore) Activity(_ context.Context, _ uuid.UUID, before time.Time, limit int) ([]notify.ActivityItem, error) {
	var out []notify.ActivityItem
	for _, a := range f.activity {
		if a.At.Before(before) && len(out) < limit {
			out = append(out, a)
		}
	}
	return out, nil
}

func (f *fakeStore) CountActivitySince(_ context.Context, _ uuid.UUID, since time.Time) (int, error) {
	n := 0
	for _, a := range f.activity {
		if a.At.After(since) {
			n++
		}
	}
	return n, nil
}

func (f *fakeStore) SeenAt(_ context.Context, userID uuid.UUID) (time.Time, error) {
	return f.seen[userID], nil
}

func (f *fakeStore) MarkSeen(_ context.Context, userID uuid.UUID, at time.Time) error {
	if at.After(f.seen[userID]) {
		f.seen[userID] = at
	}
	return nil
}

func (f *fakeStore) DailyStats(_ context.Context, userID uuid.UUID, _ time.Time) (notify.DailyStats, error) {
	s := f.stats
	s.UserID = userID
	return s, nil
}

type fakeNtfyClient struct {
	sent []notify.Notification
}

func (f *fakeNtfyClient) SendNtfy(_ context.Context, _, _, _ string, n notify.Notification) error {
	f.sent = append(f.sent, n)
	return nil
}

type fakeEmailClient struct {
	sentTo   []string
	sentSubj []string
	sentBody []string
}

func (f *fakeEmailClient) SendEmail(_ context.Context, to, subject, body string) error {
	f.sentTo = append(f.sentTo, to)
	f.sentSubj = append(f.sentSubj, subject)
	f.sentBody = append(f.sentBody, body)
	return nil
}

type fakeWebPushClient struct {
	sentUsers []uuid.UUID
	sentURLs  []string
}

func (f *fakeWebPushClient) SendWebPush(_ context.Context, userID uuid.UUID, _, _, url string) error {
	f.sentUsers = append(f.sentUsers, userID)
	f.sentURLs = append(f.sentURLs, url)
	return nil
}

func TestNotifyDispatchAllChannels(t *testing.T) {
	ctx := context.Background()
	u := uuid.New()

	st := newFakeStore()
	st.emails[u] = "primary@example.org"

	prefs := notify.Preferences{
		UserID:         u,
		EmailEnabled:   true,
		EmailAddress:   "custom@example.org",
		WebPushEnabled: true,
		NtfyEnabled:    true,
		NtfyURL:        "https://ntfy.example.org",
		NtfyTopic:      "candidate-alerts",
	}
	_ = st.SavePreferences(ctx, prefs)

	nt := &fakeNtfyClient{}
	em := &fakeEmailClient{}
	wp := &fakeWebPushClient{}

	svc := notify.NewService(st, nt, em, wp, "https://app.example.org", nil)

	n := notify.Notification{
		UserID:   u,
		Event:    notify.EventNeedsInput,
		Title:    "Input needed",
		Body:     "Please answer question",
		URL:      "/applications/123",
		Priority: notify.PriorityHigh,
	}

	if err := svc.Notify(ctx, n); err != nil {
		t.Fatalf("Notify: %v", err)
	}

	// Verify Web Push received full URL
	if len(wp.sentUsers) != 1 || wp.sentUsers[0] != u {
		t.Errorf("webpush sentUsers = %v, want [%v]", wp.sentUsers, u)
	}
	if len(wp.sentURLs) != 1 || wp.sentURLs[0] != "https://app.example.org/applications/123" {
		t.Errorf("webpush url = %v", wp.sentURLs)
	}

	// Mail goes to the verified account address, never to the typed one.
	if len(em.sentTo) != 1 || em.sentTo[0] != "primary@example.org" {
		t.Errorf("email to = %v, want the account address primary@example.org", em.sentTo)
	}
	if len(em.sentBody) != 1 || !strings.Contains(em.sentBody[0], "https://app.example.org/applications/123") {
		t.Errorf("email body missing url: %v", em.sentBody)
	}

	// Verify ntfy received
	if len(nt.sent) != 1 || nt.sent[0].Title != "Input needed" {
		t.Errorf("ntfy sent = %v", nt.sent)
	}
}

func TestQuietHoursSuppression(t *testing.T) {
	ctx := context.Background()
	u := uuid.New()

	st := newFakeStore()
	prefs := notify.Preferences{
		UserID:            u,
		WebPushEnabled:    true,
		EmailEnabled:      true,
		QuietHoursEnabled: true,
		QuietHoursStart:   "22:00",
		QuietHoursEnd:     "08:00",
	}
	_ = st.SavePreferences(ctx, prefs)

	wp := &fakeWebPushClient{}
	em := &fakeEmailClient{}
	svc := notify.NewService(st, nil, em, wp, "https://app.example.org", nil)

	// Set time inside quiet hours: 23:30
	svc.SetNow(func() time.Time {
		return time.Date(2026, 11, 8, 23, 30, 0, 0, time.UTC)
	})

	// 1. Normal priority notification must be skipped
	lowNotice := notify.Notification{
		UserID:   u,
		Event:    notify.EventStatusSuggestion,
		Priority: notify.PriorityNormal,
	}
	if err := svc.Notify(ctx, lowNotice); err != nil {
		t.Fatal(err)
	}
	if len(wp.sentUsers) != 0 || len(em.sentTo) != 0 {
		t.Errorf("expected notification skipped in quiet hours, got webpush=%d, email=%d", len(wp.sentUsers), len(em.sentTo))
	}

	// 2. High priority notification must BYPASS quiet hours
	urgentNotice := notify.Notification{
		UserID:   u,
		Event:    notify.EventHandoffClosing,
		Priority: notify.PriorityHigh,
	}
	if err := svc.Notify(ctx, urgentNotice); err != nil {
		t.Fatal(err)
	}
	if len(wp.sentUsers) != 1 {
		t.Errorf("expected urgent notification to bypass quiet hours")
	}
}

func TestDailySummaryDelivery(t *testing.T) {
	ctx := context.Background()
	u := uuid.New()

	st := newFakeStore()
	st.emails[u] = "user@example.org"
	st.locales[u] = i18n.DE
	st.stats = notify.DailyStats{
		SubmittedCount: 5,
		Invitations:    2,
	}

	prefs := notify.Preferences{
		UserID:              u,
		DailySummaryEnabled: true,
		DailySummaryTime:    "18:00",
		EmailEnabled:        true,
	}
	_ = st.SavePreferences(ctx, prefs)

	em := &fakeEmailClient{}
	svc := notify.NewService(st, nil, em, nil, "https://app.example.org", nil)

	// Time is 18:15 (due)
	now := time.Date(2026, 11, 8, 18, 15, 0, 0, time.UTC)
	svc.SetNow(func() time.Time { return now })

	sent, err := svc.CheckAndSendDailySummaries(ctx)
	if err != nil {
		t.Fatalf("CheckAndSendDailySummaries: %v", err)
	}
	if sent != 1 {
		t.Errorf("sent = %d, want 1", sent)
	}
	if len(em.sentBody) != 1 || !strings.Contains(em.sentBody[0], "Gesendete Bewerbungen: 5") {
		t.Errorf("email body = %v", em.sentBody)
	}
	if st.summaries[u].IsZero() {
		t.Errorf("expected last summary time updated")
	}
}

func TestNotifyFollowsTheUsersLanguage(t *testing.T) {
	ctx := context.Background()
	u := uuid.New()
	st := newFakeStore()
	st.emails[u] = "user@example.org"
	st.locales[u] = i18n.DE
	_ = st.SavePreferences(ctx, notify.Preferences{UserID: u, EmailEnabled: true})
	em := &fakeEmailClient{}
	svc := notify.NewService(st, nil, em, nil, "https://app.example.org", nil)
	if err := svc.Notify(ctx, notify.Notification{UserID: u, Event: notify.EventHandoffClosing, Key: "handoff_closing",
		Params: map[string]any{"time": "18:30 UTC"}, URL: "/needs-input?app=x", Priority: notify.PriorityHigh}); err != nil {
		t.Fatal(err)
	}
	if len(em.sentBody) != 1 || !strings.Contains(em.sentBody[0], "bis 18:30 UTC auf dich") ||
		!strings.Contains(em.sentBody[0], "https://app.example.org/needs-input?app=x") {
		t.Errorf("body = %v", em.sentBody)
	}
}

func TestActivityUnreadAndMarkRead(t *testing.T) {
	ctx := context.Background()
	u := uuid.New()
	st := newFakeStore()
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	app := uuid.New()
	st.activity = []notify.ActivityItem{
		{ID: "2", Kind: notify.KindState, State: "needs_input", ApplicationID: app, At: now.Add(-time.Minute)},
		{ID: "1", Kind: notify.KindState, State: "submitted", ApplicationID: app, At: now.Add(-time.Hour)},
	}
	st.seen[u] = now.Add(-30 * time.Minute)
	svc := notify.NewService(st, nil, nil, nil, "", nil)
	svc.SetNow(func() time.Time { return now })
	a, err := svc.Activity(ctx, u, time.Time{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if a.Unread != 1 || len(a.Items) != 1 || !a.Items[0].Unread || !a.More {
		t.Fatalf("activity = %+v", a)
	}
	if got := a.Items[0].Link(); got != "/needs-input?app="+app.String() {
		t.Errorf("link %s", got)
	}
	if err := svc.MarkRead(ctx, u); err != nil {
		t.Fatal(err)
	}
	if a, _ := svc.Activity(ctx, u, time.Time{}, 10); a.Unread != 0 || a.Items[0].Unread {
		t.Errorf("after mark read: %+v", a)
	}
}

// An ntfy topic stays one path segment of the server URL.
func TestNtfyTopicShape(t *testing.T) {
	svc := notify.NewService(newFakeStore(), nil, nil, nil, "", nil)
	for topic, ok := range map[string]bool{"getafjob-max_1": true, "../admin": false, "a/b": false, "x?y=1": false} {
		p := notify.DefaultPreferences(uuid.New(), "")
		p.NtfyEnabled, p.NtfyTopic = true, topic
		err := svc.SavePreferences(t.Context(), p)
		if (err == nil) != ok {
			t.Errorf("topic %q: err = %v, want ok=%v", topic, err, ok)
		}
	}
}

// Invalid names every field that cannot be saved; the defaults are valid.
func TestPreferencesInvalid(t *testing.T) {
	p := notify.DefaultPreferences(uuid.New(), "")
	if bad := p.Invalid(); len(bad) != 0 {
		t.Fatalf("defaults invalid: %v", bad)
	}
	p.NtfyEnabled, p.DailySummaryTime, p.QuietHoursStart, p.QuietHoursEnd = true, "25:00", "7", "08:60"
	want := "ntfy_topic daily_summary_time quiet_hours_start quiet_hours_end"
	if got := strings.Join(p.Invalid(), " "); got != want {
		t.Errorf("Invalid() = %q, want %q", got, want)
	}
}
