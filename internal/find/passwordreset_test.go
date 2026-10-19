package find

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Schayan-nk/docfind-messenger/internal/auth"
	"github.com/Schayan-nk/docfind-messenger/internal/sms"
)

// fakePanel is an SMS panel that hands each text it is asked to send to the
// test, as "destination|message".
func fakePanel(t *testing.T, s *Server) <-chan string {
	t.Helper()
	got := make(chan string, 16)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		got <- r.Form.Get("destination") + "|" + r.Form.Get("message")
		w.Write([]byte(`{"meta":{"status":200,"message":"ok"},"data":[1]}`))
	}))
	t.Cleanup(srv.Close)
	s.sms = &smsState{store: &memSettings{rows: map[string][]byte{}}, loaded: true,
		cfg: sms.Config{URL: srv.URL, Username: "u", Password: "p", From: "3000"}}
	return got
}

// nextText waits for the panel's next text, or reports that none came.
func nextText(got <-chan string, wait time.Duration) (string, bool) {
	select {
	case m := <-got:
		return m, true
	case <-time.After(wait):
		return "", false
	}
}

func TestNoResetWithoutAnSMSPanel(t *testing.T) {
	s, _, _, _ := accountsTestServer(t)
	if me := asUser(t, s, "GET", "/api/me", "", ""); strings.Contains(me.Body.String(), `"password_reset":true`) {
		t.Errorf("the sign-in page was offered a reset with no panel: %s", me.Body.String())
	}
	if rec := asUser(t, s, "POST", "/api/login/forgot", "", `{"username":"reader"}`); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("asking for a code with no panel answered %d", rec.Code)
	}
}

func TestForgottenPasswordBySMS(t *testing.T) {
	s, admin, viewer, _ := accountsTestServer(t)
	got := fakePanel(t, s)
	if err := s.guard.Users.SetPhone("reader", "09121234567"); err != nil {
		t.Fatal(err)
	}
	if me := asUser(t, s, "GET", "/api/me", "", ""); !strings.Contains(me.Body.String(), `"password_reset":true`) {
		t.Fatalf("the sign-in page was not offered a reset: %s", me.Body.String())
	}

	// The answer is the same for a name with no account as for one with.
	nobody := asUser(t, s, "POST", "/api/login/forgot", "", `{"username":"nobody.here"}`)
	reader := asUser(t, s, "POST", "/api/login/forgot", "", `{"username":" Reader "}`)
	if nobody.Code != http.StatusOK || reader.Code != http.StatusOK || nobody.Body.String() != reader.Body.String() {
		t.Fatalf("the answers differ: %d %s / %d %s", nobody.Code, nobody.Body.String(), reader.Code, reader.Body.String())
	}
	text, ok := nextText(got, 5*time.Second)
	if !ok {
		t.Fatal("no code was texted to the account's phone")
	}
	if !strings.Contains(text, "9121234567") || !strings.Contains(text, "بازیابی") {
		t.Fatalf("the text: %q", text)
	}
	code := sixDigits.FindString(text)
	if code == "" {
		t.Fatalf("no code in %q", text)
	}
	if extra, ok := nextText(got, 300*time.Millisecond); ok {
		t.Errorf("a second text went out, for the name with no account: %q", extra)
	}
	// Asking again at once: the same answer, and nothing sent.
	if again := asUser(t, s, "POST", "/api/login/forgot", "", `{"username":"reader"}`); again.Body.String() != reader.Body.String() {
		t.Errorf("an immediate second request answered %s", again.Body.String())
	}
	if extra, ok := nextText(got, 300*time.Millisecond); ok {
		t.Errorf("a code went out again within thirty seconds: %q", extra)
	}

	reset := func(name, code, pw string) *httptest.ResponseRecorder {
		return asUser(t, s, "POST", "/api/login/reset", "",
			`{"username":"`+name+`","code":"`+code+`","password":"`+pw+`"}`)
	}
	const fresh = "a much better passphrase"
	// A password that is too short is refused before the code is spent.
	if rec := reset("reader", code, "short"); rec.Code != http.StatusBadRequest {
		t.Errorf("a short password answered %d", rec.Code)
	}
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	if rec := reset("reader", wrong, fresh); rec.Code != http.StatusUnauthorized {
		t.Errorf("a wrong code answered %d", rec.Code)
	}
	// Another account's name with this code does nothing either.
	if rec := reset("boss", code, fresh); rec.Code != http.StatusUnauthorized {
		t.Errorf("the code worked for another account: %d", rec.Code)
	}
	if rec := reset("READER", code, fresh); rec.Code != http.StatusOK {
		t.Fatalf("the right code answered %d %s", rec.Code, rec.Body.String())
	}
	// Spent: it does not work twice.
	if rec := reset("reader", code, "yet another passphrase"); rec.Code != http.StatusUnauthorized {
		t.Errorf("the code worked twice: %d", rec.Code)
	}

	// Every session the account had is gone, and only the new password works.
	if me := asUser(t, s, "GET", "/api/me", viewer, ""); strings.Contains(me.Body.String(), `"username":"reader"`) {
		t.Errorf("the old session survived the reset: %s", me.Body.String())
	}
	if rec := asUser(t, s, "POST", "/api/login", "", `{"username":"reader","password":"correct horse battery"}`); rec.Code == http.StatusOK {
		t.Error("the old password still signs in")
	}
	if rec := asUser(t, s, "POST", "/api/login", "", `{"username":"reader","password":"`+fresh+`"}`); rec.Code != http.StatusOK {
		t.Errorf("the new password does not sign in: %d %s", rec.Code, rec.Body.String())
	}

	// The store refuses the password it already has, and a disabled account.
	if err := s.guard.Users.ResetPassword("reader", fresh); !errors.Is(err, auth.ErrSamePassword) {
		t.Errorf("resetting to the same password: %v", err)
	}
	if _, err := s.guard.Users.Update("reader", "Reader", auth.RoleViewer, true); err != nil {
		t.Fatal(err)
	}
	if err := s.guard.Users.ResetPassword("reader", "something else again"); !errors.Is(err, auth.ErrDisabled) {
		t.Errorf("resetting a disabled account: %v", err)
	}

	// One address asking for codes again and again is stopped, whatever the
	// names, and an administrator can lift it.
	limited := false
	for i := 0; i < 12 && !limited; i++ {
		limited = asUser(t, s, "POST", "/api/login/forgot", "", `{"username":"spray`+string(rune('a'+i))+`"}`).Code == http.StatusTooManyRequests
	}
	if !limited {
		t.Fatal("twelve requests from one address were all answered")
	}
	var key string
	for _, l := range s.guard.Throttle.Lockouts() {
		if l.Kind == "resetip" {
			key = l.Key
		}
	}
	if key == "" {
		t.Fatalf("the address lock is not listed: %+v", s.guard.Throttle.Lockouts())
	}
	if rec := asUser(t, s, "POST", "/api/admin/security/unlock", admin, `{"key":"`+key+`"}`); rec.Code != http.StatusOK {
		t.Fatalf("unlocking %s: %d %s", key, rec.Code, rec.Body.String())
	}
	if rec := asUser(t, s, "POST", "/api/login/forgot", "", `{"username":"reader"}`); rec.Code != http.StatusOK {
		t.Errorf("after the unlock: %d", rec.Code)
	}
}
