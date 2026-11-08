package portals

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ErrSessionExpired indicates that the portal session has expired and requires signing in again.
var ErrSessionExpired = errors.New("portal session has expired: please sign in again in the remote browser")

// Service coordinates portal session management, cookie encryption, rate limits, and ban risk compliance.
type Service struct {
	store        Store
	cipher       SessionCipher
	autonomy     AutonomySettingsReader
	adminEnabled bool
	now          func() time.Time
}

// NewService returns a new portals Service.
func NewService(store Store, cipher SessionCipher, autonomy AutonomySettingsReader, adminEnabled bool) *Service {
	return &Service{
		store:        store,
		cipher:       cipher,
		autonomy:     autonomy,
		adminEnabled: adminEnabled,
		now:          time.Now,
	}
}

// SetNow overrides the time provider (for deterministic testing).
func (s *Service) SetNow(now func() time.Time) {
	s.now = now
}

// AdminEnabled reports whether the Easy Apply module is enabled by the system administrator.
func (s *Service) AdminEnabled() bool {
	return s.adminEnabled
}

// SaveCookies encrypts the provided raw cookies JSON and stores an active session.
func (s *Service) SaveCookies(ctx context.Context, userID uuid.UUID, portal string, cookiesJSON []byte, dailyLimit int) error {
	if !s.adminEnabled {
		return ErrEasyApplyDisabled
	}
	portal = strings.ToLower(strings.TrimSpace(portal))
	if err := ValidatePortal(portal); err != nil {
		return err
	}
	if dailyLimit == 0 {
		dailyLimit = DefaultDailyLimit
	}
	if err := ValidateDailyLimit(dailyLimit); err != nil {
		return err
	}
	if len(cookiesJSON) == 0 {
		return errors.New("cookies JSON is empty")
	}

	if s.autonomy != nil {
		enabled, termsAccepted, err := s.autonomy.IsEasyApplyEnabled(ctx, userID)
		if err != nil {
			return fmt.Errorf("checking autonomy settings: %w", err)
		}
		if !enabled || !termsAccepted {
			return fmt.Errorf("%w: user has not opted in or accepted terms of use", ErrEasyApplyDisabled)
		}
	}

	sealed, err := s.cipher.SealCookies(ctx, userID, portal, cookiesJSON)
	if err != nil {
		return fmt.Errorf("encrypting portal cookies: %w", err)
	}

	now := s.now()
	sess := Session{
		UserID:           userID,
		Portal:           portal,
		Status:           StatusActive,
		EncryptedCookies: sealed,
		DailyLimit:       dailyLimit,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	return s.store.SaveSession(ctx, sess)
}

// GetDecryptedCookies retrieves and decrypts the session cookies for a portal.
func (s *Service) GetDecryptedCookies(ctx context.Context, userID uuid.UUID, portal string) ([]byte, error) {
	if !s.adminEnabled {
		return nil, ErrEasyApplyDisabled
	}
	portal = strings.ToLower(strings.TrimSpace(portal))
	if err := ValidatePortal(portal); err != nil {
		return nil, err
	}

	sess, err := s.store.GetSession(ctx, userID, portal)
	if err != nil {
		return nil, err
	}
	if sess.Status != StatusActive {
		return nil, ErrSessionExpired
	}
	if len(sess.EncryptedCookies) == 0 {
		return nil, errors.New("no encrypted cookies available")
	}

	cookiesJSON, err := s.cipher.OpenCookies(ctx, userID, portal, sess.EncryptedCookies)
	if err != nil {
		return nil, fmt.Errorf("decrypting portal cookies: %w", err)
	}
	return cookiesJSON, nil
}

// MarkExpired flags a portal session as expired (triggered by runner when encountering an authwall/login).
func (s *Service) MarkExpired(ctx context.Context, userID uuid.UUID, portal string) error {
	portal = strings.ToLower(strings.TrimSpace(portal))
	if err := ValidatePortal(portal); err != nil {
		return err
	}
	return s.store.UpdateStatus(ctx, userID, portal, StatusExpired, s.now())
}

// Disconnect removes or deactivates a portal session.
func (s *Service) Disconnect(ctx context.Context, userID uuid.UUID, portal string) error {
	portal = strings.ToLower(strings.TrimSpace(portal))
	if err := ValidatePortal(portal); err != nil {
		return err
	}
	return s.store.DeleteSession(ctx, userID, portal)
}

// SetDailyLimit updates the user's daily limit for a portal session.
func (s *Service) SetDailyLimit(ctx context.Context, userID uuid.UUID, portal string, dailyLimit int) error {
	portal = strings.ToLower(strings.TrimSpace(portal))
	if err := ValidatePortal(portal); err != nil {
		return err
	}
	if dailyLimit < MinDailyLimit || dailyLimit > MaxDailyLimit {
		return ErrInvalidDailyLimit
	}
	sess, err := s.store.GetSession(ctx, userID, portal)
	if err != nil {
		return err
	}
	sess.DailyLimit = dailyLimit
	sess.UpdatedAt = s.now()
	return s.store.SaveSession(ctx, sess)
}

// CanApply checks whether an Easy Apply run may be executed for the user on this portal.
func (s *Service) CanApply(ctx context.Context, userID uuid.UUID, portal string) (bool, string, error) {
	if !s.adminEnabled {
		return false, "Easy Apply module is disabled by administrator", nil
	}
	portal = strings.ToLower(strings.TrimSpace(portal))
	if err := ValidatePortal(portal); err != nil {
		return false, "", err
	}

	if s.autonomy != nil {
		enabled, termsAccepted, err := s.autonomy.IsEasyApplyEnabled(ctx, userID)
		if err != nil {
			return false, "", err
		}
		if !enabled || !termsAccepted {
			return false, "Easy Apply is not enabled or terms notice not accepted", nil
		}
	}

	sess, err := s.store.GetSession(ctx, userID, portal)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			return false, "portal account not connected: please sign in via remote browser", nil
		}
		return false, "", err
	}

	now := s.now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	submitsToday, err := s.store.SubmitsToday(ctx, userID, portal, midnight)
	if err != nil {
		return false, "", fmt.Errorf("counting submits today: %w", err)
	}

	activeRuns, err := s.store.ActiveRuns(ctx, userID, portal)
	if err != nil {
		return false, "", fmt.Errorf("checking active runs: %w", err)
	}

	ok, reason := CanApply(sess, submitsToday, activeRuns)
	return ok, reason, nil
}

