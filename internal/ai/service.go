package ai

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Service provides AI capabilities (Planner, Drafter, Extractor, Classifier)
// following ADR 0012, 0013, and #44.
type Service struct {
	store              Store
	cipher             KeyCipher
	client             Client
	adminEnabled       bool
	operatorKey        string
	defaultModel       string
	budgetPerUserCents int
	now                func() time.Time
}

// NewService constructs an AI Service.
func NewService(store Store, cipher KeyCipher, client Client, adminEnabled bool, operatorKey, defaultModel string, budgetPerUserCents int) *Service {
	if defaultModel == "" {
		defaultModel = ModelClaude35Haiku
	}
	if budgetPerUserCents <= 0 {
		budgetPerUserCents = 500 // $5.00 default cap per user
	}
	return &Service{
		store:              store,
		cipher:             cipher,
		client:             client,
		adminEnabled:       adminEnabled,
		operatorKey:        strings.TrimSpace(operatorKey),
		defaultModel:       defaultModel,
		budgetPerUserCents: budgetPerUserCents,
		now:                time.Now,
	}
}

// SetNow overrides time for testing.
func (s *Service) SetNow(now func() time.Time) {
	s.now = now
}

// AdminEnabled reports whether the administrator has turned on the AI module.
func (s *Service) AdminEnabled() bool {
	return s.adminEnabled
}

// resolveAuth resolves the API key, model and whether the operator key is being used.
func (s *Service) resolveAuth(ctx context.Context, userID uuid.UUID) (apiKey, model string, isOperatorKey bool, err error) {
	if !s.adminEnabled {
		return "", "", false, ErrDisabled
	}
	settings, encKey, err := s.store.GetSettings(ctx, userID)
	if err != nil {
		return "", "", false, err
	}
	if !settings.Enabled {
		return "", "", false, ErrUserAIDisabled
	}
	if len(encKey) > 0 && s.cipher != nil {
		key, err := s.cipher.OpenKey(ctx, userID, encKey)
		if err != nil {
			return "", "", false, err
		}
		if key != "" {
			return key, s.defaultModel, false, nil
		}
	}
	if s.operatorKey != "" {
		if settings.SpentCents >= s.budgetPerUserCents {
			return "", "", false, ErrBudgetExceeded
		}
		return s.operatorKey, s.defaultModel, true, nil
	}
	return "", "", false, ErrNoAPIKey
}

func (s *Service) recordUsage(ctx context.Context, userID uuid.UUID, task TaskType, model string, tokensIn, tokensOut int, isOperatorKey bool) error {
	costCents := CalculateCostCents(model, tokensIn, tokensOut)
	if isOperatorKey && costCents > 0 {
		_ = s.store.IncrementSpent(ctx, userID, costCents)
	}
	return s.store.RecordUsage(ctx, userID, task, model, tokensIn, tokensOut, costCents)
}

// Plan plans form fields given user facts and form structure.
func (s *Service) Plan(ctx context.Context, userID uuid.UUID, req PlanRequest) (PlanResult, error) {
	apiKey, model, isOperatorKey, err := s.resolveAuth(ctx, userID)
	if err != nil {
		return PlanResult{}, err
	}
	res, tIn, tOut, err := s.client.CompletePlan(ctx, apiKey, model, req)
	if err != nil {
		return PlanResult{}, err
	}
	VerifyPlanProvenance(&res, req.Facts, req.Answers)
	_ = s.recordUsage(ctx, userID, TaskPlan, model, tIn, tOut, isOperatorKey)
	return res, nil
}

// isCoverLetter checks whether a field or prompt asks for a cover letter.
func isCoverLetter(label, name string) bool {
	lower := strings.ToLower(label + " " + name)
	return strings.Contains(lower, "cover letter") ||
		strings.Contains(lower, "anschreiben") ||
		strings.Contains(lower, "motivation letter") ||
		strings.Contains(lower, "motivationsschreiben")
}

// Draft generates an answer draft for an open-ended question.
// Per system policy, cover letters are refused.
// Per ADR 0012/0013, drafts ALWAYS require manual approval.
func (s *Service) Draft(ctx context.Context, userID uuid.UUID, req DraftRequest) (DraftResult, error) {
	if isCoverLetter(req.Field.Label, req.Field.Name) {
		return DraftResult{}, ErrCoverLetterRefused
	}
	apiKey, model, isOperatorKey, err := s.resolveAuth(ctx, userID)
	if err != nil {
		return DraftResult{}, err
	}
	res, tIn, tOut, err := s.client.CompleteDraft(ctx, apiKey, model, req)
	if err != nil {
		return DraftResult{}, err
	}
	res.RequiresApproval = true // ADR 0012: drafts ALWAYS require user approval
	_ = s.recordUsage(ctx, userID, TaskDraft, model, tIn, tOut, isOperatorKey)
	return res, nil
}

// Extract extracts structured job posting data from text/HTML.
func (s *Service) Extract(ctx context.Context, userID uuid.UUID, req ExtractRequest) (ExtractResult, error) {
	apiKey, model, isOperatorKey, err := s.resolveAuth(ctx, userID)
	if err != nil {
		return ExtractResult{}, err
	}
	res, tIn, tOut, err := s.client.CompleteExtract(ctx, apiKey, model, req)
	if err != nil {
		return ExtractResult{}, err
	}
	// Sanity check: min salary <= max salary
	if res.SalaryMin != nil && res.SalaryMax != nil && *res.SalaryMin > *res.SalaryMax {
		res.SalaryMin, res.SalaryMax = res.SalaryMax, res.SalaryMin
	}
	_ = s.recordUsage(ctx, userID, TaskExtract, model, tIn, tOut, isOperatorKey)
	return res, nil
}

// Classify categorizes applicant communication (e.g. emails).
func (s *Service) Classify(ctx context.Context, userID uuid.UUID, req ClassifyRequest) (ClassifyResult, error) {
	apiKey, model, isOperatorKey, err := s.resolveAuth(ctx, userID)
	if err != nil {
		return ClassifyResult{}, err
	}
	res, tIn, tOut, err := s.client.CompleteClassify(ctx, apiKey, model, req)
	if err != nil {
		return ClassifyResult{}, err
	}
	_ = s.recordUsage(ctx, userID, TaskClassify, model, tIn, tOut, isOperatorKey)
	return res, nil
}

// GetSettings loads the user's AI settings with budget cap information.
func (s *Service) GetSettings(ctx context.Context, userID uuid.UUID) (UserSettings, error) {
	settings, encKey, err := s.store.GetSettings(ctx, userID)
	if err != nil {
		return UserSettings{}, err
	}
	settings.HasCustomKey = len(encKey) > 0
	settings.BudgetCents = s.budgetPerUserCents
	return settings, nil
}

// SaveSettings updates the user's AI opt-in and optional custom Anthropic key.
func (s *Service) SaveSettings(ctx context.Context, userID uuid.UUID, enabled bool, customKey string, clearKey bool) error {
	var encKey []byte
	switch {
	case clearKey:
		encKey = nil
	case customKey != "" && s.cipher != nil:
		sealed, err := s.cipher.SealKey(ctx, userID, customKey)
		if err != nil {
			return err
		}
		encKey = sealed
	default:
		// Keep existing key if not changed or cleared
		_, existingEnc, err := s.store.GetSettings(ctx, userID)
		if err == nil {
			encKey = existingEnc
		}
	}
	return s.store.SaveSettings(ctx, userID, enabled, encKey)
}
