package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/1xxn/getafjob/internal/application"
	applicationstore "github.com/1xxn/getafjob/internal/application/adapters/pgstore"
	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/identity/adapters/pgstore"
	"github.com/1xxn/getafjob/internal/matching"
	matchingstore "github.com/1xxn/getafjob/internal/matching/adapters/pgstore"
	"github.com/1xxn/getafjob/internal/platform/blob"
	"github.com/1xxn/getafjob/internal/platform/config"
	"github.com/1xxn/getafjob/internal/platform/crypto"
	"github.com/1xxn/getafjob/internal/platform/db"
	"github.com/1xxn/getafjob/internal/privacy"
	"github.com/1xxn/getafjob/internal/profile"
	profilestore "github.com/1xxn/getafjob/internal/profile/adapters/pgstore"
)

func newPrivacyServiceFromConfig(cfg config.Config, pool *pgxpool.Pool) (*privacy.Service, error) {
	ring, err := crypto.NewKeyring(cfg.MasterKey, cfg.MasterKeyOld)
	if err != nil {
		return nil, fmt.Errorf("init keyring: %w", err)
	}
	env := crypto.NewEnvelope(ring, pool)

	var files blob.Store
	if cfg.BlobURL != "" {
		st, err := blob.Open(cfg.BlobURL, blob.S3Credentials{AccessKey: cfg.BlobAccessKey, SecretKey: cfg.BlobSecretKey})
		if err != nil {
			return nil, fmt.Errorf("open blob store: %w", err)
		}
		files = st
	}
	var sealedFiles *blob.Sealed
	if files != nil {
		sealedFiles = blob.NewSealed(files, env)
	}

	pstore := profilestore.New(pool)
	ids := identity.NewService(pgstore.New(pool), nil, cfg.PublicURL)
	profiles := profile.NewService(pstore, env)
	items := profile.NewItems(pstore, env)
	docs := profile.NewDocuments(pstore, sealedFiles, env)
	apps := application.NewService(applicationstore.New(pool))
	listings := matching.NewListings(matchingstore.New(pool))

	return privacy.NewService(ids, profiles, items, docs, apps, listings, env, sealedFiles), nil
}

func adminDeleteUser(ctx context.Context, args []string, env Env) int {
	fs := flag.NewFlagSet("admin delete-user", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	email := fs.String("email", "", "account email address to permanently delete")
	if err := fs.Parse(args); err != nil || *email == "" || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(env.Stderr, "usage: getafjob admin delete-user -email max.mustermann@example.com")
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

	ids := identity.NewService(pgstore.New(pool), nil, cfg.PublicURL)
	u, err := ids.UserByEmail(ctx, *email)
	if errors.Is(err, identity.ErrNotFound) || errors.Is(err, identity.ErrInvalidEmail) {
		_, _ = fmt.Fprintln(env.Stderr, "no account with this address")
		return ExitError
	}
	if err != nil {
		_, _ = fmt.Fprintln(env.Stderr, "user lookup failed:", err)
		return ExitError
	}

	ps, err := newPrivacyServiceFromConfig(cfg, pool)
	if err != nil {
		_, _ = fmt.Fprintln(env.Stderr, "init privacy service failed:", err)
		return ExitError
	}

	if err := ps.DeleteAccount(ctx, u.ID); err != nil {
		_, _ = fmt.Fprintln(env.Stderr, "delete account failed:", err)
		return ExitError
	}

	_, _ = fmt.Fprintf(env.Stdout, "account %s (%s) and all personal data permanently deleted\n", *email, u.ID)
	return ExitOK
}

func adminExportUser(ctx context.Context, args []string, env Env) int {
	fs := flag.NewFlagSet("admin export-user", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	email := fs.String("email", "", "account email address to export")
	outFile := fs.String("out", "", "target output file (ZIP)")
	if err := fs.Parse(args); err != nil || *email == "" || *outFile == "" || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(env.Stderr, "usage: getafjob admin export-user -email max.mustermann@example.com -out export.zip")
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

	ids := identity.NewService(pgstore.New(pool), nil, cfg.PublicURL)
	u, err := ids.UserByEmail(ctx, *email)
	if errors.Is(err, identity.ErrNotFound) || errors.Is(err, identity.ErrInvalidEmail) {
		_, _ = fmt.Fprintln(env.Stderr, "no account with this address")
		return ExitError
	}
	if err != nil {
		_, _ = fmt.Fprintln(env.Stderr, "user lookup failed:", err)
		return ExitError
	}

	ps, err := newPrivacyServiceFromConfig(cfg, pool)
	if err != nil {
		_, _ = fmt.Fprintln(env.Stderr, "init privacy service failed:", err)
		return ExitError
	}

	f, err := os.Create(*outFile)
	if err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "create file %s: %v\n", *outFile, err)
		return ExitError
	}
	defer f.Close()

	if err := ps.Export(ctx, u.ID, f); err != nil {
		_, _ = fmt.Fprintln(env.Stderr, "export failed:", err)
		return ExitError
	}

	_, _ = fmt.Fprintf(env.Stdout, "exported account %s data to %s\n", *email, *outFile)
	return ExitOK
}

func adminCleanup(ctx context.Context, _ []string, env Env) int {
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

	ps, err := newPrivacyServiceFromConfig(cfg, pool)
	if err != nil {
		_, _ = fmt.Fprintln(env.Stderr, "init privacy service failed:", err)
		return ExitError
	}

	if err := ps.Cleanup(ctx); err != nil {
		_, _ = fmt.Fprintln(env.Stderr, "cleanup failed:", err)
		return ExitError
	}

	_, _ = fmt.Fprintln(env.Stdout, "retention cleanup completed")
	return ExitOK
}
