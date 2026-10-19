package auth

import (
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestAnEmergencyTokenIsShortAndMarked(t *testing.T) {
	s := NewSigner([]byte("0123456789abcdef0123456789abcdef"), 12*time.Hour)
	tok, exp, err := s.SignEmergency("boss", 3, "sid")
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Until(exp); d > EmergencySessionAge+time.Minute || d < EmergencySessionAge-time.Minute {
		t.Errorf("an emergency session lasts %v", d)
	}
	c, err := s.Verify(tok)
	if err != nil || !c.Emergency {
		t.Fatalf("claims %+v, %v", c, err)
	}
	normal, _, _ := s.SignSession("boss", 3, "sid")
	if c, _ := s.Verify(normal); c.Emergency {
		t.Error("an ordinary session is marked emergency")
	}
}

// The guard carries the mark from the token onto the request's account --
// and only onto that copy.
func TestTheGuardCarriesTheEmergencyMark(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "users.json"))
	if err != nil {
		t.Fatal(err)
	}
	u, err := store.Create("boss", "Boss", "correct horse battery", RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	signer := NewSigner([]byte("0123456789abcdef0123456789abcdef"), time.Hour)
	g := &Guard{Users: store, Signer: signer}
	tok, _, _ := signer.SignEmergency(u.Username, u.TokenVersion, "s1")
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	r2, ok := g.Authenticate(r)
	if !ok {
		t.Fatal("the emergency token was refused")
	}
	if got := UserFrom(r2); got == nil || !got.Emergency {
		t.Errorf("the request's account is not marked: %+v", got)
	}
	if stored, _ := store.Get("boss"); stored.Emergency {
		t.Error("the mark leaked into the stored account")
	}
}

// Degraded while the database is down, ordinary again once a refresh finds
// it back -- no restart.
func TestTheStoreLeavesEmergencyWhenTheDatabaseReturns(t *testing.T) {
	snap := filepath.Join(tempDir(t), "users.snapshot.json")
	spy := newSpy()
	if err := NewFallback(spy, snap, testSnapKey).Save(oneUser("ali")); err != nil {
		t.Fatal(err)
	}
	flaky := &flakyBackend{spy: spy, down: true}
	store, err := OpenStoreWith(filepath.Join(tempDir(t), "users.json"), NewFallback(flaky, snap, testSnapKey))
	if err != nil {
		t.Fatal(err)
	}
	if !store.Degraded() {
		t.Fatal("serving from the snapshot is not degraded")
	}
	if err := store.Refresh(); err != nil || !store.Degraded() {
		t.Fatalf("a refresh with the database still down: %v, degraded=%v", err, store.Degraded())
	}
	flaky.down = false
	if err := store.Refresh(); err != nil {
		t.Fatal(err)
	}
	if store.Degraded() {
		t.Error("still degraded after the database came back")
	}
}
