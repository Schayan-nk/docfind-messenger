package auth

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestPasskeysOnTheAccount(t *testing.T) {
	s, path, _ := twoStepStore(t)
	h1, err := s.PasskeyHandle("ali")
	if err != nil || len(h1) < 20 {
		t.Fatalf("handle %q, %v", h1, err)
	}
	if h2, _ := s.PasskeyHandle("ali"); h2 != h1 {
		t.Error("the handle changed between calls")
	}
	k := Passkey{ID: []byte("cred-1"), PublicKey: []byte{0xa5}, Alg: -7, Name: " گوشی ", CreatedAt: time.Now()}
	if err := s.AddPasskey("ali", k); err != nil {
		t.Fatal(err)
	}
	if err := s.AddPasskey("ali", k); !errors.Is(err, ErrPasskeyExists) {
		t.Errorf("the same credential twice: %v", err)
	}
	name, got, ok := s.FindPasskey(h1, []byte("cred-1"))
	if !ok || name != "ali" || got.Name != "گوشی" {
		t.Errorf("found %q %+v %v", name, got, ok)
	}
	if _, _, ok := s.FindPasskey("another-handle", []byte("cred-1")); ok {
		t.Error("a credential was found under another account's handle")
	}

	// The counter only moves forward, checked under the lock.
	if err := s.TouchPasskey("ali", []byte("cred-1"), 5, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.TouchPasskey("ali", []byte("cred-1"), 5, time.Now()); !errors.Is(err, ErrPasskeyCounter) {
		t.Errorf("the same counter twice: %v", err)
	}

	// It survives a restart and the copy cannot touch the store.
	s2, _ := OpenStore(path)
	u, _ := s2.Get("ali")
	if len(u.Passkeys) != 1 || u.Passkeys[0].SignCount != 5 || u.PasskeyHandle != h1 {
		t.Fatalf("after reopening: %+v", u.Passkeys)
	}
	u.Passkeys[0].Name = "tampered"
	if again, _ := s2.Get("ali"); again.Passkeys[0].Name == "tampered" {
		t.Error("editing a copy changed the stored passkey")
	}
	if u.Public()["passkeys"] != 1 {
		t.Errorf("Public says %v passkeys", u.Public()["passkeys"])
	}

	if err := s.RemovePasskey("ali", []byte("nope")); !errors.Is(err, ErrNoSuchPasskey) {
		t.Errorf("removing an unknown passkey: %v", err)
	}
	if err := s.RemovePasskey("ali", nil); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := s.FindPasskey("", []byte("cred-1")); ok {
		t.Error("a removed passkey is still found")
	}
}

func TestAnAccountHoldsTenPasskeysAtMost(t *testing.T) {
	s, _, _ := twoStepStore(t)
	for i := 0; i < MaxPasskeys; i++ {
		if err := s.AddPasskey("ali", Passkey{ID: []byte{byte(i), 1}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AddPasskey("ali", Passkey{ID: []byte{99}}); !errors.Is(err, ErrTooManyKeys) {
		t.Errorf("the eleventh: %v", err)
	}
}

// Emergency sign-in reads the snapshot, so the snapshot must carry passkeys.
func TestTheEmergencySnapshotKeepsPasskeys(t *testing.T) {
	snap := filepath.Join(t.TempDir(), "users.snapshot.json")
	users := oneUser("ali")
	users[0].Passkeys = []Passkey{{ID: []byte("c"), PublicKey: []byte{1}, Alg: -7, SignCount: 3}}
	users[0].PasskeyHandle = "h"
	if err := NewFallback(newSpy(), snap, testSnapKey).Save(users); err != nil {
		t.Fatal(err)
	}
	loaded, err := NewFallback(newDead(), snap, testSnapKey).Load()
	if err != nil || len(loaded) != 1 || len(loaded[0].Passkeys) != 1 || loaded[0].PasskeyHandle != "h" {
		t.Fatalf("the snapshot lost the passkeys: %+v, %v", loaded, err)
	}
}
