// Package ai provides the optional AI module (Planner, Drafter, Extractor, Classifier)
// using the Anthropic API with operator budget caps or user-supplied encrypted keys (ADR 0012, 0013, #44).
//
// In accordance with ADR 0012, everything in getafjob works without AI. When enabled:
//   - Outputs are strictly schema-validated.
//   - Values undergo provenance and fact checks against actual user facts (ADR 0013).
//   - Drafts ALWAYS require human approval before any application is submitted.
//   - Cover letters are strictly NOT generated (system-wide policy).
package ai

import (
	"errors"
	"strings"
	"unicode"
)

// TaskType identifies an AI operation for auditing and budget tracking.
type TaskType string

const (
	TaskPlan     TaskType = "plan"
	TaskDraft    TaskType = "draft"
	TaskExtract  TaskType = "extract"
	TaskClassify TaskType = "classify"
)

// ClassifyCategory identifies classified applicant communication status.
type ClassifyCategory string

const (
	CategoryRejection    ClassifyCategory = "rejection"
	CategoryInvitation   ClassifyCategory = "invitation"
	CategoryInterview    ClassifyCategory = "interview"
	CategoryOffer        ClassifyCategory = "offer"
	CategoryStatusUpdate ClassifyCategory = "status_update"
	CategoryUnrelated    ClassifyCategory = "unrelated"
)

// Standard Anthropic models.
const (
	ModelClaude35Haiku  = "claude-3-5-haiku-20241022"
	ModelClaude35Sonnet = "claude-3-5-sonnet-20241022"
	ModelClaude3Haiku   = "claude-3-haiku-20240307"
)

// Domain errors.
var (
	ErrDisabled           = errors.New("ai module is disabled by administrator")
	ErrUserAIDisabled     = errors.New("ai is not enabled in user settings")
	ErrBudgetExceeded     = errors.New("ai budget exceeded for user")
	ErrNoAPIKey           = errors.New("no anthropic api key configured")
	ErrInvalidSchema      = errors.New("ai response failed schema validation")
	ErrHallucination      = errors.New("ai response failed provenance fact check")
	ErrCoverLetterRefused = errors.New("cover letters are strictly not supported")
)

// FormField describes a field on a job application form.
type FormField struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Label    string   `json:"label"`
	Kind     string   `json:"kind"` // text, textarea, select, radio, checkbox, number, etc.
	Required bool     `json:"required"`
	Options  []string `json:"options,omitempty"`
}

// ProfileFact is a single factual datum from the user's verified profile.
type ProfileFact struct {
	Key      string `json:"key"` // e.g. "personal.first_name", "work.current_title"
	Value    string `json:"value"`
	Category string `json:"category,omitempty"` // personal, work, skill, education, etc.
}

// AnswerFact represents a verified answer from the user's answer bank.
type AnswerFact struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

// WorkExperience holds verified past employment facts.
type WorkExperience struct {
	Title      string   `json:"title"`
	Company    string   `json:"company"`
	Start      string   `json:"start"`
	End        string   `json:"end"`
	Highlights []string `json:"highlights,omitempty"`
}

// UserSettings represents the user's AI configuration.
type UserSettings struct {
	Enabled      bool `json:"enabled"`
	HasCustomKey bool `json:"has_custom_key"`
	SpentCents   int  `json:"spent_cents"`
	BudgetCents  int  `json:"budget_cents"`
}

// PlanRequest describes an application form to be planned using AI.
type PlanRequest struct {
	JobTitle       string        `json:"job_title"`
	Company        string        `json:"company"`
	JobDescription string        `json:"job_description,omitempty"`
	Fields         []FormField   `json:"fields"`
	Facts          []ProfileFact `json:"facts"`
	Answers        []AnswerFact  `json:"answers,omitempty"`
}

// PlannedField is a single planned form field value with explicit provenance.
type PlannedField struct {
	FieldID          string  `json:"field_id"`
	Value            string  `json:"value"`
	Provenance       string  `json:"provenance"` // e.g. "profile.work.current_title", "answer#1"
	Confidence       float64 `json:"confidence"`
	RequiresApproval bool    `json:"requires_approval"`
}

// PlanResult is the output of the AI Planner.
type PlanResult struct {
	Fields      []PlannedField `json:"fields"`
	Explanation string         `json:"explanation,omitempty"`
}

// DraftRequest requests a draft response for an open-ended application question.
type DraftRequest struct {
	Field          FormField        `json:"field"`
	JobTitle       string           `json:"job_title"`
	Company        string           `json:"company"`
	JobDescription string           `json:"job_description,omitempty"`
	Facts          []ProfileFact    `json:"facts"`
	Work           []WorkExperience `json:"work,omitempty"`
	Answers        []AnswerFact     `json:"answers,omitempty"`
	MaxLength      int              `json:"max_length,omitempty"`
}

