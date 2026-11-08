package portals

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
)

type fakeStore struct {
	sessions map[string]Session
	submits  map[string]int
	active   map[string]int
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		sessions: make(map[string]Session),
		submits:  make(map[string]int),
		active:   make(map[string]int),
	}
}

func (f *fakeStore) SaveSession(ctx context.Context, s Session) error {
	key := s.UserID.String() + ":" + s.Portal
	f.sessions[key] = s
	return nil
}

func (f *fakeStore) GetSession(ctx context.Context, userID uuid.UUID, portal string) (Session, error) {
	key := userID.String() + ":" + portal
	s, ok := f.sessions[key]
	if !ok {
		return Session{}, ErrSessionNotFound
	}
	return s, nil
}

func (f *fakeStore) ListSessions(ctx context.Context, userID uuid.UUID) ([]Session, error) {
	var list []Session
	prefix := userID.String() + ":"
	for k, s := range f.sessions {
		if len(k) > len(prefix) && k[:len(prefix)] == prefix {
			list = append(list, s)
		}
	}
	return list, nil
}

func (f *fakeStore) UpdateStatus(ctx context.Context, userID uuid.UUID, portal, status string, at time.Time) error {
	key := userID.String() + ":" + portal
	s, ok := f.sessions[key]
	if !ok {
		return ErrSessionNotFound
	}
	s.Status = status
	s.UpdatedAt = at
	f.sessions[key] = s
	return nil
}

func (f *fakeStore) TouchLastUsed(ctx context.Context, userID uuid.UUID, portal string, at time.Time) error {
	key := userID.String() + ":" + portal
	s, ok := f.sessions[key]
	if !ok {
		return ErrSessionNotFound
	}
	s.LastUsedAt = &at
	s.UpdatedAt = at
	f.sessions[key] = s
	return nil
}

func (f *fakeStore) DeleteSession(ctx context.Context, userID uuid.UUID, portal string) error {
	key := userID.String() + ":" + portal
	delete(f.sessions, key)
	return nil
}

func (f *fakeStore) SubmitsToday(ctx context.Context, userID uuid.UUID, portal string, since time.Time) (int, error) {
	key := userID.String() + ":" + portal
	return f.submits[key], nil
}

func (f *fakeStore) ActiveRuns(ctx context.Context, userID uuid.UUID, portal string) (int, error) {
	key := userID.String() + ":" + portal
	return f.active[key], nil
}

type fakeCipher struct{}

func (f *fakeCipher) SealCookies(ctx context.Context, userID uuid.UUID, portal string, cookiesJSON []byte) ([]byte, error) {
	return append([]byte("ENC:"), cookiesJSON...), nil
}

func (f *fakeCipher) OpenCookies(ctx context.Context, userID uuid.UUID, portal string, ciphertext []byte) ([]byte, error) {
	if len(ciphertext) < 4 || string(ciphertext[:4]) != "ENC:" {
		return nil, errors.New("bad ciphertext")
	}
	return ciphertext[4:], nil
}

type fakeAutonomy struct {
	enabled bool
	terms   bool
}

func (f *fakeAutonomy) IsEasyApplyEnabled(ctx context.Context, userID uuid.UUID) (bool, bool, error) {
	return f.enabled, f.terms, nil
}

func TestServiceSaveAndGetCookies(t *testing.T) {
	store := newFakeStore()
	cipher := &fakeCipher{}
	autonomy := &fakeAutonomy{enabled: true, terms: true}
	svc := NewService(store, cipher, autonomy, true)

	userID := uuid.New()
	rawCookies := []byte(`[{"name":"li_at","value":"secret_token"}]`)

	// 1. Save cookies
	if err := svc.SaveCookies(context.Background(), userID, PortalLinkedIn, rawCookies, 5); err != nil {
		t.Fatalf("SaveCookies failed: %v", err)
	}

	// 2. Retrieve decrypted cookies
	decrypted, err := svc.GetDecryptedCookies(context.Background(), userID, PortalLinkedIn)
	if err != nil {
		t.Fatalf("GetDecryptedCookies failed: %v", err)
	}
	if string(decrypted) != string(rawCookies) {
		t.Fatalf("got cookies %q, want %q", string(decrypted), string(rawCookies))
	}

	// 3. Mark expired
	if err := svc.MarkExpired(context.Background(), userID, PortalLinkedIn); err != nil {
		t.Fatalf("MarkExpired failed: %v", err)
	}

	// 4. Getting expired session returns ErrSessionExpired
	_, err = svc.GetDecryptedCookies(context.Background(), userID, PortalLinkedIn)
	if !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("expected ErrSessionExpired, got %v", err)
	}
}

