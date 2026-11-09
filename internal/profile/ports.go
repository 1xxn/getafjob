package profile

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Stored is the raw state of a user's profile: the sealed profile and the readable preferences.
type Stored struct {
	Sealed      []byte // nil: no profile yet
	Preferences []byte // JSON, nil: none yet
	Version     int64  // 0: nothing stored
	UpdatedAt   time.Time
}

// Store persists profiles per user (row-level security).
type Store interface {
	Load(ctx context.Context, userID uuid.UUID) (Stored, error)
	// SaveProfile stores sealed if the stored version is still version; false when someone saved in between.
	SaveProfile(ctx context.Context, userID uuid.UUID, sealed []byte, version int64, at time.Time) (bool, error)
	SavePreferences(ctx context.Context, userID uuid.UUID, prefs []byte, at time.Time) error
}

// StoredDocument is a document row; SealedName still needs opening.
type StoredDocument struct {
	Document
	SealedName []byte
}

// DocumentStore persists document metadata per user (row-level security).
type DocumentStore interface {
	InsertDocument(ctx context.Context, userID uuid.UUID, d StoredDocument) error
	Documents(ctx context.Context, userID uuid.UUID) ([]StoredDocument, error)
	Document(ctx context.Context, userID, id uuid.UUID) (StoredDocument, error) // ErrNotFound
	CountDocuments(ctx context.Context, userID uuid.UUID) (int, error)
	UpdateVariant(ctx context.Context, userID, id uuid.UUID, label, language string, focus []string) (bool, error)
	DeleteDocument(ctx context.Context, userID, id uuid.UUID) (bool, error)
}

// Files keeps file contents sealed per user (blob.Sealed).
type Files interface {
	Put(ctx context.Context, userID uuid.UUID, key string, data []byte) error
	Get(ctx context.Context, userID uuid.UUID, key string) ([]byte, error)
	Delete(ctx context.Context, userID uuid.UUID, key string) error
}

// Sealer encrypts per user (crypto.Envelope).
type Sealer interface {
	Seal(ctx context.Context, userID uuid.UUID, purpose string, plaintext []byte) ([]byte, error)
	Open(ctx context.Context, userID uuid.UUID, purpose string, ciphertext []byte) ([]byte, error)
}

// StoredItem is a sealed answer or text block.
type StoredItem struct {
	ID        uuid.UUID
	Sealed    []byte
	UpdatedAt time.Time
}

// ItemStore persists answers and text blocks per user (row-level security).
type ItemStore interface {
	Items(ctx context.Context, userID uuid.UUID, kind string) ([]StoredItem, error)
	CountItems(ctx context.Context, userID uuid.UUID, kind string) (int, error)
	InsertItem(ctx context.Context, userID uuid.UUID, kind string, it StoredItem) error
	// UpdateItem and DeleteItem report false when the item does not exist for the user.
	UpdateItem(ctx context.Context, userID uuid.UUID, kind string, it StoredItem) (bool, error)
	DeleteItem(ctx context.Context, userID uuid.UUID, kind string, id uuid.UUID) (bool, error)
}

// StoredWorkExperience is a work experience database row with sealed bullet points.
type StoredWorkExperience struct {
	ID            uuid.UUID
	UserID        uuid.UUID
	Company       string
	Title         string
	Location      string
	WorkplaceType string
	StartDate     string
	EndDate       string
	IsCurrent     bool
	SealedBullets []byte
	SortOrder     int
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// StoredEducation is an education database row.
type StoredEducation struct {
	ID           uuid.UUID
	UserID       uuid.UUID
	Institution  string
	Degree       string
	FieldOfStudy string
	StartDate    string
	EndDate      string
	IsCurrent    bool
	Grade        string
	SortOrder    int
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// StationStore persists work experiences and educations per user (row-level security).
type StationStore interface {
	ListWorkExperiences(ctx context.Context, userID uuid.UUID) ([]StoredWorkExperience, error)
	GetWorkExperience(ctx context.Context, userID, id uuid.UUID) (StoredWorkExperience, error)
	InsertWorkExperience(ctx context.Context, w StoredWorkExperience) error
	UpdateWorkExperience(ctx context.Context, w StoredWorkExperience) (bool, error)
	UpdateWorkExperienceOrder(ctx context.Context, userID, id uuid.UUID, sortOrder int, updatedAt time.Time) (bool, error)
	DeleteWorkExperience(ctx context.Context, userID, id uuid.UUID) (bool, error)
	CountWorkExperiences(ctx context.Context, userID uuid.UUID) (int, error)

	ListEducations(ctx context.Context, userID uuid.UUID) ([]StoredEducation, error)
	GetEducation(ctx context.Context, userID, id uuid.UUID) (StoredEducation, error)
	InsertEducation(ctx context.Context, e StoredEducation) error
	UpdateEducation(ctx context.Context, e StoredEducation) (bool, error)
	UpdateEducationOrder(ctx context.Context, userID, id uuid.UUID, sortOrder int, updatedAt time.Time) (bool, error)
	DeleteEducation(ctx context.Context, userID, id uuid.UUID) (bool, error)
	CountEducations(ctx context.Context, userID uuid.UUID) (int, error)
}
