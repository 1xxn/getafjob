package privacy

import (
	"context"

	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/application"
	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/matching"
	"github.com/1xxn/getafjob/internal/notify"
	"github.com/1xxn/getafjob/internal/profile"
	"github.com/1xxn/getafjob/internal/tracking"
)

// IdentityService supplies identity data and handles user lifecycle.
type IdentityService interface {
	UserByID(ctx context.Context, id uuid.UUID) (identity.User, error)
	Passkeys(ctx context.Context, userID uuid.UUID) ([]identity.PasskeyInfo, error)
	Sessions(ctx context.Context, userID uuid.UUID, currentToken string) ([]identity.SessionInfo, error)
	AuditEvents(ctx context.Context, userID uuid.UUID) ([]identity.AuditEntry, error)
	DeleteUser(ctx context.Context, userID uuid.UUID) error
	Cleanup(ctx context.Context) error
}

// ProfileService supplies the personal applicant profile.
type ProfileService interface {
	Get(ctx context.Context, userID uuid.UUID) (profile.Snapshot, error)
}

// StationService supplies the work history and education.
type StationService interface {
	WorkExperiences(ctx context.Context, userID uuid.UUID) ([]profile.WorkExperience, error)
	Educations(ctx context.Context, userID uuid.UUID) ([]profile.EducationStation, error)
}

// Extras are the further parts of the export; a nil field leaves its part out.
type Extras struct {
	Notes     NoteReader
	Searches  SearchLister
	Blocklist BlocklistReader
	Notify    NotifyReader
	Mailbox   MailboxReader
}

// NoteReader returns the private notes of an application (application.ErrNotFound when there are none).
type NoteReader interface {
	GetNotes(ctx context.Context, userID, appID uuid.UUID) (application.NoteData, error)
}

// SearchLister lists the saved searches.
type SearchLister interface {
	List(ctx context.Context, userID uuid.UUID) ([]matching.SavedSearch, error)
}

// BlocklistReader lists the blocked employers and keywords.
type BlocklistReader interface {
	Blocklist(ctx context.Context, userID uuid.UUID) ([]matching.BlocklistItem, error)
}

// NotifyReader returns the notification settings.
type NotifyReader interface {
	GetPreferences(ctx context.Context, userID uuid.UUID) (notify.Preferences, error)
}

// MailboxReader returns the inbox tracking settings and suggestions.
type MailboxReader interface {
	GetMailbox(ctx context.Context, userID uuid.UUID) (tracking.MailboxAccount, error)
	Suggestions(ctx context.Context, userID uuid.UUID, status string) ([]tracking.StatusSuggestion, error)
}

// ItemService supplies answer bank and text blocks.
type ItemService interface {
	Answers(ctx context.Context, userID uuid.UUID) ([]profile.Answer, error)
	TextBlocks(ctx context.Context, userID uuid.UUID) ([]profile.TextBlock, error)
}

// DocumentService manages the user's uploaded files.
type DocumentService interface {
	List(ctx context.Context, userID uuid.UUID) ([]profile.Document, error)
	Read(ctx context.Context, userID, id uuid.UUID) (profile.Document, []byte, error)
}

// ApplicationService supplies application history and screenshot keys.
type ApplicationService interface {
	UserApplications(ctx context.Context, userID uuid.UUID) ([]application.ApplicationSummary, error)
	UserScreenshots(ctx context.Context, userID uuid.UUID) ([]string, error)
	Settings(ctx context.Context, userID uuid.UUID) (application.Settings, error)
	PurgeExpiredScreenshots(ctx context.Context, blobs application.ScreenshotDeleter) (int, error)
}

// MatchingService supplies matched postings.
type MatchingService interface {
	UserMatches(ctx context.Context, userID uuid.UUID) ([]matching.MatchSummary, error)
}

// KeyManager handles cryptographic shredding of per-user keys.
type KeyManager interface {
	Forget(ctx context.Context, userID uuid.UUID) (bool, error)
}

// BlobStore removes stored files and screenshots.
type BlobStore interface {
	Delete(ctx context.Context, userID uuid.UUID, key string) error
}
