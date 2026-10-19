package find

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// fakeMailer keeps what would have been mailed.
type fakeMailer struct {
	mu   sync.Mutex
	sent []struct{ to, subject, body string }
}

func (f *fakeMailer) SendMail(_ context.Context, to, subject, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, struct{ to, subject, body string }{to, subject, body})
	return nil
}

var sixDigits = regexp.MustCompile(`\b\d{6}\b`)

// last returns the address and the code of the latest message.
func (f *fakeMailer) last(t *testing.T) (string, string) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sent) == 0 {
		t.Fatal("nothing was mailed")
	}
	m := f.sent[len(f.sent)-1]
	return m.to, sixDigits.FindString(m.body)
}

// emailTwoStepFor turns on the email way for "reader" through the routes.
func emailTwoStepFor(t *testing.T, s *Server, token string, mailer *fakeMailer) {
	t.Helper()
	rec := asUser(t, s, "POST", "/api/account/two-step/email/begin", token,
		`{"password":"correct horse battery","email":"reader@example.com"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("starting: %d %s", rec.Code, rec.Body.String())
	}
	to, code := mailer.last(t)
	if to != "reader@example.com" || code == "" {
		t.Fatalf("the setup mail went to %q with code %q", to, code)
	}
	conf := asUser(t, s, "POST", "/api/account/two-step/email/confirm", token, `{"code":"`+code+`"}`)
	if conf.Code != http.StatusOK || !strings.Contains(conf.Body.String(), "recovery_codes") {
		t.Fatalf("confirming: %d %s", conf.Code, conf.Body.String())
	}
}

func TestTwoStepByEmail(t *testing.T) {
	s, _, viewer, _ := accountsTestServer(t)
	mailer := &fakeMailer{}
	s.UseCodeMail(mailer)

	// A bad address is refused before anything is mailed.
	if rec := asUser(t, s, "POST", "/api/account/two-step/email/begin", viewer,
		`{"password":"correct horse battery","email":"a@b.de\r\nBcc: x@y.de"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("a header-smuggling address answered %d", rec.Code)
	}
	emailTwoStepFor(t, s, viewer, mailer)
	u, _ := s.guard.Users.Get("reader")
	if u.Email != "reader@example.com" || s.guard.Users.SecondFactorMethod("reader") != "email" {
		t.Fatalf("after setup: email %q, method %q", u.Email, s.guard.Users.SecondFactorMethod("reader"))
	}

	// Signing in: the password mails a code to that address.
	login := asUser(t, s, "POST", "/api/login", "", `{"username":"reader","password":"correct horse battery"}`)
	body := decode(t, login.Body.String())
	if body["method"] != "email" || body["sent_to"] == nil || sessionCookie(login) {
		t.Fatalf("the password step: %s", login.Body.String())
	}
	if strings.Contains(login.Body.String(), "reader@example.com") {
		t.Error("the full address was shown before the second step")
	}
	challenge := body["challenge"].(string)
	to, code := mailer.last(t)
	if to != "reader@example.com" {
		t.Errorf("the sign-in code went to %q", to)
	}
	second := func(c string) int {
		b, _ := json.Marshal(map[string]string{"challenge": challenge, "code": c})
		return asUser(t, s, "POST", "/api/login/second-step", "", string(b)).Code
	}
	// Asking again at once is refused: one mail per thirty seconds.
	b, _ := json.Marshal(map[string]string{"challenge": challenge})
	if rec := asUser(t, s, "POST", "/api/login/second-step/resend", "", string(b)); rec.Code != http.StatusTooManyRequests {
		t.Errorf("an immediate resend answered %d", rec.Code)
	}
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	if c := second(wrong); c != http.StatusUnauthorized {
		t.Errorf("a wrong code answered %d", c)
	}
	if c := second(code); c != http.StatusOK {
		t.Fatalf("the mailed code answered %d", c)
	}

	// Turning it off: the password and a freshly mailed code.
	if rec := asUser(t, s, "POST", "/api/account/two-step/send-code", viewer, "{}"); rec.Code != http.StatusOK {
		t.Fatalf("sending a confirm code: %d %s", rec.Code, rec.Body.String())
	}
	_, confirm := mailer.last(t)
	off := asUser(t, s, "POST", "/api/account/two-step/disable", viewer,
		`{"password":"correct horse battery","code":"`+confirm+`"}`)
	if off.Code != http.StatusOK || s.guard.Users.SecondFactorOn("reader") {
		t.Errorf("turning it off: %d %s", off.Code, off.Body.String())
	}
}

// Five wrong tries and the code is gone -- even the right one then fails.
func TestAMailedCodeDiesAfterFiveWrongTries(t *testing.T) {
	s := NewServer("")
	s.UseCodeMail(&fakeMailer{})
	mailer := s.codeMail.(*fakeMailer)
	if err := s.sendCode(context.Background(), "k", "a@example.com", codeForLogin); err != nil {
		t.Fatal(err)
	}
	_, code := mailer.last(t)
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	for i := 0; i < mailedCodeTries; i++ {
		if _, ok := s.checkCode("k", wrong); ok {
			t.Fatal("a wrong code was accepted")
		}
	}
	if _, ok := s.checkCode("k", code); ok {
		t.Error("the right code still worked after five wrong tries")
	}
	// And a code is good once.
	s.mailedCodes = nil
	s.sendCode(context.Background(), "k2", "a@example.com", codeForLogin)
	_, code = mailer.last(t)
	if _, ok := s.checkCode("k2", code); !ok {
		t.Fatal("the right code was refused")
	}
	if _, ok := s.checkCode("k2", code); ok {
		t.Error("a code worked twice")
	}
}

func TestTheMaskShowsEnoughToRecognise(t *testing.T) {
	for in, want := range map[string]string{
		"r.ahmadi@example.com": "r.******@example.com",
		"ab@x.de":              "a***@x.de",
		"broken":               "***",
	} {
		if got := maskEmail(in); got != want {
			t.Errorf("maskEmail(%q) = %q, want %q", in, got, want)
		}
	}
}
