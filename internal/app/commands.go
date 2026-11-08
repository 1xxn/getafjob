package app

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/1xxn/getafjob/internal/platform/config"
	"github.com/1xxn/getafjob/internal/platform/webpush"
)

const adminUsage = `Usage:
  getafjob admin <command>

Commands:
  boards         company boards for the crawler (admin boards import | list)
  check-config   validate the configuration (prints variable names, never values)
  crawl          queue a crawl now (the worker runs it)
  demo           invented users, profiles and jobs for development (admin demo [-jobs 2000] [-seed 1])
  env            list all configuration variables
  gen-secret     print 32 random bytes (base64url) for passwords and keys
  gen-vapid-key  print a fresh VAPID P-256 key pair for web push notifications
  invite         create a one-time invitation link (admin invite -email … [-role admin] [-send])
  inbox          queue inbox check now (the worker runs it)
  notify         queue daily summary notification (admin notify summary)
  plan           queue planning now (the worker runs it)
  sign-out       end all sessions of one account (admin sign-out -email …)
  rotate-master-key  re-wrap all data keys with GETAFJOB_MASTER_KEY (the previous key in GETAFJOB_MASTER_KEY_OLD)
`

// admin bundles administrative commands.
func admin(ctx context.Context, args []string, env Env) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(env.Stderr, adminUsage)
		return ExitUsage
	}
	switch args[0] {
	case "check-config":
		cfg, err := config.Load(env.Config)
		if err != nil {
			_, _ = fmt.Fprintf(env.Stderr, "configuration error:\n%v\n", err)
			return ExitError
		}
		for _, u := range cfg.Unknown {
			_, _ = fmt.Fprintf(env.Stdout, "warning: unknown variable %s\n", u)
		}
		_, _ = fmt.Fprintln(env.Stdout, "configuration ok")
		return ExitOK
	case "env":
		for _, n := range config.Names() {
			_, _ = fmt.Fprintln(env.Stdout, n)
		}
		return ExitOK
	case "invite":
		return adminInvite(ctx, args[1:], env)
	case "boards":
		return adminBoards(ctx, args[1:], env)
	case "sign-out":
		return adminSignOut(ctx, args[1:], env)
	case "rotate-master-key":
		return adminRotate(ctx, args[1:], env)
	case "demo":
		return adminDemo(ctx, args[1:], env)
	case "crawl":
		return adminCrawl(ctx, args[1:], env)
	case "plan":
		return adminPlan(ctx, args[1:], env)
	case "inbox":
		return adminInbox(ctx, args[1:], env)
	case "notify":
		return adminNotifySummary(ctx, args[1:], env)
	case "gen-secret":
		b := make([]byte, 32)
		_, _ = rand.Read(b) // crypto/rand.Read never returns an error (it crashes instead)
		_, _ = fmt.Fprintln(env.Stdout, base64.RawURLEncoding.EncodeToString(b))
		return ExitOK
	case "gen-vapid-key":
		keys, err := webpush.GenerateKeys()
		if err != nil {
			_, _ = fmt.Fprintf(env.Stderr, "generate vapid key: %v\n", err)
			return ExitError
		}
		_, _ = fmt.Fprintf(env.Stdout, "GETAFJOB_VAPID_KEY=%s\n# Public key (for browsers):\n# %s\n", keys.Private, keys.Public)
		return ExitOK
	case "help", "-h", "--help":
		_, _ = fmt.Fprint(env.Stdout, adminUsage)
		return ExitOK
	default:
		_, _ = fmt.Fprintf(env.Stderr, "unknown admin command %q\n\n%s", args[0], adminUsage)
		return ExitUsage
	}
}

// healthcheck probes an endpoint in its own container: http(s)://… must answer 2xx, tcp://host:port must
// accept connections. Replaces curl/wget in distroless images.
func healthcheck(ctx context.Context, args []string, env Env) int {
	fs := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	timeout := fs.Duration("timeout", 3*time.Second, "timeout")
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 {
		_, _ = fmt.Fprintln(env.Stderr, "usage: getafjob healthcheck [-timeout 3s] <http://127.0.0.1:8080/readyz | tcp://127.0.0.1:4750>")
		return ExitUsage
	}
	u, err := url.Parse(fs.Arg(0))
	if err != nil || u.Host == "" {
		_, _ = fmt.Fprintln(env.Stderr, "healthcheck: invalid target")
		return ExitUsage
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	switch u.Scheme {
	case "tcp":
		conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", u.Host)
		if err != nil {
			_, _ = fmt.Fprintln(env.Stderr, "healthcheck: unreachable")
			return ExitError
		}
		_ = conn.Close()
		return ExitOK
	case "http", "https":
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return ExitUsage
		}
		client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		resp, err := client.Do(req)
		if err != nil {
			_, _ = fmt.Fprintln(env.Stderr, "healthcheck: unreachable")
			return ExitError
		}
		defer func() { _ = resp.Body.Close() }()
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			_, _ = fmt.Fprintf(env.Stderr, "healthcheck: status %d\n", resp.StatusCode)
			return ExitError
		}
		return ExitOK
	default:
		_, _ = fmt.Fprintln(env.Stderr, "healthcheck: scheme must be http, https or tcp")
		return ExitUsage
	}
}
