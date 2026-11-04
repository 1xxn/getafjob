package app

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-rod/rod/lib/proto"

	"github.com/1xxn/getafjob/internal/platform/config"
	"github.com/1xxn/getafjob/internal/platform/db"
	"github.com/1xxn/getafjob/internal/platform/httpx"
	"github.com/1xxn/getafjob/internal/runner/browser"
	"github.com/1xxn/getafjob/internal/web/apiv1"
)

// maxBody caps request bodies of the web role (uploads get their own, larger limits later).
const maxBody = 1 << 20

// serve: web UI, API v1, SSE and runner gateway. Phase 0: health endpoints only.
func serve(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	pool, err := db.Open(ctx, cfg.DatabaseURL, cfg.DatabasePassword, "getafjob-serve")
	if err != nil {
		return err
	}
	defer pool.Close()

	health := httpx.NewHealth(logger)
	health.Add("database", db.Ping(pool))
	health.Add("schema", db.SchemaCurrent(pool))
	mux := http.NewServeMux()
	health.Register(mux)
	apiv1.Mount(mux)

	h := httpx.Protect(mux, maxBody, cfg.PublicURL.Scheme == "https")
	return httpx.Run(ctx, httpx.NewServer(cfg.HTTPAddr, h, logger), cfg.ShutdownTimeout, logger)
}

// worker: background jobs (River). Phase 0: database reachable, health on the internal address.
func worker(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	pool, err := db.Open(ctx, cfg.DatabaseURL, cfg.DatabasePassword, "getafjob-worker")
	if err != nil {
		return err
	}
	defer pool.Close()

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
