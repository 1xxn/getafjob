package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/1xxn/getafjob/internal/ai"
)

func TestClientNoAPIKey(t *testing.T) {
	c := NewClient()
	_, _, _, err := c.CompletePlan(context.Background(), "", ai.ModelHaiku, ai.PlanRequest{})
	if !errors.Is(err, ai.ErrNoAPIKey) {
		t.Fatalf("expected ErrNoAPIKey, got %v", err)
	}
}

func TestClientAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"type": "error",
			"error": map[string]any{
				"type":    "authentication_error",
				"message": "invalid x-api-key",
			},
		})
	}))
	defer srv.Close()

	c := NewClient(WithBaseURL(srv.URL))
	_, _, _, err := c.CompletePlan(context.Background(), "sk-bad-key", ai.ModelHaiku, ai.PlanRequest{})
	if err == nil || !strings.Contains(err.Error(), "authentication_error") {
		t.Fatalf("expected authentication_error, got %v", err)
	}
}

func TestClientCompletePlan(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "sk-test-key" {
			t.Errorf("unexpected api key: %s", r.Header.Get("x-api-key"))
		}
		if r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Errorf("unexpected version header: %s", r.Header.Get("anthropic-version"))
		}
		var sent apiRequest
		if err := json.NewDecoder(r.Body).Decode(&sent); err != nil || sent.Model != "claude-haiku-5-5" ||
			sent.Thinking == nil || sent.Thinking.Type != "disabled" || sent.ToolChoice == nil {
			t.Errorf("request = %+v, %v; want the current model, thinking off and a forced tool", sent, err)
		}

		planInput := map[string]any{
			"explanation": "Matched verified profile facts",
			"fields": []map[string]any{
				{
					"field_id":   "first_name",
					"value":      "Alice",
					"provenance": "personal.first_name",
					"confidence": 1.0,
				},
			},
		}
		rawInput, _ := json.Marshal(planInput)

		resp := apiResponse{
			ID:   "msg_123",
			Type: "message",
			Role: "assistant",
			Content: []contentBlock{
				{
					Type:  "tool_use",
					ID:    "toolu_123",
					Name:  "submit_plan",
					Input: rawInput,
				},
			},
			Usage: usage{InputTokens: 150, OutputTokens: 60},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := NewClient(WithBaseURL(srv.URL))
	res, inTok, outTok, err := c.CompletePlan(context.Background(), "sk-test-key", ai.ModelHaiku, ai.PlanRequest{})
	if err != nil {
		t.Fatalf("CompletePlan: %v", err)
	}
	if inTok != 150 || outTok != 60 {
		t.Errorf("tokens = %d, %d, want 150, 60", inTok, outTok)
	}
	if len(res.Fields) != 1 || res.Fields[0].Value != "Alice" {
		t.Errorf("unexpected fields: %+v", res.Fields)
	}
}

func TestClientCompleteDraft(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		draftInput := map[string]any{
			"draft_text": "I have 5 years of experience deploying Go services to Kubernetes.",
			"facts_used": []string{"work.title", "skills.go"},
		}
		rawInput, _ := json.Marshal(draftInput)

		resp := apiResponse{
			Content: []contentBlock{
				{
					Type:  "tool_use",
					Name:  "submit_draft",
					Input: rawInput,
				},
			},
			Usage: usage{InputTokens: 200, OutputTokens: 80},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := NewClient(WithBaseURL(srv.URL))
	res, _, _, err := c.CompleteDraft(context.Background(), "sk-test", ai.ModelHaiku, ai.DraftRequest{
		Field: ai.FormField{ID: "f_exp"},
	})
	if err != nil {
		t.Fatalf("CompleteDraft: %v", err)
	}
	if res.FieldID != "f_exp" || !res.RequiresApproval {
		t.Errorf("unexpected draft result: %+v", res)
	}
}

func TestClientCompleteExtract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		minS, maxS := 90000, 110000
		extractInput := map[string]any{
			"title":           "DevOps Specialist",
			"company":         "Acme Tech",
			"salary_min":      minS,
			"salary_max":      maxS,
			"salary_currency": "EUR",
			"remote":          true,
			"skills":          []string{"Kubernetes", "Terraform"},
		}
		rawInput, _ := json.Marshal(extractInput)

		resp := apiResponse{
			Content: []contentBlock{
				{
					Type:  "tool_use",
					Name:  "submit_extraction",
					Input: rawInput,
				},
			},
			Usage: usage{InputTokens: 300, OutputTokens: 100},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := NewClient(WithBaseURL(srv.URL))
	res, _, _, err := c.CompleteExtract(context.Background(), "sk-test", ai.ModelHaiku, ai.ExtractRequest{
		Text: "DevOps Specialist at Acme Tech in Berlin or Remote. Salary 90000 - 110000 EUR.",
	})
	if err != nil {
		t.Fatalf("CompleteExtract: %v", err)
	}
	if res.Title != "DevOps Specialist" || res.Company != "Acme Tech" || !res.Remote || *res.SalaryMin != 90000 {
		t.Errorf("unexpected extract result: %+v", res)
	}
}

func TestClientCompleteClassify(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		classifyInput := map[string]any{
			"category":   "invitation",
			"confidence": 0.98,
			"reasoning":  "Email invites candidate for an interview next week.",
		}
		rawInput, _ := json.Marshal(classifyInput)

		resp := apiResponse{
			Content: []contentBlock{
				{
					Type:  "tool_use",
					Name:  "submit_classification",
					Input: rawInput,
				},
			},
			Usage: usage{InputTokens: 250, OutputTokens: 40},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := NewClient(WithBaseURL(srv.URL))
	res, _, _, err := c.CompleteClassify(context.Background(), "sk-test", ai.ModelHaiku, ai.ClassifyRequest{
		Subject: "Invitation for interview",
		Body:    "We would love to invite you for a virtual interview.",
	})
	if err != nil {
		t.Fatalf("CompleteClassify: %v", err)
	}
	if res.Category != ai.CategoryInvitation || res.Confidence < 0.95 {
		t.Errorf("unexpected classify result: %+v", res)
	}
}
