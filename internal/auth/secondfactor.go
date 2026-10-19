package auth

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/Schayan-nk/docfind-messenger/internal/sms"
)

// The second sign-in step, on the account store (totp.go has the arithmetic).
//
// Everything here takes the WRITE lock, checking a code included: a code is
// good once, and two sign-ins racing with the same six digits must not both
// get past the replay guard because each read LastStep before the other
// wrote it.

// Sealer protects the second-factor key at rest -- the same AES-GCM box, with
// a DPAPI-wrapped key file, that protects the stored mail passwords.
type Sealer interface {
	Seal(plain []byte) ([]byte, error)
	Open(sealed []byte) ([]byte, error)
}

var (
	ErrSecondFactorOn  = errors.New("auth: two-step sign-in is already on for this account")
	ErrSecondFactorOff = errors.New("auth: two-step sign-in is not set up for this account")
	ErrBadSecondFactor = errors.New("auth: that code is not right")
	ErrNoSealer        = errors.New("auth: no key to protect a second factor with")
	// ErrRecoveryUnsaved: a recovery code is single-use, so one that could not
	// be struck off durably (the database is down) is refused rather than
	// accepted and left usable again.
	ErrRecoveryUnsaved = errors.New("auth: a recovery code cannot be used while accounts cannot be saved")
)

// UseSealer installs the key the second-factor secrets are sealed with.
func (s *Store) UseSealer(x Sealer) {
	s.mu.Lock()
	s.sealer = x
	s.mu.Unlock()
}

// SecondFactorOn reports whether this account signs in in two steps.
func (s *Store) SecondFactorOn(username string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.users[normalizeName(username)]
	return ok && u.SecondFactor.On()
}

func (s *Store) sealSecret(secret string) (string, error) {
	if s.sealer == nil {
		return "", ErrNoSealer
	}
	b, err := s.sealer.Seal([]byte(secret))
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

func (s *Store) openSecret(sealed string) (string, error) {
	if s.sealer == nil {
		return "", ErrNoSealer
	}
	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		return "", err
	}
	b, err := s.sealer.Open(raw)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// BeginSecondFactor starts setting up the app: a new key, stored sealed and
// not yet in force, returned in the clear for the QR code. Starting again
// replaces a key nobody confirmed.
func (s *Store) BeginSecondFactor(username string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[normalizeName(username)]
	if !ok {
		return "", ErrNoSuchUser
	}
	if u.SecondFactor.On() {
		return "", ErrSecondFactorOn
	}
	secret, err := NewTOTPSecret()
	if err != nil {
		return "", err
	}
	sealed, err := s.sealSecret(secret)
	if err != nil {
		return "", err
	}
	before := u.SecondFactor
	u.SecondFactor = &SecondFactor{Method: MethodApp, Secret: sealed}
	if err := s.save(); err != nil {
		u.SecondFactor = before
		return "", err
	}
	return secret, nil
}

// ConfirmSecondFactor turns the step on once the app's first code is right,
// and returns the recovery codes -- the only time they exist in the clear.
func (s *Store) ConfirmSecondFactor(username, code string, now time.Time) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[normalizeName(username)]
	if !ok {
		return nil, ErrNoSuchUser
	}
	f := u.SecondFactor
	if f == nil || f.Enabled || f.Secret == "" {
		return nil, ErrSecondFactorOff
	}
	secret, err := s.openSecret(f.Secret)
	if err != nil {
		return nil, err
	}
	step, ok := VerifyTOTP(secret, code, now, f.LastStep)
	if !ok {
		return nil, ErrBadSecondFactor
	}
	codes, hashes, err := NewRecoveryCodes(RecoveryCodeCount)
	if err != nil {
		return nil, err
	}
	before := *f
	f.Enabled, f.LastStep, f.Recovery, f.EnabledAt = true, step, hashes, now.UTC()
	if err := s.save(); err != nil {
		*f = before
		return nil, err
	}
	return codes, nil
}

// VerifySecondFactor checks a sign-in's second step: six digits from the app,
// or a recovery code. It reports whether a recovery code was spent and how
// many are left.
func (s *Store) VerifySecondFactor(username, code string, now time.Time) (recovery bool, left int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[normalizeName(username)]
	if !ok {
		return false, 0, ErrNoSuchUser
	}
	f := u.SecondFactor
	if !f.On() {
		return false, 0, ErrSecondFactorOff
	}
	code = strings.TrimSpace(code)
	digits := strings.NewReplacer(" ", "", "-", "").Replace(code)
	if IsSixDigits(digits) {
		// A mailed or texted code is checked by whoever sent it
		// (internal/find keeps those in memory); only an app code is checked
		// against the key.
		if sent(f.Via()) {
			return false, len(f.Recovery), ErrBadSecondFactor
		}
		secret, err := s.openSecret(f.Secret)
		if err != nil {
			return false, 0, err
		}
		step, ok := VerifyTOTP(secret, digits, now, f.LastStep)
		if !ok {
			return false, len(f.Recovery), ErrBadSecondFactor
		}
		// Recorded in memory whatever the save does: with the database down
		// the replay guard still holds for as long as this process runs,
		// which is the length of an emergency session.
		f.LastStep = step
		_ = s.save()
		return false, len(f.Recovery), nil
	}
	rest, ok := useRecoveryCode(f.Recovery, code)
	if !ok {
		return false, len(f.Recovery), ErrBadSecondFactor
	}
	before := f.Recovery
	f.Recovery = rest
	if err := s.save(); err != nil {
		f.Recovery = before
		return false, len(before), ErrRecoveryUnsaved
	}
	return true, len(rest), nil
}

