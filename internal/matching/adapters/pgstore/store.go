// Package pgstore implements the matching ports on PostgreSQL: jobs from the global index, results in the
// user's own transaction (row-level security).
package pgstore

//go:generate go tool sqlc generate -f ../../../../sqlc.yaml

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/1xxn/getafjob/internal/matching"
	"github.com/1xxn/getafjob/internal/matching/adapters/pgstore/q"
	"github.com/1xxn/getafjob/internal/platform/db"
)

// Store is the PostgreSQL matching store.
type Store struct{ pool *pgxpool.Pool }

var (
	_ matching.Jobs  = (*Store)(nil)
	_ matching.Store = (*Store)(nil)
)

// New returns a store on pool (application role).
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// OpenJobs returns up to limit open jobs with an ID above afterID, in ID order.
func (s *Store) OpenJobs(ctx context.Context, afterID int64, limit int) ([]matching.Job, error) {
	rows, err := q.New(s.pool).OpenJobsPage(ctx, q.OpenJobsPageParams{ID: afterID, Limit: int32(min(limit, 10_000))}) //nolint:gosec // bounded
	if err != nil {
		return nil, err
	}
	out := make([]matching.Job, len(rows))
	for i, r := range rows {
		out[i] = matching.Job{ID: r.ID, Company: r.Company, Title: r.Title, Location: r.Location,
			Workplace: r.Workplace, Description: r.Description, Posted: date(r.Posted), FirstSeen: r.FirstSeen}
	}
	return out, nil
}

type row struct {
	JobID    int64             `json:"job_id"`
	Score    int               `json:"score"`
	Decision matching.Decision `json:"decision"`
	Reasons  []matching.Reason `json:"reasons"`
}

// SaveMatches upserts results; unchanged rows are not written.
func (s *Store) SaveMatches(ctx context.Context, userID uuid.UUID, rs []matching.Result, at time.Time) error {
	if len(rs) == 0 {
		return nil
	}
	rows := make([]row, len(rs))
	for i, r := range rs {
		rows[i] = row{JobID: r.JobID, Score: r.Score, Decision: r.Decision, Reasons: r.Reasons}
		if rows[i].Reasons == nil {
			rows[i].Reasons = []matching.Reason{}
		}
	}
	data, err := json.Marshal(rows)
	if err != nil {
		return err
	}
	return db.InUserTx(ctx, s.pool, userID, func(tx pgx.Tx) error {
		return q.New(tx).SaveMatches(ctx, q.SaveMatchesParams{UserID: userID, Rules: matching.RulesVersion,
			ComputedAt: at, Results: data})
	})
}

var _ matching.Reader = (*Store)(nil)

func date(d pgtype.Date) time.Time {
	if d.Valid {
		return d.Time
	}
	return time.Time{}
}

// ListMatches returns one page of the user's open jobs and the number of hits.
func (s *Store) ListMatches(ctx context.Context, userID uuid.UUID, f matching.Filter, offset, limit int) (
	items []matching.Item, total int, err error,
) {
	ds := make([]string, len(f.Decisions))
	for i, d := range f.Decisions {
		ds[i] = string(d)
	}
	err = db.InUserTx(ctx, s.pool, userID, func(tx pgx.Tx) error {
		rows, err := q.New(tx).ListMatches(ctx, q.ListMatchesParams{
			UserID:        userID,
			Decisions:     ds,
			Workplace:     f.Workplace,
			Location:      f.Location,
			Source:        f.Source,
			MinSalary:     int32(min(max(f.MinSalary, 0), 10_000_000)),
			FreshnessDays: int32(min(max(f.FreshnessDays, 0), 365)),
			Query:         f.Query,
			ShowBlocked:   f.Blocked,
			SeenAfter:     seenAfter(f.SeenAfter),
			Sort:          f.Sort,
			PageOffset:    int32(min(max(offset, 0), 1_000_000)),
			PageSize:      int32(min(max(limit, 0), 1000)),
		})
		if err != nil {
			return err
		}
		items = make([]matching.Item, len(rows))
		for i, r := range rows {
			items[i] = matching.Item{
				JobID:          r.ID,
				Title:          r.Title,
				Company:        r.Company,
				Location:       r.Location,
				Workplace:      r.Workplace,
				Posted:         date(r.Posted),
				FirstSeen:      r.FirstSeen,
				Score:          int(r.Score),
				Decision:       matching.Decision(r.Decision),
				SalaryMin:      int(r.SalaryMin),
				SalaryMax:      int(r.SalaryMax),
				SalaryCurrency: r.SalaryCurrency,
				Source:         r.Source,
				Blocked:        r.Blocked,
			}
			total = int(r.Total)
		}
		return nil
	})
	return items, total, err
}

