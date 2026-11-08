package application

import (
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestAutonomySettings(t *testing.T) {
	if err := DefaultSettings.Validate(); err != nil {
		t.Fatalf("DefaultSettings invalid: %v", err)
	}
	if !DefaultSettings.AutoQueue() {
		t.Fatalf("DefaultSettings.AutoQueue() = false, want true")
	}

	suggest := Settings{Level: LevelSuggest, MinScore: 50, DailyLimit: 10}
	if err := suggest.Validate(); err != nil {
		t.Fatalf("suggest settings invalid: %v", err)
	}
	if suggest.AutoQueue() {
		t.Fatalf("suggest.AutoQueue() = true, want false")
	}

	bad := []Settings{
		{Level: "unknown", MinScore: 80, DailyLimit: 5},
		{Level: LevelAutonomous, MinScore: -1, DailyLimit: 5},
		{Level: LevelAutonomous, MinScore: 101, DailyLimit: 5},
		{Level: LevelAutonomous, MinScore: 80, DailyLimit: -1},
		{Level: LevelAutonomous, MinScore: 80, DailyLimit: 51},
	}
	for _, b := range bad {
		if err := b.Validate(); err == nil {
			t.Errorf("validate(%+v) = nil, want error", b)
		}
	}
}

func TestGreenRule(t *testing.T) {
	now := time.Now()
	validID := uuid.New().String()

	greenInput := func() RuleInput {
		return RuleInput{
			Settings: Settings{
				Level:      LevelAutonomous,
				MinScore:   80,
				DailyLimit: 5,
			},
			Score:   85,
			Matched: true,
			Candidate: Candidate{
				Excluded: false,
			},
			Filled: []SubmitField{
				{Kind: "text", Source: "profile.first_name"},
				{Kind: "textarea", Source: "textblock#" + validID},
				{Kind: "richtext", Source: "answer#" + validID},
			},
			OpenFields:       0,
			FormErrors:       0,
			Submits:          2,
			Adapter:          "greenhouse",
			AdapterRuns:      10,
			AdapterSubmitted: 9,
		}
	}

	t.Run("all green", func(t *testing.T) {
		in := greenInput()
		reasons := Rule(in, now)
		if len(reasons) > 0 {
			t.Fatalf("Rule() = %v, want empty (green)", reasons)
		}
	})

	t.Run("level not autonomous", func(t *testing.T) {
		in := greenInput()
		in.Settings.Level = LevelApprove
		reasons := Rule(in, now)
		if !slices.Contains(reasons, ReasonLevel) {
			t.Fatalf("Rule() = %v, missing ReasonLevel", reasons)
		}
	})

	t.Run("score below threshold", func(t *testing.T) {
		in := greenInput()
		in.Score = 75
		reasons := Rule(in, now)
		if !slices.Contains(reasons, ReasonScore) {
			t.Fatalf("Rule() = %v, missing ReasonScore", reasons)
		}
	})

	t.Run("not matched", func(t *testing.T) {
		in := greenInput()
		in.Matched = false
		reasons := Rule(in, now)
		if !slices.Contains(reasons, ReasonScore) {
			t.Fatalf("Rule() = %v, missing ReasonScore", reasons)
		}
	})

	t.Run("guard excluded", func(t *testing.T) {
		in := greenInput()
		in.Candidate.Excluded = true
		reasons := Rule(in, now)
		if !slices.Contains(reasons, ReasonExcluded) {
			t.Fatalf("Rule() = %v, missing ReasonExcluded", reasons)
		}
	})

	t.Run("guard duplicate", func(t *testing.T) {
		in := greenInput()
		in.Candidate.SameJob = []State{Submitted}
		reasons := Rule(in, now)
		if !slices.Contains(reasons, ReasonDuplicate) {
			t.Fatalf("Rule() = %v, missing ReasonDuplicate", reasons)
		}
	})

	t.Run("guard company limit", func(t *testing.T) {
		in := greenInput()
		in.Candidate.CompanyApplied = []time.Time{now.Add(-2 * time.Hour), now.Add(-3 * time.Hour)}
		reasons := Rule(in, now)
		if !slices.Contains(reasons, ReasonCompanyLimit) {
			t.Fatalf("Rule() = %v, missing ReasonCompanyLimit", reasons)
		}
	})

	t.Run("invalid provenance", func(t *testing.T) {
		in := greenInput()
		in.Filled = append(in.Filled, SubmitField{Kind: "text", Source: "untrusted_guess"})
		reasons := Rule(in, now)
		if !slices.Contains(reasons, ReasonProvenance) {
			t.Fatalf("Rule() = %v, missing ReasonProvenance", reasons)
		}
	})

	t.Run("free text without approved user source", func(t *testing.T) {
		in := greenInput()
		in.Filled = append(in.Filled, SubmitField{Kind: "textarea", Source: "profile.summary"})
		reasons := Rule(in, now)
		if !slices.Contains(reasons, ReasonFreeText) {
			t.Fatalf("Rule() = %v, missing ReasonFreeText", reasons)
		}
	})

	t.Run("open required fields", func(t *testing.T) {
		in := greenInput()
		in.OpenFields = 1
		reasons := Rule(in, now)
		if !slices.Contains(reasons, ReasonOpenQuestion) {
			t.Fatalf("Rule() = %v, missing ReasonOpenQuestion", reasons)
		}
	})

	t.Run("form errors", func(t *testing.T) {
		in := greenInput()
		in.FormErrors = 2
		reasons := Rule(in, now)
		if !slices.Contains(reasons, ReasonFormErrors) {
			t.Fatalf("Rule() = %v, missing ReasonFormErrors", reasons)
		}
	})

	t.Run("quota used up", func(t *testing.T) {
		in := greenInput()
		in.Submits = 5
		reasons := Rule(in, now)
		if !slices.Contains(reasons, ReasonQuota) {
			t.Fatalf("Rule() = %v, missing ReasonQuota", reasons)
		}
	})

	t.Run("adapter maturity runs too low", func(t *testing.T) {
		in := greenInput()
		in.AdapterRuns = 4
		in.AdapterSubmitted = 4
		reasons := Rule(in, now)
		if !slices.Contains(reasons, ReasonMaturity) {
			t.Fatalf("Rule() = %v, missing ReasonMaturity", reasons)
		}
	})

	t.Run("adapter maturity rate too low", func(t *testing.T) {
		in := greenInput()
		in.AdapterRuns = 10
		in.AdapterSubmitted = 7 // 70% < 80%
		reasons := Rule(in, now)
		if !slices.Contains(reasons, ReasonMaturity) {
			t.Fatalf("Rule() = %v, missing ReasonMaturity", reasons)
		}
	})
}
