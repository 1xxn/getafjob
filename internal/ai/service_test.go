package ai

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
)

type fakeStore struct {
	mu       sync.Mutex
	settings map[uuid.UUID]UserSettings
	encKeys  map[uuid.UUID][]byte
	usage    []aiUsageRecord
}

type aiUsageRecord struct {
	userID    uuid.UUID
	task      TaskType
	model     string
	tokensIn  int
	tokensOut int
	costCents int
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		settings: make(map[uuid.UUID]UserSettings),
		encKeys:  make(map[uuid.UUID][]byte),
	}
}

func (f *fakeStore) GetSettings(_ context.Context, userID uuid.UUID) (UserSettings, []byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st, ok := f.settings[userID]
	if !ok {
		st = UserSettings{Enabled: false}
	}
	return st, f.encKeys[userID], nil
}

func (f *fakeStore) SaveSettings(_ context.Context, userID uuid.UUID, enabled bool, encKey []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	st := f.settings[userID]
	st.Enabled = enabled
	f.settings[userID] = st
	f.encKeys[userID] = encKey
	return nil
}

func (f *fakeStore) IncrementSpent(_ context.Context, userID uuid.UUID, costCents int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	st := f.settings[userID]
	st.SpentCents += costCents
	f.settings[userID] = st
	return nil
}

func (f *fakeStore) RecordUsage(_ context.Context, userID uuid.UUID, task TaskType, model string, tokensIn, tokensOut, costCents int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.usage = append(f.usage, aiUsageRecord{
		userID:    userID,
		task:      task,
		model:     model,
		tokensIn:  tokensIn,
		tokensOut: tokensOut,
		costCents: costCents,
	})
	return nil
}

type fakeCipher struct {
	sealed map[string]string
}

func newFakeCipher() *fakeCipher {
	return &fakeCipher{sealed: make(map[string]string)}
}

func (c *fakeCipher) SealKey(_ context.Context, userID uuid.UUID, key string) ([]byte, error) {
	c.sealed[userID.String()] = key
	return []byte("enc:" + key), nil
}

func (c *fakeCipher) OpenKey(_ context.Context, userID uuid.UUID, ciphertext []byte) (string, error) {
	str := string(ciphertext)
	if len(str) > 4 && str[:4] == "enc:" {
		return str[4:], nil
	}
	return "", errors.New("bad ciphertext")
}

type fakeClient struct {
	planRes     PlanResult
	draftRes    DraftResult
	extractRes  ExtractResult
	classifyRes ClassifyResult
	tokensIn    int
	tokensOut   int
	lastAPIKey  string
	lastModel   string
}

func (c *fakeClient) CompletePlan(_ context.Context, apiKey, model string, _ PlanRequest) (PlanResult, int, int, error) {
	c.lastAPIKey = apiKey
	c.lastModel = model
	return c.planRes, c.tokensIn, c.tokensOut, nil
}

func (c *fakeClient) CompleteDraft(_ context.Context, apiKey, model string, _ DraftRequest) (DraftResult, int, int, error) {
	c.lastAPIKey = apiKey
	c.lastModel = model
	return c.draftRes, c.tokensIn, c.tokensOut, nil
}

func (c *fakeClient) CompleteExtract(_ context.Context, apiKey, model string, _ ExtractRequest) (ExtractResult, int, int, error) {
	c.lastAPIKey = apiKey
	c.lastModel = model
	return c.extractRes, c.tokensIn, c.tokensOut, nil
}

func (c *fakeClient) CompleteClassify(_ context.Context, apiKey, model string, _ ClassifyRequest) (ClassifyResult, int, int, error) {
	c.lastAPIKey = apiKey
	c.lastModel = model
	return c.classifyRes, c.tokensIn, c.tokensOut, nil
}

func TestServiceAdminDisabled(t *testing.T) {
	store := newFakeStore()
	cipher := newFakeCipher()
	client := &fakeClient{}
	svc := NewService(store, cipher, client, false, "sk-operator", ModelClaude35Haiku, 500)

	userID := uuid.New()
	_ = store.SaveSettings(context.Background(), userID, true, nil)

	_, err := svc.Plan(context.Background(), userID, PlanRequest{})
	if !errors.Is(err, ErrDisabled) {
		t.Fatalf("expected ErrDisabled, got %v", err)
	}
}

func TestServiceUserNotOptedIn(t *testing.T) {
	store := newFakeStore()
	cipher := newFakeCipher()
	client := &fakeClient{}
	svc := NewService(store, cipher, client, true, "sk-operator", ModelClaude35Haiku, 500)

	userID := uuid.New()
	// Enabled is false by default

	_, err := svc.Plan(context.Background(), userID, PlanRequest{})
	if !errors.Is(err, ErrUserAIDisabled) {
		t.Fatalf("expected ErrUserAIDisabled, got %v", err)
	}
}

func TestServiceBudgetExceeded(t *testing.T) {
	store := newFakeStore()
	cipher := newFakeCipher()
	client := &fakeClient{}
	svc := NewService(store, cipher, client, true, "sk-operator", ModelClaude35Haiku, 500)

	userID := uuid.New()
	_ = store.SaveSettings(context.Background(), userID, true, nil)
	_ = store.IncrementSpent(context.Background(), userID, 500) // Budget reached!

	_, err := svc.Plan(context.Background(), userID, PlanRequest{})
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("expected ErrBudgetExceeded, got %v", err)
	}
}

