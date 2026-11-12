package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/1xxn/getafjob/internal/application"
	"github.com/1xxn/getafjob/internal/notify"
	notifymail "github.com/1xxn/getafjob/internal/notify/adapters/mail"
	"github.com/1xxn/getafjob/internal/notify/adapters/ntfy"
	notifystore "github.com/1xxn/getafjob/internal/notify/adapters/pgstore"
	notifywebpush "github.com/1xxn/getafjob/internal/notify/adapters/webpush"
	"github.com/1xxn/getafjob/internal/platform/config"
	"github.com/1xxn/getafjob/internal/platform/db"
	"github.com/1xxn/getafjob/internal/platform/mail"
	"github.com/1xxn/getafjob/internal/platform/netguard"
	"github.com/1xxn/getafjob/internal/platform/queue"
)

func newNotifyService(pool *pgxpool.Pool, cfg config.Config, logger *slog.Logger) (*notify.Service, pushNotifier, string) {
	notifier, vapidPub := newPushNotifier(pool, cfg, logger)
	nstore := notifystore.New(pool)
	var ntfyClient *ntfy.Client
	if cfg.EgressProxy != nil {
		ntfyClient = ntfy.NewClientWithHTTP(netguard.NewClient(netguard.ClientOptions{Proxy: cfg.EgressProxy, Timeout: 10 * time.Second}))
	} else {
		ntfyClient = ntfy.NewClient()
	}

	var emailClient notify.EmailClient
	if cfg.SMTPURL != "" {
		if s, err := mail.New(cfg.SMTPURL.Reveal(), cfg.MailFrom, cfg.EgressProxy); err == nil {
			emailClient = notifymail.NewSender(s)
		} else {
			logger.Warn("invalid SMTP_URL", "err", err)
		}
	}

	webpushClient := notifywebpush.NewNotifier(notifier.push)
	baseURL := cfg.PublicURL.String()
	notifySvc := notify.NewService(nstore, ntfyClient, emailClient, webpushClient, baseURL, logger)

	return notifySvc, notifier, vapidPub
}

// DailySummaryArgs triggers scheduled delivery of daily application summaries.
type DailySummaryArgs struct{}

// Kind implements river.JobArgs.
func (DailySummaryArgs) Kind() string { return "daily_summary" }

// InsertOpts ensures at most one daily summary check waits or runs.
func (DailySummaryArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		MaxAttempts: 3,
		UniqueOpts: river.UniqueOpts{
			ByArgs: true,
			ByState: []rivertype.JobState{
				rivertype.JobStateAvailable,
				rivertype.JobStatePending,
				rivertype.JobStateRunning,
				rivertype.JobStateRetryable,
				rivertype.JobStateScheduled,
			},
		},
	}
}

type dailySummaryWorker struct {
	river.WorkerDefaults[DailySummaryArgs]
	svc    *notify.Service
	logger *slog.Logger
}

func (w *dailySummaryWorker) Timeout(*river.Job[DailySummaryArgs]) time.Duration {
	return 15 * time.Minute
}

func (w *dailySummaryWorker) Work(ctx context.Context, _ *river.Job[DailySummaryArgs]) error {
	n, err := w.svc.CheckAndSendDailySummaries(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		w.logger.InfoContext(ctx, "daily summaries delivered", "count", n)
	}
	return nil
}

// notifyAppNotifier adapts notify.Service to application.Notifier.
type notifyAppNotifier struct {
	svc *notify.Service
}

func newNotifyAppNotifier(svc *notify.Service) *notifyAppNotifier {
	return &notifyAppNotifier{svc: svc}
}

func (n *notifyAppNotifier) HandoffClosing(ctx context.Context, r application.Ref, until time.Time) error {
	return n.svc.Notify(ctx, notify.Notification{
		UserID:    r.UserID,
		Event:     notify.EventHandoffClosing,
		Key:       "handoff_closing",
		Params:    map[string]any{"time": until.UTC().Format("15:04") + " UTC"},
		URL:       "/needs-input?app=" + r.ID.String(),
		Priority:  notify.PriorityHigh,
		Tags:      []string{"warning", "hourglass"},
		CreatedAt: time.Now(),
	})
}

func (n *notifyAppNotifier) YourTurn(ctx context.Context, userID, appID uuid.UUID) {
	_ = n.svc.Notify(ctx, notify.Notification{
		UserID:    userID,
		Event:     notify.EventNeedsInput,
		Key:       "your_turn",
		URL:       "/needs-input?app=" + appID.String(),
		Priority:  notify.PriorityUrgent,
		Tags:      []string{"question", "robot"},
		CreatedAt: time.Now(),
	})
}

// adminNotifySummary queues a daily summary run now.
func adminNotifySummary(ctx context.Context, args []string, env Env) int {
	if len(args) != 0 {
		_, _ = fmt.Fprintln(env.Stderr, "usage: getafjob admin notify summary")
		return ExitUsage
	}
	cfg, err := config.Load(env.Config)
	if err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "configuration error:\n%v\n", err)
		return ExitUsage
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL, cfg.DatabasePassword, "getafjob-admin")
	if err != nil {
		_, _ = fmt.Fprintln(env.Stderr, err)
		return ExitError
	}
	defer pool.Close()
	ins, err := queue.NewInserter(pool, nil)
	if err != nil {
		_, _ = fmt.Fprintln(env.Stderr, err)
		return ExitError
	}
	if _, err := ins.Insert(ctx, DailySummaryArgs{}, nil); err != nil {
		_, _ = fmt.Fprintln(env.Stderr, err)
		return ExitError
	}
	_, _ = fmt.Fprintln(env.Stdout, "daily summary check queued")
	return ExitOK
}
