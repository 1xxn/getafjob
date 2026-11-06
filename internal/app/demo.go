package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/1xxn/getafjob/internal/demo"
	"github.com/1xxn/getafjob/internal/discovery"
	discoverystore "github.com/1xxn/getafjob/internal/discovery/adapters/pgstore"
	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/identity/adapters/pgstore"
	"github.com/1xxn/getafjob/internal/matching"
	matchingstore "github.com/1xxn/getafjob/internal/matching/adapters/pgstore"
	"github.com/1xxn/getafjob/internal/platform/config"
	"github.com/1xxn/getafjob/internal/platform/crypto"
	"github.com/1xxn/getafjob/internal/platform/db"
	"github.com/1xxn/getafjob/internal/platform/i18n"
	"github.com/1xxn/getafjob/internal/profile"
	profilestore "github.com/1xxn/getafjob/internal/profile/adapters/pgstore"
)

const maxDemoJobs = 50_000

// adminDemo fills a development database with invented users, profiles and jobs and matches them.
func adminDemo(ctx context.Context, args []string, env Env) int {
	fs := flag.NewFlagSet("admin demo", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	jobs := fs.Int("jobs", 2000, fmt.Sprintf("number of invented postings (at most %d)", maxDemoJobs))
	seed := fs.Uint64("seed", 1, "seed; the same seed gives the same postings")
	force := fs.Bool("force", false, "also run when GETAFJOB_PUBLIC_URL is not a local address")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *jobs < 0 || *jobs > maxDemoJobs {
		_, _ = fmt.Fprintln(env.Stderr, "usage: getafjob admin demo [-jobs 2000] [-seed 1] [-force]")
		return ExitUsage
	}
	cfg, err := config.Load(env.Config)
	if err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "configuration error:\n%v\n", err)
		return ExitUsage
	}
	if !*force && !localURL(cfg.PublicURL) {
		_, _ = fmt.Fprintln(env.Stderr, "refusing demo data: GETAFJOB_PUBLIC_URL is not local (use -force for a test system)")
		return ExitUsage
	}
	ring, err := crypto.NewKeyring(cfg.MasterKey, cfg.MasterKeyOld)
	if err != nil {
		_, _ = fmt.Fprintln(env.Stderr, "GETAFJOB_MASTER_KEY:", err)
		return ExitUsage
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL, cfg.DatabasePassword, "getafjob-admin")
	if err != nil {
		_, _ = fmt.Fprintln(env.Stderr, err)
		return ExitError
	}
	defer pool.Close()
	st, err := seedDemo(ctx, pool, crypto.NewEnvelope(ring, pool), cfg.PublicURL, *jobs, *seed, time.Now(), env.Stdout)
	if err != nil {
		_, _ = fmt.Fprintln(env.Stderr, "demo failed:", err)
		return ExitError
	}
	_, _ = fmt.Fprintf(env.Stdout, "matched %d jobs for %d users: %d match, %d maybe, %d skip\n", st.Jobs,
		st.Users, st.Decisions[matching.Match], st.Decisions[matching.Maybe], st.Decisions[matching.Skip])
	return ExitOK
}

// localURL reports a development address: localhost, loopback, *.localhost or *.test.
func localURL(u *url.URL) bool {
	if u == nil {
		return false
	}
	h := u.Hostname()
	return h == "localhost" || h == "127.0.0.1" || h == "::1" || strings.HasSuffix(h, ".localhost") ||
		strings.HasSuffix(h, ".test")
}

// seedDemo creates the demo users (or reuses them), saves their profiles, upserts the postings and matches them.
func seedDemo(ctx context.Context, pool *pgxpool.Pool, sealer profile.Sealer, public *url.URL, jobs int, seed uint64,
	now time.Time, out io.Writer,
) (matching.Stats, error) {
	ids := identity.NewService(pgstore.New(pool), nil, public)
	pstore := profilestore.New(pool)
	profiles := profile.NewService(pstore, sealer)
	var users []uuid.UUID
	for _, p := range demo.People() {
		id, err := demoUser(ctx, ids, p)
		if err != nil {
			return matching.Stats{}, fmt.Errorf("%s: %w", p.Email, err)
		}
		snap, err := profiles.Get(ctx, id)
		if err != nil {
			return matching.Stats{}, err
		}
		if err := profiles.SaveProfile(ctx, id, p.Profile, snap.Version); err != nil {
			return matching.Stats{}, err
		}
		if err := profiles.SavePreferences(ctx, id, p.Preferences); err != nil {
			return matching.Stats{}, err
		}
		users = append(users, id)
		_, _ = fmt.Fprintf(out, "user %s (sign in with a mail link)\n", p.Email)
	}

	ds := discoverystore.New(pool)
	run, err := ds.StartRun(ctx, []string{demo.Source}, now)
	if err != nil {
		return matching.Stats{}, err
	}
	stats := discovery.Stats{Changes: map[discovery.Change]int{}}
	for _, p := range demo.Postings(jobs, seed, now) {
		change, _, err := ds.Upsert(ctx, run, p, now)
		if err != nil {
			return matching.Stats{}, err
		}
		stats.Fetched++
		stats.Changes[change]++
	}
	if err := ds.FinishRun(ctx, run, stats, nil, now); err != nil {
		return matching.Stats{}, err
	}
	_, _ = fmt.Fprintf(out, "postings: %d (%d new)\n", stats.Fetched, stats.Changes[discovery.ChangeNew])

	ms := matchingstore.New(pool)
	return matching.NewService(profileCriteria{svc: profiles, store: pstore}, ms, ms).Run(ctx, users)
}

// demoUser returns the account of p, creating it through an invitation when it does not exist yet.
func demoUser(ctx context.Context, ids *identity.Service, p demo.Person) (uuid.UUID, error) {
	u, err := ids.UserByEmail(ctx, p.Email)
	if err == nil {
		return u.ID, nil
	}
	if !errors.Is(err, identity.ErrNotFound) {
		return uuid.Nil, err
	}
	link, _, err := ids.Invite(ctx, identity.InviteRequest{Email: p.Email, Role: identity.RoleUser})
	if err != nil {
		return uuid.Nil, err
	}
	u, token, err := ids.AcceptInvitation(ctx, link[strings.LastIndex(link, "/")+1:], p.Name, i18n.Parse("en"))
	if err != nil {
		return uuid.Nil, err
	}
	return u.ID, ids.SignOut(ctx, string(token), u.ID) // no session is left behind
}