// RecordUsed records that a run completed for this portal.
func (s *Service) RecordUsed(ctx context.Context, userID uuid.UUID, portal string) error {
	portal = strings.ToLower(strings.TrimSpace(portal))
	if err := ValidatePortal(portal); err != nil {
		return err
	}
	return s.store.TouchLastUsed(ctx, userID, portal, s.now())
}

// ListAccounts returns the user-facing account status for all supported portals.
func (s *Service) ListAccounts(ctx context.Context, userID uuid.UUID) ([]AccountView, error) {
	sessions, err := s.store.ListSessions(ctx, userID)
	if err != nil {
		return nil, err
	}

	byPortal := make(map[string]Session)
	for _, sess := range sessions {
		byPortal[sess.Portal] = sess
	}

	now := s.now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	views := make([]AccountView, 0, len(AllPortals()))
	for _, p := range AllPortals() {
		sess, ok := byPortal[p]
		if !ok {
			views = append(views, AccountView{
				Portal:        p,
				Status:        StatusDisconnected,
				DailyLimit:    DefaultDailyLimit,
				CanApply:      false,
				BlockedReason: "not connected",
			})
			continue
		}

		submits, _ := s.store.SubmitsToday(ctx, userID, p, midnight)
		active, _ := s.store.ActiveRuns(ctx, userID, p)
		can, reason := CanApply(sess, submits, active)

		views = append(views, AccountView{
			Portal:        p,
			Status:        sess.Status,
			DailyLimit:    sess.DailyLimit,
			SubmitsToday:  submits,
			ActiveRuns:    active,
			LastUsedAt:    sess.LastUsedAt,
			CanApply:      can,
			BlockedReason: reason,
		})
	}
	return views, nil
}
