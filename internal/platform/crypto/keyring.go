// Package crypto implements envelope encryption (ADR 0006): a data key per user encrypts that user's secrets and
// files; the master key (Docker secret) wraps the data keys. Rotating the master key re-wraps the data keys and
// leaves the data alone; deleting a user's data key makes their data unreadable.
//
// Everything uses AES-256-GCM with random nonces and associated data that binds a ciphertext to its user and
// purpose, so ciphertexts cannot be swapped between users or fields.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/1xxn/getafjob/internal/platform/config"
)

// KeySize is the size of master and data keys.
const KeySize = 32

// version prefixes every ciphertext, so the format can change later.
const version = 1

var (
	// ErrBadKey reports a master key that is not 32 bytes in base64url (admin gen-secret).
	ErrBadKey = errors.New("master key must be 32 random bytes, base64url encoded")
	// ErrUnknownMasterKey reports a data key wrapped with a master key that is neither current nor old.
	ErrUnknownMasterKey = errors.New("data key wrapped with an unknown master key")
	// ErrDecrypt reports a ciphertext that is damaged, foreign or bound to other associated data.
	ErrDecrypt = errors.New("decryption failed")
)

type masterKey struct {
	id   string
	aead cipher.AEAD
}

// Keyring holds the current master key and, during a rotation, the previous one.
type Keyring struct {
	current masterKey
	old     *masterKey
}

// NewKeyring parses the current master key and an optional old one.
func NewKeyring(current, old config.Secret) (*Keyring, error) {
	cur, err := parseMaster(current)
	if err != nil {
		return nil, err
	}
	k := &Keyring{current: cur}
	if strings.TrimSpace(old.Reveal()) != "" {
		o, err := parseMaster(old)
		if err != nil {
			return nil, err
		}
		if o.id != cur.id {
			k.old = &o
		}
	}
	return k, nil
}

// ValidMasterKey reports whether s is a usable master key (config checks).
func ValidMasterKey(s string) bool {
	_, err := decodeKey(s)
	return err == nil
}

func decodeKey(s string) ([]byte, error) {
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(strings.TrimSpace(s), "="))
	if err != nil || len(b) != KeySize {
		return nil, ErrBadKey
	}
	return b, nil
}

func parseMaster(s config.Secret) (masterKey, error) {
	raw, err := decodeKey(s.Reveal())
	if err != nil {
		return masterKey{}, err
	}
	wrap, err := hkdf.Key(sha256.New, raw, nil, "getafjob master key wrap v1", KeySize)
	if err != nil {
		return masterKey{}, err
	}
	id, err := hkdf.Key(sha256.New, raw, nil, "getafjob master key id v1", 8)
	if err != nil {
		return masterKey{}, err
	}
	a, err := newAEAD(wrap)
	if err != nil {
		return masterKey{}, err
	}
	return masterKey{id: hex.EncodeToString(id), aead: a}, nil
}

// CurrentID names the current master key (a hash, safe to store and log).
func (k *Keyring) CurrentID() string { return k.current.id }

// wrap encrypts a data key with the current master key.
func (k *Keyring) wrap(dataKey, aad []byte) (id string, wrapped []byte, err error) {
	w, err := seal(k.current.aead, dataKey, aad)
	return k.current.id, w, err
}

// unwrap decrypts a data key wrapped with the master key id.
func (k *Keyring) unwrap(id string, wrapped, aad []byte) ([]byte, error) {
	var m *masterKey
	switch {
	case id == k.current.id:
		m = &k.current
	case k.old != nil && id == k.old.id:
		m = k.old
	default:
		return nil, ErrUnknownMasterKey
	}
	dk, err := open(m.aead, wrapped, aad)
	if err != nil || len(dk) != KeySize {
		return nil, ErrDecrypt
	}
	return dk, nil
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// MaxPlaintext bounds what one call seals (the largest blob plus room); more is a caller bug.
const MaxPlaintext = 128 << 20

// ErrTooLarge reports a plaintext above MaxPlaintext.
var ErrTooLarge = errors.New("plaintext too large")

// seal returns version || nonce || ciphertext.
func seal(a cipher.AEAD, plaintext, aad []byte) ([]byte, error) {
	if len(plaintext) > MaxPlaintext {
		return nil, ErrTooLarge
	}
	nonce := make([]byte, a.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := make([]byte, 0, 1+len(nonce)+len(plaintext)+a.Overhead())
	out = append(append(out, version), nonce...)
	return a.Seal(out, nonce, plaintext, aad), nil
}

func open(a cipher.AEAD, ciphertext, aad []byte) ([]byte, error) {
	n := a.NonceSize()
	if len(ciphertext) < 1+n+a.Overhead() || ciphertext[0] != version {
		return nil, ErrDecrypt
	}
	pt, err := a.Open(nil, ciphertext[1:1+n], ciphertext[1+n:], aad)
	if err != nil {
		return nil, ErrDecrypt
	}
	return pt, nil
}
