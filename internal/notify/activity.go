package notify

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// ActivityStates are the application states the activity feed reports: the ones a user wants to hear about.
var ActivityStates = []string{"needs_input", "submitted", "confirmed", "interview", "offer", "rejected", "parked",
	"failed", "submit_unknown"}

// ActivityKind tells a state change from a status suggestion read from the mailbox.
type ActivityKind string

const (
	KindState      ActivityKind = "state"
	KindSuggestion ActivityKind = "suggestion"
)

// ActivityItem is one entry of the feed.
type ActivityItem struct {
	ID            string // unique within its kind
	Kind          ActivityKind
	State         string // the new state, or the suggested one
	ApplicationID uuid.UUID
	Title         string
	Company       string
	At            time.Time
	Unread        bool
}

// Link is where the entry leads: the inbox for a run that waits, the application otherwise.
func (a ActivityItem) Link() string {
	if a.Kind == KindState && a.State == "needs_input" {
		return "/needs-input?app=" + a.ApplicationID.String()
	}
	return "/applications/" + a.ApplicationID.String()
}

// Activity is a page of the feed and how many entries are unread.
type Activity struct {
	Items  []ActivityItem
	Unread int
	More   bool // older entries exist
}

// maxActivity bounds one page of the feed.
const maxActivity = 100

// Activity returns the newest entries before the given time (zero: now), at most limit.
func (s *Service) Activity(ctx context.Context, userID uuid.UUID, before time.Time, limit int) (Activity, error) {
	limit = min(max(limit, 1), maxActivity)
	if before.IsZero() {
		before = s.now().Add(time.Second)
	}
	seen, err := s.store.SeenAt(ctx, userID)
	if err != nil {
		return Activity{}, err
	}
	items, err := s.store.Activity(ctx, userID, before, limit+1)
	if err != nil {
		return Activity{}, err
	}
	unread, err := s.store.CountActivitySince(ctx, userID, seen)
	if err != nil {
		return Activity{}, err
	}
	a := Activity{Unread: unread, More: len(items) > limit}
	a.Items = items[:min(limit, len(items))]
	for i := range a.Items {
		a.Items[i].Unread = a.Items[i].At.After(seen)
	}
	return a, nil
}

// MarkRead marks every entry up to now as read.
func (s *Service) MarkRead(ctx context.Context, userID uuid.UUID) error {
	return s.store.MarkSeen(ctx, userID, s.now())
}
