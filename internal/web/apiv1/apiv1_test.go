package apiv1

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/assist"
)

type fakeAssistService struct {
	pairedToken assist.Token
	userName    string
	facts       map[string]string
	answers     []assist.Answer
	texts       []assist.TextBlock
	docs        []assist.Document
	submitted   bool
	approvals   []assist.ApprovalItem
}

func (f *fakeAssistService) Pair(_ context.Context, code, clientName string) (assist.Token, string, error) {
	if code != "GAJ-TEST" {
		return assist.Token{}, "", assist.ErrInvalidPairingCode
	}
	f.pairedToken = assist.Token{
		ID:         uuid.New(),
		UserID:     uuid.New(),
		TokenPlain: "secret-bearer-token-12345",
		ClientName: clientName,
		ExpiresAt:  time.Now().Add(24 * time.Hour),
	}
	f.userName = "Alice"
	return f.pairedToken, f.userName, nil
}

func (f *fakeAssistService) Authenticate(_ context.Context, tokenPlain string) (assist.Token, error) {
	if tokenPlain == f.pairedToken.TokenPlain && tokenPlain != "" {
		return f.pairedToken, nil
	}
	return assist.Token{}, assist.ErrInvalidToken
}

func (f *fakeAssistService) GetProfile(_ context.Context, userID uuid.UUID) (map[string]string, []assist.Answer, []assist.TextBlock, []assist.Document, error) {
	if userID != f.pairedToken.UserID {
		return nil, nil, nil, nil, assist.ErrNotFound
	}
	return f.facts, f.answers, f.texts, f.docs, nil
}

func (f *fakeAssistService) Inspect(_ context.Context, _ uuid.UUID, _, _ string, fields []assist.FormField) (assist.InspectResult, error) {
	return assist.MatchForm(fields, f.facts, f.answers, f.texts), nil
}

func (f *fakeAssistService) Submit(_ context.Context, _ uuid.UUID, _, _, _ string, _ int, approved bool, _ string) (uuid.UUID, string, error) {
	if !approved {
		return uuid.Nil, "", assist.ErrUnapprovedSubmit
	}
	f.submitted = true
	return uuid.New(), "submitted", nil
}

func (f *fakeAssistService) ListApprovals(_ context.Context, _ uuid.UUID) ([]assist.ApprovalItem, error) {
	return f.approvals, nil
}

func (f *fakeAssistService) DecideApproval(_ context.Context, _, appID uuid.UUID, _ string) error {
	for i, a := range f.approvals {
		if a.ID == appID {
			f.approvals = append(f.approvals[:i], f.approvals[i+1:]...)
			return nil
		}
	}
	return nil
}

func TestGetMeta(t *testing.T) {
	mux := http.NewServeMux()
	Mount(mux, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/meta", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var m Meta
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m.Api != V1 || m.Version == "" {
		t.Fatalf("meta = %+v", m)
	}
}

func TestUnknownRouteIs404(t *testing.T) {
	mux := http.NewServeMux()
	Mount(mux, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestAssistEndpoints(t *testing.T) {
	svc := &fakeAssistService{
		facts: map[string]string{
			"personal.first_name": "Bob",
			"personal.email":      "bob@example.com",
		},
		answers: []assist.Answer{
			{ID: "ans-1", Question: "Years of Go?", Answer: "5"},
		},
		approvals: []assist.ApprovalItem{
			{ID: uuid.New(), Company: "Acme", Title: "Dev", Score: 85, CreatedAt: time.Now()},
		},
	}

	mux := http.NewServeMux()
	Mount(mux, svc)

	// 1. Unauthorized access to /assist/profile
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/assist/profile", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}

	// 2. Pair extension with invalid code
	body, _ := json.Marshal(PairRequest{Code: "WRONG", ClientName: "Firefox"})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/assist/pair", bytes.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}

	// 3. Pair extension with valid code
	body, _ = json.Marshal(PairRequest{Code: "GAJ-TEST", ClientName: "Chrome 130"})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/assist/pair", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("pair status = %d: %s", rec.Code, rec.Body.String())
	}
	var pairResp PairResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &pairResp)
	if pairResp.Token == "" || pairResp.UserName != "Alice" {
		t.Fatalf("unexpected pair resp: %+v", pairResp)
	}

	bearer := "Bearer " + pairResp.Token

	// 4. Authorized access to /assist/profile
	req := httptest.NewRequest(http.MethodGet, "/api/v1/assist/profile", nil)
	req.Header.Set("Authorization", bearer)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("profile status = %d: %s", rec.Code, rec.Body.String())
	}

	// 5. Inspect form fields
	label := "First Name"
	inspectReq := InspectRequest{
		Url:   "https://jobs.example.org/apply",
		Title: "Software Engineer",
		Fields: []FormField{
			{Id: "field-1", Name: "first_name", Label: &label, Type: "text", Required: true},
		},
	}
	body, _ = json.Marshal(inspectReq)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/assist/inspect", bytes.NewReader(body))
	req.Header.Set("Authorization", bearer)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("inspect status = %d: %s", rec.Code, rec.Body.String())
	}
	var inspectResp InspectResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &inspectResp)
	if len(inspectResp.Matches) != 1 || inspectResp.Matches[0].Value != "Bob" {
		t.Fatalf("unexpected inspect resp: %+v", inspectResp)
	}

	// 6. Submit without approval fails
	subReq := SubmitRequest{
		Url:          "https://jobs.example.org/apply",
		Company:      "Acme",
		Title:        "Dev",
		FieldsFilled: 1,
		Approved:     false,
	}
	body, _ = json.Marshal(subReq)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/assist/submit", bytes.NewReader(body))
	req.Header.Set("Authorization", bearer)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unapproved submit, got %d", rec.Code)
	}

	// 7. Submit with approval succeeds
	subReq.Approved = true
	body, _ = json.Marshal(subReq)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/assist/submit", bytes.NewReader(body))
	req.Header.Set("Authorization", bearer)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("submit status = %d: %s", rec.Code, rec.Body.String())
	}
	if !svc.submitted {
		t.Fatalf("expected service to record submit")
	}

	// 8. List approvals
	req = httptest.NewRequest(http.MethodGet, "/api/v1/assist/approvals", nil)
	req.Header.Set("Authorization", bearer)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("approvals status = %d: %s", rec.Code, rec.Body.String())
	}

	// 9. Decide approval
	decideReq := ApprovalDecisionRequest{Decision: Approve}
	body, _ = json.Marshal(decideReq)
	appID := svc.approvals[0].ID
	req = httptest.NewRequest(http.MethodPost, "/api/v1/assist/approvals/"+appID.String()+"/decide", bytes.NewReader(body))
	req.Header.Set("Authorization", bearer)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("decide status = %d: %s", rec.Code, rec.Body.String())
	}
}

// Pairing attempts are rate limited, so pairing codes cannot be guessed by trying many.
func TestPairIsRateLimited(t *testing.T) {
	mux := http.NewServeMux()
	Mount(mux, &fakeAssistService{})
	codes := map[int]int{}
	for range 15 {
		body, _ := json.Marshal(PairRequest{Code: "WRONG", ClientName: "Firefox"})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/assist/pair", bytes.NewReader(body)))
		codes[rec.Code]++
	}
	if codes[http.StatusTooManyRequests] == 0 || codes[http.StatusBadRequest] > 10 {
		t.Fatalf("status counts = %v, want at most 10 tries and then 429", codes)
	}
}