// MatchCounts counts the user's open jobs per decision and the number of blocked jobs.
func (s *Store) MatchCounts(ctx context.Context, userID uuid.UUID) (map[matching.Decision]int, int, error) {
	out := map[matching.Decision]int{}
	var blockedCount int64
	err := db.InUserTx(ctx, s.pool, userID, func(tx pgx.Tx) error {
		queries := q.New(tx)
		rows, err := queries.MatchCounts(ctx, userID)
		if err != nil {
			return err
		}
		for _, r := range rows {
			out[matching.Decision(r.Decision)] = int(r.N)
		}
		blockedCount, err = queries.BlockedCount(ctx, userID)
		return err
	})
	return out, int(blockedCount), err
}

// Match returns one job with the user's result.
func (s *Store) Match(ctx context.Context, userID uuid.UUID, jobID int64) (d matching.Detail, err error) {
	err = db.InUserTx(ctx, s.pool, userID, func(tx pgx.Tx) error {
		r, err := q.New(tx).GetMatch(ctx, q.GetMatchParams{UserID: userID, ID: jobID})
		if errors.Is(err, pgx.ErrNoRows) {
			return matching.ErrNotFound
		}
		if err != nil {
			return err
		}
		d = matching.Detail{
			Item: matching.Item{
				JobID:          r.ID,
				Title:          r.Title,
				Company:        r.Company,
				Location:       r.Location,
				Workplace:      r.Workplace,
				Posted:         date(r.Posted),
				FirstSeen:      r.FirstSeen,
				Score:          int(r.Score),
				Decision:       matching.Decision(r.Decision),
				SalaryMin:      int(r.SalaryMin),
				SalaryMax:      int(r.SalaryMax),
				SalaryCurrency: r.SalaryCurrency,
				Source:         r.Source,
				Blocked:        r.Blocked,
			},
			Description: r.Description,
			URL:         r.Url,
			ApplyURL:    r.ApplyUrl,
			Open:        r.Status == "open",
			ComputedAt:  r.ComputedAt,
		}
		return json.Unmarshal(r.Reasons, &d.Reasons)
	})
	return d, err
}

// UserMatches returns all job matches for the user.
func (s *Store) UserMatches(ctx context.Context, userID uuid.UUID) (out []matching.MatchSummary, err error) {
	err = db.InUserTx(ctx, s.pool, userID, func(tx pgx.Tx) error {
		rows, err := q.New(tx).UserMatches(ctx, userID)
		if err != nil {
			return err
		}
		out = make([]matching.MatchSummary, len(rows))
		for i, r := range rows {
			var reasons []matching.Reason
			if len(r.Reasons) > 0 {
				_ = json.Unmarshal(r.Reasons, &reasons)
			}
			out[i] = matching.MatchSummary{
				JobID:      r.ID,
				Title:      r.Title,
				Company:    r.Company,
				Location:   r.Location,
				Workplace:  r.Workplace,
				Score:      int(r.Score),
				Decision:   matching.Decision(r.Decision),
				Reasons:    reasons,
				ComputedAt: r.ComputedAt,
			}
		}
		return nil
	})
	return out, err
}

// ListBlocklist returns all blocked employers and keywords for the user.
func (s *Store) ListBlocklist(ctx context.Context, userID uuid.UUID) ([]matching.BlocklistItem, error) {
	var out []matching.BlocklistItem
	err := db.InUserTx(ctx, s.pool, userID, func(tx pgx.Tx) error {
		rows, err := q.New(tx).ListBlocklist(ctx, userID)
		if err != nil {
			return err
		}
		out = make([]matching.BlocklistItem, len(rows))
		for i, r := range rows {
			out[i] = matching.BlocklistItem{
				ID:        r.ID,
				UserID:    r.UserID,
				Kind:      r.Kind,
				Pattern:   r.Pattern,
				CreatedAt: r.CreatedAt,
			}
		}
		return nil
	})
	return out, err
}

// AddBlocklist adds an employer or keyword to the candidate's blocklist.
func (s *Store) AddBlocklist(ctx context.Context, userID uuid.UUID, kind, pattern string) error {
	return db.InUserTx(ctx, s.pool, userID, func(tx pgx.Tx) error {
		return q.New(tx).InsertBlocklist(ctx, q.InsertBlocklistParams{
			UserID:  userID,
			Kind:    kind,
			Pattern: pattern,
		})
	})
}

