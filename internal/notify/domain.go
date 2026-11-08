// Package notify dispatches user notifications across multiple channels
// (Web Push, Mail/SMTP, ntfy) and schedules daily application summaries (#38).
package notify

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ErrInvalidPreferences reports validation failure in notification preferences.
var ErrInvalidPreferences = errors.New("invalid notification preferences")

// ErrNotFound indicates notification preferences were not found.
var ErrNotFound = errors.New("notification preferences not found")

// Event classifies the trigger for a notification.
type Event string

const (
	EventHandoffClosing   Event = "handoff_closing"
	EventNeedsInput       Event = "needs_input"
	EventStatusSuggestion Event = "status_suggestion"
	EventApprovalQueued   Event = "approval_queued"
	EventDailySummary     Event = "daily_summary"
	EventTest             Event = "test"
)

// Priority levels for notifications.
type Priority int

const (
	PriorityLow    Priority = 2
	PriorityNormal Priority = 3
	PriorityHigh   Priority = 4
	PriorityUrgent Priority = 5
)

// Notification is an outbound message to a user.
type Notification struct {
	UserID    uuid.UUID
	Event     Event
	Title     string
	Body      string
	URL       string // relative or absolute click link
	Priority  Priority
	Tags      []string
	CreatedAt time.Time
}

// Preferences holds user notification settings.
type Preferences struct {
	UserID              uuid.UUID
	EmailEnabled        bool
	EmailAddress        string // if empty, fall back to account email
	WebPushEnabled      bool
	NtfyEnabled         bool
	NtfyURL             string
	NtfyTopic           string
	NtfyToken           string
	DailySummaryEnabled bool
	DailySummaryTime    string // "HH:MM", defaults to "18:00"
	LastSummaryAt       *time.Time
	QuietHoursEnabled   bool
	QuietHoursStart     string // "HH:MM", defaults to "22:00"
	QuietHoursEnd       string // "HH:MM", defaults to "08:00"
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// DefaultPreferences returns standard preferences for a user.
func DefaultPreferences(userID uuid.UUID, fallbackEmail string) Preferences {
	now := time.Now()
	return Preferences{
		UserID:              userID,
		EmailEnabled:        fallbackEmail != "",
		EmailAddress:        fallbackEmail,
		WebPushEnabled:      true,
		NtfyEnabled:         false,
		NtfyURL:             "https://ntfy.sh",
		NtfyTopic:           "",
		NtfyToken:           "",
		DailySummaryEnabled: true,
		DailySummaryTime:    "18:00",
		QuietHoursEnabled:   false,
		QuietHoursStart:     "22:00",
		QuietHoursEnd:       "08:00",
		CreatedAt:           now,
		UpdatedAt:           now,
	}
}

// InQuietHours returns true if the given time falls within the configured quiet hours.
func (p Preferences) InQuietHours(t time.Time) bool {
	if !p.QuietHoursEnabled {
		return false
	}
	return isTimeBetween(t, p.QuietHoursStart, p.QuietHoursEnd)
}

// IsSummaryDue returns true if daily summary is enabled and due at current time.
func (p Preferences) IsSummaryDue(now time.Time) bool {
	if !p.DailySummaryEnabled {
		return false
	}
	targetHour, targetMin, err := parseHourMinute(p.DailySummaryTime)
	if err != nil {
		targetHour, targetMin = 18, 0
	}
	if p.LastSummaryAt != nil && now.Sub(*p.LastSummaryAt) < 20*time.Hour {
		return false
	}
	currentHour, currentMin := now.Hour(), now.Minute()
	if currentHour > targetHour || (currentHour == targetHour && currentMin >= targetMin) {
		return true
	}
	return false
}

// DailyStats represents aggregated statistics for a user's 24h summary.
type DailyStats struct {
	UserID             uuid.UUID
	PeriodStart        time.Time
	PeriodEnd          time.Time
	SubmittedCount     int64
	NeedsInputCount    int64
	PendingSuggestions int64
	PendingApprovals   int64
	Confirmations      int64
	Invitations        int64
	Rejections         int64
}

// FormatSummary formats a readable daily summary text for email or push notifications.
func (s DailyStats) FormatSummary() (title, body string) {
	title = "getafjob · Daily Summary"
	var sb strings.Builder
	sb.WriteString("Summary for the past 24 hours:\n")
	fmt.Fprintf(&sb, "• Applications submitted: %d\n", s.SubmittedCount)
	if s.PendingApprovals > 0 {
		fmt.Fprintf(&sb, "• Awaiting your approval: %d\n", s.PendingApprovals)
	}
	if s.NeedsInputCount > 0 {
		fmt.Fprintf(&sb, "• Action required (CAPTCHA / Question): %d\n", s.NeedsInputCount)
	}
	if s.PendingSuggestions > 0 {
		fmt.Fprintf(&sb, "• Pending status suggestions: %d\n", s.PendingSuggestions)
	}
	if s.Invitations > 0 || s.Confirmations > 0 || s.Rejections > 0 {
		fmt.Fprintf(&sb, "• Responses received: %d invitations, %d confirmations, %d rejections\n",
			s.Invitations, s.Confirmations, s.Rejections)
	}
	return title, strings.TrimRight(sb.String(), "\n")
}

func parseHourMinute(s string) (hour, minute int, err error) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 2 {
		return 0, 0, errors.New("invalid format, expected HH:MM")
	}
	hour, err = strconv.Atoi(parts[0])
	if err != nil || hour < 0 || hour > 23 {
		return 0, 0, errors.New("invalid hour")
	}
	minute, err = strconv.Atoi(parts[1])
	if err != nil || minute < 0 || minute > 59 {
		return 0, 0, errors.New("invalid minute")
	}
	return hour, minute, nil
}

func isTimeBetween(t time.Time, startStr, endStr string) bool {
	startHour, startMin, err := parseHourMinute(startStr)
	if err != nil {
		startHour, startMin = 22, 0
	}
	endHour, endMin, err := parseHourMinute(endStr)
	if err != nil {
		endHour, endMin = 8, 0
	}

	startMinutes := startHour*60 + startMin
	endMinutes := endHour*60 + endMin
	currentMinutes := t.Hour()*60 + t.Minute()

	if startMinutes <= endMinutes {
		return currentMinutes >= startMinutes && currentMinutes < endMinutes
	}
	// Wraps around midnight (e.g. 22:00 to 08:00)
	return currentMinutes >= startMinutes || currentMinutes < endMinutes
}
