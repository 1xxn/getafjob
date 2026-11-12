package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/1xxn/getafjob/internal/demo"
	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/platform/config"
	"github.com/1xxn/getafjob/internal/platform/db"
	"github.com/1xxn/getafjob/internal/platform/loadtest"
	"github.com/1xxn/getafjob/internal/web"
)

// adminLoadTest runs concurrent load testing against a running getafjob instance.
func adminLoadTest(ctx context.Context, args []string, env Env) int {
	fs := flag.NewFlagSet("admin load-test", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)

	var (
		rawURL   string
		users    int
		duration time.Duration
		requests int
		slo      time.Duration
		email    string
		cookie   string
		insecure bool
	)

	fs.StringVar(&rawURL, "url", "", "target base URL (defaults to GETAFJOB_PUBLIC_URL or http://localhost:8080)")
	fs.IntVar(&users, "users", 5, "concurrent virtual users")
	fs.IntVar(&users, "c", 5, "concurrent virtual users (shorthand)")
	fs.DurationVar(&duration, "duration", 10*time.Second, "test run duration")
	fs.DurationVar(&duration, "d", 10*time.Second, "test run duration (shorthand)")
	fs.IntVar(&requests, "requests", 0, "maximum total requests (0 for duration-based)")
	fs.IntVar(&requests, "n", 0, "maximum total requests (shorthand)")
	fs.DurationVar(&slo, "slo", loadtest.DefaultTargetSLO, "p95 latency SLO threshold")
	fs.StringVar(&email, "email", "", "user email to authenticate as via database session")
	fs.StringVar(&cookie, "cookie", "", "session cookie value (__Host-gaj_session)")
	fs.BoolVar(&insecure, "insecure", false, "skip TLS certificate validation (e.g. for localhost HTTPS)")
	fs.BoolVar(&insecure, "k", false, "skip TLS certificate validation (shorthand)")

	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}

	cfg, _ := config.Load(env.Config)
	if rawURL == "" {
		if cfg.PublicURL != nil && cfg.PublicURL.String() != "" {
			rawURL = cfg.PublicURL.String()
		} else {
			rawURL = "http://localhost:8080"
		}
	}

	var sessionCookie *http.Cookie
	var sampleJobID int64

	// If a direct cookie was passed, use it
	if cookie != "" {
		sessionCookie = &http.Cookie{Name: web.SessionCookie, Value: cookie, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode}
	} else if cfg.DatabaseURL != "" {
		// Attempt to create a valid session from the database for the demo/target user
		targetEmail := email
		if targetEmail == "" && len(demo.People()) > 0 {
			targetEmail = demo.People()[0].Email
		}

		if targetEmail != "" {
			pool, err := db.Open(ctx, cfg.DatabaseURL, cfg.DatabasePassword, "getafjob-loadtest")
			if err == nil {
				defer pool.Close()
				var userID uuid.UUID
				now := time.Now()
				err := db.InSystemTx(ctx, pool, func(tx pgx.Tx) error {
					if err := tx.QueryRow(ctx, "SELECT id FROM users WHERE email = $1", targetEmail).Scan(&userID); err != nil {
						return err
					}
					// Query for a sample job ID
					_ = tx.QueryRow(ctx, "SELECT id FROM jobs ORDER BY id DESC LIMIT 1").Scan(&sampleJobID)

					st, sh := identity.NewToken()
					_, err := tx.Exec(ctx,
						"INSERT INTO sessions (token_hash, user_id, method, created_at, expires_at) VALUES ($1, $2, $3, $4, $5)",
						sh[:], userID, string(identity.MethodMagicLink), now, now.Add(24*time.Hour),
					)
					if err != nil {
						return err
					}
					sessionCookie = &http.Cookie{Name: web.SessionCookie, Value: string(st), Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode}
					return nil
				})
				if err != nil && !errors.Is(err, pgx.ErrNoRows) {
					_, _ = fmt.Fprintf(env.Stderr, "warning: could not seed session from database: %v\n", err)
				}
			}
		}
	}

	var paths []loadtest.WeightedPath
	if sessionCookie != nil {
		paths = []loadtest.WeightedPath{
			{Path: "/", Weight: 15},
			{Path: "/jobs", Weight: 30},
			{Path: "/jobs?q=engineer", Weight: 20},
			{Path: "/jobs?workplace=remote", Weight: 15},
			{Path: "/jobs?decision=all&page=2", Weight: 15},
			{Path: "/applications", Weight: 10},
			{Path: "/pipeline", Weight: 10},
			{Path: "/needs-input", Weight: 5},
			{Path: "/approvals", Weight: 5},
			{Path: "/live", Weight: 5},
			{Path: "/activity", Weight: 5},
			{Path: "/notifications/bell", Weight: 15},
			{Path: "/profile", Weight: 10},
			{Path: "/settings/search", Weight: 5},
			{Path: "/healthz", Weight: 5},
		}
		if sampleJobID > 0 {
			paths = append(paths, loadtest.WeightedPath{
				Path:   "/jobs/" + strconv.FormatInt(sampleJobID, 10),
				Weight: 15,
			})
		}
	} else {
		paths = []loadtest.WeightedPath{
			{Path: "/", Weight: 40},
			{Path: "/login", Weight: 30},
			{Path: "/healthz", Weight: 15},
			{Path: "/livez", Weight: 15},
		}
	}

	var cookies []*http.Cookie
	if sessionCookie != nil {
		cookies = append(cookies, sessionCookie)
	}

	loadCfg := loadtest.Config{
		BaseURL:     rawURL,
		Paths:       paths,
		Cookies:     cookies,
		Concurrency: users,
		Duration:    duration,
		MaxRequests: requests,
		TargetSLO:   slo,
		Insecure:    insecure,
	}

	_, _ = fmt.Fprintf(env.Stdout, "Starting load test against %s (%d workers, %s)...\n", rawURL, users, duration)
	stats, err := loadtest.Run(ctx, loadCfg)
	if err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "load test execution error: %v\n", err)
		return ExitError
	}

	_, _ = fmt.Fprintln(env.Stdout, stats.Summary())

	if !stats.PassedSLO {
		_, _ = fmt.Fprintf(env.Stderr, "SLO check failed: p95 latency %s exceeded threshold %s or error rate was >= 1%%\n",
			stats.P95.Round(100*time.Microsecond), stats.TargetSLO)
		return ExitError
	}

	return ExitOK
}