func TestServiceCustomUserKey(t *testing.T) {
	store := newFakeStore()
	cipher := newFakeCipher()
	client := &fakeClient{
		planRes: PlanResult{
			Fields: []PlannedField{{FieldID: "email", Value: "bob@example.com"}},
		},
		tokensIn:  1000,
		tokensOut: 100,
	}
	svc := NewService(store, cipher, client, true, "sk-operator", ModelClaude35Haiku, 500)

	userID := uuid.New()
	err := svc.SaveSettings(context.Background(), userID, true, "sk-user-custom-key", false)
	if err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	// Even if operator spent is 500 (or beyond), user's own key should succeed
	_ = store.IncrementSpent(context.Background(), userID, 600)

	res, err := svc.Plan(context.Background(), userID, PlanRequest{
		Facts: []ProfileFact{{Key: "personal.email", Value: "bob@example.com"}},
	})
	if err != nil {
		t.Fatalf("Plan with custom key failed: %v", err)
	}
	if client.lastAPIKey != "sk-user-custom-key" {
		t.Errorf("expected client to be called with custom key, got %q", client.lastAPIKey)
	}
	if len(res.Fields) != 1 || res.Fields[0].Provenance != "personal.email" {
		t.Errorf("unexpected plan result: %+v", res)
	}

	// Check that operator budget was NOT incremented for custom key
	st, _ := svc.GetSettings(context.Background(), userID)
	if st.SpentCents != 600 {
		t.Errorf("spent cents = %d, expected 600 (not incremented)", st.SpentCents)
	}
}

func TestServiceDraftApprovalAndCoverLetterRefusal(t *testing.T) {
	store := newFakeStore()
	cipher := newFakeCipher()
	client := &fakeClient{
		draftRes: DraftResult{
			FieldID:          "q1",
			DraftText:        "I have extensive experience running Kubernetes in production.",
			RequiresApproval: false, // Provider might return false, but service MUST force true!
		},
		tokensIn:  2000,
		tokensOut: 200,
	}
	svc := NewService(store, cipher, client, true, "sk-operator", ModelClaude35Haiku, 500)

	userID := uuid.New()
	_ = svc.SaveSettings(context.Background(), userID, true, "", false)

	// 1. Cover letter drafting MUST be refused
	_, err := svc.Draft(context.Background(), userID, DraftRequest{
		Field: FormField{Label: "Please attach your cover letter"},
	})
	if !errors.Is(err, ErrCoverLetterRefused) {
		t.Fatalf("expected ErrCoverLetterRefused for cover letter, got %v", err)
	}

	// 2. Open-ended question drafting succeeds and RequiresApproval is strictly true
	draft, err := svc.Draft(context.Background(), userID, DraftRequest{
		Field: FormField{ID: "q1", Label: "Describe your cloud experience"},
	})
	if err != nil {
		t.Fatalf("Draft failed: %v", err)
	}
	if !draft.RequiresApproval {
		t.Errorf("expected RequiresApproval to be forced true, got false")
	}
}

func TestServiceExtractAndClassify(t *testing.T) {
	store := newFakeStore()
	cipher := newFakeCipher()
	minSal, maxSal := 100_000, 80_000 // Inverted on purpose to test service sanity correction
	client := &fakeClient{
		extractRes: ExtractResult{
			Title:          "Senior Go Engineer",
			Company:        "Acme Cloud",
			SalaryMin:      &minSal,
			SalaryMax:      &maxSal,
			SalaryCurrency: "EUR",
			Remote:         true,
		},
		classifyRes: ClassifyResult{
			Category:   CategoryRejection,
			Confidence: 0.95,
			Reasoning:  "Thank you for your interest, but we have decided to pursue other candidates.",
		},
		tokensIn:  500,
		tokensOut: 50,
	}
	svc := NewService(store, cipher, client, true, "sk-operator", ModelClaude35Haiku, 500)

	userID := uuid.New()
	_ = svc.SaveSettings(context.Background(), userID, true, "", false)

	// Extract
	ext, err := svc.Extract(context.Background(), userID, ExtractRequest{Text: "Raw job description"})
	if err != nil {
		t.Fatalf("Extract failed: %v", err)
	}
	if *ext.SalaryMin > *ext.SalaryMax {
		t.Errorf("expected SalaryMin (%d) <= SalaryMax (%d)", *ext.SalaryMin, *ext.SalaryMax)
	}

	// Classify
	cls, err := svc.Classify(context.Background(), userID, ClassifyRequest{
		Subject: "Your application at Acme",
		Body:    "Thank you for your interest, but we have decided to pursue other candidates.",
	})
	if err != nil {
		t.Fatalf("Classify failed: %v", err)
	}
	if cls.Category != CategoryRejection || cls.Confidence < 0.9 {
		t.Errorf("unexpected classify result: %+v", cls)
	}

	// Verify usage was recorded
	if len(store.usage) != 2 {
		t.Fatalf("expected 2 usage records, got %d", len(store.usage))
	}
}
