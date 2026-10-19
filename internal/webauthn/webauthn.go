// Package webauthn checks passkey sign-ins (W3C Web Authentication, Level 2):
// registering a passkey, and signing in with one.
//
// What it verifies, and what it deliberately does not:
//
//   - The client data: that the browser was asked for THIS challenge, on
//     THIS origin, for the right ceremony.
//
//   - The authenticator data: that it is for THIS relying party (the hash of
//     the RP ID), that a person was present, and -- because a passkey here
//     replaces both the password and the second step -- that they were
//     VERIFIED (PIN, fingerprint, face).
//
//   - The signature, with the key registered for that credential; and the
//     signature counter, where the authenticator keeps one, to notice a
//     cloned credential.
//
//   - NOT the attestation. Registration asks for attestation "none": the
//     question is whether the person who registered the key is the one using
//     it, not which company made the device, and verifying vendor
//     certificate chains would be a great deal of code for an answer nobody
//     here acts on.
//
// Keys: ES256 (P-256), EdDSA (Ed25519) and RS256 -- what platforms and
// security keys actually offer.
package webauthn

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
)

// COSE algorithm identifiers.
const (
	AlgES256 = -7
	AlgEdDSA = -8
	AlgRS256 = -257
)

// Algorithms is what registration offers, most preferred first.
var Algorithms = []int{AlgES256, AlgEdDSA, AlgRS256}

// ErrVerify is wrapped by every refusal.
var ErrVerify = errors.New("webauthn: verification failed")

func fail(why string) error { return fmt.Errorf("%w: %s", ErrVerify, why) }

// Credential is one registered passkey.
type Credential struct {
	ID        []byte `json:"id"`
	PublicKey []byte `json:"public_key"` // the COSE key, as registered
	Alg       int    `json:"alg"`
	SignCount uint32 `json:"sign_count"`
}

// Relying is who is asking: the RP ID (a host name -- never an IP address,
// which browsers refuse) and the origins pages are served from.
type Relying struct {
	ID      string
	Origins []string
}

// NewChallenge returns 32 random bytes.
func NewChallenge() ([]byte, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}

var b64 = base64.RawURLEncoding

// clientData is the part of the browser's clientDataJSON checked here.
type clientData struct {
	Type      string `json:"type"`
	Challenge string `json:"challenge"`
	Origin    string `json:"origin"`
}

func (rp Relying) checkClientData(raw []byte, wantType string, challenge []byte) error {
	var cd clientData
	if err := json.Unmarshal(raw, &cd); err != nil {
		return fail("client data is not JSON")
	}
	if cd.Type != wantType {
		return fail("wrong ceremony: " + cd.Type)
	}
	got, err := b64.DecodeString(cd.Challenge)
	if err != nil || subtle.ConstantTimeCompare(got, challenge) != 1 {
		return fail("not the challenge that was issued")
	}
	for _, o := range rp.Origins {
		if cd.Origin == o {
			return nil
		}
	}
	return fail("origin " + cd.Origin + " is not this site")
}

// authData is the authenticator's own record of the ceremony.
type authData struct {
	rpIDHash  []byte
	flags     byte
	signCount uint32
	credID    []byte
	credKey   []byte // raw COSE key, registration only
}

const (
	flagUP = 0x01 // user present
	flagUV = 0x04 // user verified
	flagAT = 0x40 // attested credential data follows
)

func parseAuthData(b []byte, wantCredential bool) (*authData, error) {
	if len(b) < 37 {
		return nil, fail("authenticator data too short")
	}
	a := &authData{rpIDHash: b[:32], flags: b[32], signCount: binary.BigEndian.Uint32(b[33:37])}
	if !wantCredential {
		return a, nil
	}
	if a.flags&flagAT == 0 || len(b) < 37+16+2 {
		return nil, fail("no credential in the authenticator data")
	}
	rest := b[37+16:] // skip the AAGUID
	n := int(binary.BigEndian.Uint16(rest[:2]))
	rest = rest[2:]
	if n == 0 || n > 1023 || len(rest) < n {
		return nil, fail("bad credential id")
	}
	a.credID = append([]byte(nil), rest[:n]...)
	rest = rest[n:]
	if _, used, err := decodeCBOR(rest); err != nil {
		return nil, fail("bad credential key")
	} else {
		a.credKey = append([]byte(nil), rest[:used]...)
	}
	return a, nil
}

func (rp Relying) checkAuthData(a *authData) error {
	want := sha256.Sum256([]byte(rp.ID))
	if subtle.ConstantTimeCompare(a.rpIDHash, want[:]) != 1 {
		return fail("the passkey is for another site")
	}
	if a.flags&flagUP == 0 {
		return fail("nobody was present")
	}
	if a.flags&flagUV == 0 {
		return fail("the person was not verified (no PIN or biometric)")
	}
	return nil
}

