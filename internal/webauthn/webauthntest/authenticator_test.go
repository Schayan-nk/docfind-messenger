package webauthntest

import (
	"encoding/base64"
	"testing"

	"github.com/Schayan-nk/docfind-messenger/internal/webauthn"
)

// The software passkey is checked against the real verifier, so the find
// tests that lean on it are leaning on something that works.
func TestTheSoftwarePasskeyPassesTheRealChecks(t *testing.T) {
	rp := webauthn.Relying{ID: "docfind.example.ir", Origins: []string{"https://docfind.example.ir"}}
	a := New(rp.ID, rp.Origins[0])
	ch, _ := webauthn.NewChallenge()
	cd, att := a.Register(webauthn.Encode(ch), "aGFuZGxl")
	raw := func(s string) []byte { b, _ := base64.RawURLEncoding.DecodeString(s); return b }
	cred, err := rp.Register(ch, raw(cd), raw(att))
	if err != nil {
		t.Fatalf("registering: %v", err)
	}
	ch2, _ := webauthn.NewChallenge()
	_, cd2, ad2, sig, handle := a.Assert(webauthn.Encode(ch2))
	if _, err := rp.Assert(cred, ch2, raw(cd2), raw(ad2), raw(sig)); err != nil {
		t.Fatalf("signing in: %v", err)
	}
	if handle != "aGFuZGxl" {
		t.Errorf("user handle %q", handle)
	}
}
