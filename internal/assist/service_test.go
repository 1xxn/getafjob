package assist_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/assist"
)

type mockStore struct {
	pairings map[string]assist.PairingCode
	tokens   map[string]assist.Token
}

func newMockStore() *mockStore {
	return &mockStore{
		pairings: make(map[string]assist.PairingCode),
		tokens:   make(map[string]assist.Token),
	}
}

func (m *mockStore) CreatePairingCode(_ context.Context, p assist.PairingCode) error {
	m.pairings[string(p.CodeHash)] = p
	return nil
}

func (m *mockStore) GetPairingCode(_ context.Context, codeHash []byte, now time.Time) (assist.PairingCode, error) {
	p, ok := m.pairings[string(codeHash)]
	if !ok || p.ExpiresAt.Before(now) {
		return assist.PairingCode{}, assist.ErrNotFound
	}
	return p, nil
}

func (m *mockStore) DeletePairingCode(_ context.Context, id uuid.UUID) error {
	for k, p := range m.pairings {
		if p.ID == id {
			delete(m.pairings, k)
		}
	}
	return nil
}

func (m *mockStore) CreateExtensionToken(_ context.Context, t assist.Token) error {
	m.tokens[string(t.TokenHash)] = t
	return nil
}

func (m *mockStore) GetExtensionToken(_ context.Context, tokenHash []byte, now time.Time) (assist.Token, error) {
	t, ok := m.tokens[string(tokenHash)]
	if !ok || t.ExpiresAt.Before(now) {
		return assist.Token{}, assist.ErrNotFound
	}
	return t, nil
}

func (m *mockStore) TouchExtensionToken(_ context.Context, tokenHash []byte, now time.Time) error {
	if t, ok := m.tokens[string(tokenHash)]; ok {
		t.LastUsedAt = &now
		m.tokens[string(tokenHash)] = t
	}
	return nil
}

func (m *mockStore) ListExtensionTokens(_ context.Context, userID uuid.UUID) ([]assist.Token, error) {
	var res []assist.Token
	for _, t := range m.tokens {
		if t.UserID == userID {
			res = append(res, t)
		}
	}
	return res, nil
}

func (m *mockStore) RevokeExtensionToken(_ context.Context, id, userID uuid.UUID) error {
	for k, t := range m.tokens {
		if t.ID == id && t.UserID == userID {
			delete(m.tokens, k)
		}
	}
	return nil
}

type mockProfileReader struct {
	facts   map[string]string
	answers []assist.Answer
	texts   []assist.TextBlock
	docs    []assist.Document
}

func (m *mockProfileReader) ReadAssistProfile(_ context.Context, _ uuid.UUID) (map[string]string, []assist.Answer, []assist.TextBlock, []assist.Document, error) {
	return m.facts, m.answers, m.texts, m.docs, nil
}

type mockAppService struct {
	approvals []assist.ApprovalItem
	submitted []string
}

func (m *mockAppService) ListPendingApprovals(_ context.Context, _ uuid.UUID) ([]assist.ApprovalItem, error) {
	return m.approvals, nil
}

func (m *mockAppService) DecideApproval(_ context.Context, _, appID uuid.UUID, _ bool) error {
	for i, a := range m.approvals {
		if a.ID == appID {
			m.approvals = append(m.approvals[:i], m.approvals[i+1:]...)
			return nil
		}
	}
	return nil
}

func (m *mockAppService) RecordExternalSubmit(_ context.Context, _ uuid.UUID, pageURL, company, _ string, _ int, _ string) (uuid.UUID, error) {
	m.submitted = append(m.submitted, company+"@"+pageURL)
	return uuid.New(), nil
}

type mockUserReader struct {
	name string
}

func (m *mockUserReader) GetUserName(_ context.Context, _ uuid.UUID) (string, error) {
	return m.name, nil
}

func TestAssistServicePairingAndAuth(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()
	userReader := &mockUserReader{name: "Alice Developer"}
	svc := assist.NewService(store, nil, nil, userReader, nil)

	userID := uuid.New()
	code, err := svc.GeneratePairingCode(ctx, userID)
	if err != nil {
		t.Fatalf("GeneratePairingCode() err = %v", err)
	}

	// Pair with valid code
	tok, userName, err := svc.Pair(ctx, code, "Chrome Extension v1")
	if err != nil {
		t.Fatalf("Pair() err = %v", err)
	}
	if userName != "Alice Developer" {
		t.Errorf("expected user name 'Alice Developer', got %q", userName)
	}
	if tok.UserID != userID || tok.TokenPlain == "" {
		t.Fatalf("unexpected token returned: %+v", tok)
	}

	// Pairing code is single use; second pair must fail
	if _, _, err := svc.Pair(ctx, code, "Duplicate"); !errors.Is(err, assist.ErrInvalidPairingCode) {
		t.Fatalf("expected ErrInvalidPairingCode, got %v", err)
	}

	// Authenticate bearer token
	authTok, err := svc.Authenticate(ctx, tok.TokenPlain)
	if err != nil {
		t.Fatalf("Authenticate() err = %v", err)
	}
	if authTok.ID != tok.ID || authTok.UserID != userID {
		t.Fatalf("unexpected authenticated token: %+v", authTok)
	}

	// Invalid token fails
	if _, err := svc.Authenticate(ctx, "invalid-token-value"); !errors.Is(err, assist.ErrInvalidToken) {
		t.Fatalf("expected ErrInvalidToken, got %v", err)
	}
}

func TestAssistServiceSubmitApproval(t *testing.T) {
	ctx := context.Background()
	apps := &mockAppService{}
	svc := assist.NewService(newMockStore(), nil, apps, nil, nil)
	userID := uuid.New()

	// Unapproved submit is refused per ADR 0016
	_, _, err := svc.Submit(ctx, userID, "https://example.com/jobs/1", "Acme Corp", "Go Engineer", 5, false, "")
	if !errors.Is(err, assist.ErrUnapprovedSubmit) {
		t.Fatalf("expected ErrUnapprovedSubmit, got %v", err)
	}

	// Approved submit succeeds
	appID, status, err := svc.Submit(ctx, userID, "https://example.com/jobs/1", "Acme Corp", "Go Engineer", 5, true, "")
	if err != nil {
		t.Fatalf("Submit() err = %v", err)
	}
	if appID == uuid.Nil || status != "submitted" {
		t.Fatalf("unexpected submit response: %v, %s", appID, status)
	}
	if len(apps.submitted) != 1 || apps.submitted[0] != "Acme Corp@https://example.com/jobs/1" {
		t.Fatalf("unexpected submitted records: %v", apps.submitted)
	}
}

func TestAssistServiceGetProfile(t *testing.T) {
	ctx := context.Background()
	pr := &mockProfileReader{
		facts:   map[string]string{"name": "Alice"},
		answers: []assist.Answer{{ID: "1", Question: "Q", Answer: "A"}},
		texts:   []assist.TextBlock{{ID: "1", Name: "Bio", Body: "Text"}},
		docs:    []assist.Document{{ID: "1", Name: "cv.pdf", Kind: "cv"}},
	}
	svc := assist.NewService(newMockStore(), pr, nil, nil, nil)
	userID := uuid.New()
	facts, answers, texts, docs, err := svc.GetProfile(ctx, userID)
	if err != nil {
		t.Fatalf("GetProfile() err = %v", err)
	}
	if facts["name"] != "Alice" || len(answers) != 1 || len(texts) != 1 || len(docs) != 1 {
		t.Fatalf("unexpected profile result: facts=%v, ans=%d, texts=%d, docs=%d", facts, len(answers), len(texts), len(docs))
	}
}
