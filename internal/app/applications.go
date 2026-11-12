package app

import (
	"context"
	"crypto/ecdsa"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/1xxn/getafjob/internal/application"
	"github.com/1xxn/getafjob/internal/platform/config"
	"github.com/1xxn/getafjob/internal/platform/netguard"
	"github.com/1xxn/getafjob/internal/platform/webpush"
)

// RecoverArgs turns applications stuck in submitting into submit_unknown.
type RecoverArgs struct{}

// Kind implements river.JobArgs.
func (RecoverArgs) Kind() string { return "application_recover" }

type recoverWorker struct {
	river.WorkerDefaults[RecoverArgs]
	svc    *application.Service
	logger *slog.Logger
}

func (w *recoverWorker) Work(ctx context.Context, _ *river.Job[RecoverArgs]) error {
	n, err := w.svc.RecoverCrashed(ctx)
	if n > 0 {
		w.logger.WarnContext(ctx, "applications stuck in submitting marked submit_unknown", "count", n)
	}
	return err
}

// HandoffArgs warns before handoff windows close and parks applications whose window closed.
type HandoffArgs struct{}

// Kind implements river.JobArgs.
func (HandoffArgs) Kind() string { return "application_handoffs" }

type handoffWorker struct {
	river.WorkerDefaults[HandoffArgs]
	svc      *application.Service
	notifier application.Notifier
	logger   *slog.Logger
}

func (w *handoffWorker) Work(ctx context.Context, _ *river.Job[HandoffArgs]) error {
	warned, parked, err := w.svc.WatchHandoffs(ctx, w.notifier)
	if warned+parked > 0 {
		w.logger.InfoContext(ctx, "handoff windows", "warned", warned, "parked", parked)
	}
	return err
}

type pushNotifier struct{ push *webpush.Notifier }

func newPushNotifier(pool *pgxpool.Pool, cfg config.Config, logger *slog.Logger) (pushNotifier, string) {
	var priv *ecdsa.PrivateKey
	var pub string
	if cfg.VAPIDKey != "" {
		if k, err := webpush.ParsePrivateKey(cfg.VAPIDKey.Reveal()); err == nil {
			priv = k
			pub = webpush.PublicKeyString(k)
		} else {
			logger.Warn("invalid GETAFJOB_VAPID_KEY", "err", err)
		}
	}
	var client *webpush.Client
	if priv != nil {
		subject := cfg.VAPIDSubject
		if subject == "" {
			subject = "mailto:" + cfg.MailFrom
		}
		var hc *http.Client
		if cfg.EgressProxy != nil {
			hc = netguard.NewClient(netguard.ClientOptions{Proxy: cfg.EgressProxy, Timeout: 10 * time.Second})
		}
		client = &webpush.Client{VAPIDKey: priv, Subject: subject, HTTPClient: hc}
	}
	return pushNotifier{push: &webpush.Notifier{Store: webpush.NewStore(pool), Client: client, Logger: logger}}, pub
}
