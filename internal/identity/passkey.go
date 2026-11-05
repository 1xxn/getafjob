package identity

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
)

// Passkey policy.
const (
	challengeTTL     = 5 * time.Minute
	maxPasskeys      = 20
	maxPasskeyName   = 100
	challengeLogin   = "login"
	challengeEnroll  = "register"
	defaultKeyName   = "Passkey"
	maxResponseBytes = 64 << 10
)

// Passkey errors.
var (
	ErrPasskeysOff     = errors.New("identity: passkeys are not configured")
	ErrPasskeyFailed   = errors.New("identity: passkey not accepted")
	ErrTooManyPasskeys = errors.New("identity: too many passkeys")
)

// PasskeyInfo describes a passkey on the account page.
type PasskeyInfo struct {
	ID        []byte
	Name      string
	CreatedAt time.Time
	LastUsed  time.Time
}

// EnablePasskeys sets up WebAuthn for the public URL: the relying party is its host, the only origin is the URL's
// origin. User verification is required, so a passkey counts as two factors (device and PIN or biometrics).
func (s *Service) EnablePasskeys(public *url.URL) error {
	wa, err := webauthn.New(&webauthn.Config{
		RPID:                  public.Hostname(),
		RPDisplayName:         "getafjob",
		RPOrigins:             []string{public.Scheme + "://" + public.Host},
		AttestationPreference: protocol.PreferNoAttestation,
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:      protocol.ResidentKeyRequirementRequired,
			UserVerification: protocol.VerificationRequired,
		},
	})
	if err != nil {
		return err
	}
	s.webauthn = wa
	return nil
}

// waUser adapts a user and their passkeys to webauthn.User.
type waUser struct {
	u     User
	creds []webauthn.Credential
}

func (w waUser) WebAuthnID() []byte                         { return w.u.ID[:] }
func (w waUser) WebAuthnName() string                       { return w.u.Email }
func (w waUser) WebAuthnDisplayName() string                { return w.u.DisplayName }
func (w waUser) WebAuthnCredentials() []webauthn.Credential { return w.creds }

func (s *Service) loadUser(ctx context.Context, id uuid.UUID) (waUser, error) {
	u, keys, err := s.store.UserByID(ctx, id)
	if err != nil {
		return waUser{}, err
	}
	w := waUser{u: u}
	for _, k := range keys {
		var c webauthn.Credential
		if err := json.Unmarshal(k.Credential, &c); err != nil {
			return waUser{}, err
		}
		w.creds = append(w.creds, c)
	}
	return w, nil
}

func (s *Service) saveChallenge(ctx context.Context, userID *uuid.UUID, kind string, sd *webauthn.SessionData) (uuid.UUID, error) {
	data, err := json.Marshal(sd)
	if err != nil {
		return uuid.Nil, err
	}
	return s.store.SaveChallenge(ctx, Challenge{UserID: userID, Kind: kind, Data: data, ExpiresAt: s.now().Add(challengeTTL)})
}

func (s *Service) takeChallenge(ctx context.Context, id uuid.UUID, kind string) (Challenge, webauthn.SessionData, error) {
	c, err := s.store.TakeChallenge(ctx, id, kind, s.now())
	if errors.Is(err, ErrNotFound) {
		return Challenge{}, webauthn.SessionData{}, ErrPasskeyFailed
	}
	if err != nil {
		return Challenge{}, webauthn.SessionData{}, err
	}
	var sd webauthn.SessionData
	if err := json.Unmarshal(c.Data, &sd); err != nil {
		return Challenge{}, webauthn.SessionData{}, ErrPasskeyFailed
	}
	return c, sd, nil
}

// BeginPasskeyRegistration returns the creation options for a new passkey of the signed-in user. The session must
// be fresh (ErrReauthRequired), so a stolen session cannot add a key.
func (s *Service) BeginPasskeyRegistration(ctx context.Context, userID uuid.UUID, token string) (options any, challenge uuid.UUID, err error) {
	if s.webauthn == nil {
		return nil, uuid.Nil, ErrPasskeysOff
	}
	if _, err := s.freshSession(ctx, userID, token); err != nil {
		return nil, uuid.Nil, err
	}
	w, err := s.loadUser(ctx, userID)
	if err != nil {
		return nil, uuid.Nil, err
	}
	if len(w.creds) >= maxPasskeys {
		return nil, uuid.Nil, ErrTooManyPasskeys
	}
	exclude := make([]protocol.CredentialDescriptor, len(w.creds))
	for i, c := range w.creds {
		exclude[i] = c.Descriptor()
	}
	creation, sd, err := s.webauthn.BeginRegistration(w, webauthn.WithExclusions(exclude))
	if err != nil {
		return nil, uuid.Nil, err
	}
	id, err := s.saveChallenge(ctx, &userID, challengeEnroll, sd)
	return creation, id, err
}

