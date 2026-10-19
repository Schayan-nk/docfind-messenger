package webauthn

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math/big"
	"sort"
	"testing"
)

// A software authenticator: it does what a phone or a security key does, so
// every check in this package can be driven both ways.

// cbor is a minimal encoder for the test side.
func cbor(v any) []byte {
	head := func(major byte, n uint64) []byte {
		switch {
		case n < 24:
			return []byte{major<<5 | byte(n)}
		case n < 1<<8:
			return []byte{major<<5 | 24, byte(n)}
		case n < 1<<16:
			return []byte{major<<5 | 25, byte(n >> 8), byte(n)}
		}
		b := []byte{major<<5 | 26, 0, 0, 0, 0}
		binary.BigEndian.PutUint32(b[1:], uint32(n))
		return b
	}
	switch x := v.(type) {
	case int:
		if x >= 0 {
			return head(0, uint64(x))
		}
		return head(1, uint64(-1-x))
	case []byte:
		return append(head(2, uint64(len(x))), x...)
	case string:
		return append(head(3, uint64(len(x))), x...)
	case map[any]any:
		// Canonical-ish order, so the same key always encodes the same way.
		keys := make([]any, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return string(cbor(keys[i])) < string(cbor(keys[j])) })
		out := head(5, uint64(len(x)))
		for _, k := range keys {
			out = append(out, cbor(k)...)
			out = append(out, cbor(x[k])...)
		}
		return out
	}
	panic("cbor: unsupported")
}

type softKey struct {
	cose []byte
	sign func([]byte) []byte
}

func es256Key(t *testing.T) softKey {
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	pad := func(b *big.Int) []byte { out := make([]byte, 32); b.FillBytes(out); return out }
	return softKey{
		cose: cbor(map[any]any{1: 2, 3: AlgES256, -1: 1, -2: pad(k.X), -3: pad(k.Y)}),
		sign: func(d []byte) []byte {
			h := sha256.Sum256(d)
			s, _ := ecdsa.SignASN1(rand.Reader, k, h[:])
			return s
		},
	}
}

func ed25519Key(t *testing.T) softKey {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	return softKey{
		cose: cbor(map[any]any{1: 1, 3: AlgEdDSA, -1: 6, -2: []byte(pub)}),
		sign: func(d []byte) []byte { return ed25519.Sign(priv, d) },
	}
}

func rs256Key(t *testing.T) softKey {
	k, _ := rsa.GenerateKey(rand.Reader, 2048)
	return softKey{
		cose: cbor(map[any]any{1: 3, 3: AlgRS256, -1: k.N.Bytes(), -2: big.NewInt(int64(k.E)).Bytes()}),
		sign: func(d []byte) []byte {
			h := sha256.Sum256(d)
			s, _ := rsa.SignPKCS1v15(rand.Reader, k, crypto.SHA256, h[:])
			return s
		},
	}
}

var rp = Relying{ID: "docfind.example.ir", Origins: []string{"https://docfind.example.ir"}}

func clientJSON(typ string, challenge []byte, origin string) []byte {
	b, _ := json.Marshal(map[string]string{"type": typ, "challenge": Encode(challenge), "origin": origin})
	return b
}

func authDataFor(rpID string, flags byte, count uint32, credID, cose []byte) []byte {
	h := sha256.Sum256([]byte(rpID))
	out := append([]byte(nil), h[:]...)
	out = append(out, flags)
	out = binary.BigEndian.AppendUint32(out, count)
	if credID != nil {
		out = append(out, make([]byte, 16)...) // AAGUID
		out = binary.BigEndian.AppendUint16(out, uint16(len(credID)))
		out = append(out, credID...)
		out = append(out, cose...)
	}
	return out
}

func register(t *testing.T, key softKey, count uint32) (*Credential, []byte) {
	t.Helper()
	ch, _ := NewChallenge()
	id := []byte("credential-" + t.Name())
	att := cbor(map[any]any{"fmt": "none", "attStmt": map[any]any{},
		"authData": authDataFor(rp.ID, flagUP|flagUV|flagAT, count, id, key.cose)})
	cred, err := rp.Register(ch, clientJSON("webauthn.create", ch, rp.Origins[0]), att)
	if err != nil {
		t.Fatalf("registering: %v", err)
	}
	return cred, id
}

