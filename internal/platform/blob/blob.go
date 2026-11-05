// Package blob stores files (documents, screenshots, confirmation pages) in a directory volume or an S3-compatible
// bucket. Sealed encrypts every object for its user (platform/crypto), so the storage only ever sees ciphertext.
package blob

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/platform/config"
)

// MaxObject bounds one object (uploads have their own, smaller limits).
const MaxObject = 64 << 20

var (
	// ErrNotFound reports a missing object.
	ErrNotFound = errors.New("blob not found")
	// ErrBadKey reports a key that is not a safe relative path.
	ErrBadKey = errors.New("invalid blob key")
	// ErrTooLarge reports an object above MaxObject.
	ErrTooLarge = errors.New("blob too large")
)

// Store keeps opaque objects by key.
type Store interface {
	Put(ctx context.Context, key string, data []byte) error
	Get(ctx context.Context, key string) ([]byte, error)
	// Delete removes an object; a missing one is not an error.
	Delete(ctx context.Context, key string) error
}

// segment is one part of a key: lower-case letters, digits, '.', '_' and '-', starting with a letter or digit.
var segment = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)

// ValidKey reports whether key is safe as a relative path and an object name: 1 to 12 segments joined by '/'.
func ValidKey(key string) bool {
	segs := strings.Split(key, "/")
	if len(segs) > 12 {
		return false
	}
	for _, s := range segs {
		if !segment.MatchString(s) {
			return false
		}
	}
	return true
}

// S3Credentials are the access key pair of a bucket.
type S3Credentials struct {
	AccessKey, SecretKey config.Secret
}

// Open returns the store a URL names: file:///var/lib/getafjob/blobs or
// s3://bucket/prefix?endpoint=https://s3.example.org&region=eu-central-1.
func Open(raw string, creds S3Credentials) (Store, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("blob URL: cannot parse")
	}
	switch u.Scheme {
	case "file":
		if u.Host != "" || u.Path == "" {
			return nil, errors.New("blob URL: use file:///absolute/path")
		}
		p := u.Path
		if len(p) > 2 && p[0] == '/' && p[2] == ':' { // file:///C:/… on Windows (development)
			p = p[1:]
		}
		return NewDir(p)
	case "s3":
		return newS3(u, creds)
	default:
		return nil, fmt.Errorf("blob URL: unknown scheme %q", u.Scheme)
	}
}

// Sealer encrypts per user (crypto.Envelope).
type Sealer interface {
	Seal(ctx context.Context, userID uuid.UUID, purpose string, plaintext []byte) ([]byte, error)
	Open(ctx context.Context, userID uuid.UUID, purpose string, ciphertext []byte) ([]byte, error)
}

// Sealed stores the objects of each user under users/<id>/, encrypted with the user's data key and bound to the
// key, so an object moved to another key or user does not open.
type Sealed struct {
	store  Store
	sealer Sealer
}

// NewSealed returns an encrypting store over store.
func NewSealed(store Store, sealer Sealer) *Sealed { return &Sealed{store: store, sealer: sealer} }

func userKey(userID uuid.UUID, key string) (string, error) {
	if userID == uuid.Nil || !ValidKey(key) {
		return "", ErrBadKey
	}
	return "users/" + userID.String() + "/" + key, nil
}

// Put encrypts data and stores it.
func (s *Sealed) Put(ctx context.Context, userID uuid.UUID, key string, data []byte) error {
	k, err := userKey(userID, key)
	if err != nil {
		return err
	}
	if len(data) > MaxObject-1024 {
		return ErrTooLarge
	}
	ct, err := s.sealer.Seal(ctx, userID, "blob:"+key, data)
	if err != nil {
		return err
	}
	return s.store.Put(ctx, k, ct)
}

// Get loads and decrypts an object.
func (s *Sealed) Get(ctx context.Context, userID uuid.UUID, key string) ([]byte, error) {
	k, err := userKey(userID, key)
	if err != nil {
		return nil, err
	}
	ct, err := s.store.Get(ctx, k)
	if err != nil {
		return nil, err
	}
	return s.sealer.Open(ctx, userID, "blob:"+key, ct)
}

// Delete removes an object.
func (s *Sealed) Delete(ctx context.Context, userID uuid.UUID, key string) error {
	k, err := userKey(userID, key)
	if err != nil {
		return err
	}
	return s.store.Delete(ctx, k)
}
