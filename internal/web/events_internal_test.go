package web

import (
	"testing"

	"github.com/1xxn/getafjob/internal/platform/events"
)

// The crawl and new ratings go out as "jobs", everything else as "app".
func TestEventName(t *testing.T) {
	for topic, want := range map[string]string{"": "app", events.TopicSuggestion: "app", events.TopicCrawl: "jobs", events.TopicMatches: "jobs"} {
		if got := eventName(events.Event{Topic: topic}); got != want {
			t.Errorf("%q: %s, want %s", topic, got, want)
		}
	}
}
