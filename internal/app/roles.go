package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-rod/rod/lib/proto"
	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/1xxn/getafjob/internal/discovery"
	discoverystore "github.com/1xxn/getafjob/internal/discovery/adapters/pgstore"
	"github.com/1xxn/getafjob/internal/discovery/adapters/sources"
	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/identity/adapters/pgstore"
	"github.com/1xxn/getafjob/internal/matching"
	matchingstore "github.com/1xxn/getafjob/internal/matching/adapters/pgstore"
	"github.com/1xxn/getafjob/internal/platform/blob"
	"github.com/1xxn/getafjob/internal/platform/config"
	"github.com/1xxn/getafjob/internal/platform/crypto"
	"github.com/1xxn/getafjob/internal/platform/db"
	"github.com/1xxn/getafjob/internal/platform/httpx"
	"github.com/1xxn/getafjob/internal/platform/queue"
	"github.com/1xxn/getafjob/internal/profile"
	profilestore "github.com/1xxn/getafjob/internal/profile/adapters/pgstore"
	"github.com/1xxn/getafjob/internal/profile/cvimport"
	"github.com/1xxn/getafjob/internal/runner/browser"
	"github.com/1xxn/getafjob/internal/web"
	"github.com/1xxn/getafjob/internal/web/apiv1"
)

// maxBody caps request bodies of the web role (uploads get their own, larger limits later).
const maxBody = 1 << 20

// serve: web UI, API v1, SSE and runner gateway.
func serve(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	pool, err := db.Open(ctx, cfg.DatabaseURL, cfg.DatabasePassword, "getafjob-serve")
	if err != nil {
		return err
	}
	defer pool.Close()

	health := httpx.NewHealth(logger)
	health.Add("database", db.Ping(pool))
	health.Add("schema", db.SchemaCurrent(pool))
	mailer, err := newMailer(cfg)
	if err != nil {
		return err
	}
	ids := identity.NewService(pgstore.New(pool), mailer, cfg.PublicURL)
	if err := ids.EnablePasskeys(cfg.PublicURL); err != nil {
		return err
	}

	mux := http.NewServeMux()
	health.Register(mux)
	apiv1.Mount(mux)
	ring, err := crypto.NewKeyring(cfg.MasterKey, cfg.MasterKeyOld)
	if err != nil {
		return fmt.Errorf("GETAFJOB_MASTER_KEY: %w", err)
	}
	env := crypto.NewEnvelope(ring, pool)
	if cfg.BlobURL == "" {
		return errors.New("GETAFJOB_BLOB_URL is not set")
	}
	files, err := blob.Open(cfg.BlobURL, blob.S3Credentials{AccessKey: cfg.BlobAccessKey, SecretKey: cfg.BlobSecretKey})
	if err != nil {
		return err
	}
	pstore := profilestore.New(pool)
	inserter, err := queue.NewInserter(pool, logger)
	if err != nil {
		return err
	}
	profiles := profile.NewService(pstore, env).WithOnChange(func(ctx context.Context, userID uuid.UUID) {
		if _, err := inserter.Insert(context.WithoutCancel(ctx), MatchArgs{UserID: userID}, nil); err != nil {
			logger.WarnContext(ctx, "matching not queued", "err", err)
		}
	})
	docs := profile.NewDocuments(pstore, blob.NewSealed(files, env), env)
	extractor, err := cvimport.NewExtractor()
	if err != nil {
		return err
	}
	(&web.Server{Identity: ids, Logger: logger, Profile: profiles, Documents: docs, Items: profile.NewItems(pstore, env),
		Importer: cvimport.NewImporter(docs, profiles, extractor)}).Register(mux)

	h := httpx.Instrument(httpx.Protect(httpx.Routes(mux), maxBody, cfg.PublicURL.Scheme == "https",
		httpx.Upload{Path: "/documents", Max: web.MaxUpload}, httpx.Upload{Path: "/setup/cv", Max: web.MaxUpload}))
	return httpx.Run(ctx, httpx.NewServer(cfg.HTTPAddr, h, logger), cfg.ShutdownTimeout, logger)
}