// DraftResult represents a generated draft. In compliance with ADR 0012/0013,
// all drafts MUST have RequiresApproval = true.
type DraftResult struct {
	FieldID          string   `json:"field_id"`
	DraftText        string   `json:"draft_text"`
	FactsUsed        []string `json:"facts_used"`
	RequiresApproval bool     `json:"requires_approval"`
}

// ExtractRequest requests structured extraction from raw job posting text or HTML.
type ExtractRequest struct {
	Text string `json:"text"`
	HTML string `json:"html,omitempty"`
}

// ExtractResult holds structured information extracted from a job posting.
type ExtractResult struct {
	Title          string   `json:"title"`
	Company        string   `json:"company"`
	SalaryMin      *int     `json:"salary_min,omitempty"`
	SalaryMax      *int     `json:"salary_max,omitempty"`
	SalaryCurrency string   `json:"salary_currency,omitempty"`
	SalaryInterval string   `json:"salary_interval,omitempty"` // yearly, monthly, hourly
	Location       string   `json:"location,omitempty"`
	Remote         bool     `json:"remote"`
	Skills         []string `json:"skills,omitempty"`
	EmploymentType string   `json:"employment_type,omitempty"` // full_time, part_time, contract
}

// ClassifyRequest requests classification of an applicant communication (e.g. tracking email).
type ClassifyRequest struct {
	Subject string `json:"subject"`
	Sender  string `json:"sender"`
	Body    string `json:"body"`
}

// ClassifyResult is the classification outcome.
type ClassifyResult struct {
	Category   ClassifyCategory `json:"category"`
	Confidence float64          `json:"confidence"`
	Reasoning  string           `json:"reasoning,omitempty"`
}

// CalculateCostCents computes the cost in USD cents for token consumption.
// Prices per 1M tokens (cents):
//   - Claude 3.5 Haiku:  80c prompt, 400c completion
//   - Claude 3.5 Sonnet: 300c prompt, 1500c completion
//   - Claude 3 Haiku:    25c prompt, 125c completion
//   - Default:           300c prompt, 1500c completion
func CalculateCostCents(model string, tokensIn, tokensOut int) int {
	if tokensIn <= 0 && tokensOut <= 0 {
		return 0
	}
	var inCentsPerMTok, outCentsPerMTok int64
	switch {
	case strings.Contains(model, "3-5-haiku"):
		inCentsPerMTok = 80
		outCentsPerMTok = 400
	case strings.Contains(model, "3-5-sonnet"):
		inCentsPerMTok = 300
		outCentsPerMTok = 1500
	case strings.Contains(model, "3-haiku"):
		inCentsPerMTok = 25
		outCentsPerMTok = 125
	default:
		inCentsPerMTok = 300
		outCentsPerMTok = 1500
	}

	totalMillionths := int64(tokensIn)*inCentsPerMTok + int64(tokensOut)*outCentsPerMTok
	if totalMillionths <= 0 {
		return 0
	}
	// Ceil division
	costCents := int((totalMillionths + 999_999) / 1_000_000)
	if costCents == 0 && (tokensIn > 0 || tokensOut > 0) {
		return 1
	}
	return costCents
}

// VerifyPlanProvenance checks that planned field values are grounded in user facts.
// Automated values must correspond to a verified profile fact or answer bank entry.
// Values that cannot be proven must be marked as requiring approval.
func VerifyPlanProvenance(plan *PlanResult, facts []ProfileFact, answers []AnswerFact) {
	factMap := make(map[string]string, len(facts))
	for _, f := range facts {
		factMap[clean(f.Value)] = f.Key
	}
	answerMap := make(map[string]string, len(answers))
	for _, a := range answers {
		answerMap[clean(a.Answer)] = "answer:" + a.Question
	}

	for i := range plan.Fields {
		pf := &plan.Fields[i]
		cVal := clean(pf.Value)
		if cVal == "" {
			continue
		}
		if key, ok := factMap[cVal]; ok {
			pf.Provenance = key
			continue
		}
		if key, ok := answerMap[cVal]; ok {
			pf.Provenance = key
			continue
		}
		// If value was not directly found in facts, it must require manual approval
		pf.RequiresApproval = true
		if pf.Provenance == "" {
			pf.Provenance = "ai_proposed (unverified)"
		}
	}
}

// clean normalizes a string for loose comparison (whitespace, lowercasing).
func clean(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if !unicode.IsSpace(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}
