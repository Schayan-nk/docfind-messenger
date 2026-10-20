package find

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Schayan-nk/docfind-messenger/internal/auth"
)

// registrationServer is a server with accounts on and invite registration
// wired, plus the admin token and the invite store.
func registrationServer(t *testing.T) (*Server, string, *auth.Invites) {
	t.Helper()
	dir := tempDir(t)
	users, err := auth.OpenStore(filepath.Join(dir, "users.json"))
	if err != nil {
		t.Fatalf("accounts: %v", err)
	}
	if _, err := users.Create("boss", "Boss", "correct horse battery", auth.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	key, err := auth.LoadOrCreateKey(filepath.Join(dir, "key"))
	if err != nil {
		t.Fatal(err)
	}
	signer := auth.NewSigner(key, time.Hour)
	s := NewServer("")
	s.UseAccounts(users, signer, false)
	s.UsePermissions(auth.NewCatalogue())
	iv := auth.OpenInvites(filepath.Join(dir, "invites.json"))
	s.UseInvites(iv)
	return s, signFor(t, signer, users, "boss"), iv
}

func decodeBody(t *testing.T, body string, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(body), v); err != nil {
		t.Fatalf("decoding %q: %v", body, err)
	}
}

// The whole intended path: an administrator mints a code, somebody registers
// with it and is signed in, and the account then works on its own.
func TestInviteRegistrationSignsTheNewAccountIn(t *testing.T) {
	s, admin, _ := registrationServer(t)

	rec := asUser(t, s, "POST", "/api/admin/invites", admin, `{"role":"viewer","max_uses":1}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("minting a code: %d %s", rec.Code, rec.Body.String())
	}
	var minted struct {
		Invite auth.Invite `json:"invite"`
		Link   string      `json:"link"`
	}
	decodeBody(t, rec.Body.String(), &minted)
	if minted.Invite.Code == "" || minted.Link == "" {
		t.Fatalf("mint gave no code or link: %+v", minted)
	}

	body := `{"invite":"` + minted.Invite.Code + `","username":"newbie","display_name":"تازه‌وارد","password":"correct horse battery"}`
	rec = asUser(t, s, "POST", "/api/register", "", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("registering: %d %s", rec.Code, rec.Body.String())
	}
	var reg struct {
		User  map[string]any `json:"user"`
		Token string         `json:"token"`
	}
	decodeBody(t, rec.Body.String(), &reg)
	if reg.Token == "" || reg.User["username"] != "newbie" {
		t.Fatalf("registration did not sign in: %+v", reg)
	}
	if reg.User["role"] != string(auth.RoleViewer) {
		t.Errorf("new account role = %v, want viewer", reg.User["role"])
	}
	// The account exists and the session works.
	if _, err := s.guard.Users.Get("newbie"); err != nil {
		t.Errorf("account not created: %v", err)
	}
	if rec := asUser(t, s, "GET", "/api/me", reg.Token, ""); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"username":"newbie"`) {
		t.Errorf("the new session does not work: %d %s", rec.Code, rec.Body.String())
	}
	// The one use is gone: the same code is refused now.
	if rec := asUser(t, s, "POST", "/api/register", "",
		`{"invite":"`+minted.Invite.Code+`","username":"second","display_name":"x","password":"correct horse battery"}`); rec.Code != http.StatusForbidden {
		t.Errorf("a spent code was accepted: %d %s", rec.Code, rec.Body.String())
	}
}

