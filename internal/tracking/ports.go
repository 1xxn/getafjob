package tracking

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound reports a missing mailbox or suggestion.
var ErrNotFound = errors.New("tracking record not found")

// MailboxAccount holds user IMAP settings for tracking.
type MailboxAccount struct {
	UserID            uuid.UUID
	Host              string
	Port              int
	TLS               bool
	Username          string
	EncryptedPassword []byte
	Enabled           bool
	LastUID           uint32
	UIDValidity       uint32
	LastCheckedAt     *time.Time
	LastError         string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// MailboxClient communicates with an IMAP server read-only (EXAMINE, BODY.PEEK).
type MailboxClient interface {
	// FetchNewMessages returns emails with UID > acc.LastUID without modifying any mailbox flags.
	FetchNewMessages(ctx context.Context, acc MailboxAccount, password string) (messages []EmailMessage, newLastUID uint32, newUIDValidity uint32, err error)
	// TestConnection verifies credentials against the IMAP server.
	TestConnection(ctx context.Context, acc MailboxAccount, password string) error
}

// PasswordCipher seals and opens user mailbox passwords using envelope encryption.
type PasswordCipher interface {
	SealPassword(ctx context.Context, userID uuid.UUID, password string) ([]byte, error)
	OpenPassword(ctx context.Context, userID uuid.UUID, ciphertext []byte) (string, error)
}

// ApplicationUpdater executes status transitions on applications when suggestions are accepted.
type ApplicationUpdater interface {
	Transition(ctx context.Context, userID, appID uuid.UUID, state string, detail map[string]any) error
}

// Store persists mailbox configurations, cursors, and suggestions.
type Store interface {
	GetMailbox(ctx context.Context, userID uuid.UUID) (MailboxAccount, error)
	SaveMailbox(ctx context.Context, acc MailboxAccount) error
	UpdateMailboxCursor(ctx context.Context, userID uuid.UUID, lastUID, uidValidity uint32, at time.Time, lastErr string) error
	ListEnabledMailboxes(ctx context.Context) ([]MailboxAccount, error)
	ListActiveApplications(ctx context.Context, userID uuid.UUID) ([]ApplicationSummary, error)
	CreateSuggestion(ctx context.Context, s StatusSuggestion) error
	Suggestions(ctx context.Context, userID uuid.UUID, status string) ([]StatusSuggestion, error)
	SuggestionsForApplication(ctx context.Context, userID uuid.UUID, appID uuid.UUID) ([]StatusSuggestion, error)
	Suggestion(ctx context.Context, userID uuid.UUID, id uuid.UUID) (StatusSuggestion, error)
	SetSuggestionStatus(ctx context.Context, userID uuid.UUID, id uuid.UUID, status string) error
}
