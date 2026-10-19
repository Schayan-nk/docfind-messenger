package find

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/Schayan-nk/docfind-messenger/internal/auth"
)

// Two-step sign-in by EMAIL: the other way to get the six digits, for
// somebody who would rather not install an authenticator app. The code is
// mailed from the mailbox handed to UseCodeMail (in this build, the one
// DOCFIND_SMTP_* names; cmd/messenger/smtpmail.go).
//
// Weaker than the app, and said so on the settings page: whoever can read
// the mailbox can finish the sign-in. It still means a stolen password alone
// is not enough.
//
// Codes live in memory only, hashed, keyed by what they are for -- a sign-in
// (the challenge's id), proving an address during setup, or confirming a
// change on one's own account. Each is good once, for ten minutes, for five
// tries; a new one can be asked for every thirty seconds, five times. A
// restart forgets them all, which costs somebody one more "send again".

// codeSender is a mailbox's outgoing side.
type codeSender interface {
	SendMail(ctx context.Context, to, subject, body string) error
}

// UseCodeMail turns on codes by email, sent through m. Without it the
// settings page does not offer email as a second step.
func (s *Server) UseCodeMail(m codeSender) { s.codeMail = m }

type mailedCode struct {
	hash  [32]byte
	to    string
	until time.Time
	sent  time.Time
	tries int
	sends int
}

const (
	mailedCodeLife  = 10 * time.Minute
	mailedCodeTries = 5
	mailedCodeGap   = 30 * time.Second
	mailedCodeSends = 5
)

var (
	errNoCodeMail  = errors.New("find: outbound mail is not configured")
	errCodeTooSoon = errors.New("find: a code was sent a moment ago")
	errCodeLimit   = errors.New("find: too many codes sent")
)

// What a mailed code is for, which decides the words around it.
const (
	codeForLogin   = "login"
	codeForSetup   = "setup"
	codeForConfirm = "confirm"
	codeForReset   = "reset" // a forgotten password, by SMS only (passwordreset.go)
)

func codeHash(key, code string) [32]byte { return sha256.Sum256([]byte(key + "\x00" + code)) }

// sendCode mails a fresh code for key to address to.
func (s *Server) sendCode(ctx context.Context, key, to, purpose string) error {
	if s.codeMail == nil {
		return errNoCodeMail
	}
	code, err := s.issueCode(key, to)
	if err != nil {
		return err
	}
	subject, body := codeMessage(purpose, code)
	c, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := s.codeMail.SendMail(c, to, subject, body); err != nil {
		return fmt.Errorf("mailing a sign-in code: %w", err)
	}
	return nil
}

// sendCodeVia sends a code the account's way: by SMS to a phone for
// auth.MethodSMS, by email otherwise.
func (s *Server) sendCodeVia(ctx context.Context, method, key, to, purpose string) error {
	if method == auth.MethodSMS {
		return s.sendSMSCode(ctx, key, to, purpose)
	}
	return s.sendCode(ctx, key, to, purpose)
}

// issueCode makes a fresh code for key and records it, with the limits above;
// whoever calls it delivers the code. Shared by mail and SMS.
func (s *Server) issueCode(key, to string) (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	code := fmt.Sprintf("%06d", n.Int64())
	now := time.Now()

	s.mailedMu.Lock()
	if s.mailedCodes == nil {
		s.mailedCodes = map[string]*mailedCode{}
	}
	for k, c := range s.mailedCodes {
		if now.After(c.until) {
			delete(s.mailedCodes, k)
		}
	}
	prev := s.mailedCodes[key]
	sends := 0
	if prev != nil {
		if now.Sub(prev.sent) < mailedCodeGap {
			s.mailedMu.Unlock()
			return "", errCodeTooSoon
		}
		if prev.sends >= mailedCodeSends {
			s.mailedMu.Unlock()
			return "", errCodeLimit
		}
		sends = prev.sends
	}
	// A new code replaces the old one: only the latest one sent works.
	s.mailedCodes[key] = &mailedCode{hash: codeHash(key, code), to: to,
		until: now.Add(mailedCodeLife), sent: now, sends: sends + 1}
	s.mailedMu.Unlock()
	return code, nil
}

// checkCode spends a code if it is right, and returns the address it went
// to. Five wrong tries and it is gone.
func (s *Server) checkCode(key, code string) (string, bool) {
	code = strings.NewReplacer(" ", "", "-", "").Replace(strings.TrimSpace(code))
	s.mailedMu.Lock()
	defer s.mailedMu.Unlock()
	c := s.mailedCodes[key]
	if c == nil || time.Now().After(c.until) {
		return "", false
	}
	h := codeHash(key, code)
	if subtle.ConstantTimeCompare(h[:], c.hash[:]) != 1 {
		if c.tries++; c.tries >= mailedCodeTries {
			delete(s.mailedCodes, key)
		}
		return "", false
	}
	delete(s.mailedCodes, key)
	return c.to, true
}

func codeMessage(purpose, code string) (subject, body string) {
	const tail = "\n\nاین کد تا ۱۰ دقیقه و فقط یک بار معتبر است.\n"
	switch purpose {
	case codeForSetup:
		return "تأیید ایمیل برای ورود دومرحله‌ای docfind",
			"برای روشن کردن ورود دومرحله‌ای با ایمیل در docfind، این کد را وارد کنید:\n\n    " + code + tail +
				"اگر شما این کار را شروع نکرده‌اید، این نامه را نادیده بگیرید.\n"
	case codeForConfirm:
		return "کد تأیید docfind",
			"کد تأیید برای تغییر تنظیمات ورود حساب شما در docfind:\n\n    " + code + tail +
				"اگر شما نبودید، همین حالا گذرواژه‌تان را عوض کنید.\n"
	}
	return "کد ورود به docfind",
		"کد ورود شما به docfind:\n\n    " + code + tail +
			"اگر شما نبودید، کسی گذرواژهٔ شما را دارد: همین حالا آن را عوض کنید.\n"
}

// maskEmail shows enough of an address to recognise it: ja*****@example.com.
func maskEmail(a string) string {
	at := strings.LastIndex(a, "@")
	if at < 1 {
		return "***"
	}
	local := []rune(a[:at])
	keep := 2
	if len(local) <= 2 {
		keep = 1
	}
	return string(local[:keep]) + strings.Repeat("*", max(len(local)-keep, 3)) + a[at:]
}
