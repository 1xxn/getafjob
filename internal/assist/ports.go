package assist

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Store persists pairing codes and extension tokens.
type Store interface {
	CreatePairingCode(ctx context.Context, p PairingCode) error
	// ConsumePairingCode redeems a valid code once (ErrNotFound when unknown, expired or already used).
	ConsumePairingCode(ctx context.Context, codeHash []byte, now time.Time) (PairingCode, error)
	CreateExtensionToken(ctx context.Context, t Token) error
	GetExtensionToken(ctx context.Context, tokenHash []byte, now time.Time) (Token, error)
	TouchExtensionToken(ctx context.Context, tokenHash []byte, now time.Time) error
	ListExtensionTokens(ctx context.Context, userID uuid.UUID) ([]Token, error)
	RevokeExtensionToken(ctx context.Context, id, userID uuid.UUID) error
}

// ProfileReader accesses user facts, answers, text blocks, and documents.
type ProfileReader interface {
	ReadAssistProfile(ctx context.Context, userID uuid.UUID) (facts map[string]string, answers []Answer, texts []TextBlock, docs []Document, err error)
}

// ApplicationService accesses pending approvals and records extension submissions.
type ApplicationService interface {
	ListPendingApprovals(ctx context.Context, userID uuid.UUID) ([]ApprovalItem, error)
	DecideApproval(ctx context.Context, userID, applicationID uuid.UUID, approve bool) error
	RecordExternalSubmit(ctx context.Context, userID uuid.UUID, pageURL, company, title string, fieldsFilled int, notes string) (uuid.UUID, error)
}

// UserReader resolves user display name.
type UserReader interface {
	GetUserName(ctx context.Context, userID uuid.UUID) (string, error)
}
