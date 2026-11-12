package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/1xxn/getafjob/internal/application"
	applicationstore "github.com/1xxn/getafjob/internal/application/adapters/pgstore"
	"github.com/1xxn/getafjob/internal/demo"
	"github.com/1xxn/getafjob/internal/discovery"
	discoverystore "github.com/1xxn/getafjob/internal/discovery/adapters/pgstore"
	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/identity/adapters/pgstore"
	"github.com/1xxn/getafjob/internal/matching"
	matchingstore "github.com/1xxn/getafjob/internal/matching/adapters/pgstore"
	"github.com/1xxn/getafjob/internal/platform/blob"
	"github.com/1xxn/getafjob/internal/platform/config"
	"github.com/1xxn/getafjob/internal/platform/crypto"
	"github.com/1xxn/getafjob/internal/platform/db"
	"github.com/1xxn/getafjob/internal/platform/i18n"
	"github.com/1xxn/getafjob/internal/profile"
	profilestore "github.com/1xxn/getafjob/internal/profile/adapters/pgstore"
)

const (
	maxDemoJobs = 50_000
	maxFresh    = 100
)

// adminDemo fills a development database with invented users, profiles and jobs and matches them.
func adminDemo(ctx context.Context, args []string, env Env) int {
	fs := flag.NewFlagSet("admin demo", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	jobs := fs.Int("jobs", 2000, fmt.Sprintf("number of invented postings (at most %d)", maxDemoJobs))
	seed := fs.Uint64("seed", 1, "seed; the same seed gives the same postings")
	force := fs.Bool("force", false, "also run when GETAFJOB_PUBLIC_URL is not a local address")
	fixtures := fs.String("fixtures", "", "point the postings at this fixture site (development stack: http://fixtures:8080)")
	autonomy := fs.String("autonomy", "", "autonomy level of the demo users: suggest, approve or autonomous (empty: keep)")
	fresh := fs.Int("fresh", 0, "also add this many fixture postings, each at a new company, and print their ids (needs -fixtures)")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *jobs < 0 || *jobs > maxDemoJobs || !validLevel(*autonomy) ||
		*fresh < 0 || *fresh > maxFresh || (*fresh > 0 && *fixtures == "") {
		_, _ = fmt.Fprintln(env.Stderr, "usage: getafjob admin demo [-jobs 2000] [-seed 1] [-fixtures URL [-fresh 8]] [-autonomy suggest|approve|autonomous] [-force]")
		return ExitUsage
	}
	level := application.Level(*autonomy)
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
	var site *url.URL
	if *fixtures != "" {
		if site, err = url.Parse(*fixtures); err != nil || (site.Scheme != "http" && site.Scheme != "https") || site.Host == "" {
			_, _ = fmt.Fprintln(env.Stderr, "-fixtures needs an http(s) URL")
			return ExitUsage
		}
	}
	envelope := crypto.NewEnvelope(ring, pool)
	var docs *profile.Documents
	if cfg.BlobURL != "" {
		files, err := blob.Open(cfg.BlobURL, blob.S3Credentials{AccessKey: cfg.BlobAccessKey, SecretKey: cfg.BlobSecretKey})
		if err != nil {
			_, _ = fmt.Fprintln(env.Stderr, "open blob store:", err)
			return ExitError
		}
		docs = profile.NewDocuments(profilestore.New(pool), blob.NewSealed(files, envelope), envelope)
	}
	st, err := seedDemo(ctx, pool, envelope, cfg.PublicURL, *jobs, *seed, time.Now(), site, *fresh, docs, level, env.Stdout)
	if err != nil {
		_, _ = fmt.Fprintln(env.Stderr, "demo failed:", err)
		return ExitError
	}
	_, _ = fmt.Fprintf(env.Stdout, "matched %d jobs for %d users: %d match, %d maybe, %d skip\n", st.Jobs,
		st.Users, st.Decisions[matching.Match], st.Decisions[matching.Maybe], st.Decisions[matching.Skip])
	return ExitOK
}

// validLevel accepts an empty level (keep the users' setting) or one of the autonomy levels.
func validLevel(l string) bool {
	switch application.Level(l) {
	case "", application.LevelSuggest, application.LevelApprove, application.LevelAutonomous:
		return true
	}
	return false
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
	now time.Time, fixtures *url.URL, fresh int, docs *profile.Documents, level application.Level, out io.Writer,
) (matching.Stats, error) {
	apps := application.NewService(applicationstore.New(pool))
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
		// The level is set before the postings arrive, so the planner never queues for a user who should only get
		// suggestions (the end to end stack: journeys start their own runs).
		if level != "" {
			st, err := apps.Settings(ctx, id)
			if err != nil {
				return matching.Stats{}, err
			}
			st.Level = level
			if err := apps.SaveSettings(ctx, id, st); err != nil {
				return matching.Stats{}, err
			}
		}
		if err := demoCV(ctx, docs, id, p); err != nil {
			return matching.Stats{}, fmt.Errorf("%s cv: %w", p.Email, err)
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
	postings := demo.Postings(jobs, seed, now)
	if fixtures != nil {
		demo.PointAt(postings, fixtures)
	}
	for _, p := range postings {
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
	if fresh > 0 {
		for _, p := range demo.Fresh(fresh, strconv.FormatInt(now.UnixNano(), 36), fixtures, now) {
			id, _, err := ds.UpsertPosting(ctx, p, now)
			if err != nil {
				return matching.Stats{}, err
			}
			_, _ = fmt.Fprintf(out, "fixture job %d\n", id)
		}
	}

	ms := matchingstore.New(pool)
	return matching.NewService(profileCriteria{svc: profiles, store: pstore}, ms, ms).Run(ctx, users)
}

// demoCV uploads the invented CV of p unless the user has a CV already; without a blob store it does nothing.
func demoCV(ctx context.Context, docs *profile.Documents, userID uuid.UUID, p demo.Person) error {
	if docs == nil {
		return nil
	}
	list, err := docs.List(ctx, userID)
	if err != nil {
		return err
	}
	for _, d := range list {
		if d.Kind == profile.KindCV {
			return nil
		}
	}
	name := strings.ReplaceAll(p.Profile.Personal.FirstName+"_"+p.Profile.Personal.LastName, " ", "_") + "_CV.pdf"
	_, err = docs.Upload(ctx, userID, profile.KindCV, name, demo.CV(p))
	return err
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
