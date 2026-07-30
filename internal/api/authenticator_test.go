package api

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"testing"

	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
)

// A fake authenticator, so the passkey paths can be tested for real rather than mocked.
//
// ES256 on P-256 with stdlib crypto, plus the CBOR encoder the library already depends on
// for the two structures that need it: the "none" attestation object, and the COSE public
// key inside the attested credential data.
//
// Everything below is the authenticator's half of the WebAuthn ceremonies, which is what
// makes it possible to assert that a flipped signature byte, a wrong origin or a wrong rpId
// is actually rejected — a mock would only assert that we called the library.

// Authenticator data flags, from §6.1 of the spec.
const (
	flagUserPresent  byte = 1 << 0
	flagUserVerified byte = 1 << 2
	flagAttestedData byte = 1 << 6
)

type fakeAuthenticator struct {
	key    *ecdsa.PrivateKey
	aaguid [16]byte
	credID []byte

	// counter is what goes into authData. Real authenticators either increment it or leave
	// it at zero forever; both are exercised below.
	counter uint32
	flags   byte
}

func newFakeAuthenticator(t *testing.T) *fakeAuthenticator {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	credID := make([]byte, 32)
	if _, err := rand.Read(credID); err != nil {
		t.Fatalf("generate credential id: %v", err)
	}
	return &fakeAuthenticator{
		key:    key,
		aaguid: [16]byte{0xad, 0xce, 0x00, 0x02, 0x35, 0xbc, 0xc6, 0x0a, 0x64, 0x8b, 0x0b, 0x25, 0xf1, 0xf0, 0x55, 0x03},
		credID: credID,
		flags:  flagUserPresent | flagUserVerified,
	}
}

// withCounter sets the signature counter the next assertion reports.
func (a *fakeAuthenticator) withCounter(n uint32) *fakeAuthenticator {
	a.counter = n
	return a
}

// withFlags replaces the authenticator data flags, for the tests that clear UP.
func (a *fakeAuthenticator) withFlags(f byte) *fakeAuthenticator {
	a.flags = f
	return a
}

// coseKey encodes the public key as the CBOR map an authenticator embeds:
// {1: 2 (EC2), 3: -7 (ES256), -1: 1 (P-256), -2: x, -3: y}.
func (a *fakeAuthenticator) coseKey(t *testing.T) []byte {
	t.Helper()
	// int keys, so a map[int]any would not round-trip in the order the spec wants; the
	// library's decoder does not care about order, only about the keys.
	key := map[int]any{
		1:  2,
		3:  -7,
		-1: 1,
		-2: a.key.PublicKey.X.FillBytes(make([]byte, 32)),
		-3: a.key.PublicKey.Y.FillBytes(make([]byte, 32)),
	}
	b, err := webauthncbor.Marshal(key)
	if err != nil {
		t.Fatalf("marshal COSE key: %v", err)
	}
	return b
}

// authData builds rpIdHash | flags | counter, plus attested credential data when asked.
func (a *fakeAuthenticator) authData(t *testing.T, rpID string, attested bool) []byte {
	t.Helper()
	rpIDHash := sha256.Sum256([]byte(rpID))

	flags := a.flags
	if attested {
		flags |= flagAttestedData
	}

	out := make([]byte, 0, 128)
	out = append(out, rpIDHash[:]...)
	out = append(out, flags)
	out = binary.BigEndian.AppendUint32(out, a.counter)

	if attested {
		out = append(out, a.aaguid[:]...)
		out = binary.BigEndian.AppendUint16(out, uint16(len(a.credID)))
		out = append(out, a.credID...)
		out = append(out, a.coseKey(t)...)
	}
	return out
}

func clientData(t *testing.T, typ, challenge, origin string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"type":        typ,
		"challenge":   challenge,
		"origin":      origin,
		"crossOrigin": false,
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// register produces the JSON body POST /api/passkeys/register/finish expects.
//
// typ is "webauthn.create" for the happy path; the tests that post the wrong ceremony type
// pass something else.
func (a *fakeAuthenticator) register(t *testing.T, typ, challenge, origin, rpID string) string {
	t.Helper()

	attObj, err := webauthncbor.Marshal(map[string]any{
		"fmt":      "none",
		"attStmt":  map[string]any{},
		"authData": a.authData(t, rpID, true),
	})
	if err != nil {
		t.Fatalf("marshal attestation object: %v", err)
	}

	return mustJSON(t, map[string]any{
		"id":                      b64(a.credID),
		"rawId":                   b64(a.credID),
		"type":                    "public-key",
		"authenticatorAttachment": "platform",
		"clientExtensionResults":  map[string]any{"credProps": map[string]any{"rk": true}},
		"response": map[string]any{
			"clientDataJSON":    b64(clientData(t, typ, challenge, origin)),
			"attestationObject": b64(attObj),
			"transports":        []string{"internal"},
		},
	})
}

// assertion produces the JSON body POST /api/passkeys/login/finish expects, signing over
// authData || sha256(clientDataJSON) exactly as an authenticator does.
//
// mangle is called on the finished payload so a test can flip a byte in one field; nil
// leaves it alone.
func (a *fakeAuthenticator) assertion(t *testing.T, typ, challenge, origin, rpID string, handle []byte, mangle func(m map[string]any)) string {
	t.Helper()

	authData := a.authData(t, rpID, false)
	cd := clientData(t, typ, challenge, origin)
	cdHash := sha256.Sum256(cd)

	signed := append(append([]byte{}, authData...), cdHash[:]...)
	digest := sha256.Sum256(signed)
	sig, err := ecdsa.SignASN1(rand.Reader, a.key, digest[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	response := map[string]any{
		"clientDataJSON":    b64(cd),
		"authenticatorData": b64(authData),
		"signature":         b64(sig),
		"userHandle":        b64(handle),
	}
	payload := map[string]any{
		"id":                     b64(a.credID),
		"rawId":                  b64(a.credID),
		"type":                   "public-key",
		"clientExtensionResults": map[string]any{},
		"response":               response,
	}
	if mangle != nil {
		mangle(payload)
	}
	return mustJSON(t, payload)
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// flipLastByte corrupts one byte of a base64url field inside the assertion response, so a
// test can prove the signature actually covers what it claims to.
func flipLastByte(t *testing.T, m map[string]any, field string) {
	t.Helper()
	resp, ok := m["response"].(map[string]any)
	if !ok {
		t.Fatal("no response object")
	}
	raw, err := base64.RawURLEncoding.DecodeString(resp[field].(string))
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 {
		t.Fatalf("%s is empty", field)
	}
	raw[len(raw)-1] ^= 0x01
	resp[field] = b64(raw)
}
