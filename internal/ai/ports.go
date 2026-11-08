package ai

import (
	"context"

	"github.com/google/uuid"
)

// Client abstracts the external LLM provider (Anthropic API).
type Client interface {
	CompletePlan(ctx context.Context, apiKey, model string, req PlanRequest) (PlanResult, int, int, error)
	CompleteDraft(ctx context.Context, apiKey, model string, req DraftRequest) (DraftResult, int, int, error)
	CompleteExtract(ctx context.Context, apiKey, model string, req ExtractRequest) (ExtractResult, int, int, error)
	CompleteClassify(ctx context.Context, apiKey, model string, req ClassifyRequest) (ClassifyResult, int, int, error)
}

// Store persists user AI settings and logs token usage for auditing and budgets.
type Store interface {
	GetSettings(ctx context.Context, userID uuid.UUID) (UserSettings, []byte, error)
	SaveSettings(ctx context.Context, userID uuid.UUID, enabled bool, encryptedKey []byte) error
	IncrementSpent(ctx context.Context, userID uuid.UUID, costCents int) error
	RecordUsage(ctx context.Context, userID uuid.UUID, task TaskType, model string, tokensIn, tokensOut, costCents int) error
}

// KeyCipher seals and opens personal Anthropic API keys using envelope encryption (ADR 0006).
type KeyCipher interface {
	SealKey(ctx context.Context, userID uuid.UUID, key string) ([]byte, error)
	OpenKey(ctx context.Context, userID uuid.UUID, ciphertext []byte) (string, error)
}
