package app

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/1xxn/getafjob/internal/platform/config"
	"github.com/1xxn/getafjob/internal/platform/db"
	"github.com/1xxn/getafjob/internal/web"
)

// probeTimeout bounds the egress proxy check, so the system page stays fast when the proxy is down.
const probeTimeout = 2 * time.Second

// systemFacts reads what the admin system page shows: the database, the configured channels, the last backup and
// whether the egress proxy answers.
type systemFacts struct {
	pool  *pgxpool.Pool
	cfg   config.Config
	probe func(ctx context.Context, proxy *url.URL) bool // nil: probeProxy
}

func (f systemFacts) Facts(ctx context.Context) (web.SystemFacts, error) {
	out := web.SystemFacts{EgressProxy: f.cfg.EgressProxy != nil, Mail: f.cfg.SMTPURL != "", PublicURL: f.cfg.PublicURL.String()}
	err := db.InSystemTx(ctx, f.pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, "SELECT current_setting('server_version')").Scan(&out.Postgres); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, "SELECT coalesce(max(version_id), 0) FROM goose_db_version WHERE is_applied").Scan(&out.Migration); err != nil {
			return err
		}
		var at time.Time
		var bytes int64
		err := tx.QueryRow(ctx, "SELECT finished_at, bytes FROM backups ORDER BY finished_at DESC, id DESC LIMIT 1").Scan(&at, &bytes)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return nil
		case err != nil:
			return err
		}
		out.LastBackup, out.LastBackupBytes = at, bytes
		return nil
	})
	if err != nil || f.cfg.EgressProxy == nil {
		return out, err
	}
	probe := f.probe
	if probe == nil {
		probe = probeProxy
	}
	reachable := probe(ctx, f.cfg.EgressProxy)
	out.EgressReachable = &reachable
	return out, nil
}

// probeProxy sends a CONNECT to the egress proxy and reports whether it answers with any HTTP status: a refusal
// (the target is not allowed) still shows that the proxy runs. No request leaves the proxy for an allowed host.
func probeProxy(ctx context.Context, proxy *url.URL) bool {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	host := proxy.Host
	if proxy.Port() == "" {
		host = net.JoinHostPort(proxy.Hostname(), "80")
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", host)
	if err != nil {
		return false
	}
	defer func() { _ = conn.Close() }()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	// A reserved name (RFC 2606): never a real destination, whatever the allow list says.
	if _, err := fmt.Fprint(conn, "CONNECT probe.invalid:443 HTTP/1.1\r\nHost: probe.invalid:443\r\n\r\n"); err != nil {
		return false
	}
	res, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodConnect})
	if err != nil {
		return false
	}
	_ = res.Body.Close()
	return true
}

// adminBackupDone records a finished backup for the system page; deploy/backup.sh calls it after the snapshot.
func adminBackupDone(ctx context.Context, args []string, env Env) int {
	fs := flag.NewFlagSet("admin backup-done", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	bytes := fs.Int64("bytes", 0, "size of the backed up data in bytes")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *bytes < 0 {
		_, _ = fmt.Fprintln(env.Stderr, "usage: getafjob admin backup-done -bytes N")
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
	if err := recordBackup(ctx, pool, *bytes); err != nil {
		_, _ = fmt.Fprintln(env.Stderr, "record backup:", err)
		return ExitError
	}
	_, _ = fmt.Fprintln(env.Stdout, "backup recorded")
	return ExitOK
}

func recordBackup(ctx context.Context, pool *pgxpool.Pool, bytes int64) error {
	return db.InSystemTx(ctx, pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "INSERT INTO backups (bytes) VALUES ($1)", bytes)
		return err
	})
}
