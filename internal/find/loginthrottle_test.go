package find

import (
	"net/http"
	"testing"
)

// Changing the case of the name used to buy a fresh set of guesses.
func TestSpellingTheNameDifferentlyDoesNotEscapeTheLockout(t *testing.T) {
	s, _, _, _ := accountsTestServer(t)
	for i, name := range []string{"BOSS", "Boss", "bOsS", " boss", "BoSs", "boss ", "BOSs", "bosS"} {
		rec := asUser(t, s, "POST", "/api/login", "", `{"username":"`+name+`","password":"wrong wrong"}`)
		// The eighth is the one that locks, and since 5.39.0 it says so
		// (429) instead of reading like any other wrong password.
		if want := http.StatusUnauthorized; rec.Code != want && !(i == 7 && rec.Code == http.StatusTooManyRequests) {
			t.Fatalf("attempt %d answered %d", i, rec.Code)
		}
	}
	rec := asUser(t, s, "POST", "/api/login", "", `{"username":"boss","password":"correct horse battery"}`)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("after eight wrong guesses in eight spellings, the right password answered %d, want 429", rec.Code)
	}
}

// The current password is what stands between an open session and the
// account; guessing it is throttled like a sign-in.
func TestGuessingTheCurrentPasswordIsThrottled(t *testing.T) {
	s, admin, _, _ := accountsTestServer(t)
	for i := range 8 {
		rec := asUser(t, s, "POST", "/api/account/password", admin,
			`{"current":"guess number `+string(rune('a'+i))+`","next":"a perfectly fine new one"}`)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("guess %d answered %d", i, rec.Code)
		}
	}
	rec := asUser(t, s, "POST", "/api/account/password", admin,
		`{"current":"correct horse battery","next":"a perfectly fine new one"}`)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("after eight wrong guesses the right one answered %d, want 429", rec.Code)
	}
}

// An administrator setting a new password is the way out of a lockout.
func TestAPasswordResetLiftsTheLockout(t *testing.T) {
	s, admin, _, _ := accountsTestServer(t)
	for range 8 {
		asUser(t, s, "POST", "/api/login", "", `{"username":"reader","password":"wrong wrong"}`)
	}
	if rec := asUser(t, s, "POST", "/api/login", "", `{"username":"reader","password":"correct horse battery"}`); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("not locked: %d", rec.Code)
	}
	if rec := asUser(t, s, "POST", "/api/users/password", admin,
		`{"username":"reader","password":"a new one for reader"}`); rec.Code != http.StatusOK {
		t.Fatalf("reset answered %d: %s", rec.Code, rec.Body.String())
	}
	if rec := asUser(t, s, "POST", "/api/login", "", `{"username":"reader","password":"a new one for reader"}`); rec.Code != http.StatusOK {
		t.Errorf("after the reset the new password answered %d", rec.Code)
	}
}
