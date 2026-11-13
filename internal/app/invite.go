package app

import (
	"context"
	"errors"
	"flag"
	"fmt"

	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/identity/adapters/pgstore"
	"github.com/1xxn/getafjob/internal/platform/config"
	"github.com/1xxn/getafjob/internal/platform/db"
	"github.com/1xxn/getafjob/internal/platform/i18n"
	"github.com/1xxn/getafjob/internal/platform/mail"
)

// identityMailer adapts the SMTP sender to identity.Mailer.
type identityMailer struct{ s *mail.Sender }

func (m identityMailer) Send(ctx context.Context, x identity.Mail) error {
	return m.s.Send(ctx, mail.Message{To: x.To, Subject: x.Subject, Text: x.Text})
}

// newMailer returns nil when no SMTP server is configured.
func newMailer(cfg config.Config) (identity.Mailer, error) {
	if cfg.SMTPURL == "" {
		return nil, nil
	}
	s, err := mail.New(cfg.SMTPURL.Reveal(), cfg.MailFrom, cfg.EgressProxy)
	if err != nil {
		return nil, err
	}
	return identityMailer{s: s}, nil
}

// adminInvite creates an invitation link: getafjob admin invite -email … [-role admin] [-send].
func adminInvite(ctx context.Context, args []string, env Env) int {
	fs := flag.NewFlagSet("admin invite", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	email := fs.String("email", "", "e-mail address of the new user (required)")
	role := fs.String("role", "user", "role: user or admin")
	send := fs.Bool("send", false, "also send the link by mail (needs GETAFJOB_SMTP_URL)")
	locale := fs.String("locale", "en", "language of the mail: en or de")
	if err := fs.Parse(args); err != nil || *email == "" || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(env.Stderr, "usage: getafjob admin invite -email max.mustermann@example.com [-role user|admin] [-send] [-locale en|de]")
		return ExitUsage
	}
	cfg, err := config.Load(env.Config)
	if err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "configuration error:\n%v\n", err)
		return ExitUsage
	}
	mailer, err := newMailer(cfg)
	if err != nil {
		_, _ = fmt.Fprintln(env.Stderr, err)
		return ExitUsage
	}
	if *send && mailer == nil {
		_, _ = fmt.Fprintln(env.Stderr, "-send needs GETAFJOB_SMTP_URL")
		return ExitUsage
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL, cfg.DatabasePassword, "getafjob-admin")
	if err != nil {
		_, _ = fmt.Fprintln(env.Stderr, err)
		return ExitError
	}
	defer pool.Close()
	svc := identity.NewService(pgstore.New(pool), mailer, cfg.PublicURL)
	link, expires, err := svc.Invite(ctx, identity.InviteRequest{Email: *email, Role: identity.Role(*role), Locale: i18n.Parse(*locale), Send: *send})
	switch {
	case errors.Is(err, identity.ErrInvalidEmail), errors.Is(err, identity.ErrInvalidRole):
		_, _ = fmt.Fprintln(env.Stderr, err)
		return ExitUsage
	case errors.Is(err, identity.ErrEmailTaken):
		_, _ = fmt.Fprintln(env.Stderr, "an account with this address exists already")
		return ExitError
	case err != nil:
		_, _ = fmt.Fprintln(env.Stderr, "invite failed:", err)
		return ExitError
	}
	_, _ = fmt.Fprintf(env.Stdout, "%s\nvalid until %s (one use)\n", link, expires.UTC().Format("2006-01-02 15:04 UTC"))
	return ExitOK
}

// adminSignOut ends every session of one account (ASVS 7.4.5).
func adminSignOut(ctx context.Context, args []string, env Env) int {
	fs := flag.NewFlagSet("admin sign-out", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	email := fs.String("email", "", "e-mail address of the account (required)")
	if err := fs.Parse(args); err != nil || *email == "" || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(env.Stderr, "usage: getafjob admin sign-out -email max.mustermann@example.com")
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
	n, err := identity.NewService(pgstore.New(pool), nil, cfg.PublicURL).SignOutUser(ctx, *email)
	if errors.Is(err, identity.ErrNotFound) || errors.Is(err, identity.ErrInvalidEmail) {
		_, _ = fmt.Fprintln(env.Stderr, "no account with this address")
		return ExitError
	}
	if err != nil {
		_, _ = fmt.Fprintln(env.Stderr, "sign-out failed:", err)
		return ExitError
	}
	_, _ = fmt.Fprintf(env.Stdout, "%d sessions ended\n", n)
	return ExitOK
}

// adminDisable disables (cmd "disable") or enables (cmd "enable") the account of an address.
func adminDisable(ctx context.Context, cmd string, args []string, env Env) int {
	fs := flag.NewFlagSet("admin "+cmd, flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	email := fs.String("email", "", "e-mail address of the account (required)")
	if err := fs.Parse(args); err != nil || *email == "" || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(env.Stderr, "usage: getafjob admin "+cmd+" -email max.mustermann@example.com")
		return ExitUsage
	}
	cfg, err := config.Load(env.Config)
	if err != nil {
		_, _ = fmt.Fprintln(env.Stderr, "configuration error:")
		_, _ = fmt.Fprintln(env.Stderr, err)
		return ExitUsage
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL, cfg.DatabasePassword, "getafjob-admin")
	if err != nil {
		_, _ = fmt.Fprintln(env.Stderr, err)
		return ExitError
	}
	defer pool.Close()
	err = identity.NewService(pgstore.New(pool), nil, cfg.PublicURL).SetDisabledByEmail(ctx, *email, cmd == "disable")
	switch {
	case errors.Is(err, identity.ErrNotFound) || errors.Is(err, identity.ErrInvalidEmail):
		_, _ = fmt.Fprintln(env.Stderr, "no account with this address")
		return ExitError
	case errors.Is(err, identity.ErrLastAdmin):
		_, _ = fmt.Fprintln(env.Stderr, "the last admin cannot be disabled")
		return ExitError
	case err != nil:
		_, _ = fmt.Fprintln(env.Stderr, cmd+" failed:", err)
		return ExitError
	}
	_, _ = fmt.Fprintln(env.Stdout, "account "+cmd+"d")
	return ExitOK
}
