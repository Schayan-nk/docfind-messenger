package find

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Schayan-nk/docfind-messenger/internal/webauthn"
	"github.com/Schayan-nk/docfind-messenger/internal/webauthn/webauthntest"
)

var testRP = &webauthn.Relying{ID: "docfind.example.ir", Origins: []string{"https://docfind.example.ir"}}

// registerPasskey puts a software passkey on "reader" through the routes.
func registerPasskey(t *testing.T, s *Server, token string) *webauthntest.Authenticator {
	t.Helper()
	opts := asUser(t, s, "POST", "/api/account/passkeys/options", token, `{"password":"correct horse battery"}`)
	if opts.Code != http.StatusOK {
		t.Fatalf("options: %d %s", opts.Code, opts.Body.String())
	}
	var o struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
			User      struct {
				ID string `json:"id"`
			} `json:"user"`
			AuthenticatorSelection map[string]any `json:"authenticatorSelection"`
		} `json:"publicKey"`
	}
	json.Unmarshal(opts.Body.Bytes(), &o)
	if o.PublicKey.AuthenticatorSelection["userVerification"] != "required" {
		t.Error("registration does not require the person to be verified")
	}
	a := webauthntest.New(testRP.ID, testRP.Origins[0])
	cd, att := a.Register(o.PublicKey.Challenge, o.PublicKey.User.ID)
	b, _ := json.Marshal(map[string]string{"name": "گوشی من", "client_data": cd, "attestation": att})
	if rec := asUser(t, s, "POST", "/api/account/passkeys", token, string(b)); rec.Code != http.StatusOK {
		t.Fatalf("adding: %d %s", rec.Code, rec.Body.String())
	}
	return a
}

func passkeySignIn(t *testing.T, s *Server, a *webauthntest.Authenticator) (int, string) {
	t.Helper()
	opts := asUser(t, s, "POST", "/api/login/passkey/options", "", "{}")
	var o struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
		} `json:"publicKey"`
	}
	json.Unmarshal(opts.Body.Bytes(), &o)
	id, cd, ad, sig, handle := a.Assert(o.PublicKey.Challenge)
	b, _ := json.Marshal(map[string]string{"id": id, "client_data": cd, "authenticator_data": ad,
		"signature": sig, "user_handle": handle})
	rec := asUser(t, s, "POST", "/api/login/passkey", "", string(b))
	return rec.Code, rec.Body.String()
}

func TestAPasskeyThatWasNotVerifiedOrWasCopiedIsRefused(t *testing.T) {
	s, _, viewer, _ := accountsTestServer(t)
	s.UsePasskeys(testRP)
	a := registerPasskey(t, s, viewer)

	a.Unverified = true
	if code, _ := passkeySignIn(t, s, a); code != http.StatusUnauthorized {
		t.Errorf("an unverified sign-in answered %d", code)
	}
	a.Unverified = false
	if code, _ := passkeySignIn(t, s, a); code != http.StatusOK {
		t.Fatalf("a good sign-in answered %d", code)
	}
	// A copy of the key whose counter is behind: the original moved on.
	a.Count -= 2
	if code, _ := passkeySignIn(t, s, a); code != http.StatusUnauthorized {
		t.Errorf("a counter that went backwards answered %d", code)
	}
}

func TestAPasskeyChallengeIsGoodOnce(t *testing.T) {
	s, _, viewer, _ := accountsTestServer(t)
	s.UsePasskeys(testRP)
	a := registerPasskey(t, s, viewer)
	opts := asUser(t, s, "POST", "/api/login/passkey/options", "", "{}")
	var o struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
		} `json:"publicKey"`
	}
	json.Unmarshal(opts.Body.Bytes(), &o)
	send := func() int {
		id, cd, ad, sig, handle := a.Assert(o.PublicKey.Challenge)
		b, _ := json.Marshal(map[string]string{"id": id, "client_data": cd, "authenticator_data": ad,
			"signature": sig, "user_handle": handle})
		return asUser(t, s, "POST", "/api/login/passkey", "", string(b)).Code
	}
	if code := send(); code != http.StatusOK {
		t.Fatalf("first use answered %d", code)
	}
	if code := send(); code != http.StatusUnauthorized {
		t.Errorf("the same challenge a second time answered %d", code)
	}
}

func TestPasskeysAreOffUntilConfigured(t *testing.T) {
	s, _, viewer, _ := accountsTestServer(t)
	if rec := asUser(t, s, "POST", "/api/login/passkey/options", "", "{}"); rec.Code != http.StatusNotFound {
		t.Errorf("sign-in options with passkeys off answered %d", rec.Code)
	}
	if rec := asUser(t, s, "POST", "/api/account/passkeys/options", viewer, `{"password":"correct horse battery"}`); rec.Code != http.StatusNotFound {
		t.Errorf("registration with passkeys off answered %d", rec.Code)
	}
}

func TestRemovingAPasskeyAsksForThePassword(t *testing.T) {
	s, admin, viewer, _ := accountsTestServer(t)
	s.UsePasskeys(testRP)
	a := registerPasskey(t, s, viewer)
	id := webauthn.Encode(a.CredID)
	if rec := asUser(t, s, "POST", "/api/account/passkeys/remove", viewer, `{"id":"`+id+`","password":"wrong"}`); rec.Code != http.StatusForbidden {
		t.Errorf("removing with a wrong password answered %d", rec.Code)
	}
	if rec := asUser(t, s, "POST", "/api/account/passkeys/remove", viewer, `{"id":"`+id+`","password":"correct horse battery"}`); rec.Code != http.StatusOK {
		t.Fatalf("removing: %d %s", rec.Code, rec.Body.String())
	}
	if code, _ := passkeySignIn(t, s, a); code != http.StatusUnauthorized {
		t.Errorf("a removed passkey still signed in: %d", code)
	}
	// An administrator can clear somebody's passkeys; a viewer cannot.
	b := registerPasskey(t, s, viewer)
	if rec := asUser(t, s, "POST", "/api/users/passkeys/reset?u=reader", viewer, ""); rec.Code != http.StatusForbidden {
		t.Errorf("a viewer's reset answered %d", rec.Code)
	}
	if rec := asUser(t, s, "POST", "/api/users/passkeys/reset?u=reader", admin, ""); rec.Code != http.StatusOK {
		t.Fatalf("the administrator's reset: %d", rec.Code)
	}
	if code, _ := passkeySignIn(t, s, b); code != http.StatusUnauthorized {
		t.Errorf("a passkey cleared by the administrator still signed in: %d", code)
	}
}
