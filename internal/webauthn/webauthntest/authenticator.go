// Package webauthntest is a software passkey, for tests: it answers the
// same registration and sign-in ceremonies a phone or a security key does,
// with an ES256 key it holds in memory. Only tests import it.
package webauthntest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"math/big"
)

var b64 = base64.RawURLEncoding

// Authenticator is one passkey on one device.
type Authenticator struct {
	RPID, Origin string
	CredID       []byte
	UserHandle   []byte
	Count        uint32
	// Unverified makes the device report that nobody was verified (no PIN or
	// biometric), which the server must refuse.
	Unverified bool
	key        *ecdsa.PrivateKey
}

// New makes a passkey for rpID, used from origin.
func New(rpID, origin string) *Authenticator {
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	id := make([]byte, 16)
	rand.Read(id)
	return &Authenticator{RPID: rpID, Origin: origin, CredID: id, key: k}
}

func (a *Authenticator) flags(attested bool) byte {
	f := byte(0x01) // user present
	if !a.Unverified {
		f |= 0x04
	}
	if attested {
		f |= 0x40
	}
	return f
}

func (a *Authenticator) authData(attested bool) []byte {
	h := sha256.Sum256([]byte(a.RPID))
	out := append([]byte(nil), h[:]...)
	out = append(out, a.flags(attested))
	out = binary.BigEndian.AppendUint32(out, a.Count)
	if attested {
		out = append(out, make([]byte, 16)...)
		out = binary.BigEndian.AppendUint16(out, uint16(len(a.CredID)))
		out = append(out, a.CredID...)
		out = append(out, a.coseKey()...)
	}
	return out
}

func (a *Authenticator) coseKey() []byte {
	pad := func(b *big.Int) []byte { o := make([]byte, 32); b.FillBytes(o); return o }
	// {1: 2, 3: -7, -1: 1, -2: x, -3: y}, keys in canonical order.
	out := []byte{0xa5, 0x01, 0x02, 0x03, 0x26, 0x20, 0x01, 0x21, 0x58, 0x20}
	out = append(out, pad(a.key.X)...)
	out = append(out, 0x22, 0x58, 0x20)
	return append(out, pad(a.key.Y)...)
}

func (a *Authenticator) clientData(typ, challenge string) []byte {
	b, _ := json.Marshal(map[string]string{"type": typ, "challenge": challenge, "origin": a.Origin})
	return b
}

// Register answers a registration: the challenge (base64url, as the server
// sent it) and the user handle it asked to store. It returns the two fields
// the page posts back, base64url.
func (a *Authenticator) Register(challenge, userHandle string) (clientData, attestation string) {
	a.UserHandle, _ = b64.DecodeString(userHandle)
	ad := a.authData(true)
	// {"fmt": "none", "attStmt": {}, "authData": ad}
	att := []byte{0xa3, 0x63, 'f', 'm', 't', 0x64, 'n', 'o', 'n', 'e',
		0x67, 'a', 't', 't', 'S', 't', 'm', 't', 0xa0,
		0x68, 'a', 'u', 't', 'h', 'D', 'a', 't', 'a', 0x59, byte(len(ad) >> 8), byte(len(ad))}
	att = append(att, ad...)
	return b64.EncodeToString(a.clientData("webauthn.create", challenge)), b64.EncodeToString(att)
}

// Assert answers a sign-in, moving the counter on first.
func (a *Authenticator) Assert(challenge string) (id, clientData, authData, signature, userHandle string) {
	a.Count++
	cd := a.clientData("webauthn.get", challenge)
	ad := a.authData(false)
	sum := sha256.Sum256(cd)
	h := sha256.Sum256(append(append([]byte(nil), ad...), sum[:]...))
	sig, _ := ecdsa.SignASN1(rand.Reader, a.key, h[:])
	return b64.EncodeToString(a.CredID), b64.EncodeToString(cd), b64.EncodeToString(ad),
		b64.EncodeToString(sig), b64.EncodeToString(a.UserHandle)
}
