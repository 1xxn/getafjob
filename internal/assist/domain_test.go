package assist_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/assist"
)

func TestGenerateAndHashCode(t *testing.T) {
	code, hash, err := assist.GenerateCode()
	if err != nil {
		t.Fatalf("GenerateCode() err = %v", err)
	}
	if !strings.HasPrefix(code, "GAJ-") || len(code) != 8 {
		t.Fatalf("unexpected code format: %q", code)
	}

	h2 := assist.HashCode(code)
	if string(hash) != string(h2) {
		t.Fatalf("HashCode() does not match: %x != %x", hash, h2)
	}

	// Case-insensitivity check
	hLower := assist.HashCode(strings.ToLower(code))
	if string(hash) != string(hLower) {
		t.Fatalf("HashCode() is not case-insensitive")
	}
}

func TestNewToken(t *testing.T) {
	userID := uuid.New()
	now := time.Now()
	tok, err := assist.NewToken(userID, "TestClient", now, 24*time.Hour)
	if err != nil {
		t.Fatalf("NewToken() err = %v", err)
	}
	if tok.UserID != userID || tok.ClientName != "TestClient" {
		t.Fatalf("unexpected token fields: %+v", tok)
	}
	if len(tok.TokenPlain) != 64 { // 32 hex bytes = 64 chars
		t.Fatalf("unexpected plain token length: %d", len(tok.TokenPlain))
	}

	h := assist.HashToken(tok.TokenPlain)
	if string(tok.TokenHash) != string(h) {
		t.Fatalf("HashToken() mismatch")
	}
}

func TestMatchForm(t *testing.T) {
	facts := map[string]string{
		"personal.first_name": "Jane",
		"personal.last_name":  "Doe",
		"personal.email":      "jane.doe@example.com",
		"personal.phone":      "+49 170 1234567",
	}
	answers := []assist.Answer{
		{
			ID:       "ans-1",
			Question: "Sponsorship required?",
			Answer:   "No, authorized to work indefinitely.",
		},
	}

	fields := []assist.FormField{
		{ID: "f1", Name: "first_name", Label: "First Name", Type: "text", Required: true},
		{ID: "f2", Name: "email", Label: "E-Mail Address", Type: "email", Required: true},
		{ID: "f3", Name: "custom_q", Label: "Sponsorship required?", Type: "text", Required: true},
		{ID: "f4", Name: "salary", Label: "Expected Salary", Type: "number", Required: true},
	}

	res := assist.MatchForm(fields, facts, answers, nil)
	if len(res.Matches) != 3 {
		t.Fatalf("expected 3 matches, got %d", len(res.Matches))
	}
	if len(res.MissingRequired) != 1 || res.MissingRequired[0] != "Expected Salary" {
		t.Fatalf("expected missing salary, got %v", res.MissingRequired)
	}
	if !res.RequiresApproval {
		t.Fatalf("expected RequiresApproval to be true")
	}

	// Verify matches
	m0 := res.Matches[0]
	if m0.FieldID != "f1" || m0.Value != "Jane" || m0.Provenance != "profile:personal.first_name" {
		t.Fatalf("unexpected match 0: %+v", m0)
	}
	m1 := res.Matches[1]
	if m1.FieldID != "f2" || m1.Value != "jane.doe@example.com" {
		t.Fatalf("unexpected match 1: %+v", m1)
	}
	m2 := res.Matches[2]
	if m2.FieldID != "f3" || m2.Value != "No, authorized to work indefinitely." || m2.Source != "answer_bank" {
		t.Fatalf("unexpected match 2: %+v", m2)
	}
}
