package auth

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testSigner(t *testing.T) *Signer {
	t.Helper()
	key, err := LoadOrCreateKey(filepath.Join(tempDir(t), "token.key"))
	if err != nil {
		t.Fatal(err)
	}
	return NewSigner(key, time.Hour)
}

func TestSignAndVerifyRoundTrip(t *testing.T) {
	s := testSigner(t)
	token, expires, err := s.Sign("someone", 3)
	if err != nil {
		t.Fatal(err)
	}
	if !expires.After(time.Now()) {
		t.Error("token expires in the past")
	}
	claims, err := s.Verify(token)
	if err != nil {
		t.Fatalf("a freshly signed token did not verify: %v", err)
	}
	if claims.Subject != "someone" || claims.Version != 3 {
		t.Errorf("claims = %+v, want someone/3", claims)
	}
}

// The classic JWT failure: trusting the algorithm named in the token's own
// header. A caller who sets it to "none" and drops the signature must not be
// let in.
func TestAlgNoneIsRejected(t *testing.T) {
	s := testSigner(t)
	b64 := base64.RawURLEncoding

	payload, _ := json.Marshal(Claims{
		Subject: "attacker", Version: 0,
		IssuedAt: time.Now().Unix(), Expires: time.Now().Add(time.Hour).Unix(),
	})
	forged := b64.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`)) + "." +
		b64.EncodeToString(payload) + "."

	if _, err := s.Verify(forged); err == nil {
		t.Fatal("a token with alg:none was accepted")
	}
}

// Changing the payload must invalidate the signature -- otherwise anyone
// holding a viewer's token could promote themselves by editing one field.
func TestTamperedPayloadIsRejected(t *testing.T) {
	s := testSigner(t)
	token, _, err := s.Sign("viewer", 1)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(token, ".")
	b64 := base64.RawURLEncoding

	payload, _ := json.Marshal(Claims{
		Subject: "admin", Version: 1,
		IssuedAt: time.Now().Unix(), Expires: time.Now().Add(time.Hour).Unix(),
	})
	tampered := parts[0] + "." + b64.EncodeToString(payload) + "." + parts[2]

	if _, err := s.Verify(tampered); err == nil {
		t.Fatal("a token with a rewritten subject was accepted")
	}
}

// A token signed with a different key is somebody else's.
func TestForeignKeyIsRejected(t *testing.T) {
	a, b := testSigner(t), testSigner(t)
	token, _, err := a.Sign("someone", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Verify(token); err == nil {
		t.Fatal("a token signed with another key verified")
	}
}

func TestExpiredTokenIsRejected(t *testing.T) {
	key, err := LoadOrCreateKey(filepath.Join(tempDir(t), "token.key"))
	if err != nil {
		t.Fatal(err)
	}
	// Negative max age puts the expiry in the past; NewSigner would replace a
	// non-positive value with its default, so sign with a real one and wait.
	s := NewSigner(key, 40*time.Millisecond)
	token, _, err := s.Sign("someone", 0)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(70 * time.Millisecond)
	if _, err := s.Verify(token); err == nil {
		t.Fatal("an expired token was accepted")
	}
}

func TestMalformedTokensAreRejected(t *testing.T) {
	s := testSigner(t)
	for _, bad := range []string{
		"", "not.a.token", "onlyonepart",
		"a.b", "a.b.c.d",
		"!!!.!!!.!!!",
	} {
		if _, err := s.Verify(bad); err == nil {
			t.Errorf("malformed token %q was accepted", bad)
		}
	}
}

// The signing key must survive a restart, or every restart signs everyone out.
func TestKeyIsStableAcrossLoads(t *testing.T) {
	path := filepath.Join(tempDir(t), "nested", "token.key")
	first, err := LoadOrCreateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadOrCreateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Error("the signing key changed between loads")
	}
	if len(first) < 32 {
		t.Errorf("key is %d bytes, want at least 32", len(first))
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 && os.PathSeparator == '/' {
		t.Errorf("key file mode is %o, want no group or world access", mode)
	}
}

// The point of the version claim: bumping it on the account invalidates every
// token already issued, which is what makes a signed token revocable.
func TestTokenVersionRevokesOldTokens(t *testing.T) {
	dir := tempDir(t)
	store, err := OpenStore(filepath.Join(dir, "users.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create("keeper.admin", "Keeper", "a-long-password", RoleAdmin); err != nil {
		t.Fatal(err)
	}
	u, err := store.Create("target.user", "Target", "a-long-password", RoleManager)
	if err != nil {
		t.Fatal(err)
	}

	s := testSigner(t)
	token, _, err := s.Sign(u.Username, u.TokenVersion)
	if err != nil {
		t.Fatal(err)
	}

	// Still the current version, so the token matches the account.
	claims, err := s.Verify(token)
	if err != nil {
		t.Fatal(err)
	}
	current, _ := store.Get(u.Username)
	if claims.Version != current.TokenVersion {
		t.Fatal("a fresh token did not match the account's version")
	}

	// Changing the password must strand it.
	if err := store.SetPassword(u.Username, "another-long-password"); err != nil {
		t.Fatal(err)
	}
	after, _ := store.Get(u.Username)
	if claims.Version == after.TokenVersion {
		t.Error("changing a password did not bump the token version")
	}

	// So must disabling the account.
	before := after.TokenVersion
	if _, err := store.Update(u.Username, "Target", RoleManager, true); err != nil {
		t.Fatal(err)
	}
	disabled, _ := store.Get(u.Username)
	if disabled.TokenVersion == before {
		t.Error("disabling an account did not bump the token version")
	}

	// And an explicit revoke.
	before = disabled.TokenVersion
	if err := store.RevokeTokens(u.Username); err != nil {
		t.Fatal(err)
	}
	revoked, _ := store.Get(u.Username)
	if revoked.TokenVersion == before {
		t.Error("RevokeTokens did not bump the token version")
	}
}
