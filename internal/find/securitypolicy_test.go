package find

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Schayan-nk/docfind-messenger/internal/auth"
)

// signIn posts a sign-in and returns the answer and its token.
func signIn(t *testing.T, s *Server, user, password string) (int, map[string]any, string) {
	t.Helper()
	rec := asUser(t, s, "POST", "/api/login", "", `{"username":"`+user+`","password":"`+password+`"}`)
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	tok, _ := body["token"].(string)
	return rec.Code, body, tok
}

// The lockout the security centre sets, said on the attempt that triggers it,
// listed for an administrator, and undone by the unlock button.
func TestTheLockoutPolicyLocksListsAndUnlocks(t *testing.T) {
	s, admin, _, _ := accountsTestServer(t)
	s.guard.SetPolicy(auth.Policy{LockoutAttempts: 3, LockoutMinutes: 5})
	for i := 0; i < 2; i++ {
		if code, _, _ := signIn(t, s, "reader", "wrong password!"); code != http.StatusUnauthorized {
			t.Fatalf("wrong password %d answered %d", i+1, code)
		}
	}
	code, body, _ := signIn(t, s, "reader", "wrong password!")
	if code != http.StatusTooManyRequests || !strings.Contains(body["error"].(string), "۵ دقیقه") {
		t.Fatalf("the third wrong password: %d %v", code, body)
	}
	if code, _, _ := signIn(t, s, "reader", "correct horse battery"); code != http.StatusTooManyRequests {
		t.Errorf("the right password got through a lock: %d", code)
	}
	rec := asUser(t, s, "GET", "/api/admin/security/lockouts", admin, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"user:reader"`) {
		t.Fatalf("the lockout list: %d %s", rec.Code, rec.Body.String())
	}
	if rec := asUser(t, s, "POST", "/api/admin/security/unlock", admin, `{"key":"user:reader"}`); rec.Code != http.StatusOK {
		t.Fatalf("unlock: %d %s", rec.Code, rec.Body.String())
	}
	if code, _, _ := signIn(t, s, "reader", "correct horse battery"); code != http.StatusOK {
		t.Errorf("after unlocking: %d", code)
	}
	// Only an account manager sees or lifts locks.
	_, _, viewer := signIn(t, s, "reader", "correct horse battery")
	if rec := asUser(t, s, "POST", "/api/admin/security/unlock", viewer, `{"key":"ip:1.2.3.4"}`); rec.Code != http.StatusForbidden {
		t.Errorf("a viewer unlocked: %d", rec.Code)
	}
}

// A policy made stricter asks for a new password at the next sign-in, and
// until then the session reaches nothing but the change.
func TestAPasswordThePolicyRefusesIsReplacedAtSignIn(t *testing.T) {
	s, _, _, _ := accountsTestServer(t)
	s.guard.SetPolicy(auth.Policy{RequireUpper: true, RequireDigit: true})
	code, body, tok := signIn(t, s, "reader", "correct horse battery")
	user, _ := body["user"].(map[string]any)
	if code != http.StatusOK || user["update_required"] != true {
		t.Fatalf("signing in on a password the policy refuses: %d %v", code, body)
	}
	if rec := asUser(t, s, "GET", "/api/account/sessions", tok, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("the account owing a new password read /api/account/sessions: %d", rec.Code)
	}
	rec := asUser(t, s, "GET", "/api/me", tok, "")
	if !strings.Contains(rec.Body.String(), `"update_required":true`) || !strings.Contains(rec.Body.String(), "password_rules") {
		t.Errorf("/api/me does not say what is owed: %s", rec.Body.String())
	}
	rec = asUser(t, s, "POST", "/api/account/password", tok, `{"current":"correct horse battery","next":"still all lower case"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "رقم") {
		t.Errorf("a new password that breaks the policy: %d %s", rec.Code, rec.Body.String())
	}
	rec = asUser(t, s, "POST", "/api/account/password", tok, `{"current":"correct horse battery","next":"Correct Horse 2026"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("a good new password: %d %s", rec.Code, rec.Body.String())
	}
	_, _, tok = signIn(t, s, "reader", "Correct Horse 2026")
	if rec := asUser(t, s, "GET", "/api/me", tok, ""); strings.Contains(rec.Body.String(), `"update_required":true`) {
		t.Error("still owing a password after choosing a good one")
	}
}

// With two steps required, an account without one reaches the setup and
// nothing else, and is told so.
func TestTwoStepsRequiredByThePolicy(t *testing.T) {
	s, admin, _, _ := accountsTestServer(t)
	s.guard.SetPolicy(auth.Policy{RequireTwoStep: true})
	_, body, tok := signIn(t, s, "reader", "correct horse battery")
	if user, _ := body["user"].(map[string]any); user["two_step_required"] != true {
		t.Fatalf("not told: %v", body)
	}
	if rec := asUser(t, s, "GET", "/api/account/sessions", tok, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("reached /api/account/sessions: %d", rec.Code)
	}
	if rec := asUser(t, s, "GET", "/api/account/two-step", tok, ""); rec.Code == http.StatusUnauthorized {
		t.Error("refused the two-step setup")
	}
	if rec := asUser(t, s, "GET", "/api/me", tok, ""); !strings.Contains(rec.Body.String(), `"two_step_required":true`) {
		t.Errorf("/api/me: %s", rec.Body.String())
	}
	// The administrator, too, until they set one up: the policy is for all.
	if rec := asUser(t, s, "GET", "/api/account/sessions", admin, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("the administrator without two steps reached /api/account/sessions: %d", rec.Code)
	}
}

// The browser features this site's own pages use (the microphone, the camera
// and sharing the screen: voice and video messages, calls) are allowed to
// them and to nothing framed; every other one stays off. "camera=()" once
// refused every round video message without a word.
func TestThePermissionsPolicyAllowsOnlyWhatThePagesUse(t *testing.T) {
	rec := httptest.NewRecorder()
	securityHeaders(false, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(rec, httptest.NewRequest("GET", "/messages", nil))
	got := map[string]string{}
	for _, part := range strings.Split(rec.Header().Get("Permissions-Policy"), ",") {
		if k, v, ok := strings.Cut(strings.TrimSpace(part), "="); ok {
			got[k] = v
		}
	}
	for _, f := range []string{"camera", "microphone", "display-capture", "geolocation"} {
		if got[f] != "(self)" {
			t.Errorf("%s is %q, want (self)", f, got[f])
		}
	}
	for _, f := range []string{"payment", "usb", "serial", "hid", "bluetooth", "midi"} {
		if got[f] != "()" {
			t.Errorf("%s is %q, want it off", f, got[f])
		}
	}
}
