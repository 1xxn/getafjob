// Package webpush implements Web Push notifications with VAPID (RFC 8292) and payload encryption (RFC 8291,
// RFC 8188) using the Go standard library.
package webpush

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// ErrInvalidKey reports an invalid public or private key.
var ErrInvalidKey = errors.New("invalid web push key")

const (
	recordSize = 4096
	vapidTTL   = 12 * time.Hour
)

// Keys holds a VAPID P-256 key pair encoded as URL-safe base64 without padding.
type Keys struct {
	Private string // 32-byte scalar
	Public  string // 65-byte uncompressed point
}

// GenerateKeys creates a fresh VAPID P-256 key pair.
func GenerateKeys() (*Keys, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	d, err := priv.Bytes()
	if err != nil {
		return nil, err
	}
	pub, err := priv.PublicKey.Bytes()
	if err != nil {
		return nil, err
	}
	return &Keys{
		Private: base64.RawURLEncoding.EncodeToString(d),
		Public:  base64.RawURLEncoding.EncodeToString(pub),
	}, nil
}

// ParsePrivateKey decodes a 32-byte base64url private key into an ECDSA P-256 private key.
func ParsePrivateKey(s string) (*ecdsa.PrivateKey, error) {
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(strings.TrimSpace(s), "="))
	if err != nil || len(b) != 32 {
		return nil, fmt.Errorf("%w: private key must be 32 bytes base64url", ErrInvalidKey)
	}
	priv, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), b)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid private key scalar", ErrInvalidKey)
	}
	return priv, nil
}

// PublicKeyBytes returns the 65-byte uncompressed P-256 representation (0x04 || X || Y).
func PublicKeyBytes(priv *ecdsa.PrivateKey) []byte {
	pub, err := priv.PublicKey.Bytes()
	if err != nil {
		return nil
	}
	return pub
}

// PublicKeyString returns the base64url-encoded 65-byte uncompressed point.
func PublicKeyString(priv *ecdsa.PrivateKey) string {
	return base64.RawURLEncoding.EncodeToString(PublicKeyBytes(priv))
}

// VAPIDAuthorization builds the VAPID header value: "vapid t=<jwt>, k=<key>".
func VAPIDAuthorization(priv *ecdsa.PrivateKey, endpoint, subject string, now time.Time) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid push endpoint: %w", err)
	}
	aud := u.Scheme + "://" + u.Host

	header, _ := json.Marshal(map[string]string{"typ": "JWT", "alg": "ES256"})
	claims, _ := json.Marshal(map[string]any{
		"aud": aud,
		"exp": now.Add(vapidTTL).Unix(),
		"sub": subject,
	})

	signingInput := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	h := sha256.Sum256([]byte(signingInput))

	r, s, err := ecdsa.Sign(rand.Reader, priv, h[:])
	if err != nil {
		return "", fmt.Errorf("sign vapid token: %w", err)
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])

	jwt := signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
	pub := PublicKeyString(priv)
	return fmt.Sprintf("vapid t=%s, k=%s", jwt, pub), nil
}

// Encrypt encrypts the plaintext payload according to RFC 8291 (aes128gcm).
func Encrypt(plaintext []byte, receiverP256dh, receiverAuth string) ([]byte, error) {
	recPubBytes, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(strings.TrimSpace(receiverP256dh), "="))
	if err != nil || len(recPubBytes) != 65 || recPubBytes[0] != 0x04 {
		return nil, fmt.Errorf("%w: p256dh must be 65 uncompressed bytes", ErrInvalidKey)
	}
	authSecret, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(strings.TrimSpace(receiverAuth), "="))
	if err != nil || len(authSecret) < 16 {
		return nil, fmt.Errorf("%w: auth secret must be at least 16 bytes", ErrInvalidKey)
	}

	recPubKey, err := ecdh.P256().NewPublicKey(recPubBytes)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid receiver public key: %w", ErrInvalidKey, err)
	}

	// Ephemeral sender key
	senderPriv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	senderPubBytes := senderPriv.PublicKey().Bytes()

	// Salt
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}

	return encryptWithKeys(plaintext, recPubKey, recPubBytes, senderPriv, senderPubBytes, authSecret, salt)
}

func encryptWithKeys(plaintext []byte, recPubKey *ecdh.PublicKey, recPubBytes []byte,
	senderPriv *ecdh.PrivateKey, senderPubBytes, authSecret, salt []byte,
) ([]byte, error) {
	// ECDH shared secret
	sharedSecret, err := senderPriv.ECDH(recPubKey)
	if err != nil {
		return nil, fmt.Errorf("ecdh: %w", err)
	}

	// RFC 8291 § 3.2: PRK key derivation
	// info = "WebPush: info\0" || receiver_public || sender_public
	info := make([]byte, 0, len("WebPush: info\x00")+len(recPubBytes)+len(senderPubBytes))
	info = append(info, "WebPush: info\x00"...)
	info = append(info, recPubBytes...)
	info = append(info, senderPubBytes...)

	prkKey, err := hkdf.Extract(sha256.New, sharedSecret, authSecret)
	if err != nil {
		return nil, fmt.Errorf("hkdf extract prkKey: %w", err)
	}
	ikm, err := hkdf.Expand(sha256.New, prkKey, string(info), 32)
	if err != nil {
		return nil, fmt.Errorf("hkdf expand ikm: %w", err)
	}

	// RFC 8188 § 2.2: Content-Encryption Key (CEK) and Nonce
	prk, err := hkdf.Extract(sha256.New, ikm, salt)
	if err != nil {
		return nil, fmt.Errorf("hkdf extract prk: %w", err)
	}
	cek, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, fmt.Errorf("hkdf expand cek: %w", err)
	}
	nonce, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, fmt.Errorf("hkdf expand nonce: %w", err)
	}

	// AES-GCM
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	// Record payload: plaintext || 0x02 (delimiter for final record)
	record := make([]byte, len(plaintext)+1)
	copy(record, plaintext)
	record[len(plaintext)] = 0x02

	ciphertext := gcm.Seal(nil, nonce, record, nil)

	// Header: salt (16) || record_size (4) || id_len (1) || sender_pub (65) || ciphertext
	out := make([]byte, 21+len(senderPubBytes)+len(ciphertext))
	copy(out[:16], salt)
	binary.BigEndian.PutUint32(out[16:20], uint32(recordSize))
	out[20] = 65
	copy(out[21:21+len(senderPubBytes)], senderPubBytes)
	copy(out[21+len(senderPubBytes):], ciphertext)

	return out, nil
}
