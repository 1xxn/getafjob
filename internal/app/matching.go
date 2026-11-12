package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/1xxn/getafjob/internal/matching"
	"github.com/1xxn/getafjob/internal/platform/events"
	"github.com/1xxn/getafjob/internal/profile"
	profilestore "github.com/1xxn/getafjob/internal/profile/adapters/pgstore"
)

// MatchArgs matches open jobs for one user, or for all users when UserID is nil (after a crawl).
type MatchArgs struct {
	UserID uuid.UUID `json:"user_id,omitzero"`
}

// Kind implements river.JobArgs.
func (MatchArgs) Kind() string { return "match" }

// InsertOpts: at most one run over all users waits or runs. Runs for one user are never merged, so a profile
// change during a run is not lost.
func (a MatchArgs) InsertOpts() river.InsertOpts {
	if a.UserID != uuid.Nil {
		return river.InsertOpts{MaxAttempts: 3}
	}
	return river.InsertOpts{MaxAttempts: 3, UniqueOpts: river.UniqueOpts{ByArgs: true,
		ByState: []rivertype.JobState{rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateRunning,
			rivertype.JobStateRetryable, rivertype.JobStateScheduled}}}
}

type matchWorker struct {
	river.WorkerDefaults[MatchArgs]
	svc    *matching.Service
	logger *slog.Logger
	notify notifier
}

// Timeout bounds one run over the whole index.
func (w *matchWorker) Timeout(*river.Job[MatchArgs]) time.Duration { return 30 * time.Minute }

func (w *matchWorker) Work(ctx context.Context, job *river.Job[MatchArgs]) error {
	var users []uuid.UUID
	if job.Args.UserID != uuid.Nil {
		users = []uuid.UUID{job.Args.UserID}
	}
	st, err := w.svc.Run(ctx, users)
	if err != nil {
		return err
	}
	if w.notify != nil { // one user's ratings, or (after a crawl) everyone's
		w.notify(ctx, events.TopicMatches, "rated", job.Args.UserID)
	}
	w.logger.InfoContext(ctx, "matching finished", "users", st.Users, "jobs", st.Jobs,
		"match", st.Decisions[matching.Match], "maybe", st.Decisions[matching.Maybe], "skip", st.Decisions[matching.Skip])
	if c, err := river.ClientFromContextSafely[pgx.Tx](ctx); err == nil {
		_, _ = c.Insert(ctx, PlanArgs{}, nil)
	}
	return nil
}

// profileCriteria reads the matching criteria from profiles and preferences.
type profileCriteria struct {
	svc   *profile.Service
	store *profilestore.Store
}

func (p profileCriteria) Users(ctx context.Context) ([]uuid.UUID, error) {
	return p.store.ActiveUsers(ctx)
}

func (p profileCriteria) Criteria(ctx context.Context, userID uuid.UUID) (matching.Criteria, error) {
	snap, err := p.svc.Get(ctx, userID)
	if err != nil {
		return matching.Criteria{}, err
	}
	pr, pf := snap.Preferences, snap.Profile
	c := matching.Criteria{Titles: pr.Titles, ExcludeTitles: pr.ExcludeTitles, Locations: pr.Locations,
		Remote: pr.Remote, RemoteRegions: pr.RemoteRegions, MinSalary: pr.MinSalary, Skills: pf.Skills,
		YearsTotal: pf.Work.YearsTotal}
	for _, l := range pf.Languages {
		c.Languages = append(c.Languages, matching.Language{Name: l.Name, Level: l.Level})
	}
	return c, nil
}

// searchTerms feed the search-based sources: the wanted titles and on-site cities of all users.
type searchTerms struct {
	Queries, Cities []string
}

// Limits keep one crawl's searches bounded however many users there are.
const (
	maxQueries = 25
	maxCities  = 10
)

func preferenceTerms(store *profilestore.Store, logger *slog.Logger) func(context.Context) (searchTerms, error) {
	return func(ctx context.Context) (searchTerms, error) {
		raw, err := store.ActivePreferences(ctx)
		if err != nil {
			return searchTerms{}, err
		}
		var titles, cities [][]string
		for _, b := range raw {
			var p profile.Preferences
			if err := json.Unmarshal(b, &p); err != nil {
				logger.WarnContext(ctx, "preferences skipped", "err", err)
				continue
			}
			titles, cities = append(titles, p.Titles), append(cities, p.Locations)
		}
		return searchTerms{Queries: union(titles, maxQueries), Cities: union(cities, maxCities)}, nil
	}
}

// union merges lists without case-insensitive duplicates, taking the users' entries in turn so every user's
// first choices make it under the limit.
func union(lists [][]string, limit int) []string {
	var out []string
	seen := map[string]bool{}
	for i := 0; len(out) < limit; i++ {
		more := false
		for _, l := range lists {
			if i >= len(l) {
				continue
			}
			more = true
			if k := strings.ToLower(strings.TrimSpace(l[i])); k != "" && !seen[k] && len(out) < limit {
				seen[k] = true
				out = append(out, strings.TrimSpace(l[i]))
			}
		}
		if !more {
			break
		}
	}
	return out
}