// Register checks a new passkey and returns it.
func (rp Relying) Register(challenge, clientDataJSON, attestationObject []byte) (*Credential, error) {
	if err := rp.checkClientData(clientDataJSON, "webauthn.create", challenge); err != nil {
		return nil, err
	}
	obj, _, err := decodeCBOR(attestationObject)
	if err != nil {
		return nil, fail("attestation is not CBOR")
	}
	m, ok := obj.(map[any]any)
	if !ok {
		return nil, fail("attestation is not a map")
	}
	raw, ok := m["authData"].([]byte)
	if !ok {
		return nil, fail("no authenticator data")
	}
	a, err := parseAuthData(raw, true)
	if err != nil {
		return nil, err
	}
	if err := rp.checkAuthData(a); err != nil {
		return nil, err
	}
	alg, err := coseAlg(a.credKey)
	if err != nil {
		return nil, err
	}
	return &Credential{ID: a.credID, PublicKey: a.credKey, Alg: alg, SignCount: a.signCount}, nil
}

// Assert checks a sign-in with cred and returns the authenticator's new
// signature counter, to be stored.
func (rp Relying) Assert(cred *Credential, challenge, clientDataJSON, authenticatorData, signature []byte) (uint32, error) {
	if err := rp.checkClientData(clientDataJSON, "webauthn.get", challenge); err != nil {
		return 0, err
	}
	a, err := parseAuthData(authenticatorData, false)
	if err != nil {
		return 0, err
	}
	if err := rp.checkAuthData(a); err != nil {
		return 0, err
	}
	sum := sha256.Sum256(clientDataJSON)
	signed := append(append([]byte(nil), authenticatorData...), sum[:]...)
	if err := verifySignature(cred.PublicKey, signed, signature); err != nil {
		return 0, err
	}
	// A counter that did not move forward means two copies of the key exist.
	// Zero on both sides is an authenticator that keeps no counter (passkeys
	// synced between devices do not), which is fine.
	if (a.signCount != 0 || cred.SignCount != 0) && a.signCount <= cred.SignCount {
		return 0, fail("the signature counter went backwards: the passkey may have been copied")
	}
	return a.signCount, nil
}

// coseAlg reads the algorithm out of a COSE key and checks the key is one
// this package can verify with.
func coseAlg(key []byte) (int, error) {
	_, alg, err := parseCOSE(key)
	return alg, err
}

func parseCOSE(key []byte) (any, int, error) {
	v, used, err := decodeCBOR(key)
	if err != nil || used != len(key) {
		return nil, 0, fail("bad public key")
	}
	m, ok := v.(map[any]any)
	if !ok {
		return nil, 0, fail("bad public key")
	}
	num := func(k int64) (int64, bool) { x, ok := m[k].(int64); return x, ok }
	byt := func(k int64) []byte { x, _ := m[k].([]byte); return x }
	kty, _ := num(1)
	alg, _ := num(3)
	switch {
	case kty == 2 && alg == AlgES256: // EC2, P-256
		if crv, _ := num(-1); crv != 1 {
			return nil, 0, fail("unsupported curve")
		}
		x, y := byt(-2), byt(-3)
		if len(x) != 32 || len(y) != 32 {
			return nil, 0, fail("bad P-256 key")
		}
		// The uncompressed point, 0x04 || X || Y; the parser refuses one that is
		// not on the curve.
		pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), append(append([]byte{4}, x...), y...))
		if err != nil {
			return nil, 0, fail("P-256 point not on the curve")
		}
		return pub, AlgES256, nil
	case kty == 1 && alg == AlgEdDSA: // OKP, Ed25519
		if crv, _ := num(-1); crv != 6 {
			return nil, 0, fail("unsupported curve")
		}
		x := byt(-2)
		if len(x) != ed25519.PublicKeySize {
			return nil, 0, fail("bad Ed25519 key")
		}
		return ed25519.PublicKey(x), AlgEdDSA, nil
	case kty == 3 && alg == AlgRS256:
		n, e := byt(-1), byt(-2)
		if len(n) < 256 || len(e) == 0 || len(e) > 4 {
			return nil, 0, fail("bad RSA key")
		}
		ev := 0
		for _, c := range e {
			ev = ev<<8 | int(c)
		}
		return &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: ev}, AlgRS256, nil
	}
	return nil, 0, fail(fmt.Sprintf("unsupported key type %d / algorithm %d", kty, alg))
}

func verifySignature(key, signed, sig []byte) error {
	pub, _, err := parseCOSE(key)
	if err != nil {
		return err
	}
	switch k := pub.(type) {
	case *ecdsa.PublicKey:
		h := sha256.Sum256(signed)
		if ecdsa.VerifyASN1(k, h[:], sig) {
			return nil
		}
	case ed25519.PublicKey:
		if ed25519.Verify(k, signed, sig) {
			return nil
		}
	case *rsa.PublicKey:
		h := sha256.Sum256(signed)
		if rsa.VerifyPKCS1v15(k, crypto.SHA256, h[:], sig) == nil {
			return nil
		}
	}
	return fail("the signature does not match the registered key")
}

// Encode and Decode are the base64url the browser side speaks.
func Encode(b []byte) string { return b64.EncodeToString(b) }

// Decode accepts base64url with or without padding.
func Decode(s string) ([]byte, error) {
	return b64.DecodeString(string(bytes.TrimRight([]byte(s), "=")))
}
