package webpush

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func TestGenerateAndParseKeys(t *testing.T) {
	keys, err := GenerateKeys()
	if err != nil {
		t.Fatalf("GenerateKeys: %v", err)
	}
	if len(keys.Private) == 0 || len(keys.Public) == 0 {
		t.Fatal("empty key")
	}

	priv, err := ParsePrivateKey(keys.Private)
	if err != nil {
		t.Fatalf("ParsePrivateKey: %v", err)
	}
	pubStr := PublicKeyString(priv)
	if pubStr != keys.Public {
		t.Fatalf("public key mismatch: got %s, want %s", pubStr, keys.Public)
	}

	// Invalid keys
	if _, err := ParsePrivateKey("too-short"); err == nil {
		t.Error("expected error for short private key")
	}
}

func TestVAPIDAuthorization(t *testing.T) {
	keys, err := GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	priv, err := ParsePrivateKey(keys.Private)
	if err != nil {
		t.Fatal(err)
	}

	auth, err := VAPIDAuthorization(priv, "https://push.example.com/endpoint/123", "mailto:test@example.com", time.Now())
	if err != nil {
		t.Fatalf("VAPIDAuthorization: %v", err)
	}
	if !strings.HasPrefix(auth, "vapid t=") {
		t.Errorf("expected vapid header prefix, got: %s", auth)
	}
	if !strings.Contains(auth, ", k="+keys.Public) {
		t.Errorf("expected public key in header, got: %s", auth)
	}
}

func TestEncryptAndDecryptRoundtrip(t *testing.T) {
	// Receiver keypair (browser)
	recPriv, err := ecdh.P256().GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	recPubBytes := recPriv.PublicKey().Bytes()
	recP256dh := base64.RawURLEncoding.EncodeToString(recPubBytes)

	authSecret := make([]byte, 16)
	for i := range authSecret {
		authSecret[i] = byte(i + 1)
	}
	recAuth := base64.RawURLEncoding.EncodeToString(authSecret)

	payload := []byte("Hello, this is a test notification from getafjob!")

	encrypted, err := Encrypt(payload, recP256dh, recAuth)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	// Decrypt on the receiver side
	decrypted, err := decryptPayload(encrypted, recPriv, authSecret)
	if err != nil {
		t.Fatalf("decryptPayload: %v", err)
	}

	if !bytes.Equal(decrypted, payload) {
		t.Fatalf("mismatch: got %q, want %q", decrypted, payload)
	}
}

// decryptPayload decodes an RFC 8291 (aes128gcm) payload using the receiver's private key.
func decryptPayload(data []byte, recPriv *ecdh.PrivateKey, authSecret []byte) ([]byte, error) {
	if len(data) < 16+4+1+65+16 {
		return nil, ErrInvalidKey
	}
	salt := data[:16]
	idLen := int(data[20])
	if idLen != 65 {
		return nil, ErrInvalidKey
	}
	senderPubBytes := data[21 : 21+idLen]
	ciphertext := data[21+idLen:]

	senderPub, err := ecdh.P256().NewPublicKey(senderPubBytes)
	if err != nil {
		return nil, err
	}
	sharedSecret, err := recPriv.ECDH(senderPub)
	if err != nil {
		return nil, err
	}

	recPubBytes := recPriv.PublicKey().Bytes()
	info := make([]byte, 0, len("WebPush: info\x00")+len(recPubBytes)+len(senderPubBytes))
	info = append(info, "WebPush: info\x00"...)
	info = append(info, recPubBytes...)
	info = append(info, senderPubBytes...)

	prkKey, err := hkdf.Extract(sha256.New, sharedSecret, authSecret)
	if err != nil {
		return nil, err
	}
	ikm, err := hkdf.Expand(sha256.New, prkKey, string(info), 32)
	if err != nil {
		return nil, err
	}

	prk, err := hkdf.Extract(sha256.New, ikm, salt)
	if err != nil {
		return nil, err
	}
	cek, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, err
	}

	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	record, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, err
	}
	if len(record) == 0 || record[len(record)-1] != 0x02 {
		return nil, ErrInvalidKey
	}
	return record[:len(record)-1], nil
}

// TestDeterministicVector tests encryption and decryption with known fixed keys.
func TestDeterministicVector(t *testing.T) {
	// Receiver keypair
	recPriv, err := ecdh.P256().GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	recPub := recPriv.PublicKey()
	recPubBytes := recPub.Bytes()

	// Sender keypair (ephemeral)
	senderPriv, err := ecdh.P256().GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	senderPubBytes := senderPriv.PublicKey().Bytes()

	authSecret := []byte("0123456789abcdef")
	salt := []byte("fedcba9876543210")
	plaintext := []byte("When I grow up, I want to be a toaster")

	encrypted, err := encryptWithKeys(plaintext, recPub, recPubBytes, senderPriv, senderPubBytes, authSecret, salt)
	if err != nil {
		t.Fatalf("encryptWithKeys: %v", err)
	}

	decrypted, err := decryptPayload(encrypted, recPriv, authSecret)
	if err != nil {
		t.Fatalf("decryptPayload: %v", err)
	}

	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("got %q, want %q", decrypted, plaintext)
	}
}