func TestServiceAdminDisabled(t *testing.T) {
	store := newFakeStore()
	cipher := &fakeCipher{}
	autonomy := &fakeAutonomy{enabled: true, terms: true}
	svc := NewService(store, cipher, autonomy, false) // admin switch OFF

	userID := uuid.New()
	err := svc.SaveCookies(context.Background(), userID, PortalLinkedIn, []byte(`[]`), 5)
	if !errors.Is(err, ErrEasyApplyDisabled) {
		t.Fatalf("expected ErrEasyApplyDisabled, got %v", err)
	}

	can, reason, err := svc.CanApply(context.Background(), userID, PortalLinkedIn)
	if err != nil || can || reason == "" {
		t.Fatalf("CanApply when admin disabled: can=%v, reason=%q, err=%v", can, reason, err)
	}
}

func TestServiceUserNotOptedIn(t *testing.T) {
	store := newFakeStore()
	cipher := &fakeCipher{}
	autonomy := &fakeAutonomy{enabled: false, terms: false} // user not opted in
	svc := NewService(store, cipher, autonomy, true)

	userID := uuid.New()
	err := svc.SaveCookies(context.Background(), userID, PortalLinkedIn, []byte(`[]`), 5)
	if !errors.Is(err, ErrEasyApplyDisabled) {
		t.Fatalf("expected ErrEasyApplyDisabled, got %v", err)
	}

	can, reason, err := svc.CanApply(context.Background(), userID, PortalLinkedIn)
	if err != nil || can || reason == "" {
		t.Fatalf("CanApply when not opted in: can=%v, reason=%q", can, reason)
	}
}

func TestServiceRateLimitsAndConcurrency(t *testing.T) {
	store := newFakeStore()
	cipher := &fakeCipher{}
	autonomy := &fakeAutonomy{enabled: true, terms: true}
	svc := NewService(store, cipher, autonomy, true)

	userID := uuid.New()
	_ = svc.SaveCookies(context.Background(), userID, PortalIndeed, []byte(`[{"name":"SHP","value":"1"}]`), 3)

	// Can apply initially
	ok, reason, err := svc.CanApply(context.Background(), userID, PortalIndeed)
	if err != nil || !ok {
		t.Fatalf("CanApply initial: ok=%v, reason=%q, err=%v", ok, reason, err)
	}

	// Simulate active run (one run per account)
	store.active[userID.String()+":"+PortalIndeed] = 1
	ok, reason, err = svc.CanApply(context.Background(), userID, PortalIndeed)
	if ok || reason == "" {
		t.Fatalf("CanApply during active run should be blocked: ok=%v, reason=%q", ok, reason)
	}
	store.active[userID.String()+":"+PortalIndeed] = 0

	// Simulate hitting daily limit (3 submits)
	store.submits[userID.String()+":"+PortalIndeed] = 3
	ok, reason, err = svc.CanApply(context.Background(), userID, PortalIndeed)
	if ok || reason == "" {
		t.Fatalf("CanApply when daily limit reached should be blocked: ok=%v, reason=%q", ok, reason)
	}
}

func TestServiceListAccounts(t *testing.T) {
	store := newFakeStore()
	cipher := &fakeCipher{}
	autonomy := &fakeAutonomy{enabled: true, terms: true}
	svc := NewService(store, cipher, autonomy, true)

	userID := uuid.New()
	_ = svc.SaveCookies(context.Background(), userID, PortalLinkedIn, []byte(`[]`), 5)

	accounts, err := svc.ListAccounts(context.Background(), userID)
	if err != nil {
		t.Fatalf("ListAccounts failed: %v", err)
	}
	if len(accounts) != 2 {
		t.Fatalf("expected 2 accounts, got %d", len(accounts))
	}

	idxLI := slices.IndexFunc(accounts, func(a AccountView) bool { return a.Portal == PortalLinkedIn })
	idxInd := slices.IndexFunc(accounts, func(a AccountView) bool { return a.Portal == PortalIndeed })

	if accounts[idxLI].Status != StatusActive {
		t.Errorf("LinkedIn status = %s, want active", accounts[idxLI].Status)
	}
	if accounts[idxInd].Status != StatusDisconnected {
		t.Errorf("Indeed status = %s, want disconnected", accounts[idxInd].Status)
	}
}

func TestServiceSetDailyLimit(t *testing.T) {
	store := newFakeStore()
	cipher := &fakeCipher{}
	autonomy := &fakeAutonomy{enabled: true, terms: true}
	svc := NewService(store, cipher, autonomy, true)

	userID := uuid.New()
	_ = svc.SaveCookies(context.Background(), userID, PortalLinkedIn, []byte(`[]`), 5)

	if err := svc.SetDailyLimit(context.Background(), userID, PortalLinkedIn, 10); err != nil {
		t.Fatalf("SetDailyLimit failed: %v", err)
	}

	sess, err := store.GetSession(context.Background(), userID, PortalLinkedIn)
	if err != nil || sess.DailyLimit != 10 {
		t.Fatalf("DailyLimit = %d, want 10", sess.DailyLimit)
	}

	if err := svc.SetDailyLimit(context.Background(), userID, PortalLinkedIn, 50); !errors.Is(err, ErrInvalidDailyLimit) {
		t.Fatalf("expected ErrInvalidDailyLimit for limit 50, got %v", err)
	}
}
