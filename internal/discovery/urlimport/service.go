package urlimport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/discovery"
	"github.com/1xxn/getafjob/internal/discovery/adapters/sources"
	"github.com/1xxn/getafjob/internal/platform/netguard"
)

var (
	// ErrInvalidURL reports a malformed or non-public URL.
	ErrInvalidURL = errors.New("invalid or non-public URL")
	// ErrFetchFailed reports an HTTP error when fetching the page.
	ErrFetchFailed = errors.New("failed to fetch URL")
	// ErrNoJobFound reports that no job title or company could be extracted.
	ErrNoJobFound = errors.New("could not extract job posting from URL")
)

// maxPage is how much of a page is read; career sites often embed megabytes of scripts after the posting data,
// so a longer page is cut, not refused (#213).
const maxPage = 8 << 20

var (
	// ErrUnsupportedBoard reports that no ATS or career site could be detected.
	ErrUnsupportedBoard = errors.New("could not detect a supported job board at this URL")
)

// CrawlStatus summarizes current and previous crawl runs for the UI.
type CrawlStatus struct {
	Running    bool
	Queued     bool
	LastRunAt  *time.Time
	LastStats  discovery.Stats
	LastErrors int
}

// JobUpserter stores a single posting in the index and returns its ID.
type JobUpserter interface {
	UpsertPosting(ctx context.Context, p discovery.Posting, at time.Time) (int64, discovery.Change, error)
	LatestCrawlRun(ctx context.Context) (*discovery.CrawlRun, error)
	ActiveCrawlRun(ctx context.Context, since time.Time) (*discovery.ActiveRun, error)
}

// MatchEvaluator evaluates a job and saves its match result for a user.
type MatchEvaluator interface {
	EvaluateAndSave(ctx context.Context, userID uuid.UUID, jobID int64, p discovery.Posting, at time.Time) error
}

// CrawlQueue enqueues manual crawls and checks queue status.
type CrawlQueue interface {
	QueueCrawl(ctx context.Context) (queued bool, err error)
	IsQueued(ctx context.Context) (bool, error)
}

// Service coordinates job URL ingestion, crawl controls, and career site additions.
type Service struct {
	client       *http.Client
	allowPrivate bool // for tests
	discovery    JobUpserter
	boards       discovery.Boards
	matcher      MatchEvaluator
	queue        CrawlQueue
	now          func() time.Time
}

// Options configure the urlimport Service.
type Options struct {
	Client       *http.Client
	AllowPrivate bool
	Discovery    JobUpserter
	Boards       discovery.Boards
	Matcher      MatchEvaluator
	Queue        CrawlQueue
	Now          func() time.Time
}

