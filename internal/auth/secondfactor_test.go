package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// gcmSealer is a real AES-GCM box, so "the key is stored sealed" is tested
// against encryption and not against a stand-in.
type gcmSealer struct{ aead cipher.AEAD }

func newGCMSealer(t *testing.T) *gcmSealer {
	t.Helper()
	key := make([]byte, 32)
	rand.Read(key)
	blk, _ := aes.NewCipher(key)
	aead, _ := cipher.NewGCM(blk)
	return &gcmSealer{aead}
}

func (g *gcmSealer) Seal(p []byte) ([]byte, error) {
	n := make([]byte, g.aead.NonceSize())
	rand.Read(n)
	return g.aead.Seal(n, n, p, nil), nil
}

func (g *gcmSealer) Open(s []byte) ([]byte, error) {
	ns := g.aead.NonceSize()
	if len(s) < ns {
		return nil, errors.New("short")
	}
	return g.aead.Open(nil, s[:ns], s[ns:], nil)
}

func twoStepStore(t *testing.T) (*Store, string, *gcmSealer) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "users.json")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create("ali", "Ali", "correct horse battery", RoleViewer); err != nil {
		t.Fatal(err)
	}
	box := newGCMSealer(t)
	s.UseSealer(box)
	return s, path, box
}

// With the database down, accounts come from the signed snapshot -- which
// must carry the second factor, or emergency sign-in is a way round it.
func TestTheEmergencySnapshotKeepsTheSecondStep(t *testing.T) {
	snap := filepath.Join(t.TempDir(), "users.snapshot.json")
	users := oneUser("ali")
	users[0].SecondFactor = &SecondFactor{Secret: "c2VhbGVk", Enabled: true, Recovery: []string{"h1"}}
	if err := NewFallback(newSpy(), snap, testSnapKey).Save(users); err != nil {
		t.Fatal(err)
	}
	loaded, err := NewFallback(newDead(), snap, testSnapKey).Load()
	if err != nil || len(loaded) != 1 {
		t.Fatalf("loading the snapshot: %d, %v", len(loaded), err)
	}
	if !loaded[0].SecondFactor.On() || len(loaded[0].SecondFactor.Recovery) != 1 {
		t.Errorf("the snapshot lost the second step: %+v", loaded[0].SecondFactor)
	}
}

// The email way: no key at all, the address recorded, and a six-digit code
// never checked against the account (the mailer checks its own) -- only
// recovery codes are.
func TestTheEmailWay(t *testing.T) {
	s, _, _ := twoStepStore(t)
	if _, err := s.EnableEmailFactor("ali", "not an address", time.Now()); err == nil {
		t.Error("a malformed address was accepted")
	}
	codes, err := s.EnableEmailFactor("ali", "ali@example.com", time.Now())
	if err != nil || len(codes) != RecoveryCodeCount {
		t.Fatalf("turning it on: %v", err)
	}
	u, _ := s.Get("ali")
	if !s.SecondFactorOn("ali") || s.SecondFactorMethod("ali") != MethodEmail || u.Email != "ali@example.com" {
		t.Errorf("on=%v method=%q email=%q", s.SecondFactorOn("ali"), s.SecondFactorMethod("ali"), u.Email)
	}
	if u.SecondFactor.Secret != "" {
		t.Error("the email way stored a key")
	}
	if _, _, err := s.VerifySecondFactor("ali", "123456", time.Now()); !errors.Is(err, ErrBadSecondFactor) {
		t.Errorf("six digits were checked against the account: %v", err)
	}
	if rec, _, err := s.VerifySecondFactor("ali", codes[2], time.Now()); err != nil || !rec {
		t.Errorf("a recovery code on the email way: %v", err)
	}
	if _, err := s.EnableEmailFactor("ali", "other@example.com", time.Now()); !errors.Is(err, ErrSecondFactorOn) {
		t.Errorf("turning it on twice: %v", err)
	}
}

func TestWithoutAKeyNothingIsSetUp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "users.json")
	s, _ := OpenStore(path)
	s.Create("ali", "Ali", "correct horse battery", RoleViewer)
	if _, err := s.BeginSecondFactor("ali"); !errors.Is(err, ErrNoSealer) {
		t.Errorf("setting up with no key: %v, want ErrNoSealer", err)
	}
}
