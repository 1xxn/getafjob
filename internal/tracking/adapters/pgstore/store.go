// Package pgstore implements tracking.Store on PostgreSQL.
package pgstore

//go:generate go tool sqlc generate -f ../../../../sqlc.yaml

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/1xxn/getafjob/internal/platform/db"
	"github.com/1xxn/getafjob/internal/tracking"
	"github.com/1xxn/getafjob/internal/tracking/adapters/pgstore/q"
)

// Store is the PostgreSQL tracking store.
type Store struct {
	pool *pgxpool.Pool
}

var _ tracking.Store = (*Store)(nil)

// New returns a new tracking store.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) user(ctx context.Context, userID uuid.UUID, fn func(*q.Queries) error) error {
	return db.InUserTx(ctx, s.pool, userID, func(tx pgx.Tx) error {
		return fn(q.New(tx))
	})
}

func (s *Store) system(ctx context.Context, fn func(*q.Queries) error) error {
	return db.InSystemTx(ctx, s.pool, func(tx pgx.Tx) error {
		return fn(q.New(tx))
	})
}

// GetMailbox returns the configured mailbox of the user.
func (s *Store) GetMailbox(ctx context.Context, userID uuid.UUID) (tracking.MailboxAccount, error) {
	var acc tracking.MailboxAccount
	err := s.user(ctx, userID, func(qs *q.Queries) error {
		r, err := qs.GetMailbox(ctx, userID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return tracking.ErrNotFound
			}
			return err
		}

		lastUID := uint32(0)
		if r.LastUid >= 0 && r.LastUid <= math.MaxUint32 {
			lastUID = uint32(r.LastUid)
		}
		uidVal := uint32(0)
		if r.UidValidity >= 0 && r.UidValidity <= math.MaxUint32 {
			uidVal = uint32(r.UidValidity)
		}

		lastErr := ""
		if r.LastError != nil {
			lastErr = *r.LastError
		}

		acc = tracking.MailboxAccount{
			UserID:            r.UserID,
			Host:              r.Host,
			Port:              int(r.Port),
			TLS:               r.Tls,
			Username:          r.Username,
			EncryptedPassword: r.EncryptedPassword,
			Enabled:           r.Enabled,
			LastUID:           lastUID,
			UIDValidity:       uidVal,
			LastCheckedAt:     r.LastCheckedAt,
			LastError:         lastErr,
			CreatedAt:         r.CreatedAt,
			UpdatedAt:         r.UpdatedAt,
		}
		return nil
	})
	return acc, err
}

// SaveMailbox stores or updates mailbox settings for the user.
func (s *Store) SaveMailbox(ctx context.Context, acc tracking.MailboxAccount) error {
	port := acc.Port
	if port < 1 || port > 65535 {
		port = 993
	}

	return s.user(ctx, acc.UserID, func(qs *q.Queries) error {
		return qs.SaveMailbox(ctx, q.SaveMailboxParams{
			UserID:            acc.UserID,
			Host:              acc.Host,
			Port:              int32(port), //nolint:gosec // G115: bounded between 1 and 65535
			Tls:               acc.TLS,
			Username:          acc.Username,
			EncryptedPassword: acc.EncryptedPassword,
			Enabled:           acc.Enabled,
			CreatedAt:         acc.CreatedAt,
		})
	})
}

// UpdateMailboxCursor updates the sync cursor and last checked timestamp.
func (s *Store) UpdateMailboxCursor(ctx context.Context, userID uuid.UUID, lastUID, uidValidity uint32, at time.Time, lastErr string) error {
	var errPtr *string
	if lastErr != "" {
		errPtr = &lastErr
	}

	return s.user(ctx, userID, func(qs *q.Queries) error {
		return qs.UpdateMailboxCursor(ctx, q.UpdateMailboxCursorParams{
			UserID:        userID,
			LastUid:       int64(lastUID),
			UidValidity:   int64(uidValidity),
			LastCheckedAt: &at,
			LastError:     errPtr,
		})
	})
}