// RemoveBlocklist removes a blocklist item by ID.
func (s *Store) RemoveBlocklist(ctx context.Context, userID uuid.UUID, id int64) error {
	return db.InUserTx(ctx, s.pool, userID, func(tx pgx.Tx) error {
		return q.New(tx).DeleteBlocklist(ctx, q.DeleteBlocklistParams{
			UserID: userID,
			ID:     id,
		})
	})
}

// RemoveBlocklistByPattern removes a blocklist item by kind and pattern.
func (s *Store) RemoveBlocklistByPattern(ctx context.Context, userID uuid.UUID, kind, pattern string) error {
	return db.InUserTx(ctx, s.pool, userID, func(tx pgx.Tx) error {
		return q.New(tx).DeleteBlocklistByPattern(ctx, q.DeleteBlocklistByPatternParams{
			UserID: userID,
			Kind:   kind,
			Lower:  pattern,
		})
	})
}

func seenAfter(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// SavedSearches lists the user's saved searches by name.
func (s *Store) SavedSearches(ctx context.Context, userID uuid.UUID) (out []matching.SavedSearch, err error) {
	err = db.InUserTx(ctx, s.pool, userID, func(tx pgx.Tx) error {
		rows, err := q.New(tx).SavedSearches(ctx, userID)
		for _, r := range rows {
			out = append(out, matching.SavedSearch{ID: r.ID, Name: r.Name, Query: r.Query, Alert: r.Alert, CheckedAt: r.CheckedAt})
		}
		return err
	})
	return out, err
}

// CreateSavedSearch saves a search or gives an existing one of the same name the new query.
func (s *Store) CreateSavedSearch(ctx context.Context, userID uuid.UUID, ss matching.SavedSearch) (id int64, err error) {
	err = db.InUserTx(ctx, s.pool, userID, func(tx pgx.Tx) error {
		id, err = q.New(tx).CreateSavedSearch(ctx, q.CreateSavedSearchParams{UserID: userID, Name: ss.Name, Query: ss.Query,
			Alert: ss.Alert, CheckedAt: ss.CheckedAt})
		return err
	})
	return id, err
}

// RenameSavedSearch renames a saved search.
func (s *Store) RenameSavedSearch(ctx context.Context, userID uuid.UUID, id int64, name string) (bool, error) {
	var n int64
	err := db.InUserTx(ctx, s.pool, userID, func(tx pgx.Tx) error {
		var err error
		n, err = q.New(tx).RenameSavedSearch(ctx, q.RenameSavedSearchParams{UserID: userID, ID: id, Name: name})
		return err
	})
	return n > 0, err
}

// SetSavedSearchAlert turns the alert on or off; turning it on starts counting at at.
func (s *Store) SetSavedSearchAlert(ctx context.Context, userID uuid.UUID, id int64, alert bool, at time.Time) (bool, error) {
	var n int64
	err := db.InUserTx(ctx, s.pool, userID, func(tx pgx.Tx) error {
		var err error
		n, err = q.New(tx).SetSavedSearchAlert(ctx, q.SetSavedSearchAlertParams{UserID: userID, ID: id, Alert: alert, CheckedAt: at})
		return err
	})
	return n > 0, err
}

// DeleteSavedSearch removes a saved search.
func (s *Store) DeleteSavedSearch(ctx context.Context, userID uuid.UUID, id int64) (bool, error) {
	var n int64
	err := db.InUserTx(ctx, s.pool, userID, func(tx pgx.Tx) error {
		var err error
		n, err = q.New(tx).DeleteSavedSearch(ctx, q.DeleteSavedSearchParams{UserID: userID, ID: id})
		return err
	})
	return n > 0, err
}

// DueSearchAlerts lists alerts last checked at or before (system role).
func (s *Store) DueSearchAlerts(ctx context.Context, before time.Time) (out []matching.SavedSearch, err error) {
	err = db.InSystemTx(ctx, s.pool, func(tx pgx.Tx) error {
		rows, err := q.New(tx).DueSearchAlerts(ctx, before)
		for _, r := range rows {
			out = append(out, matching.SavedSearch{ID: r.ID, UserID: r.UserID, Name: r.Name, Query: r.Query, Alert: true,
				CheckedAt: r.CheckedAt})
		}
		return err
	})
	return out, err
}

// MarkSearchChecked records when an alert last looked for new jobs.
func (s *Store) MarkSearchChecked(ctx context.Context, userID uuid.UUID, id int64, at time.Time) error {
	return db.InUserTx(ctx, s.pool, userID, func(tx pgx.Tx) error {
		return q.New(tx).MarkSearchChecked(ctx, q.MarkSearchCheckedParams{UserID: userID, ID: id, CheckedAt: at})
	})
}
