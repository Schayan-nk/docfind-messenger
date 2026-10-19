package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Where accounts are kept.
//
// AN INTERFACE SO THE STORE DOES NOT CHANGE. This package has fifteen account
// methods -- Create, Update, SetPassword, SetPrefs, RevokeTokens and the rest
// -- and every one of them mutates an in-memory map and then persists the
// whole set. Moving accounts into a database by rewriting those fifteen into
// SQL would be fifteen opportunities to change a behaviour nobody asked to
// change. So the methods are left exactly as they are and only the persisting
// is swapped.
//
// THE WHOLE SET, NOT ONE ACCOUNT. Save takes every account, because that is
// what writing the accounts file has always meant and because save() is called
// from eleven places with the lock already held and no idea which account
// changed. A per-account interface would have meant editing all eleven and
// inventing an undo for each -- more risk than a hundred-row upsert is worth
// at this scale. If the account list ever grows into the thousands this is the
// thing to revisit, and the database implementation can then write one row
// without this interface changing shape for its callers.
//
// READS DO NOT GO THROUGH THIS. The store holds every account in memory -- of
// the order of a hundred of them -- so a sign-in is a map lookup. A database
// outage therefore cannot make signing in slow, and that property is the
// reason the fallback in fallback.go can be as simple as it is.
type Backend interface {
	// Load returns every account. Called once, at open.
	Load() ([]*User, error)
	// Save persists every account, replacing what was there.
	Save(users []*User) error
	// Healthy reports whether writes are currently possible. A backend that
	// has lost its database answers false, and the health page says so
	// instead of the application offering controls that will fail.
	Healthy() bool
	// Describe names this backend for a log line and the health page.
	Describe() string
}

// jsonBackend is the accounts file, which is where accounts have always lived.
//
// Its Load and Save are the bodies that used to sit in OpenStore and save(),
// moved rather than rewritten: this file is the reason the refactor is
// behaviour-preserving, so the logic in it should be recognisable as the same
// logic.
type jsonBackend struct{ path string }

func newJSONBackend(path string) *jsonBackend { return &jsonBackend{path: path} }

// Describe names the file, not the full path: this reaches a page, and the
// full path of the accounts file is not something to put on one.
func (b *jsonBackend) Describe() string {
	if b.path == "" {
		return "پروندهٔ حساب‌ها"
	}
	return "پروندهٔ حساب‌ها (" + filepath.Base(b.path) + ")"
}

// Healthy is always true. A local file has no connection to lose, and
// answering false because a single write failed would take the application
// read-only over a transient disk error.
func (b *jsonBackend) Healthy() bool { return true }

// Load reads the accounts file.
//
// A missing file is the ordinary first run and not an error -- the
// first-administrator screen depends on getting an empty store rather than a
// failure.
func (b *jsonBackend) Load() ([]*User, error) {
	raw, err := os.ReadFile(b.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("auth: reading %s: %w", b.path, err)
	}
	var list []*User
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("auth: parsing %s: %w", b.path, err)
	}
	return list, nil
}

// Save replaces the accounts file.
//
// Written to a temporary file and renamed, because a crash midway through
// writing this file would leave a truncated one -- and a truncated accounts
// file locks everybody out of the application. The rename is atomic on this
// filesystem, which is what makes the old file survive a failure rather than
// being replaced by half of a new one.
func (b *jsonBackend) Save(list []*User) error {
	raw, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return fmt.Errorf("auth: encoding accounts: %w", err)
	}
	if dir := filepath.Dir(b.path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("auth: creating %s: %w", dir, err)
		}
	}
	tmp := b.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("auth: writing accounts: %w", err)
	}
	if err := os.Rename(tmp, b.path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("auth: replacing accounts: %w", err)
	}
	return nil
}
