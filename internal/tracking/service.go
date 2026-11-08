package tracking

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/application"
)

// AppService executes state transitions for applications.
type AppService interface {
	Fire(ctx context.Context, userID, appID uuid.UUID, ev application.Event, actor application.Actor, detail map[string]any) (application.State, error)
}

// Service orchestrates inbox tracking, mailbox configuration, and status suggestions.
type Service struct {
	store  Store
	client MailboxClient
	cipher PasswordCipher
	apps   AppService
	now    func() time.Time
}

// NewService returns a new tracking service.
func NewService(store Store, client MailboxClient, cipher PasswordCipher, apps AppService) *Service {
	return &Service{
		store:  store,
		client: client,
		cipher: cipher,
		apps:   apps,
		now:    time.Now,
	}
}

// SetNow overrides the time source (used in tests).
func (s *Service) SetNow(now func() time.Time) {
	s.now = now
}

// SaveMailbox updates or creates mailbox settings for the user.
func (s *Service) SaveMailbox(ctx context.Context, userID uuid.UUID, host string, port int, tls bool, username, password string, enabled bool) error {
	host = strings.TrimSpace(host)
	username = strings.TrimSpace(username)
	if host == "" {
		return errors.New("mailbox host is required")
	}
	if username == "" {
		return errors.New("mailbox username is required")
	}
	if port <= 0 {
		port = 993
	}

	var encPass []byte
	if password != "" {
		sealed, err := s.cipher.SealPassword(ctx, userID, password)
		if err != nil {
			return fmt.Errorf("sealing password: %w", err)
		}
		encPass = sealed
	} else {
		existing, err := s.store.GetMailbox(ctx, userID)
		if err != nil {
			return errors.New("password is required for new mailbox")
		}
		encPass = existing.EncryptedPassword
	}

	now := s.now()
	acc := MailboxAccount{
		UserID:            userID,
		Host:              host,
		Port:              port,
		TLS:               tls,
		Username:          username,
		EncryptedPassword: encPass,
		Enabled:           enabled,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	return s.store.SaveMailbox(ctx, acc)
}

// GetMailbox returns the configured mailbox settings of the user.
func (s *Service) GetMailbox(ctx context.Context, userID uuid.UUID) (MailboxAccount, error) {
	return s.store.GetMailbox(ctx, userID)
}

// TestConnection checks whether IMAP credentials connect successfully.
func (s *Service) TestConnection(ctx context.Context, userID uuid.UUID, host string, port int, tls bool, username, password string) error {
	if port <= 0 {
		port = 993
	}
	if password == "" {
		existing, err := s.store.GetMailbox(ctx, userID)
		if err != nil {
			return errors.New("password is required")
		}
		opened, err := s.cipher.OpenPassword(ctx, userID, existing.EncryptedPassword)
		if err != nil {
			return fmt.Errorf("opening stored password: %w", err)
		}
		password = opened
	}

	acc := MailboxAccount{
		UserID:   userID,
		Host:     host,
		Port:     port,
		TLS:      tls,
		Username: username,
	}
	return s.client.TestConnection(ctx, acc, password)
}

// CheckMailbox fetches new emails from the user's mailbox read-only, classifies them,
// and records status suggestions for active applications.
func (s *Service) CheckMailbox(ctx context.Context, userID uuid.UUID) ([]StatusSuggestion, error) {
	acc, err := s.store.GetMailbox(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if !acc.Enabled {
		return nil, nil
	}

	pass, err := s.cipher.OpenPassword(ctx, userID, acc.EncryptedPassword)
	if err != nil {
		now := s.now()
		_ = s.store.UpdateMailboxCursor(ctx, userID, acc.LastUID, acc.UIDValidity, now, "decryption failed: "+err.Error())
		return nil, fmt.Errorf("decrypting password: %w", err)
	}

	messages, newUID, newValidity, err := s.client.FetchNewMessages(ctx, acc, pass)
	now := s.now()
	if err != nil {
		_ = s.store.UpdateMailboxCursor(ctx, userID, acc.LastUID, acc.UIDValidity, now, err.Error())
		return nil, fmt.Errorf("fetching mail: %w", err)
	}

	apps, err := s.store.ListActiveApplications(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("listing active applications: %w", err)
	}

	var created []StatusSuggestion
	for _, msg := range messages {
		cat, classReasons := Classify(msg)
		// Security codes: NEVER processed into suggestions or read by runners (ADR 0015).
		if cat == CategorySecurityCode || cat == CategoryOther {
			continue
		}

		targetState, ok := SuggestedState(cat)
		if !ok {
			continue
		}

		matchedApp, matchReasons := MatchApplication(msg, apps)
		if matchedApp == nil {
			continue
		}

		// Don't suggest state if application is already in or beyond that state
		if matchedApp.CurrentState == targetState {
			continue
		}

		sug := StatusSuggestion{
			ID:             uuid.New(),
			UserID:         userID,
			ApplicationID:  matchedApp.ID,
			MessageUID:     msg.UID,
			MessageID:      msg.MessageID,
			MessageFrom:    msg.From,
			MessageSubject: msg.Subject,
			MessageDate:    msg.Date,
			Category:       cat,
			SuggestedState: targetState,
			Reasons:        append(classReasons, matchReasons...),
			Status:         SuggestionPending,
			CreatedAt:      now,
			UpdatedAt:      now,
			Company:        matchedApp.Company,
			JobTitle:       matchedApp.Title,
			CurrentState:   matchedApp.CurrentState,
		}

		if err := s.store.CreateSuggestion(ctx, sug); err != nil {
			return nil, fmt.Errorf("creating suggestion: %w", err)
		}
		created = append(created, sug)
	}

	if err := s.store.UpdateMailboxCursor(ctx, userID, newUID, newValidity, now, ""); err != nil {
		return nil, fmt.Errorf("updating cursor: %w", err)
	}

	return created, nil
}

// CheckAllMailboxes checks every enabled mailbox in the system.
func (s *Service) CheckAllMailboxes(ctx context.Context) (int, error) {
	mailboxes, err := s.store.ListEnabledMailboxes(ctx)
	if err != nil {
		return 0, err
	}

	checked := 0
	for _, m := range mailboxes {
		if _, err := s.CheckMailbox(ctx, m.UserID); err != nil {
			// Log or continue so one failing mailbox does not block others
			continue
		}
		checked++
	}
	return checked, nil
}

// AcceptSuggestion transitions the application to the suggested state and marks the suggestion accepted.
func (s *Service) AcceptSuggestion(ctx context.Context, userID, suggestionID uuid.UUID) error {
	sug, err := s.store.Suggestion(ctx, userID, suggestionID)
	if err != nil {
		return err
	}
	if sug.Status != SuggestionPending {
		return ErrInvalidSuggestion
	}

	var ev application.Event
	switch sug.SuggestedState {
	case application.Confirmed:
		ev = application.EvConfirm
	case application.Rejected:
		ev = application.EvReject
	case application.Interview:
		ev = application.EvInterview
	default:
		return fmt.Errorf("%w: unknown suggested state %s", ErrInvalidSuggestion, sug.SuggestedState)
	}

	if _, err := s.apps.Fire(ctx, userID, sug.ApplicationID, ev, application.ActorUser, map[string]any{
		"via":        "mail_suggestion",
		"message_id": sug.MessageID,
		"from":       sug.MessageFrom,
	}); err != nil {
		return fmt.Errorf("transitioning application: %w", err)
	}

	return s.store.SetSuggestionStatus(ctx, userID, suggestionID, SuggestionAccepted)
}

// DismissSuggestion dismisses a status suggestion.
func (s *Service) DismissSuggestion(ctx context.Context, userID, suggestionID uuid.UUID) error {
	return s.store.SetSuggestionStatus(ctx, userID, suggestionID, SuggestionDismissed)
}

// Suggestions returns suggestions for the user, optionally filtered by status ("pending", "accepted", "dismissed").
func (s *Service) Suggestions(ctx context.Context, userID uuid.UUID, status string) ([]StatusSuggestion, error) {
	return s.store.Suggestions(ctx, userID, status)
}

// SuggestionsForApplication returns all suggestions for a specific application.
func (s *Service) SuggestionsForApplication(ctx context.Context, userID, appID uuid.UUID) ([]StatusSuggestion, error) {
	return s.store.SuggestionsForApplication(ctx, userID, appID)
}
