package store

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"testing"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
)

const (
	flagUP = 0x01
	flagUV = 0x04
	flagBE = 0x08
	flagBS = 0x10
	flagAT = 0x40
)

// authenticator is a software passkey: an ES256 key that answers ceremonies
// as a browser and platform authenticator would. Tests change its origin,
// relying party, flags, or counter to forge the failures a real one cannot.
type authenticator struct {
	key    *ecdsa.PrivateKey
	id     []byte
	user   []byte
	origin string
	rpID   string
	flags  byte
	count  uint32
}

func newAuthenticator(t *testing.T, origin, rpID string) *authenticator {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 32)
	if _, err = rand.Read(id); err != nil {
		t.Fatal(err)
	}
	return &authenticator{key: key, id: id, origin: origin, rpID: rpID, flags: flagUP | flagUV | flagBE | flagBS}
}

func encode(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func (a *authenticator) clientData(t *testing.T, kind string, challenge protocol.URLEncodedBase64) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]any{"type": kind, "challenge": challenge.String(), "origin": a.origin, "crossOrigin": false})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func (a *authenticator) authData(flags byte, attested []byte) []byte {
	rp := sha256.Sum256([]byte(a.rpID))
	data := append(rp[:], flags)
	data = binary.BigEndian.AppendUint32(data, a.count)
	return append(data, attested...)
}

func (a *authenticator) credential(t *testing.T, response map[string]string) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]any{"id": encode(a.id), "rawId": encode(a.id), "type": "public-key", "response": response, "clientExtensionResults": map[string]any{}, "authenticatorAttachment": "platform"})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// register answers creation options with a "none" attestation, remembering
// the user handle for later assertions.
func (a *authenticator) register(t *testing.T, options any) []byte {
	t.Helper()
	creation := options.(protocol.PublicKeyCredentialCreationOptions)
	a.user = creation.User.ID.(protocol.URLEncodedBase64)
	point, err := a.key.PublicKey.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	// An uncompressed P-256 point is 0x04, then X and Y in 32 bytes each.
	publicKey, err := webauthncbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: point[1:33], -3: point[33:]})
	if err != nil {
		t.Fatal(err)
	}
	attested := append(make([]byte, 16), byte(len(a.id)>>8), byte(len(a.id)))
	attested = append(append(attested, a.id...), publicKey...)
	object, err := webauthncbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": a.authData(a.flags|flagAT, attested)})
	if err != nil {
		t.Fatal(err)
	}
	return a.credential(t, map[string]string{"clientDataJSON": encode(a.clientData(t, "webauthn.create", creation.Challenge)), "attestationObject": encode(object)})
}

// assert signs request options with the passkey, advancing its counter.
func (a *authenticator) assert(t *testing.T, options any) []byte {
	t.Helper()
	request := options.(protocol.PublicKeyCredentialRequestOptions)
	a.count++
	authData := a.authData(a.flags, nil)
	clientData := a.clientData(t, "webauthn.get", request.Challenge)
	digest := sha256.Sum256(clientData)
	signed := sha256.Sum256(append(append([]byte{}, authData...), digest[:]...))
	signature, err := ecdsa.SignASN1(rand.Reader, a.key, signed[:])
	if err != nil {
		t.Fatal(err)
	}
	return a.credential(t, map[string]string{"clientDataJSON": encode(clientData), "authenticatorData": encode(authData), "signature": encode(signature), "userHandle": encode(a.user)})
}
