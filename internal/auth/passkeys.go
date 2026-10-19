package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"strings"
	"time"
)

// Passkeys on the account store (the checking is internal/webauthn).
//
// A passkey is a sign-in on its own -- something the person has, unlocked by
// something they are or know (the platform's PIN or biometric, which
// registration and sign-in both REQUIRE) -- so it stands in for the password
// and the second step together.
//
// Carried on the User like the second factor, and for the same reason: the
// account travels whole into the database, the file and the emergency
// snapshot. Nothing here is secret -- a public key, an id, a counter -- so
// nothing is sealed.

// Passkey is one registered passkey.
type Passkey struct {
	ID        []byte    `json:"id"`
	PublicKey []byte    `json:"public_key"` // COSE
	Alg       int       `json:"alg"`
	SignCount uint32    `json:"sign_count"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	LastUsed  time.Time `json:"last_used,omitempty"`
}

// MaxPasskeys bounds how many one account may hold.
const MaxPasskeys = 10

var (
	ErrPasskeyExists  = errors.New("auth: that passkey is already registered")
	ErrTooManyKeys    = errors.New("auth: this account has as many passkeys as it may hold")
	ErrNoSuchPasskey  = errors.New("auth: no such passkey")
	ErrPasskeyCounter = errors.New("auth: the passkey's counter went backwards")
)

// PasskeyHandle returns the opaque id browsers keep with this account's
// passkeys, making one the first time. Random rather than derived from the
// name: it is stored on the phone and is nobody's business.
func (s *Store) PasskeyHandle(username string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[normalizeName(username)]
	if !ok {
		return "", ErrNoSuchUser
	}
	if u.PasskeyHandle != "" {
		return u.PasskeyHandle, nil
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	u.PasskeyHandle = base64.RawURLEncoding.EncodeToString(b)
	if err := s.save(); err != nil {
		u.PasskeyHandle = ""
		return "", err
	}
	return u.PasskeyHandle, nil
}

// AddPasskey records a newly registered passkey. A credential id already on
// ANY account is refused: ids are the authenticator's, and one showing up
// twice is a replayed registration.
func (s *Store) AddPasskey(username string, p Passkey) error {
	p.Name = strings.TrimSpace(p.Name)
	if r := []rune(p.Name); len(r) > 60 {
		p.Name = string(r[:60])
	}
	if p.Name == "" {
		p.Name = "کلید عبور"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[normalizeName(username)]
	if !ok {
		return ErrNoSuchUser
	}
	for _, other := range s.users {
		for _, k := range other.Passkeys {
			if sameBytes(k.ID, p.ID) {
				return ErrPasskeyExists
			}
		}
	}
	if len(u.Passkeys) >= MaxPasskeys {
		return ErrTooManyKeys
	}
	before := u.Passkeys
	u.Passkeys = append(append([]Passkey(nil), u.Passkeys...), p)
	if err := s.save(); err != nil {
		u.Passkeys = before
		return err
	}
	return nil
}

// RemovePasskey deletes one; an empty id removes every one (an
// administrator's reset for a lost device).
func (s *Store) RemovePasskey(username string, id []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[normalizeName(username)]
	if !ok {
		return ErrNoSuchUser
	}
	var keep []Passkey
	found := false
	for _, k := range u.Passkeys {
		if len(id) == 0 || sameBytes(k.ID, id) {
			found = true
			continue
		}
		keep = append(keep, k)
	}
	if !found && len(id) > 0 {
		return ErrNoSuchPasskey
	}
	before := u.Passkeys
	u.Passkeys = keep
	if err := s.save(); err != nil {
		u.Passkeys = before
		return err
	}
	return nil
}

// FindPasskey returns the account and passkey a sign-in names. handle is the
// user handle the browser sent back (empty if it sent none).
func (s *Store) FindPasskey(handle string, id []byte) (string, Passkey, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for name, u := range s.users {
		if handle != "" && subtle.ConstantTimeCompare([]byte(u.PasskeyHandle), []byte(handle)) != 1 {
			continue
		}
		for _, k := range u.Passkeys {
			if sameBytes(k.ID, id) {
				return name, k, true
			}
		}
	}
	return "", Passkey{}, false
}

// TouchPasskey records a sign-in: the new counter and the time. The counter
// is checked again here, under the write lock, so two sign-ins racing with
// one value cannot both move it. With the database down the change is kept
// in memory, as the second step's replay guard is.
func (s *Store) TouchPasskey(username string, id []byte, count uint32, when time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[normalizeName(username)]
	if !ok {
		return ErrNoSuchUser
	}
	for i := range u.Passkeys {
		k := &u.Passkeys[i]
		if !sameBytes(k.ID, id) {
			continue
		}
		if (count != 0 || k.SignCount != 0) && count <= k.SignCount {
			return ErrPasskeyCounter
		}
		k.SignCount, k.LastUsed = count, when.UTC()
		_ = s.save()
		return nil
	}
	return ErrNoSuchPasskey
}

func sameBytes(a, b []byte) bool {
	return len(a) == len(b) && subtle.ConstantTimeCompare(a, b) == 1
}