// IsSixDigits reports whether code (spaces and dashes removed) is a
// six-digit code rather than a recovery code.
func IsSixDigits(code string) bool {
	code = strings.NewReplacer(" ", "", "-", "").Replace(strings.TrimSpace(code))
	return len(code) == totpDigits && strings.Trim(code, "0123456789") == ""
}

// SecondFactorMethod is how this account's code arrives, or "" if it has no
// second step.
func (s *Store) SecondFactorMethod(username string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if u, ok := s.users[normalizeName(username)]; ok && u.SecondFactor.On() {
		return u.SecondFactor.Via()
	}
	return ""
}

// EnableEmailFactor turns on the second step by email, to an address the
// caller has just PROVED is this person's (a code sent there came back), and
// records it as the account's address. Returns the recovery codes.
func (s *Store) EnableEmailFactor(username, email string, now time.Time) ([]string, error) {
	email = strings.TrimSpace(email)
	if !validEmail(email) {
		return nil, fmt.Errorf("auth: %q is not an email address", email)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[normalizeName(username)]
	if !ok {
		return nil, ErrNoSuchUser
	}
	if u.SecondFactor.On() {
		return nil, ErrSecondFactorOn
	}
	codes, hashes, err := NewRecoveryCodes(RecoveryCodeCount)
	if err != nil {
		return nil, err
	}
	beforeF, beforeE := u.SecondFactor, u.Email
	u.SecondFactor = &SecondFactor{Method: MethodEmail, Enabled: true, Recovery: hashes, EnabledAt: now.UTC()}
	u.Email = email
	if err := s.save(); err != nil {
		u.SecondFactor, u.Email = beforeF, beforeE
		return nil, err
	}
	return codes, nil
}

// EnableSMSFactor turns on the second step by SMS, to a number the caller has
// just PROVED is this person's (a code sent there came back), and records it
// as the account's phone. Returns the recovery codes.
func (s *Store) EnableSMSFactor(username, phone string, now time.Time) ([]string, error) {
	p, ok := sms.NormalizePhone(phone)
	if !ok {
		return nil, fmt.Errorf("auth: %q is not a mobile number", phone)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	u, found := s.users[normalizeName(username)]
	if !found {
		return nil, ErrNoSuchUser
	}
	if u.SecondFactor.On() {
		return nil, ErrSecondFactorOn
	}
	codes, hashes, err := NewRecoveryCodes(RecoveryCodeCount)
	if err != nil {
		return nil, err
	}
	beforeF, beforeP := u.SecondFactor, u.Phone
	u.SecondFactor = &SecondFactor{Method: MethodSMS, Enabled: true, Recovery: hashes, EnabledAt: now.UTC()}
	u.Phone = p
	if err := s.save(); err != nil {
		u.SecondFactor, u.Phone = beforeF, beforeP
		return nil, err
	}
	return codes, nil
}

func validEmail(s string) bool {
	a, err := mail.ParseAddress(s)
	return err == nil && a.Address == s && len(s) <= 254
}

// DisableSecondFactor turns the step off. The caller checks who is asking:
// the person themselves with their password and a code, or an administrator
// for somebody who has lost their phone.
func (s *Store) DisableSecondFactor(username string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[normalizeName(username)]
	if !ok {
		return ErrNoSuchUser
	}
	before := u.SecondFactor
	u.SecondFactor = nil
	if err := s.save(); err != nil {
		u.SecondFactor = before
		return err
	}
	return nil
}

// RenewRecoveryCodes replaces every recovery code, spent or not.
func (s *Store) RenewRecoveryCodes(username string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[normalizeName(username)]
	if !ok {
		return nil, ErrNoSuchUser
	}
	if !u.SecondFactor.On() {
		return nil, ErrSecondFactorOff
	}
	codes, hashes, err := NewRecoveryCodes(RecoveryCodeCount)
	if err != nil {
		return nil, err
	}
	before := u.SecondFactor.Recovery
	u.SecondFactor.Recovery = hashes
	if err := s.save(); err != nil {
		u.SecondFactor.Recovery = before
		return nil, err
	}
	return codes, nil
}

// RecoveryLeft is how many unused recovery codes an account holds.
func (s *Store) RecoveryLeft(username string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if u, ok := s.users[normalizeName(username)]; ok && u.SecondFactor != nil {
		return len(u.SecondFactor.Recovery)
	}
	return 0
}
