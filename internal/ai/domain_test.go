package ai

import (
	"testing"
)

func TestCalculateCostCents(t *testing.T) {
	tests := []struct {
		name      string
		model     string
		tokensIn  int
		tokensOut int
		wantCents int
	}{
		{"zero tokens", ModelHaiku, 0, 0, 0},
		{"minimal tokens rounded up", ModelHaiku, 10, 10, 1},
		{"haiku 100k in 10k out", ModelHaiku, 100_000, 10_000, 2},       // 0.01 + 0.005 = $0.015 -> 2 cents
		{"haiku long prompt", ModelHaiku, 200_000, 10_000, 13},          // 0.10 + 0.025 = $0.125 -> 13 cents
		{"sonnet 100k in 10k out", ModelSonnet, 100_000, 10_000, 30},    // 0.20 + 0.10 = $0.30
		{"unknown priced high", "claude-future-x", 100_000, 10_000, 60}, // 0.40 + 0.20 = $0.60
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CalculateCostCents(tt.model, tt.tokensIn, tt.tokensOut)
			if got != tt.wantCents {
				t.Errorf("CalculateCostCents(%s, %d, %d) = %d cents, want %d", tt.model, tt.tokensIn, tt.tokensOut, got, tt.wantCents)
			}
		})
	}
}

func TestVerifyPlanProvenance(t *testing.T) {
	facts := []ProfileFact{
		{Key: "personal.email", Value: "alice@example.com"},
		{Key: "personal.phone", Value: "+49 170 1234567"},
		{Key: "work.current_title", Value: "Site Reliability Engineer"},
	}
	answers := []AnswerFact{
		{Question: "Why work with us?", Answer: "I am passionate about infrastructure reliability and high throughput systems."},
	}

	plan := &PlanResult{
		Fields: []PlannedField{
			{FieldID: "email", Value: "alice@example.com"},
			{FieldID: "phone", Value: "+49 170 1234567"},
			{FieldID: "title", Value: "Site Reliability Engineer"},
			{FieldID: "why", Value: "I am passionate about infrastructure reliability and high throughput systems."},
			{FieldID: "unknown_field", Value: "Unverified hallucinated data"},
		},
	}

	VerifyPlanProvenance(plan, facts, answers)

	if plan.Fields[0].Provenance != "personal.email" || plan.Fields[0].RequiresApproval {
		t.Errorf("expected personal.email provenance, got %+v", plan.Fields[0])
	}
	if plan.Fields[1].Provenance != "personal.phone" || plan.Fields[1].RequiresApproval {
		t.Errorf("expected personal.phone provenance, got %+v", plan.Fields[1])
	}
	if plan.Fields[2].Provenance != "work.current_title" || plan.Fields[2].RequiresApproval {
		t.Errorf("expected work.current_title provenance, got %+v", plan.Fields[2])
	}
	if plan.Fields[3].Provenance != "answer:Why work with us?" || plan.Fields[3].RequiresApproval {
		t.Errorf("expected answer provenance, got %+v", plan.Fields[3])
	}
	if !plan.Fields[4].RequiresApproval {
		t.Errorf("expected unverified field to require approval, got %+v", plan.Fields[4])
	}
}