// ListEnabledMailboxes lists all enabled mailboxes across users (system role).
func (s *Store) ListEnabledMailboxes(ctx context.Context) ([]tracking.MailboxAccount, error) {
	var accounts []tracking.MailboxAccount
	err := s.system(ctx, func(qs *q.Queries) error {
		rows, err := qs.ListEnabledMailboxes(ctx)
		if err != nil {
			return err
		}
		accounts = make([]tracking.MailboxAccount, len(rows))
		for i, r := range rows {
			lastUID := uint32(0)
			if r.LastUid >= 0 && r.LastUid <= math.MaxUint32 {
				lastUID = uint32(r.LastUid)
			}
			uidVal := uint32(0)
			if r.UidValidity >= 0 && r.UidValidity <= math.MaxUint32 {
				uidVal = uint32(r.UidValidity)
			}
			lastErr := ""
			if r.LastError != nil {
				lastErr = *r.LastError
			}
			accounts[i] = tracking.MailboxAccount{
				UserID:            r.UserID,
				Host:              r.Host,
				Port:              int(r.Port),
				TLS:               r.Tls,
				Username:          r.Username,
				EncryptedPassword: r.EncryptedPassword,
				Enabled:           r.Enabled,
				LastUID:           lastUID,
				UIDValidity:       uidVal,
				LastCheckedAt:     r.LastCheckedAt,
				LastError:         lastErr,
				CreatedAt:         r.CreatedAt,
				UpdatedAt:         r.UpdatedAt,
			}
		}
		return nil
	})
	return accounts, err
}

// ListActiveApplications returns applications that can match incoming status emails.
func (s *Store) ListActiveApplications(ctx context.Context, userID uuid.UUID) ([]tracking.ApplicationSummary, error) {
	var apps []tracking.ApplicationSummary
	err := s.user(ctx, userID, func(qs *q.Queries) error {
		rows, err := qs.ActiveApplicationsForTracking(ctx, userID)
		if err != nil {
			return err
		}
		apps = make([]tracking.ApplicationSummary, len(rows))
		for i, r := range rows {
			apps[i] = tracking.ApplicationSummary{
				ID:           r.ID,
				UserID:       r.UserID,
				JobID:        r.JobID,
				CurrentState: r.State,
				Company:      r.Company,
				Title:        r.Title,
				MatchKey:     r.MatchKey,
				URL:          r.Url,
			}
		}
		return nil
	})
	return apps, err
}

// CreateSuggestion records a proposed application state change.
func (s *Store) CreateSuggestion(ctx context.Context, sug tracking.StatusSuggestion) error {
	reasonsJSON, err := json.Marshal(sug.Reasons)
	if err != nil {
		return fmt.Errorf("marshaling reasons: %w", err)
	}

	var msgIDPtr *string
	if sug.MessageID != "" {
		msgIDPtr = &sug.MessageID
	}

	return s.user(ctx, sug.UserID, func(qs *q.Queries) error {
		return qs.CreateSuggestion(ctx, q.CreateSuggestionParams{
			ID:             sug.ID,
			UserID:         sug.UserID,
			ApplicationID:  sug.ApplicationID,
			MessageUid:     int64(sug.MessageUID),
			MessageID:      msgIDPtr,
			MessageFrom:    sug.MessageFrom,
			MessageSubject: sug.MessageSubject,
			MessageDate:    sug.MessageDate,
			Category:       string(sug.Category),
			SuggestedState: string(sug.SuggestedState),
			Reasons:        reasonsJSON,
			CreatedAt:      sug.CreatedAt,
		})
	})
}

// Suggestions returns suggestions for the user.
func (s *Store) Suggestions(ctx context.Context, userID uuid.UUID, status string) ([]tracking.StatusSuggestion, error) {
	var statusPtr *string
	if status != "" {
		statusPtr = &status
	}

	var out []tracking.StatusSuggestion
	err := s.user(ctx, userID, func(qs *q.Queries) error {
		rows, err := qs.SuggestionsByUser(ctx, q.SuggestionsByUserParams{
			UserID: userID,
			Status: statusPtr,
		})
		if err != nil {
			return err
		}
		out = make([]tracking.StatusSuggestion, len(rows))
		for i, r := range rows {
			out[i] = mapSuggestionRow(r.ID, r.UserID, r.ApplicationID, r.MessageUid, r.MessageID,
				r.MessageFrom, r.MessageSubject, r.MessageDate, r.Category, r.SuggestedState,
				r.Reasons, r.Status, r.CreatedAt, r.UpdatedAt, r.Company, r.Title, r.CurrentState)
		}
		return nil
	})
	return out, err
}

