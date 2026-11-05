package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/1xxn/getafjob/internal/discovery"
	"github.com/1xxn/getafjob/internal/discovery/adapters/sources"
	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/platform/config"
	"github.com/1xxn/getafjob/internal/platform/netguard"
	"github.com/1xxn/getafjob/internal/platform/queue"
)

// CrawlArgs starts a crawl of all sources. Manual crawls (admin crawl) are unique apart from scheduled ones, so one
// can wait while a scheduled crawl runs.
type CrawlArgs struct {
	Manual bool `json:"manual,omitempty"`
}

// Kind implements river.JobArgs.
func (CrawlArgs) Kind() string { return "crawl" }

// InsertOpts puts crawls on their own queue (one runs at a time); per kind of trigger at most one waits or runs.
func (CrawlArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: queue.Crawl, MaxAttempts: 3, UniqueOpts: river.UniqueOpts{ByArgs: true,
		ByState: []rivertype.JobState{rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateRunning,
			rivertype.JobStateRetryable, rivertype.JobStateScheduled}}}
}

// crawlWorker runs a crawl over the built-in sources and every enabled board.
type crawlWorker struct {
	river.WorkerDefaults[CrawlArgs]
	svc     *discovery.Service
	boards  discovery.Boards
	client  *sources.Client
	cfg     config.Config
	logger  *slog.Logger
	timeout time.Duration
}

// Timeout bounds one crawl (a crawl with detail requests takes minutes, not hours).
func (w *crawlWorker) Timeout(*river.Job[CrawlArgs]) time.Duration { return w.timeout }

func (w *crawlWorker) Work(ctx context.Context, _ *river.Job[CrawlArgs]) error {
	srcs, err := crawlSources(ctx, w.client, w.boards, w.cfg, w.logger)
	if err != nil {
		return err
	}
	rep, err := w.svc.Crawl(ctx, srcs)
	if err != nil {
		return err
	}
	w.logger.InfoContext(ctx, "crawl finished", "run", rep.RunID, "sources", len(srcs), "fetched", rep.Stats.Fetched,
		"new", rep.Stats.Changes[discovery.ChangeNew], "updated", rep.Stats.Changes[discovery.ChangeUpdated],
		"closed", rep.Stats.Closed, "failed_sources", len(rep.Errors))
	return nil
}

// crawlSources lists what a crawl reads: career sites and job boards, search-based sources when search terms are
// configured, and every enabled company board. A board that cannot be built is skipped and logged.
func crawlSources(ctx context.Context, c *sources.Client, boards discovery.Boards, cfg config.Config, logger *slog.Logger) ([]discovery.Source, error) {
	out := []discovery.Source{sources.NewGoogle(c), sources.NewApple(c, "germany-DEU"), sources.NewArbeitnow(c, 10)}
	if len(cfg.CrawlQueries) > 0 {
		out = append(out, sources.NewHimalayas(c, cfg.CrawlQueries, "Germany"), sources.NewAmazon(c, cfg.CrawlQueries, "DEU"))
		if len(cfg.CrawlCities) > 0 {
			out = append(out, sources.NewArbeitsagentur(c, cfg.CrawlQueries, cfg.CrawlCities))
		}
	}
	list, err := boards.EnabledBoards(ctx)
	if err != nil {
		return nil, err
	}
	for _, b := range list {
		src, err := sources.FromBoard(c, sources.Board{ATS: b.ATS, Slug: b.Slug, Company: b.Company, Config: b.Config})
		if err != nil {
			logger.WarnContext(ctx, "board skipped", "board", b.ID, "err", err)
			continue
		}
		out = append(out, src)
	}
	return out, nil
}

// sourceClient fetches through the egress proxy when one is configured.
func sourceClient(cfg config.Config) *sources.Client {
	return sources.NewClient(netguard.NewClient(netguard.ClientOptions{Proxy: cfg.EgressProxy, Timeout: 2 * time.Minute}))
}

// CleanupArgs removes expired sign-in tokens, sessions and invitations.
type CleanupArgs struct{}

// Kind implements river.JobArgs.
func (CleanupArgs) Kind() string { return "identity_cleanup" }

type cleanupWorker struct {
	river.WorkerDefaults[CleanupArgs]
	ids *identity.Service
}

func (w *cleanupWorker) Work(ctx context.Context, _ *river.Job[CleanupArgs]) error {
	return w.ids.Cleanup(ctx)
}

// periodicJobs schedules the crawl and the cleanup; both also run once at start.
func periodicJobs(cfg config.Config) []*river.PeriodicJob {
	start := &river.PeriodicJobOpts{RunOnStart: true}
	return []*river.PeriodicJob{
		river.NewPeriodicJob(river.PeriodicInterval(cfg.CrawlInterval),
			func() (river.JobArgs, *river.InsertOpts) { return CrawlArgs{}, nil }, start),
		river.NewPeriodicJob(river.PeriodicInterval(15*time.Minute),
			func() (river.JobArgs, *river.InsertOpts) { return CleanupArgs{}, nil }, start),
	}
}