func assertWith(key softKey, cred *Credential, count uint32, flags byte, mutate func(cd, ad []byte) ([]byte, []byte)) (uint32, error) {
	ch, _ := NewChallenge()
	cd := clientJSON("webauthn.get", ch, rp.Origins[0])
	ad := authDataFor(rp.ID, flags, count, nil, nil)
	if mutate != nil {
		cd, ad = mutate(cd, ad)
	}
	sum := sha256.Sum256(cd)
	sig := key.sign(append(append([]byte(nil), ad...), sum[:]...))
	return rp.Assert(cred, ch, cd, ad, sig)
}

func TestEveryRefusal(t *testing.T) {
	key := es256Key(t)
	cred, _ := register(t, key, 5)
	cases := map[string]struct {
		count  uint32
		flags  byte
		mutate func(cd, ad []byte) ([]byte, []byte)
	}{
		"another site's passkey": {6, flagUP | flagUV, func(cd, ad []byte) ([]byte, []byte) {
			return cd, authDataFor("evil.example", flagUP|flagUV, 6, nil, nil)
		}},
		"another origin": {6, flagUP | flagUV, func(cd, ad []byte) ([]byte, []byte) {
			var m map[string]string
			json.Unmarshal(cd, &m)
			m["origin"] = "https://docfind.example.ir.evil.example"
			b, _ := json.Marshal(m)
			return b, ad
		}},
		"a registration presented as a sign-in": {6, flagUP | flagUV, func(cd, ad []byte) ([]byte, []byte) {
			var m map[string]string
			json.Unmarshal(cd, &m)
			m["type"] = "webauthn.create"
			b, _ := json.Marshal(m)
			return b, ad
		}},
		"nobody verified":          {6, flagUP, nil},
		"nobody present":           {6, flagUV, nil},
		"a counter that went back": {5, flagUP | flagUV, nil},
	}
	for name, c := range cases {
		if _, err := assertWith(key, cred, c.count, c.flags, c.mutate); !errors.Is(err, ErrVerify) {
			t.Errorf("%s: err = %v, want a refusal", name, err)
		}
	}
	// Another key's signature, and a stale challenge.
	other := es256Key(t)
	if _, err := assertWith(other, cred, 6, flagUP|flagUV, nil); !errors.Is(err, ErrVerify) {
		t.Errorf("another key's signature: %v", err)
	}
	ch, _ := NewChallenge()
	cd := clientJSON("webauthn.get", ch, rp.Origins[0])
	ad := authDataFor(rp.ID, flagUP|flagUV, 7, nil, nil)
	sum := sha256.Sum256(cd)
	sig := key.sign(append(append([]byte(nil), ad...), sum[:]...))
	stale, _ := NewChallenge()
	if _, err := rp.Assert(cred, stale, cd, ad, sig); !errors.Is(err, ErrVerify) {
		t.Errorf("a challenge that was not issued: %v", err)
	}
	// The right thing still works, and moves the counter on.
	if n, err := assertWith(key, cred, 6, flagUP|flagUV, nil); err != nil || n != 6 {
		t.Errorf("the right sign-in: %d, %v", n, err)
	}
}

func TestRegistrationRefusesAnUnverifiedPerson(t *testing.T) {
	key := es256Key(t)
	ch, _ := NewChallenge()
	att := cbor(map[any]any{"fmt": "none", "attStmt": map[any]any{},
		"authData": authDataFor(rp.ID, flagUP|flagAT, 0, []byte("id"), key.cose)})
	if _, err := rp.Register(ch, clientJSON("webauthn.create", ch, rp.Origins[0]), att); !errors.Is(err, ErrVerify) {
		t.Errorf("registering without user verification: %v", err)
	}
}

// What strangers send is parsed defensively: nothing here panics.
func TestMalformedInputIsRefusedNotCrashedOn(t *testing.T) {
	for _, b := range [][]byte{nil, {0xff}, {0x9f}, {0x5b, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}, {0xa1, 0x01}, {0x81}} {
		if _, _, err := decodeCBOR(b); err == nil {
			t.Errorf("% x decoded", b)
		}
	}
	// Nesting past the limit.
	deep := []byte{}
	for i := 0; i < 20; i++ {
		deep = append(deep, 0x81)
	}
	deep = append(deep, 0x01)
	if _, _, err := decodeCBOR(deep); err == nil {
		t.Error("twenty nested arrays decoded")
	}
	if _, err := parseAuthData(make([]byte, 36), false); err == nil {
		t.Error("short authenticator data parsed")
	}
}
