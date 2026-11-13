package assist

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
)

// DefaultTokenTTL is the duration an extension token remains valid (90 days).
const DefaultTokenTTL = 90 * 24 * time.Hour

// DefaultPairingTTL is the duration a pairing code remains valid (10 minutes).
const DefaultPairingTTL = 10 * time.Minute

// Service coordinates browser extension authentication, inspection, and submissions.
type Service struct {
	store    Store
	profiles ProfileReader
	apps     ApplicationService
	users    UserReader
	logger   *slog.Logger
	now      func() time.Time
}

// NewService creates a new assist service.
func NewService(store Store, profiles ProfileReader, apps ApplicationService, users UserReader, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		store:    store,
		profiles: profiles,
		apps:     apps,
		users:    users,
		logger:   logger,
		now:      time.Now,
	}
}

// SetNow overrides time function (used in tests).
func (s *Service) SetNow(fn func() time.Time) {
	s.now = fn
}

// GeneratePairingCode produces a short-lived pairing code for the user to enter into the extension.
func (s *Service) GeneratePairingCode(ctx context.Context, userID uuid.UUID) (string, error) {
	plain, hash, err := GenerateCode()
	if err != nil {
		return "", fmt.Errorf("generate code: %w", err)
	}

	now := s.now()
	p := PairingCode{
		ID:        uuid.New(),
		UserID:    userID,
		CodeHash:  hash,
		CreatedAt: now,
		ExpiresAt: now.Add(DefaultPairingTTL),
	}

	if err := s.store.CreatePairingCode(ctx, p); err != nil {
		return "", fmt.Errorf("store pairing code: %w", err)
	}

	s.logger.InfoContext(ctx, "assist pairing code created", "user_id", userID)
	return plain, nil
}

// Pair exchanges a pairing code for a persistent extension bearer token.
func (s *Service) Pair(ctx context.Context, code, clientName string) (Token, string, error) {
	hash := HashCode(code)
	now := s.now()

	pairing, err := s.store.ConsumePairingCode(ctx, hash, now)
	if err != nil {
		return Token{}, "", ErrInvalidPairingCode
	}

	tok, err := NewToken(pairing.UserID, clientName, now, DefaultTokenTTL)
	if err != nil {
		return Token{}, "", fmt.Errorf("create token: %w", err)
	}

	if err := s.store.CreateExtensionToken(ctx, tok); err != nil {
		return Token{}, "", fmt.Errorf("store token: %w", err)
	}

	userName := "User"
	if s.users != nil {
		if name, err := s.users.GetUserName(ctx, pairing.UserID); err == nil && name != "" {
			userName = name
		}
	}

	s.logger.InfoContext(ctx, "assist extension paired", "user_id", pairing.UserID, "client", clientName)
	return tok, userName, nil
}

// Authenticate validates a bearer token and touches its last-used timestamp.
func (s *Service) Authenticate(ctx context.Context, tokenPlain string) (Token, error) {
	hash := HashToken(tokenPlain)
	now := s.now()

	tok, err := s.store.GetExtensionToken(ctx, hash, now)
	if err != nil {
		return Token{}, ErrInvalidToken
	}

	_ = s.store.TouchExtensionToken(ctx, hash, now)
	return tok, nil
}

// GetProfile returns the assist profile facts, answer bank items, and text blocks.
func (s *Service) GetProfile(ctx context.Context, userID uuid.UUID) (map[string]string, []Answer, []TextBlock, []Document, error) {
	if s.profiles == nil {
		return make(map[string]string), nil, nil, nil, nil
	}
	return s.profiles.ReadAssistProfile(ctx, userID)
}

// Inspect analyzes form fields on the active page and suggests values with provenance.
func (s *Service) Inspect(ctx context.Context, userID uuid.UUID, _, _ string, fields []FormField) (InspectResult, error) {
	facts, answers, texts, _, err := s.GetProfile(ctx, userID)
	if err != nil {
		return InspectResult{}, fmt.Errorf("load assist profile: %w", err)
	}

	res := MatchForm(fields, facts, answers, texts)
	return res, nil
}

// Submit records an application submission performed via the extension.
func (s *Service) Submit(ctx context.Context, userID uuid.UUID, pageURL, company, title string, fieldsFilled int, approved bool, notes string) (uuid.UUID, string, error) {
	if !approved {
		return uuid.Nil, "", ErrUnapprovedSubmit
	}

	if s.apps == nil {
		return uuid.Nil, "recorded", nil
	}

	appID, err := s.apps.RecordExternalSubmit(ctx, userID, pageURL, company, title, fieldsFilled, notes)
	if err != nil {
		return uuid.Nil, "", fmt.Errorf("record submit: %w", err)
	}

	s.logger.InfoContext(ctx, "assist application submitted", "user_id", userID, "app_id", appID, "company", company)
	return appID, "submitted", nil
}

// ListApprovals retrieves pending application approvals for the user.
func (s *Service) ListApprovals(ctx context.Context, userID uuid.UUID) ([]ApprovalItem, error) {
	if s.apps == nil {
		return nil, nil
	}
	return s.apps.ListPendingApprovals(ctx, userID)
}

// DecideApproval approves or rejects an application submission.
func (s *Service) DecideApproval(ctx context.Context, userID, applicationID uuid.UUID, decision string) error {
	if s.apps == nil {
		return nil
	}
	approve := strings.ToLower(decision) == "approve"
	return s.apps.DecideApproval(ctx, userID, applicationID, approve)
}

// ListExtensionTokens returns all active extension tokens for a user.
func (s *Service) ListExtensionTokens(ctx context.Context, userID uuid.UUID) ([]Token, error) {
	return s.store.ListExtensionTokens(ctx, userID)
}

// RevokeExtensionToken revokes an extension token.
func (s *Service) RevokeExtensionToken(ctx context.Context, id, userID uuid.UUID) error {
	return s.store.RevokeExtensionToken(ctx, id, userID)
}
