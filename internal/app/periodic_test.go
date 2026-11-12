package app

import (
	"testing"
	"time"

	"github.com/1xxn/getafjob/internal/platform/config"
)

// A crawl interval of 0 (the development stack) schedules no crawl; every other periodic job stays.
func TestPeriodicJobsWithoutCrawl(t *testing.T) {
	with := periodicJobs(config.Config{CrawlInterval: 6 * time.Hour})
	without := periodicJobs(config.Config{})
	if len(with) != len(without)+1 {
		t.Fatalf("jobs with crawl %d, without %d", len(with), len(without))
	}
}
