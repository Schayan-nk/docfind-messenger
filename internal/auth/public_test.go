package auth

import (
	"encoding/json"
	"strings"
	"testing"
)

// Public() is the whole contract between an account and every screen, and it
// is hand-built rather than derived from the struct -- so a field can exist on
// User, be saved to disk, be read back correctly, and still never reach the
// browser.
//
// That is not hypothetical: a feature once shipped with its field on User,
// its endpoint working and its page rendering, and no way in, because the
// card that linked to it tested a key Public() did not emit.
func TestPublicCarriesWhatTheScreensRead(t *testing.T) {
	// Every key the front end actually reads off an account. Grep for them in
	// internal/find before removing one.
	want := []string{
		"username", "display_name", "role", "disabled", "created_at",
		// The permission matrix reads grants.
		"last_login", "grants", "has_avatar", "prefs",
		// A key Public() does not emit reads on a page as "undefined".
		"english_name",
	}
	got := (&User{Username: "someone"}).Public()
	for _, k := range want {
		if _, ok := got[k]; !ok {
			t.Errorf("Public() omits %q, so anything on a page that reads it is "+
				"permanently undefined", k)
		}
	}

	// And it must never carry the hash, whatever else is added here.
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"password", "hash", "$2a$", "$argon"} {
		if strings.Contains(strings.ToLower(string(raw)), leak) {
			t.Errorf("Public() looks like it leaks a credential: found %q in %s", leak, raw)
		}
	}
}
