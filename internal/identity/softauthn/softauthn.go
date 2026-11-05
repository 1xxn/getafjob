// Package softauthn is a software WebAuthn authenticator for tests: one ES256 key, "none" attestation. It answers
// the options the server sends like a browser with a platform authenticator would.
package softauthn

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"

	"github.com/fxamacker/cbor/v2"
)

var b64 = base64.RawURLEncoding

// idLen is the length of credential IDs.
const idLen = 32

// Key is one credential.
type Key struct {
	ID         []byte
	RPID       string
	Origin     string
	Count      uint32 // signature counter, incremented per assertion
	UserHandle []byte
	NoUV       bool // answer without user verification
	priv       *ecdsa.PrivateKey
}

// New returns a fresh key for the relying party rpID at origin.
func New(rpID, origin string) *Key {
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	id := make([]byte, idLen)
	_, _ = rand.Read(id)
	return &Key{ID: id, RPID: rpID, Origin: origin, priv: priv}
}

type options struct {
	PublicKey struct {
		Challenge string `json:"challenge"`
		User      struct {
			ID string `json:"id"`
		} `json:"user"`
	} `json:"publicKey"`
}

func (k *Key) clientData(typ, challenge string) []byte {
	b, _ := json.Marshal(map[string]any{"type": typ, "challenge": challenge, "origin": k.Origin, "crossOrigin": false})
	return b
}

func (k *Key) flags(at bool) byte {
	f := byte(0x01) // user present
	if !k.NoUV {
		f |= 0x04
	}
	if at {
		f |= 0x40
	}
	return f
}

// Create answers creation options (the JSON the server sent under "options") with an attestation.
func (k *Key) Create(opts []byte) ([]byte, error) {
	var o options
	if err := json.Unmarshal(opts, &o); err != nil || o.PublicKey.Challenge == "" {
		return nil, errors.New("softauthn: bad creation options")
	}
	uh, err := b64.DecodeString(o.PublicKey.User.ID)
	if err != nil {
		return nil, err
	}
	k.UserHandle = uh
	pt, err := k.priv.PublicKey.Bytes() // 0x04 || X || Y
	if err != nil {
		return nil, err
	}
	x, y := pt[1:33], pt[33:65]
	cose, err := cbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: x, -3: y})
	if err != nil {
		return nil, err
	}
	rp := sha256.Sum256([]byte(k.RPID))
	auth := append([]byte{}, rp[:]...)
	auth = append(auth, k.flags(true))
	auth = binary.BigEndian.AppendUint32(auth, k.Count)
	auth = append(auth, make([]byte, 16)...) // AAGUID
	auth = binary.BigEndian.AppendUint16(auth, idLen)
	auth = append(auth, k.ID...)
	auth = append(auth, cose...)
	att, err := cbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": auth})
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"id": b64.EncodeToString(k.ID), "rawId": b64.EncodeToString(k.ID), "type": "public-key",
		"response": map[string]any{
			"clientDataJSON":    b64.EncodeToString(k.clientData("webauthn.create", o.PublicKey.Challenge)),
			"attestationObject": b64.EncodeToString(att),
		},
		"clientExtensionResults": map[string]any{},
	})
}

// Get answers request options with an assertion and increments the counter.
func (k *Key) Get(opts []byte) ([]byte, error) {
	var o options
	if err := json.Unmarshal(opts, &o); err != nil || o.PublicKey.Challenge == "" {
		return nil, errors.New("softauthn: bad request options")
	}
	k.Count++
	rp := sha256.Sum256([]byte(k.RPID))
	auth := append([]byte{}, rp[:]...)
	auth = append(auth, k.flags(false))
	auth = binary.BigEndian.AppendUint32(auth, k.Count)
	cd := k.clientData("webauthn.get", o.PublicKey.Challenge)
	h := sha256.Sum256(cd)
	digest := sha256.Sum256(append(append([]byte{}, auth...), h[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, k.priv, digest[:])
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"id": b64.EncodeToString(k.ID), "rawId": b64.EncodeToString(k.ID), "type": "public-key",
		"response": map[string]any{
			"clientDataJSON":    b64.EncodeToString(cd),
			"authenticatorData": b64.EncodeToString(auth),
			"signature":         b64.EncodeToString(sig),
			"userHandle":        b64.EncodeToString(k.UserHandle),
		},
		"clientExtensionResults": map[string]any{},
	})
}
