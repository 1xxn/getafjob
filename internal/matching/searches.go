package matching

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// AlertInterval is how often at most a saved search alerts its user.
const AlertInterval = 24 * time.Hour

// ErrInvalidSearch rejects a saved search without a name, with a too long name or query.
var ErrInvalidSearch = errors.New("invalid saved search")

// SavedSearch is a named job list filter; with Alert the user hears about new jobs once a day at most.
type SavedSearch struct {
	ID        int64
	UserID    uuid.UUID // set for due alerts (system role)
	Name      string
	Query     string // the job list's URL query, without "?"
	Alert     bool
	CheckedAt time.Time // jobs first seen after it are new
}

// Searches stores saved searches per user (row-level security).
type Searches interface {
	SavedSearches(ctx context.Context, userID uuid.UUID) ([]SavedSearch, error)
	// CreateSavedSearch saves a search; a search of the same name gets the new query.
	CreateSavedSearch(ctx context.Context, userID uuid.UUID, s SavedSearch) (int64, error)
	RenameSavedSearch(ctx context.Context, userID uuid.UUID, id int64, name string) (bool, error)
	SetSavedSearchAlert(ctx context.Context, userID uuid.UUID, id int64, alert bool, at time.Time) (bool, error)
	DeleteSavedSearch(ctx context.Context, userID uuid.UUID, id int64) (bool, error)
	// DueSearchAlerts lists searches with an alert last checked at or before (system role).
	DueSearchAlerts(ctx context.Context, before time.Time) ([]SavedSearch, error)
	MarkSearchChecked(ctx context.Context, userID uuid.UUID, id int64, at time.Time) error
}

// cleanName trims a search name and checks its length.
func cleanName(name string) (string, error) {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" || utf8.RuneCountInString(name) > 100 {
		return "", ErrInvalidSearch
	}
	return name, nil
}

// SavedSearchService manages saved searches and their alerts.
type SavedSearchService struct {
	store    Searches
	listings *Listings
	now      func() time.Time
}

// NewSavedSearches returns the saved search service; listings counts new jobs for the alerts.
func NewSavedSearches(store Searches, listings *Listings) *SavedSearchService {
	return &SavedSearchService{store: store, listings: listings, now: time.Now}
}

// SetNow replaces the clock (tests).
func (s *SavedSearchService) SetNow(now func() time.Time) { s.now = now }

// List returns the user's saved searches by name.
func (s *SavedSearchService) List(ctx context.Context, userID uuid.UUID) ([]SavedSearch, error) {
	return s.store.SavedSearches(ctx, userID)
}

// Save stores the filter under a name; the alert counts jobs from now.
func (s *SavedSearchService) Save(ctx context.Context, userID uuid.UUID, name, query string, alert bool) (int64, error) {
	name, err := cleanName(name)
	if err != nil || len(query) > 2000 {
		return 0, ErrInvalidSearch
	}
	return s.store.CreateSavedSearch(ctx, userID, SavedSearch{Name: name, Query: strings.TrimPrefix(query, "?"),
		Alert: alert, CheckedAt: s.now()})
}

// Rename gives a saved search a new name; false when it does not exist.
func (s *SavedSearchService) Rename(ctx context.Context, userID uuid.UUID, id int64, name string) (bool, error) {
	name, err := cleanName(name)
	if err != nil {
		return false, err
	}
	return s.store.RenameSavedSearch(ctx, userID, id, name)
}

// SetAlert turns the alert of a saved search on or off.
func (s *SavedSearchService) SetAlert(ctx context.Context, userID uuid.UUID, id int64, alert bool) (bool, error) {
	return s.store.SetSavedSearchAlert(ctx, userID, id, alert, s.now())
}

// Delete removes a saved search.
func (s *SavedSearchService) Delete(ctx context.Context, userID uuid.UUID, id int64) (bool, error) {
	return s.store.DeleteSavedSearch(ctx, userID, id)
}

// Alert is one saved search with new jobs since its last check.
type Alert struct {
	Search SavedSearch
	New    int
}

// CheckAlerts looks at every saved search whose alert is due (at most once per AlertInterval), counts the jobs
// first seen since its last check with filter (the search's query turned into a list filter) and calls send for
// those with new jobs. A search is marked checked whether or not it had new jobs, so every job counts once.
func (s *SavedSearchService) CheckAlerts(ctx context.Context, filter func(query string) Filter,
	send func(ctx context.Context, a Alert) error,
) (int, error) {
	now := s.now()
	due, err := s.store.DueSearchAlerts(ctx, now.Add(-AlertInterval))
	if err != nil {
		return 0, err
	}
	sent := 0
	var errs []error
	for _, ss := range due {
		f := filter(ss.Query)
		f.SeenAfter, f.Page = ss.CheckedAt, 1
		list, err := s.listings.List(ctx, ss.UserID, f)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if list.Total > 0 {
			if err := send(ctx, Alert{Search: ss, New: list.Total}); err != nil {
				errs = append(errs, err)
				continue // not marked: tried again on the next run
			}
			sent++
		}
		if err := s.store.MarkSearchChecked(ctx, ss.UserID, ss.ID, now); err != nil {
			errs = append(errs, err)
		}
	}
	return sent, errors.Join(errs...)
}
