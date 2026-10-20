package auth

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"time"
)

// Registration on this server is invite-only: there is no way to create an
// account without a code, so a public registration page cannot be used by
// whoever finds it. An administrator mints a code, hands it to one person (or
// to a few, with a use limit), and the code decides what role the new account
// gets.
//
// The codes live in a JSON file beside the accounts, the same place the
// sessions and avatars live, so the feature works with or without a database.

// Invite is one code: the role it grants, how many times it may still be
// used, and when it stops working.
type Invite struct {
	Code      string    `json:"code"` // canonical form, grouped with dashes, for display
	Role      Role      `json:"role"`
	MaxUses   int       `json:"max_uses"` // 0 means no limit
	Uses      int       `json:"uses"`
	Note      string    `json:"note,omitempty"`
	CreatedBy string    `json:"created_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at,omitempty"` // zero means never
	Revoked   bool      `json:"revoked,omitempty"`
}

// Spent reports whether the invite can no longer be redeemed: revoked, out of
// uses, or past its expiry.
func (i Invite) Spent() bool {
	return i.Revoked || (i.MaxUses > 0 && i.Uses >= i.MaxUses) ||
		(!i.ExpiresAt.IsZero() && time.Now().After(i.ExpiresAt))
}

var (
	// ErrNoInvite and ErrInviteSpent are the two ways a code is refused. They
	// are reported to a registrant as one message, so a code cannot be probed
	// for whether it ever existed.
	ErrNoInvite    = errors.New("auth: no such invite")
	ErrInviteSpent = errors.New("auth: invite cannot be used")
)

// Invites is the set of codes, kept in a JSON file. The server owns the file;
// nothing else writes it, so there is no lock to coordinate with.
type Invites struct {
	mu   sync.Mutex
	path string
	list []*Invite
}

// inviteAlphabet leaves out the characters that are read wrong by hand: I, L,
// O, 0, 1. A generated code never contains them, so reading one off paper is
// unambiguous.
const inviteAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

// OpenInvites loads the file, or starts empty when it is not there yet.
func OpenInvites(path string) *Invites {
	iv := &Invites{path: path}
	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &iv.list)
	}
	return iv
}

// normInvite reduces a typed code to just its letters and digits, uppercased,
// so "a1b2-c3d4", "A1B2 C3D4" and "a1b2c3d4" are one code.
func normInvite(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		if r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// newCode is sixteen characters of the safe alphabet, grouped four and four.
func newCode() string {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		// crypto/rand does not fail in practice; if it ever did, a predictable
		// code must never be issued, so panic rather than hand one out.
		panic("auth: invite code randomness: " + err.Error())
	}
	var b strings.Builder
	for i, c := range raw {
		if i > 0 && i%4 == 0 {
			b.WriteByte('-')
		}
		b.WriteByte(inviteAlphabet[int(c)%len(inviteAlphabet)])
	}
	return b.String()
}

// Mint creates a code. maxUses 0 is unlimited; ttl 0 never expires.
func (iv *Invites) Mint(role Role, maxUses int, ttl time.Duration, note, by string) (Invite, error) {
	if !ValidRole(role) {
		return Invite{}, ErrInvalidRole
	}
	in := &Invite{
		Code: newCode(), Role: role, MaxUses: maxUses,
		Note: strings.TrimSpace(note), CreatedBy: by, CreatedAt: time.Now().UTC(),
	}
	if ttl > 0 {
		in.ExpiresAt = in.CreatedAt.Add(ttl)
	}
	iv.mu.Lock()
	defer iv.mu.Unlock()
	iv.list = append(iv.list, in)
	if err := iv.save(); err != nil {
		iv.list = iv.list[:len(iv.list)-1]
		return Invite{}, err
	}
	return *in, nil
}

// List is every code, newest first, as copies.
func (iv *Invites) List() []Invite {
	iv.mu.Lock()
	defer iv.mu.Unlock()
	out := make([]Invite, 0, len(iv.list))
	for i := len(iv.list) - 1; i >= 0; i-- {
		out = append(out, *iv.list[i])
	}
	return out
}

// find returns the stored code matching a typed one. Caller holds the lock.
func (iv *Invites) find(code string) *Invite {
	n := normInvite(code)
	if n == "" {
		return nil
	}
	for _, in := range iv.list {
		if normInvite(in.Code) == n {
			return in
		}
	}
	return nil
}

// Redeem consumes one use of a code and returns the role it grants.
func (iv *Invites) Redeem(code string) (Role, error) {
	iv.mu.Lock()
	defer iv.mu.Unlock()
	in := iv.find(code)
	if in == nil {
		return "", ErrNoInvite
	}
	if in.Spent() {
		return "", ErrInviteSpent
	}
	in.Uses++
	if err := iv.save(); err != nil {
		in.Uses--
		return "", err
	}
	return in.Role, nil
}

// Owner is who minted a code, or "" for an unknown one: the person told
// when somebody signs up with it.
func (iv *Invites) Owner(code string) string {
	iv.mu.Lock()
	defer iv.mu.Unlock()
	if in := iv.find(code); in != nil {
		return in.CreatedBy
	}
	return ""
}

// Refund gives back a use, for when account creation failed after Redeem.
func (iv *Invites) Refund(code string) {
	iv.mu.Lock()
	defer iv.mu.Unlock()
	if in := iv.find(code); in != nil && in.Uses > 0 {
		in.Uses--
		_ = iv.save()
	}
}

// Revoke stops a code from being used, without removing it, so the record of
// who made it and how often it was used survives.
func (iv *Invites) Revoke(code string) error {
	iv.mu.Lock()
	defer iv.mu.Unlock()
	in := iv.find(code)
	if in == nil {
		return ErrNoInvite
	}
	in.Revoked = true
	return iv.save()
}

// save writes the file atomically: a temporary file, then a rename, so a
// crash mid-write cannot leave a half-written list.
func (iv *Invites) save() error {
	raw, err := json.MarshalIndent(iv.list, "", "  ")
	if err != nil {
		return err
	}
	tmp := iv.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, iv.path)
}