// A bad form never spends a use, and a bad code is refused the same way
// whether it never existed or is used up.
func TestRegistrationRefusals(t *testing.T) {
	s, admin, _ := registrationServer(t)
	var minted struct {
		Invite auth.Invite `json:"invite"`
	}
	decodeBody(t, asUser(t, s, "POST", "/api/admin/invites", admin, `{"role":"viewer","max_uses":1}`).Body.String(), &minted)
	code := minted.Invite.Code

	// A password that breaks the policy: refused, and the code is NOT spent.
	if rec := asUser(t, s, "POST", "/api/register", "",
		`{"invite":"`+code+`","username":"weak","display_name":"x","password":"short"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("a weak password was taken: %d %s", rec.Code, rec.Body.String())
	}
	// An unknown code: refused, same shape.
	if rec := asUser(t, s, "POST", "/api/register", "",
		`{"invite":"ZZZZ-ZZZZ-ZZZZ-ZZZZ","username":"nocode","display_name":"x","password":"correct horse battery"}`); rec.Code != http.StatusForbidden {
		t.Errorf("an unknown code was accepted: %d", rec.Code)
	}
	// The real code still works, because the weak-password attempt did not
	// burn its one use.
	if rec := asUser(t, s, "POST", "/api/register", "",
		`{"invite":"`+code+`","username":"okuser","display_name":"x","password":"correct horse battery"}`); rec.Code != http.StatusOK {
		t.Errorf("the code was burned by a bad form: %d %s", rec.Code, rec.Body.String())
	}
	// A name already taken: refused.
	var m2 struct {
		Invite auth.Invite `json:"invite"`
	}
	decodeBody(t, asUser(t, s, "POST", "/api/admin/invites", admin, `{"role":"viewer"}`).Body.String(), &m2)
	if rec := asUser(t, s, "POST", "/api/register", "",
		`{"invite":"`+m2.Invite.Code+`","username":"boss","display_name":"x","password":"correct horse battery"}`); rec.Code != http.StatusConflict {
		t.Errorf("a taken name was accepted: %d %s", rec.Code, rec.Body.String())
	}
}

// Minting and revoking are the administrator's alone, and a revoked code
// stops working.
func TestInviteMintIsAdminOnlyAndRevocable(t *testing.T) {
	s, admin, _ := registrationServer(t)
	// A second account that is only a viewer.
	if _, err := s.guard.Users.Create("reader", "Reader", "correct horse battery", auth.RoleViewer); err != nil {
		t.Fatal(err)
	}
	viewer := signFor(t, s.guard.Signer, s.guard.Users, "reader")
	if rec := asUser(t, s, "POST", "/api/admin/invites", viewer, `{"role":"viewer"}`); rec.Code != http.StatusForbidden {
		t.Errorf("a viewer minted a code: %d", rec.Code)
	}
	// An administrator code is never minted through the link maker.
	if rec := asUser(t, s, "POST", "/api/admin/invites", admin, `{"role":"admin"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("an admin invite was minted: %d %s", rec.Code, rec.Body.String())
	}

	var minted struct {
		Invite auth.Invite `json:"invite"`
	}
	decodeBody(t, asUser(t, s, "POST", "/api/admin/invites", admin, `{"role":"manager"}`).Body.String(), &minted)
	if rec := asUser(t, s, "DELETE", "/api/admin/invites?code="+minted.Invite.Code, admin, ""); rec.Code != http.StatusOK {
		t.Fatalf("revoking: %d %s", rec.Code, rec.Body.String())
	}
	if rec := asUser(t, s, "POST", "/api/register", "",
		`{"invite":"`+minted.Invite.Code+`","username":"late","display_name":"x","password":"correct horse battery"}`); rec.Code != http.StatusForbidden {
		t.Errorf("a revoked code still worked: %d", rec.Code)
	}
}

// With no invite store wired, registration is simply not there.
func TestRegistrationOffIs404(t *testing.T) {
	s, _, _, _ := accountsTestServer(t)
	if rec := asUser(t, s, "POST", "/api/register", "",
		`{"invite":"x","username":"nope","display_name":"x","password":"correct horse battery"}`); rec.Code != http.StatusNotFound {
		t.Errorf("registration answered where it is off: %d", rec.Code)
	}
	if rec := asUser(t, s, "GET", "/register", "", ""); rec.Code != http.StatusNotFound {
		t.Errorf("the register page showed where registration is off: %d", rec.Code)
	}
}
