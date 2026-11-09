package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/go-rod/rod/lib/proto"
	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/1xxn/getafjob/internal/ai"
	aianthropic "github.com/1xxn/getafjob/internal/ai/adapters/anthropic"
	aicrypto "github.com/1xxn/getafjob/internal/ai/adapters/crypto"
	aistore "github.com/1xxn/getafjob/internal/ai/adapters/pgstore"
	"github.com/1xxn/getafjob/internal/application"
	applicationstore "github.com/1xxn/getafjob/internal/application/adapters/pgstore"
	"github.com/1xxn/getafjob/internal/discovery"
	discoverystore "github.com/1xxn/getafjob/internal/discovery/adapters/pgstore"
	"github.com/1xxn/getafjob/internal/discovery/adapters/sources"
	"github.com/1xxn/getafjob/internal/gateway"
	runnerv1 "github.com/1xxn/getafjob/internal/gen/proto/runner/v1"
	"github.com/1xxn/getafjob/internal/gen/proto/runner/v1/runnerv1connect"
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
	"github.com/1xxn/getafjob/internal/platform/webpush"
	"github.com/1xxn/getafjob/internal/portals"
	portalscrypto "github.com/1xxn/getafjob/internal/portals/adapters/crypto"
	portalsstore "github.com/1xxn/getafjob/internal/portals/adapters/pgstore"
	"github.com/1xxn/getafjob/internal/privacy"
	"github.com/1xxn/getafjob/internal/profile"
	profilestore "github.com/1xxn/getafjob/internal/profile/adapters/pgstore"
	"github.com/1xxn/getafjob/internal/profile/cvimport"
	"github.com/1xxn/getafjob/internal/runner/agent"
	"github.com/1xxn/getafjob/internal/runner/ats"
	"github.com/1xxn/getafjob/internal/runner/browser"
	"github.com/1xxn/getafjob/internal/tracking"
	trackingcrypto "github.com/1xxn/getafjob/internal/tracking/adapters/crypto"
	trackingimap "github.com/1xxn/getafjob/internal/tracking/adapters/imap"
	trackingstore "github.com/1xxn/getafjob/internal/tracking/adapters/pgstore"
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
	sealedFiles := blob.NewSealed(files, env)
	docs := profile.NewDocuments(pstore, sealedFiles, env)
	extractor, err := cvimport.NewExtractor()
	if err != nil {
		return err
	}
	notifySvc, _, vapidPub := newNotifyService(pool, cfg, logger)
	appNotifier := newNotifyAppNotifier(notifySvc)
	astore := applicationstore.New(pool)
	apps := application.NewService(astore)
	portalsStore := portalsstore.New(pool)
	portalsCipher := portalscrypto.NewCipher(env)
	portalsSvc := portals.NewService(portalsStore, portalsCipher, newAutonomyAdapter(apps), cfg.EasyApplyEnabled)
	aiStore := aistore.New(pool)
	aiCipher := aicrypto.NewCipher(env)
	aiClient := aianthropic.NewClient()
	aiSvc := ai.NewService(aiStore, aiCipher, aiClient, cfg.AIEnabled, cfg.AnthropicAPIKey.Reveal(), cfg.AnthropicModel, cfg.AIBudgetPerUserCents)
	var gw *gateway.Server
	if cfg.RunnerSecret != "" {
		atsMetrics, err := ats.NewTracker()
		if err != nil {
			return err
		}
		gw = &gateway.Server{Runs: application.NewRuns(apps, astore), Apps: apps, Shots: sealedFiles,
			Secret: cfg.RunnerSecret.Reveal(), Logger: logger, Notify: appNotifier.YourTurn, ATSMetrics: atsMetrics,
			Portals: portalsSvc,
			Data:    runData{profiles: profiles, items: profile.NewItems(pstore, env), docs: docs, stations: profile.NewStations(pstore, env), jobs: astore, portals: portalsSvc, now: time.Now}}
	}

	tstore := trackingstore.New(pool)
	tcipher := trackingcrypto.NewCipher(env)
	trackingSvc := tracking.NewService(tstore, trackingimap.NewClient(), tcipher, newAppTransitionAdapter(apps))
	items := profile.NewItems(pstore, env)
	assistSvc := newAssistService(pool, profiles, items, docs, apps, logger)
	apiv1.Mount(mux, assistSvc)

	listings := matching.NewListings(matchingstore.New(pool))
	privacySvc := privacy.NewService(ids, profiles, items, docs, apps, listings, env, sealedFiles)
	stations := profile.NewStations(pstore, env)
	(&web.Server{Identity: ids, Logger: logger, Profile: profiles, Documents: docs, Items: items, Stations: stations,
		Importer: cvimport.NewImporter(docs, profiles, stations, extractor), Jobs: listings,
		PushStore: webpush.NewStore(pool), VAPIDPublicKey: vapidPub, Apps: apps, Tracking: trackingSvc, Notify: notifySvc, Assist: assistSvc, Portals: portalsSvc, AI: aiSvc, Privacy: privacySvc, Shots: sealedFiles, Handoffs: gw}).Register(mux)

	h := httpx.Instrument(httpx.Protect(httpx.Routes(mux), maxBody, cfg.PublicURL.Scheme == "https",
		httpx.Upload{Path: "/documents", Max: web.MaxUpload}, httpx.Upload{Path: "/setup/cv", Max: web.MaxUpload}))

	// UI and gateway stop together: when one fails, the other is shut down.
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	gatewayDone := make(chan error, 1)
	if gw == nil {
		logger.Info("runner gateway off: GETAFJOB_RUNNER_SECRET is not set")
		close(gatewayDone)
	} else {
		gmux := http.NewServeMux()
		gw.Mount(gmux)
		go func() {
			err := httpx.Run(ctx, httpx.NewServer(cfg.GatewayAddr, httpx.Instrument(gmux), logger), cfg.ShutdownTimeout, logger)
			stop()
			gatewayDone <- err
		}()
	}
	err = httpx.Run(ctx, httpx.NewServer(cfg.HTTPAddr, h, logger), cfg.ShutdownTimeout, logger)
	stop()
	return errors.Join(err, <-gatewayDone)
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
	env := crypto.NewEnvelope(ring, pool)
	var sealed *blob.Sealed
	if cfg.BlobURL != "" {
		files, err := blob.Open(cfg.BlobURL, blob.S3Credentials{AccessKey: cfg.BlobAccessKey, SecretKey: cfg.BlobSecretKey})
		if err != nil {
			return err
		}
		sealed = blob.NewSealed(files, env)
	}
	pstore := profilestore.New(pool)
	mstore := matchingstore.New(pool)
	river.AddWorker(workers, &crawlWorker{svc: discovery.NewService(store), boards: store, states: store, client: client,
		terms: preferenceTerms(pstore, logger), logger: logger, timeout: 90 * time.Minute, metrics: metrics})
	river.AddWorker(workers, &matchWorker{logger: logger, svc: matching.NewService(
		profileCriteria{svc: profile.NewService(pstore, env), store: pstore}, mstore, mstore)})
	river.AddWorker(workers, &boardDiscoveryWorker{
		discovery: discovery.NewBoardDiscovery(store, store, sources.NewFinder(client)), logger: logger, metrics: metrics})
	apps := application.NewService(applicationstore.New(pool))
	ids := identity.NewService(pgstore.New(pool), nil, cfg.PublicURL)
	docs := profile.NewDocuments(pstore, sealed, env)
	items := profile.NewItems(pstore, env)
	privacySvc := privacy.NewService(ids, profile.NewService(pstore, env), items, docs, apps, nil, env, sealed)
	river.AddWorker(workers, &cleanupWorker{privacy: privacySvc})
	river.AddWorker(workers, &recoverWorker{svc: apps, logger: logger})
	notifySvc, _, _ := newNotifyService(pool, cfg, logger)
	appNotifier := newNotifyAppNotifier(notifySvc)
	river.AddWorker(workers, &handoffWorker{svc: apps, notifier: appNotifier, logger: logger})
	river.AddWorker(workers, &dailySummaryWorker{svc: notifySvc, logger: logger})
	river.AddWorker(workers, &planWorker{svc: apps, logger: logger})
	tstore := trackingstore.New(pool)
	tcipher := trackingcrypto.NewCipher(env)
	trackingSvc := tracking.NewService(tstore, trackingimap.NewClient(), tcipher, newAppTransitionAdapter(apps))
	river.AddWorker(workers, &inboxWorker{svc: trackingSvc, logger: logger})
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
	if cfg.GatewayURL == nil || cfg.RunnerSecret == "" || selfTest != nil {
		logger.Info("runner leases no work: gateway URL or runner secret missing, or the browser self-test failed")
		return serveHealth(ctx, cfg, health, logger)
	}
	// The gateway sits on the internal network; everything else the runner loads goes through the egress proxy.
	transport := connecthttp.NewTransport(&http.Client{Timeout: 2 * time.Minute}, strings.TrimSuffix(cfg.GatewayURL.String(), "/"),
		connecthttp.WithReadMaxBytes(gateway.MaxMessage), connecthttp.WithSendMaxBytes(gateway.MaxMessage))
	client := runnerv1connect.NewRunnerServiceClient(connect.NewClient(transport))
	kind := runnerv1.RunnerKind_RUNNER_KIND_SERVER
	if cfg.RunnerKind == "home" {
		kind = runnerv1.RunnerKind_RUNNER_KIND_HOME
	}
	a := &agent.Agent{Client: client, Secret: cfg.RunnerSecret.Reveal(), Logger: logger,
		Kind: kind, Browser: browser.Options{Bin: cfg.BrowserBin, Proxy: cfg.EgressProxy}}
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx); stop() }()
	err := serveHealth(ctx, cfg, health, logger)
	stop()
	return errors.Join(err, <-done)
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
