package auth

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Profiles: what an administrator records about a person beside their
// account -- mobile, both email addresses, personnel code, picture and
// signature -- and whether they must replace their password at the next
// sign-in.

// validCode is what a personnel code may look like: up to 32 letters, digits
// and dashes. The live ones are four digits; the bound only keeps whatever
// else is in that column out of a database text field unexamined.
func validCode(s string) bool {
	if len(s) > 32 {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r == '-') {
			return false
		}
	}
	return true
}

// checkEmail is SetEmail's test, shared: refuse only what cannot be an
// address at all.
func checkEmail(email string) error {
	if len(email) > 320 {
		return fmt.Errorf("auth: that address is too long")
	}
	at := strings.IndexByte(email, '@')
	if at <= 0 || at == len(email)-1 || strings.Count(email, "@") != 1 ||
		strings.ContainsAny(email, " \t\r\n") {
		return fmt.Errorf("auth: %q is not an email address", email)
	}
	return nil
}

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// SetUpdatePassword sets or clears an account's update_password flag: an
// administrator's switch on the users page, and the sign-in's answer to a
// password that no longer meets the policy.
func (s *Store) SetUpdatePassword(username string, on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[normalizeName(username)]
	if !ok {
		return ErrNoSuchUser
	}
	if u.MustChangePassword == on {
		return nil
	}
	u.MustChangePassword = on
	if err := s.save(); err != nil {
		u.MustChangePassword = !on
		return err
	}
	return nil
}

// Profile is what the users page edits about a person beyond their name,
// role, status and password. A nil field is left as it is.
type Profile struct {
	DisplayName    *string `json:"display_name"`
	EnglishName    *string `json:"english_name"`
	Email          *string `json:"email"`
	SecondEmail    *string `json:"second_email"`
	PersonnelCode  *string `json:"personnel_code"`
	UpdatePassword *bool   `json:"update_password"`
}

// SetProfile changes the fields given, each checked as the import checks it.
func (s *Store) SetProfile(username string, p Profile) error {
	clean := func(v *string, check func(string) error) (*string, error) {
		if v == nil {
			return nil, nil
		}
		t := strings.TrimSpace(*v)
		if t != "" && check != nil {
			if err := check(t); err != nil {
				return nil, err
			}
		}
		return &t, nil
	}
	var err error
	var email, second, code, name, en *string
	if email, err = clean(p.Email, checkEmail); err != nil {
		return err
	}
	if second, err = clean(p.SecondEmail, checkEmail); err != nil {
		return err
	}
	if code, err = clean(p.PersonnelCode, func(c string) error {
		if !validCode(c) {
			return fmt.Errorf("auth: %q is not a personnel code", c)
		}
		return nil
	}); err != nil {
		return err
	}
	if name, err = clean(p.DisplayName, nil); err != nil {
		return err
	}
	if en, err = clean(p.EnglishName, nil); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[normalizeName(username)]
	if !ok {
		return ErrNoSuchUser
	}
	before := *u
	if email != nil {
		u.Email = *email
	}
	if second != nil {
		u.SecondEmail = *second
	}
	if code != nil {
		u.PersonnelCode = *code
	}
	if name != nil && *name != "" {
		u.DisplayName = clip(*name, 120)
	}
	if en != nil {
		u.EnglishName = clip(*en, 60)
	}
	if p.UpdatePassword != nil {
		u.MustChangePassword = *p.UpdatePassword
	}
	if err := s.save(); err != nil {
		*u = before
		return err
	}
	return nil
}
