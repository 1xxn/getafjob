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
