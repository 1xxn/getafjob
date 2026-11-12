package app

import (
	"cmp"
	"context"
	"log/slog"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/propagation"

	"github.com/1xxn/getafjob/internal/discovery"
	"github.com/1xxn/getafjob/internal/discovery/adapters/sources"
	"github.com/1xxn/getafjob/internal/platform/config"
	"github.com/1xxn/getafjob/internal/platform/netguard"
	"github.com/1xxn/getafjob/internal/platform/queue"
	"github.com/1xxn/getafjob/internal/privacy"
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
	states  discovery.BoardStates
	client  *sources.Client
	terms   func(context.Context) (searchTerms, error)
	logger  *slog.Logger
	timeout time.Duration
	metrics *crawlMetrics
}

// Timeout bounds one crawl (a crawl with detail requests takes minutes, not hours).
func (w *crawlWorker) Timeout(*river.Job[CrawlArgs]) time.Duration { return w.timeout }

func (w *crawlWorker) Work(ctx context.Context, _ *river.Job[CrawlArgs]) error {
	start := time.Now()
	terms, err := w.terms(ctx)
	if err != nil {
		return err
	}
	srcs, ids, err := crawlSources(ctx, w.client, w.boards, terms, w.logger)
	if err != nil {
		return err
	}
	rep, err := w.svc.Crawl(ctx, srcs)
	if err != nil {
		return err // validators stay as they were, so the next crawl fetches everything again
	}
	w.metrics.crawl(ctx, rep, len(srcs), time.Since(start))
	if err := w.states.SaveBoardStates(ctx, boardStates(ids, rep), time.Now()); err != nil {
		return err
	}
	w.logger.InfoContext(ctx, "crawl finished", "run", rep.RunID, "sources", len(srcs), "fetched", rep.Stats.Fetched,
		"new", rep.Stats.Changes[discovery.ChangeNew], "updated", rep.Stats.Changes[discovery.ChangeUpdated],
		"closed", rep.Stats.Closed, "not_modified", rep.Stats.NotModified, "failed_sources", len(rep.Errors))
	if c, err := river.ClientFromContextSafely[pgx.Tx](ctx); err == nil {
		if _, err := c.Insert(ctx, BoardDiscoveryArgs{}, nil); err != nil {
			w.logger.WarnContext(ctx, "board discovery not queued", "err", err)
		}
		if _, err := c.Insert(ctx, MatchArgs{}, nil); err != nil {
			w.logger.WarnContext(ctx, "matching not queued", "err", err)
		}
	}
	return nil
}

// BoardDiscoveryArgs looks for the own boards of companies seen only on job boards; queued after each crawl.
type BoardDiscoveryArgs struct{}

// Kind implements river.JobArgs.
func (BoardDiscoveryArgs) Kind() string { return "board_discovery" }

// InsertOpts runs discovery on the crawl queue (never alongside a crawl); at most one waits or runs.
func (BoardDiscoveryArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: queue.Crawl, MaxAttempts: 2, UniqueOpts: river.UniqueOpts{
		ByState: []rivertype.JobState{rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateRunning,
			rivertype.JobStateRetryable, rivertype.JobStateScheduled}}}
}

type boardDiscoveryWorker struct {
	river.WorkerDefaults[BoardDiscoveryArgs]
	discovery *discovery.BoardDiscovery
	logger    *slog.Logger
	metrics   *crawlMetrics
}

// Timeout bounds one run (about 30 companies with a few requests each).
func (w *boardDiscoveryWorker) Timeout(*river.Job[BoardDiscoveryArgs]) time.Duration {
	return 20 * time.Minute
}

func (w *boardDiscoveryWorker) Work(ctx context.Context, _ *river.Job[BoardDiscoveryArgs]) error {
	st, err := w.discovery.Run(ctx)
	if err != nil {
		return err
	}
	w.metrics.boards(ctx, st)
	w.logger.InfoContext(ctx, "board discovery finished", "checked", st.Checked, "found", st.Found, "added", st.Added,
		"errors", st.Errors)
	return nil
}