// FinishPasskeyRegistration checks the authenticator's answer and stores the passkey under name.
func (s *Service) FinishPasskeyRegistration(ctx context.Context, userID uuid.UUID, token string, challenge uuid.UUID, name string, response []byte) error {
	if s.webauthn == nil {
		return ErrPasskeysOff
	}
	if _, err := s.freshSession(ctx, userID, token); err != nil {
		return err
	}
	c, sd, err := s.takeChallenge(ctx, challenge, challengeEnroll)
	if err != nil {
		return err
	}
	if c.UserID == nil || *c.UserID != userID || len(response) > maxResponseBytes {
		return ErrPasskeyFailed
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(response)
	if err != nil {
		return ErrPasskeyFailed
	}
	w, err := s.loadUser(ctx, userID)
	if err != nil {
		return err
	}
	cred, err := s.webauthn.CreateCredential(w, sd, parsed)
	if err != nil {
		return ErrPasskeyFailed
	}
	data, err := json.Marshal(cred)
	if err != nil {
		return err
	}
	name = CleanDisplayName(name)
	if r := []rune(name); len(r) > maxPasskeyName {
		name = string(r[:maxPasskeyName])
	}
	if name == "" {
		name = defaultKeyName
	}
	return s.store.AddPasskey(ctx, userID, Passkey{ID: cred.ID, Name: name, Credential: data, CreatedAt: s.now()},
		AuditEvent{UserID: &userID, Actor: "user", Code: "identity.passkey_added"})
}

// BeginPasskeyLogin returns request options for a sign-in without a known user (discoverable credentials).
func (s *Service) BeginPasskeyLogin(ctx context.Context) (options any, challenge uuid.UUID, err error) {
	if s.webauthn == nil {
		return nil, uuid.Nil, ErrPasskeysOff
	}
	assertion, sd, err := s.webauthn.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		return nil, uuid.Nil, err
	}
	id, err := s.saveChallenge(ctx, nil, challengeLogin, sd)
	return assertion, id, err
}

// FinishPasskeyLogin checks the assertion and starts a session. A signature counter that went backwards (a
// cloned authenticator) is refused.
func (s *Service) FinishPasskeyLogin(ctx context.Context, challenge uuid.UUID, response []byte) (User, Token, error) {
	if s.webauthn == nil {
		return User{}, "", ErrPasskeysOff
	}
	_, sd, err := s.takeChallenge(ctx, challenge, challengeLogin)
	if err != nil {
		return User{}, "", err
	}
	if len(response) > maxResponseBytes {
		return User{}, "", ErrPasskeyFailed
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(response)
	if err != nil {
		return User{}, "", ErrPasskeyFailed
	}
	var owner waUser
	handler := func(_, userHandle []byte) (webauthn.User, error) {
		id, err := uuid.FromBytes(userHandle)
		if err != nil {
			return nil, ErrPasskeyFailed
		}
		owner, err = s.loadUser(ctx, id)
		if err != nil {
			return nil, ErrPasskeyFailed
		}
		return owner, nil
	}
	cred, err := s.webauthn.ValidateDiscoverableLogin(handler, sd, parsed)
	if err != nil {
		return User{}, "", ErrPasskeyFailed
	}
	if cred.Authenticator.CloneWarning {
		return User{}, "", ErrPasskeyFailed
	}
	data, err := json.Marshal(cred)
	if err != nil {
		return User{}, "", err
	}
	now := s.now()
	st, sh := NewToken()
	u, err := s.store.SignInWithPasskey(ctx, owner.u.ID, cred.ID, data, s.newSession(sh, MethodPasskey, now), now)
	if errors.Is(err, ErrNotFound) {
		return User{}, "", ErrPasskeyFailed
	}
	if err != nil {
		return User{}, "", err
	}
	if u.Disabled {
		return User{}, "", ErrUserDisabled
	}
	return u, st, nil
}

// Passkeys lists the user's passkeys.
func (s *Service) Passkeys(ctx context.Context, userID uuid.UUID) ([]PasskeyInfo, error) {
	_, keys, err := s.store.UserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]PasskeyInfo, len(keys))
	for i, k := range keys {
		out[i] = PasskeyInfo{ID: k.ID, Name: k.Name, CreatedAt: k.CreatedAt, LastUsed: k.LastUsed}
	}
	return out, nil
}

// DeletePasskey removes a passkey of the user; the session must be fresh.
func (s *Service) DeletePasskey(ctx context.Context, userID uuid.UUID, token string, id []byte) error {
	if _, err := s.freshSession(ctx, userID, token); err != nil {
		return err
	}
	ok, err := s.store.DeletePasskey(ctx, userID, id, AuditEvent{UserID: &userID, Actor: "user", Code: "identity.passkey_removed"})
	if err == nil && !ok {
		return ErrNotFound
	}
	return err
}

// PasskeysEnabled reports whether passkeys are configured.
func (s *Service) PasskeysEnabled() bool { return s.webauthn != nil }
