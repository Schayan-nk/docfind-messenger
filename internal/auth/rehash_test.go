package auth

import (
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/base64"
	"path/filepath"
	"strings"
	"testing"
)

// weakHash writes plain the way an older build might have: fewer iterations.
func weakHash(t *testing.T, plain string, iter int) string {
	t.Helper()
	salt := []byte("0123456789abcdef")
	key, err := pbkdf2.Key(sha256.New, plain, salt, iter, 32)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join([]string{hashScheme, "1000",
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)}, "$")
}

func TestCurrentHashesNeedNoRehash(t *testing.T) {
	h, err := HashPassword("a perfectly good password")
	if err != nil {
		t.Fatal(err)
	}
	if NeedsRehash(h) {
		t.Errorf("a hash written just now needs a rehash: %s", h)
	}
	for _, old := range []string{
		weakHash(t, "x", 1000),
		"bcrypt$10$whatever$else",
		"garbage",
	} {
		if !NeedsRehash(old) {
			t.Errorf("%q does not need a rehash", old)
		}
	}
}

// Signing in with a weakly hashed password replaces the hash -- and signs
// nobody out, because it is not a password change.
func TestSigningInUpgradesAWeakHash(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "users.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create("old.timer", "Old Timer", "remembered for years", RoleViewer); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	u := s.users["old.timer"]
	u.PasswordHash = weakHash(t, "remembered for years", 1000)
	version := u.TokenVersion
	s.mu.Unlock()

	if _, err := s.Authenticate("old.timer", "remembered for years"); err != nil {
		t.Fatalf("the weakly hashed password did not work: %v", err)
	}
	s.mu.RLock()
	after := *s.users["old.timer"]
	s.mu.RUnlock()
	if NeedsRehash(after.PasswordHash) {
		t.Errorf("the hash was not upgraded: %s", after.PasswordHash)
	}
	if after.TokenVersion != version {
		t.Error("upgrading the hash signed the account out everywhere")
	}
	if _, err := s.Authenticate("old.timer", "remembered for years"); err != nil {
		t.Errorf("the password stopped working after the upgrade: %v", err)
	}
	if _, err := s.Authenticate("old.timer", "wrong"); err == nil {
		t.Error("a wrong password worked after the upgrade")
	}
}