// NewService returns an ingestion and crawl control service.
func NewService(opts Options) *Service {
	c := opts.Client
	if c == nil {
		c = netguard.NewClient(netguard.ClientOptions{
			Timeout:      20 * time.Second,
			AllowPrivate: opts.AllowPrivate,
		})
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Service{
		client:       c,
		allowPrivate: opts.AllowPrivate,
		discovery:    opts.Discovery,
		boards:       opts.Boards,
		matcher:      opts.Matcher,
		queue:        opts.Queue,
		now:          now,
	}
}

// ImportJob fetches a job posting by URL, extracts its metadata, stores it in the index,
// and computes the match score against the user's active profile.
func (s *Service) ImportJob(ctx context.Context, userID uuid.UUID, rawURL string) (int64, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return 0, ErrInvalidURL
	}
	if !s.allowPrivate && !netguard.PublicURL(rawURL) {
		return 0, ErrInvalidURL
	}

	resp, err := netguard.Get(ctx, s.client, rawURL, s.allowPrivate)
	if err != nil {
		return 0, errors.Join(ErrFetchFailed, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, ErrFetchFailed
	}

	body, err := netguard.ReadPrefix(resp.Body, maxPage)
	if err != nil {
		return 0, errors.Join(ErrFetchFailed, err)
	}

	extracted, err := Extract(rawURL, body)
	if err != nil {
		return 0, err
	}
	if extracted.Title == "" || extracted.Company == "" {
		return 0, ErrNoJobFound
	}

	now := s.now()
	posting := extracted.ToPosting()
	jobID, _, err := s.discovery.UpsertPosting(ctx, posting, now)
	if err != nil {
		return 0, err
	}

	// Evaluate match against user profile if evaluator is configured
	if s.matcher != nil {
		_ = s.matcher.EvaluateAndSave(ctx, userID, jobID, posting, now)
	}

	return jobID, nil
}

// TriggerCrawl queues an immediate manual crawl job.
func (s *Service) TriggerCrawl(ctx context.Context) (queued bool, err error) {
	if s.queue == nil {
		return false, errors.New("crawl queue not configured")
	}
	return s.queue.QueueCrawl(ctx)
}

// CrawlStatus returns current crawler state and the last completed run stats.
func (s *Service) CrawlStatus(ctx context.Context) (CrawlStatus, error) {
	var st CrawlStatus
	now := s.now()

	// Check if a crawl is actively running in crawl_runs
	active, err := s.discovery.ActiveCrawlRun(ctx, now.Add(-2*time.Hour))
	if err == nil && active != nil {
		st.Running = true
	}

	// Check if a crawl is queued in river
	if s.queue != nil {
		if queued, err := s.queue.IsQueued(ctx); err == nil && queued {
			st.Queued = true
		}
	}

	// Read last completed crawl run
	latest, err := s.discovery.LatestCrawlRun(ctx)
	if err == nil && latest != nil {
		st.LastRunAt = latest.FinishedAt
		st.LastStats = latest.Stats
		st.LastErrors = len(latest.Errors)
	}

	return st, nil
}

// AddBoard detects the ATS platform behind a career URL and registers it in the crawl directory.
func (s *Service) AddBoard(ctx context.Context, rawURL string) (discovery.Board, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return discovery.Board{}, ErrInvalidURL
	}
	if !s.allowPrivate && !netguard.PublicURL(rawURL) {
		return discovery.Board{}, ErrInvalidURL
	}

	// 1. Direct ATS URL detection
	if sb, ok := sources.BoardFromURL(rawURL); ok {
		b := discovery.Board{
			ATS:     sb.ATS,
			Slug:    sb.Slug,
			Company: sb.Company,
			Config:  []byte(sb.Config),
		}
		if b.Company == "" {
			b.Company = formatSlug(b.Slug)
		}
		if s.boards != nil {
			if _, _, err := s.boards.ImportBoards(ctx, []discovery.Board{b}); err != nil {
				return discovery.Board{}, err
			}
		}
		return b, nil
	}

	// 2. Fetch page and detect embedded or linked boards
	resp, err := netguard.Get(ctx, s.client, rawURL, s.allowPrivate)
	if err != nil {
		return discovery.Board{}, errors.Join(ErrFetchFailed, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return discovery.Board{}, ErrFetchFailed
	}

	body, err := netguard.ReadPrefix(resp.Body, maxPage)
	if err != nil {
		return discovery.Board{}, errors.Join(ErrFetchFailed, err)
	}

	detected := sources.DetectBoards(body)
	if len(detected) > 0 {
		sb := detected[0]
		b := discovery.Board{
			ATS:     sb.ATS,
			Slug:    sb.Slug,
			Company: sb.Company,
			Config:  []byte(sb.Config),
		}
		if b.Company == "" {
			b.Company = formatSlug(b.Slug)
		}
		if s.boards != nil {
			if _, _, err := s.boards.ImportBoards(ctx, []discovery.Board{b}); err != nil {
				return discovery.Board{}, err
			}
		}
		return b, nil
	}

	// 3. Check for Schema.org JobPostings on the career page itself
	if len(sources.JobPostings(body)) > 0 {
		u, err := url.Parse(rawURL)
		if err != nil {
			return discovery.Board{}, ErrInvalidURL
		}
		config, err := json.Marshal(map[string]string{"url": rawURL})
		if err != nil {
			return discovery.Board{}, err
		}
		b := discovery.Board{
			ATS:     "careers",
			Slug:    strings.TrimPrefix(u.Hostname(), "www."),
			Company: cleanHost(u.Hostname()),
			Config:  config,
		}
		if s.boards != nil {
			if _, _, err := s.boards.ImportBoards(ctx, []discovery.Board{b}); err != nil {
				return discovery.Board{}, err
			}
		}
		return b, nil
	}

	return discovery.Board{}, ErrUnsupportedBoard
}
