// Package app wires the roles of the binary: serve, worker, runner, migrate, admin.
//
// Every role loads the same configuration (env + *_FILE), builds its logger and runs until the context ends
// (SIGINT/SIGTERM). Two helper commands without a role of their own: healthcheck (for distroless containers
// without a shell) and version.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/1xxn/getafjob/internal/platform/buildinfo"
	"github.com/1xxn/getafjob/internal/platform/config"
	"github.com/1xxn/getafjob/internal/platform/log"
)

// Exit codes.
const (
	ExitOK    = 0
	ExitError = 1
	ExitUsage = 2
)

// Env bundles everything a run gets from outside (tests replace it).
type Env struct {
	Stdout io.Writer
	Stderr io.Writer
	Config config.Source
}

const usage = `getafjob collects jobs, ranks them per user and applies in a real browser.

Usage:
  getafjob <role|command> [arguments]

Roles:
  serve        web UI, API v1, SSE and runner gateway
  worker       background jobs (crawl, matching, rules, inbox, push, cleanup, planning)
  runner       browser runner (Chromium), talks to the gateway only
  migrate      apply database migrations and exit
  admin        administrative commands (admin help)

Commands:
  healthcheck  probe a local health endpoint (container health checks)
  version      print version information

Configuration comes from GETAFJOB_* environment variables; every variable can be
given as GETAFJOB_<NAME>_FILE instead (Docker secrets). See docs/configuration.md.
`

// role is the entry point of a long-running role.
type role func(ctx context.Context, cfg config.Config, logger *slog.Logger) error

// Main runs the command in args and returns the exit code.
func Main(ctx context.Context, args []string, env Env) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(env.Stderr, usage)
		return ExitUsage
	}
	switch args[0] {
	case "serve":
		return run(ctx, env, "serve", serve)
	case "worker":
		return run(ctx, env, "worker", worker)
	case "runner":
		return run(ctx, env, "runner", runner)
	case "migrate":
		return run(ctx, env, "migrate", migrate)
	case "admin":
		return admin(ctx, args[1:], env)
	case "healthcheck":
		return healthcheck(ctx, args[1:], env)
	case "version", "--version":
		_, _ = fmt.Fprintln(env.Stdout, "getafjob", buildinfo.Get().String())
		return ExitOK
	case "help", "-h", "--help":
		_, _ = fmt.Fprint(env.Stdout, usage)
		return ExitOK
	default:
		_, _ = fmt.Fprintf(env.Stderr, "unknown role or command %q\n\n%s", args[0], usage)
		return ExitUsage
	}
}

// run loads the configuration, sets up logging and starts the role.
func run(ctx context.Context, env Env, name string, r role) int {
	cfg, err := config.Load(env.Config)
	if err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "configuration error:\n%v\n", err)
		return ExitUsage
	}
	logger := log.New(env.Stderr, cfg.LogLevel, cfg.LogFormat).With("role", name)
	for _, u := range cfg.Unknown {
		logger.Warn("unknown configuration variable ignored", "name", u)
	}
	bi := buildinfo.Get()
	logger.Info("starting", "version", bi.Version, "commit", bi.Commit, "config", cfg)
	if err := r(ctx, cfg, logger); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("stopped with error", "err", err)
		return ExitError
	}
	logger.Info("stopped")
	return ExitOK
}
