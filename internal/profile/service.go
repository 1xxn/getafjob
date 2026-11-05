package profile

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

const sealPurpose = "profile"

var (
	// ErrInvalid reports a profile or preferences that fail validation; Fields says why.
	ErrInvalid = errors.New("profile: invalid")
	// ErrConflict reports that the profile changed since it was loaded (another tab or device).
	ErrConflict = errors.New("profile: changed meanwhile")
)

// ValidationError carries the field errors of ErrInvalid.
type ValidationError struct{ Fields []FieldError }

func (e *ValidationError) Error() string { return ErrInvalid.Error() }

// Unwrap makes errors.Is(err, ErrInvalid) work.
func (e *ValidationError) Unwrap() error { return ErrInvalid }

// Service reads and writes profiles.
type Service struct {
	store    Store
	sealer   Sealer
	now      func() time.Time
	onChange func(ctx context.Context, userID uuid.UUID)
}

// NewService returns a profile service.
func NewService(store Store, sealer Sealer) *Service {
	return &Service{store: store, sealer: sealer, now: time.Now}
}

// Snapshot is a loaded profile with its version for the next save.
type Snapshot struct {
	Profile     Profile
	Preferences Preferences
	Version     int64
}

// Get loads the user's profile and preferences (empty ones if there are none yet).
func (s *Service) Get(ctx context.Context, userID uuid.UUID) (Snapshot, error) {
	st, err := s.store.Load(ctx, userID)
	if err != nil {
		return Snapshot{}, err
	}
	snap := Snapshot{Version: st.Version, Preferences: Preferences{Remote: true, Diversity: "decline", CoverLetterLang: "auto"}}
	if len(st.Sealed) > 0 {
		plain, err := s.sealer.Open(ctx, userID, sealPurpose, st.Sealed)
		if err != nil {
			return Snapshot{}, err
		}
		if err := json.Unmarshal(plain, &snap.Profile); err != nil {
			return Snapshot{}, err
		}
	}
	if len(st.Preferences) > 0 {
		if err := json.Unmarshal(st.Preferences, &snap.Preferences); err != nil {
			return Snapshot{}, err
		}
	}
	return snap, nil
}

// SaveProfile validates p and stores it sealed if the profile is still at version.
func (s *Service) SaveProfile(ctx context.Context, userID uuid.UUID, p Profile, version int64) error {
	p.Personal.LinkedIn, p.Personal.GitHub, p.Personal.Website = WebURL(p.Personal.LinkedIn), WebURL(p.Personal.GitHub), WebURL(p.Personal.Website)
	if errs := p.Validate(); len(errs) > 0 {
		return &ValidationError{Fields: errs}
	}
	plain, err := json.Marshal(p)
	if err != nil {
		return err
	}
	sealed, err := s.sealer.Seal(ctx, userID, sealPurpose, plain)
	if err != nil {
		return err
	}
	ok, err := s.store.SaveProfile(ctx, userID, sealed, version, s.now())
	if err == nil && !ok {
		return ErrConflict
	}
	if err == nil {
		s.changed(ctx, userID)
	}
	return err
}

// SavePreferences validates and stores the preferences.
func (s *Service) SavePreferences(ctx context.Context, userID uuid.UUID, p Preferences) error {
	if errs := p.Validate(); len(errs) > 0 {
		return &ValidationError{Fields: errs}
	}
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if err := s.store.SavePreferences(ctx, userID, b, s.now()); err != nil {
		return err
	}
	s.changed(ctx, userID)
	return nil
}

// WithOnChange registers fn to run after every saved profile or preferences change (matching).
func (s *Service) WithOnChange(fn func(ctx context.Context, userID uuid.UUID)) *Service {
	s.onChange = fn
	return s
}

func (s *Service) changed(ctx context.Context, userID uuid.UUID) {
	if s.onChange != nil {
		s.onChange(ctx, userID)
	}
}

// WithClock replaces the clock (tests).
func (s *Service) WithClock(now func() time.Time) *Service { s.now = now; return s }
