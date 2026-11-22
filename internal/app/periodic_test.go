package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/riverqueue/river"

	"github.com/1xxn/getafjob/internal/platform/config"
	"github.com/1xxn/getafjob/internal/platform/queue"
)

// A crawl interval of 0 (the development stack) schedules no crawl; every other periodic job stays.
func TestPeriodicJobsWithoutCrawl(t *testing.T) {
	with := periodicJobs(config.Config{CrawlInterval: 6 * time.Hour})
	without := periodicJobs(config.Config{})
	if len(with) != len(without)+1 {
		t.Fatalf("jobs with crawl %d, without %d", len(with), len(without))
	}
}

// A scheduled crawl does nothing before the first crawl someone started has finished (#507).
func TestScheduledCrawlWaitsForTheFirstOne(t *testing.T) {
	done, readErr := false, error(nil)
	w := &crawlWorker{logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		crawled: func(context.Context) (bool, error) { return done, readErr }}
	// svc is nil: touching it would panic, so a nil error shows the crawl was skipped.
	if err := w.Work(t.Context(), &river.Job[CrawlArgs]{Args: CrawlArgs{}}); err != nil {
		t.Fatalf("scheduled crawl before the first: %v", err)
	}
	readErr = errors.New("db down")
	if err := w.Work(t.Context(), &river.Job[CrawlArgs]{Args: CrawlArgs{}}); !errors.Is(err, readErr) {
		t.Fatalf("read error = %v", err)
	}
}

func TestJobQueueAssignments(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"inbox", (InboxArgs{}).InsertOpts().Queue, queue.Mail},
		{"daily_summary", (DailySummaryArgs{}).InsertOpts().Queue, queue.Mail},
		{"search_alert", (SearchAlertArgs{}).InsertOpts().Queue, queue.Mail},
		{"email_bundles", (EmailBundleArgs{}).InsertOpts().Queue, queue.Mail},
		{"plan", (PlanArgs{}).InsertOpts().Queue, queue.Apply},
		{"recover", (RecoverArgs{}).InsertOpts().Queue, queue.Apply},
		{"handoff", (HandoffArgs{}).InsertOpts().Queue, queue.Apply},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s queue = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}
