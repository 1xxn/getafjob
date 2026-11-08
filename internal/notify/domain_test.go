package notify_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/notify"
)

func TestQuietHours(t *testing.T) {
	prefs := notify.Preferences{
		QuietHoursEnabled: true,
		QuietHoursStart:   "22:00",
		QuietHoursEnd:     "08:00",
	}

	tests := []struct {
		time string
		want bool
	}{
		{"2026-11-08T23:30:00Z", true},
		{"2026-11-08T03:15:00Z", true},
		{"2026-11-08T07:59:00Z", true},
		{"2026-11-08T08:00:00Z", false},
		{"2026-11-08T12:00:00Z", false},
		{"2026-11-08T21:59:00Z", false},
		{"2026-11-08T22:00:00Z", true},
	}

	for _, tc := range tests {
		parsed, err := time.Parse(time.RFC3339, tc.time)
		if err != nil {
			t.Fatal(err)
		}
		got := prefs.InQuietHours(parsed)
		if got != tc.want {
			t.Errorf("InQuietHours(%v) = %v, want %v", tc.time, got, tc.want)
		}
	}
}

func TestSummaryDue(t *testing.T) {
	u := uuid.New()
	prefs := notify.DefaultPreferences(u, "user@example.com")
	prefs.DailySummaryEnabled = true
	prefs.DailySummaryTime = "18:00"

	// Before 18:00 -> not due
	before := time.Date(2026, 11, 8, 17, 30, 0, 0, time.UTC)
	if prefs.IsSummaryDue(before) {
		t.Errorf("expected not due before 18:00")
	}

	// At 18:05 -> due
	atTime := time.Date(2026, 11, 8, 18, 5, 0, 0, time.UTC)
	if !prefs.IsSummaryDue(atTime) {
		t.Errorf("expected due at 18:05")
	}

	// Sent 2 hours ago -> not due
	lastSent := atTime.Add(-2 * time.Hour)
	prefs.LastSummaryAt = &lastSent
	if prefs.IsSummaryDue(atTime) {
		t.Errorf("expected not due if sent recently")
	}

	// Sent 22 hours ago -> due again
	lastSentOld := atTime.Add(-22 * time.Hour)
	prefs.LastSummaryAt = &lastSentOld
	if !prefs.IsSummaryDue(atTime) {
		t.Errorf("expected due if sent >20h ago")
	}
}

func TestFormatSummary(t *testing.T) {
	stats := notify.DailyStats{
		SubmittedCount:     3,
		PendingApprovals:   2,
		NeedsInputCount:    1,
		PendingSuggestions: 4,
		Confirmations:      2,
		Invitations:        1,
		Rejections:         0,
	}

	title, body := stats.FormatSummary()
	if title == "" || !strings.Contains(title, "Daily Summary") {
		t.Errorf("title = %q", title)
	}
	if !strings.Contains(body, "Applications submitted: 3") {
		t.Errorf("missing submitted count: %s", body)
	}
	if !strings.Contains(body, "Awaiting your approval: 2") {
		t.Errorf("missing approvals count: %s", body)
	}
	if !strings.Contains(body, "Action required") {
		t.Errorf("missing needs input: %s", body)
	}
	if !strings.Contains(body, "1 invitations") {
		t.Errorf("missing invitations count: %s", body)
	}
}