// SuggestionsForApplication returns suggestions for a single application.
func (s *Store) SuggestionsForApplication(ctx context.Context, userID, appID uuid.UUID) ([]tracking.StatusSuggestion, error) {
	var out []tracking.StatusSuggestion
	err := s.user(ctx, userID, func(qs *q.Queries) error {
		rows, err := qs.SuggestionsByApplication(ctx, q.SuggestionsByApplicationParams{
			UserID:        userID,
			ApplicationID: appID,
		})
		if err != nil {
			return err
		}
		out = make([]tracking.StatusSuggestion, len(rows))
		for i, r := range rows {
			out[i] = mapSuggestionRow(r.ID, r.UserID, r.ApplicationID, r.MessageUid, r.MessageID,
				r.MessageFrom, r.MessageSubject, r.MessageDate, r.Category, r.SuggestedState,
				r.Reasons, r.Status, r.CreatedAt, r.UpdatedAt, r.Company, r.Title, r.CurrentState)
		}
		return nil
	})
	return out, err
}

// Suggestion returns a single suggestion by ID.
func (s *Store) Suggestion(ctx context.Context, userID, id uuid.UUID) (tracking.StatusSuggestion, error) {
	var sug tracking.StatusSuggestion
	err := s.user(ctx, userID, func(qs *q.Queries) error {
		r, err := qs.SuggestionByID(ctx, q.SuggestionByIDParams{ID: id, UserID: userID})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return tracking.ErrNotFound
			}
			return err
		}
		sug = mapSuggestionRow(r.ID, r.UserID, r.ApplicationID, r.MessageUid, r.MessageID,
			r.MessageFrom, r.MessageSubject, r.MessageDate, r.Category, r.SuggestedState,
			r.Reasons, r.Status, r.CreatedAt, r.UpdatedAt, r.Company, r.Title, r.CurrentState)
		return nil
	})
	return sug, err
}

// SetSuggestionStatus updates the status of a suggestion (e.g. accepted, dismissed).
func (s *Store) SetSuggestionStatus(ctx context.Context, userID, id uuid.UUID, status string) error {
	now := time.Now()
	return s.user(ctx, userID, func(qs *q.Queries) error {
		n, err := qs.SetSuggestionStatus(ctx, q.SetSuggestionStatusParams{
			ID:        id,
			UserID:    userID,
			Status:    status,
			UpdatedAt: now,
		})
		if err != nil {
			return err
		}
		if n == 0 {
			return tracking.ErrInvalidSuggestion
		}
		return nil
	})
}

func mapSuggestionRow(id, userID, appID uuid.UUID, msgUID int64, msgID *string,
	from, subject string, date time.Time, category, suggestedState string,
	reasonsRaw []byte, status string, createdAt, updatedAt time.Time,
	company, title, currentState string) tracking.StatusSuggestion {

	uid := uint32(0)
	if msgUID >= 0 && msgUID <= math.MaxUint32 {
		uid = uint32(msgUID)
	}

	mID := ""
	if msgID != nil {
		mID = *msgID
	}

	var reasons []string
	if len(reasonsRaw) > 0 {
		_ = json.Unmarshal(reasonsRaw, &reasons)
	}

	return tracking.StatusSuggestion{
		ID:             id,
		UserID:         userID,
		ApplicationID:  appID,
		MessageUID:     uid,
		MessageID:      mID,
		MessageFrom:    from,
		MessageSubject: subject,
		MessageDate:    date,
		Category:       tracking.Category(category),
		SuggestedState: suggestedState,
		Reasons:        reasons,
		Status:         status,
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
		Company:        company,
		JobTitle:       title,
		CurrentState:   currentState,
	}
}
