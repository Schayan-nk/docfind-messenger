package find

import (
	"net/http"
	"testing"
)

// The users list's row buttons sent only a role and a state. Until 5.51.0
// that replaced the person's display name with their user name, and
// "enable" could never enable.
func TestSavingAUserKeepsWhatTheRequestLeavesOut(t *testing.T) {
	s, admin, _, _ := accountsTestServer(t)
	if _, err := s.guard.Users.Update("reader", "خواننده آزمایشی", "viewer", false); err != nil {
		t.Fatal(err)
	}
	if rec := asUser(t, s, "PUT", "/api/users", admin, `{"username":"reader","role":"manager","disabled":true}`); rec.Code != http.StatusOK {
		t.Fatalf("disabling: %d %s", rec.Code, rec.Body)
	}
	u, _ := s.guard.Users.Get("reader")
	if u.DisplayName != "خواننده آزمایشی" || !u.Disabled || u.Role != "manager" {
		t.Fatalf("after disabling: %q disabled=%v role=%s", u.DisplayName, u.Disabled, u.Role)
	}
	if rec := asUser(t, s, "PUT", "/api/users", admin, `{"username":"reader","disabled":false}`); rec.Code != http.StatusOK {
		t.Fatalf("enabling: %d %s", rec.Code, rec.Body)
	}
	u, _ = s.guard.Users.Get("reader")
	if u.Disabled || u.Role != "manager" || u.DisplayName != "خواننده آزمایشی" {
		t.Fatalf("after enabling: disabled=%v role=%s name=%q", u.Disabled, u.Role, u.DisplayName)
	}
	if rec := asUser(t, s, "PUT", "/api/users", admin, `{"username":"reader","display_name":"نام تازه"}`); rec.Code != http.StatusOK {
		t.Fatalf("renaming: %d %s", rec.Code, rec.Body)
	}
	u, _ = s.guard.Users.Get("reader")
	if u.DisplayName != "نام تازه" {
		t.Fatalf("an explicit value did not apply: %q", u.DisplayName)
	}
}