// crawlSources lists what a crawl reads: career sites and job boards, search-based sources when users gave search
// terms, and every enabled company board. A board that cannot be built is skipped and logged. ids maps the
// source name of each board to its ID.
func crawlSources(ctx context.Context, c *sources.Client, boards discovery.Boards, terms searchTerms, logger *slog.Logger) (
	out []discovery.Source, ids map[string]int64, err error,
) {
	out = []discovery.Source{sources.NewGoogle(c), sources.NewApple(c, "germany-DEU"), sources.NewArbeitnow(c, 10)}
	if len(terms.Queries) > 0 {
		out = append(out, sources.NewHimalayas(c, terms.Queries, "Germany"), sources.NewAmazon(c, terms.Queries, "DEU"))
		if len(terms.Cities) > 0 {
			out = append(out, sources.NewArbeitsagentur(c, terms.Queries, terms.Cities))
		}
		loc := "Germany"
		if len(terms.Cities) > 0 {
			loc = terms.Cities[0] + ", Germany"
		}
		out = append(out, sources.NewLinkedIn(c, terms.Queries, loc), sources.NewIndeed(c, terms.Queries, loc), sources.NewGlassdoor(c, terms.Queries, loc))
	}
	list, err := boards.EnabledBoards(ctx)
	if err != nil {
		return nil, nil, err
	}
	ids = map[string]int64{}
	for _, b := range list {
		src, err := sources.FromBoard(c, sources.Board{ATS: b.ATS, Slug: b.Slug, Company: b.Company, Config: b.Config})
		if err != nil {
			logger.WarnContext(ctx, "board skipped", "board", b.ID, "err", err)
			continue
		}
		if cs, ok := src.(discovery.Conditional); ok {
			cs.SetValidators(b.Validators)
		}
		ids[src.Name()] = b.ID
		out = append(out, src)
	}
	return out, ids, nil
}

// boardStates turns a crawl report into the state of each board: its error code, or the validators of its answer.
func boardStates(ids map[string]int64, rep discovery.Report) []discovery.BoardState {
	out := make([]discovery.BoardState, 0, len(ids))
	for name, id := range ids {
		out = append(out, discovery.BoardState{ID: id, Error: rep.Errors[name], Validators: rep.Validators[name]})
	}
	slices.SortFunc(out, func(a, b discovery.BoardState) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

// sourceClient fetches through the egress proxy when one is configured.
func sourceClient(cfg config.Config) *sources.Client {
	hc := netguard.NewClient(netguard.ClientOptions{Proxy: cfg.EgressProxy, Timeout: 2 * time.Minute})
	// Client spans only: no trace headers go to third-party sites; netguard still checks every connection.
	hc.Transport = otelhttp.NewTransport(hc.Transport, otelhttp.WithPropagators(propagation.NewCompositeTextMapPropagator()))
	return sources.NewClient(hc)
}

// CleanupArgs removes expired sign-in tokens, sessions, invitations, and screenshots past retention.
type CleanupArgs struct{}

// Kind implements river.JobArgs.
func (CleanupArgs) Kind() string { return "identity_cleanup" }

type cleanupWorker struct {
	river.WorkerDefaults[CleanupArgs]
	privacy *privacy.Service
}

func (w *cleanupWorker) Work(ctx context.Context, _ *river.Job[CleanupArgs]) error {
	if w.privacy != nil {
		return w.privacy.Cleanup(ctx)
	}
	return nil
}

// periodicJobs schedules the crawl (unless its interval is 0), the cleanup, the recovery of stuck submits and the handoff watch; all also run
// once at start.
func periodicJobs(cfg config.Config) []*river.PeriodicJob {
	start := &river.PeriodicJobOpts{RunOnStart: true}
	jobs := []*river.PeriodicJob{
		river.NewPeriodicJob(river.PeriodicInterval(15*time.Minute),
			func() (river.JobArgs, *river.InsertOpts) { return CleanupArgs{}, nil }, start),
		river.NewPeriodicJob(river.PeriodicInterval(5*time.Minute),
			func() (river.JobArgs, *river.InsertOpts) { return RecoverArgs{}, nil }, start),
		river.NewPeriodicJob(river.PeriodicInterval(time.Minute),
			func() (river.JobArgs, *river.InsertOpts) { return HandoffArgs{}, nil }, start),
		river.NewPeriodicJob(river.PeriodicInterval(15*time.Minute),
			func() (river.JobArgs, *river.InsertOpts) { return PlanArgs{}, nil }, start),
		river.NewPeriodicJob(river.PeriodicInterval(15*time.Minute),
			func() (river.JobArgs, *river.InsertOpts) { return InboxArgs{}, nil }, start),
		river.NewPeriodicJob(river.PeriodicInterval(time.Hour),
			func() (river.JobArgs, *river.InsertOpts) { return DailySummaryArgs{}, nil }, start),
	}
	if cfg.CrawlInterval > 0 {
		jobs = append(jobs, river.NewPeriodicJob(river.PeriodicInterval(cfg.CrawlInterval),
			func() (river.JobArgs, *river.InsertOpts) { return CrawlArgs{}, nil }, start))
	}
	return jobs
}