// worker: background jobs on River (crawl, identity cleanup); health on the internal address.
func worker(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	pool, err := db.Open(ctx, cfg.DatabaseURL, cfg.DatabasePassword, "getafjob-worker")
	if err != nil {
		return err
	}
	defer pool.Close()

	store := discoverystore.New(pool)
	workers := river.NewWorkers()
	client := sourceClient(cfg)
	metrics, err := newCrawlMetrics()
	if err != nil {
		return err
	}
	ring, err := crypto.NewKeyring(cfg.MasterKey, cfg.MasterKeyOld)
	if err != nil {
		return fmt.Errorf("GETAFJOB_MASTER_KEY: %w", err)
	}
	pstore := profilestore.New(pool)
	mstore := matchingstore.New(pool)
	river.AddWorker(workers, &crawlWorker{svc: discovery.NewService(store), boards: store, states: store, client: client,
		terms: preferenceTerms(pstore, logger), logger: logger, timeout: 90 * time.Minute, metrics: metrics})
	river.AddWorker(workers, &matchWorker{logger: logger, svc: matching.NewService(
		profileCriteria{svc: profile.NewService(pstore, crypto.NewEnvelope(ring, pool)), store: pstore}, mstore, mstore)})
	river.AddWorker(workers, &boardDiscoveryWorker{
		discovery: discovery.NewBoardDiscovery(store, store, sources.NewFinder(client)), logger: logger, metrics: metrics})
	river.AddWorker(workers, &cleanupWorker{ids: identity.NewService(pgstore.New(pool), nil, cfg.PublicURL)})
	jobs, err := queue.NewWorker(pool, queue.Options{Workers: workers, Periodic: periodicJobs(cfg), Logger: logger})
	if err != nil {
		return err
	}
	if err := jobs.Start(ctx); err != nil {
		return err
	}
	defer func() {
		stop, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.ShutdownTimeout)
		defer cancel()
		if err := jobs.Stop(stop); err != nil {
			logger.Warn("queue stop", "err", err)
		}
	}()

	health := httpx.NewHealth(logger)
	health.Add("database", db.Ping(pool))
	health.Add("schema", db.SchemaCurrent(pool))
	return serveHealth(ctx, cfg, health, logger)
}

// runner: browser runner. Phase 0: self-test at start-up (Chromium starts with sandbox, a page opens),
// then ready. No database access; the runner will only talk to the gateway.
func runner(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	health := httpx.NewHealth(logger)
	selfTest := selfTestBrowser(ctx, cfg, logger)
	health.Add("browser", func(context.Context) error { return selfTest })
	return serveHealth(ctx, cfg, health, logger)
}

func selfTestBrowser(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	b, err := browser.Launch(ctx, browser.Options{Bin: cfg.BrowserBin, Proxy: cfg.EgressProxy})
	if err != nil {
		logger.Error("browser self-test failed", "err", err)
		return err
	}
	defer func() {
		if err := b.Close(ctx); err != nil {
			logger.Warn("browser close", "err", err)
		}
	}()
	if browser.HasFlag(b.Args(), "no-sandbox") {
		return errors.New("browser runs without sandbox")
	}
	version, err := b.Version()
	if err != nil {
		logger.Error("browser self-test failed", "err", err)
		return err
	}
	page, err := b.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		logger.Error("browser self-test failed", "err", err)
		return err
	}
	_ = page.Close()
	logger.Info("browser self-test passed", "browser", version, "proxy", cfg.EgressProxy != nil)
	return nil
}

// migrate: apply pending migrations and exit.
func migrate(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	pool, err := db.Open(ctx, cfg.DatabaseURL, cfg.DatabasePassword, "getafjob-migrate")
	if err != nil {
		return err
	}
	defer pool.Close()
	applied, err := db.Migrate(ctx, pool)
	for _, a := range applied {
		logger.Info("migration applied", "file", a)
	}
	if err != nil {
		return err
	}
	riverVersions, err := queue.Migrate(ctx, pool)
	if err != nil {
		return err
	}
	if len(riverVersions) > 0 {
		logger.Info("queue migrations applied", "versions", riverVersions)
	}
	if err := db.SetAppRolePassword(ctx, pool, cfg.DatabaseAppPassword); err != nil {
		return err
	}
	logger.Info("database schema up to date", "applied", len(applied))
	return nil
}

// serveHealth serves only /healthz and /readyz on the internal address (worker, runner).
func serveHealth(ctx context.Context, cfg config.Config, health *httpx.Health, logger *slog.Logger) error {
	mux := http.NewServeMux()
	health.Register(mux)
	return httpx.Run(ctx, httpx.NewServer(cfg.HealthAddr, httpx.SecurityHeaders(mux, false), logger),
		cfg.ShutdownTimeout, logger)
}
