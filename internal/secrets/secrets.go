// Package secrets keeps the credentials this server must be able to read back.
//
// ENCRYPTED, NOT HASHED, and the difference is the whole reason this package
// exists separately from the account store. A hash is one-way: it verifies a
// password somebody typed and cannot be turned back into the original.
// The server has to USE these again -- the Web Push signing key signs every
// notification it sends -- so hashing them would make them unusable. Hashing applies to exactly one thing in this application: the
// users' own login passwords, which are only ever verified. Those live in
// internal/auth and never here.
//
// This was asked for as "store them hashed". Hashed is right for the login
// passwords and impossible for these, so they are encrypted with the
// construction the report builder already uses for its connection strings.
//
// WHAT THE ENCRYPTION BUYS, stated as plainly as internal/reports/secret.go
// states it, because encryption at rest is easy to oversell:
//
//   - It does buy: a copied database, a backup, or a support bundle no longer
//     hands over the credentials. Those are the realistic ways a secret
//     escapes a server, and all of them move the data without the key.
//   - It does NOT buy protection from somebody already running code as this
//     service on this machine. They can read the key and then the data, in
//     that order. Nothing that stores a secret it must later use can claim
//     otherwise.
//
// THE KEY FILE MUST BE BACKED UP WITH THE DATABASE. Losing it loses these
// credentials and nothing else -- they can be re-entered -- but a restore that
// brings back the rows without the key produces six credentials nobody can
// read and an error telling them to type them again.
package secrets

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Schayan-nk/docfind-messenger/internal/pgstore"
)

// The credential keys, as a closed list.
//
// CLOSED so a typo cannot create a second credential that nothing reads. That
// is the failure which looks like success: the page saves, the administrator
// believes the key is configured, and the provider goes on refusing every call
// because the value nothing looks for is the one that was set.
const (
	// KeyVAPID is the private half of the key every device's push
	// subscription is bound to. Generated once, on the first start with
	// notifications on, and never rotated casually: a new key orphans every
	// subscribed device, and each has to be switched on again by hand.
	KeyVAPID = "webpush.vapid"
)

// Keys is every credential this application knows how to use.
//
// Adding one is a code change on purpose: something has to read it, and a key
// nothing reads is a field that quietly does nothing.
var Keys = []string{KeyVAPID}

// Box is the sealing this package needs, as an interface.
//
// An interface rather than importing internal/reports, so the dependency does
// not run backwards and so a test can supply a box of its own. reports.FileBox
// satisfies it, as does the one internal/mail declares for the same reason.
type Box interface {
	Seal(plain []byte) ([]byte, error)
	Open(sealed []byte) ([]byte, error)
}

// Store is the credential table.
type Store struct {
	db  *pgstore.DB
	box Box
}

// Open returns a store over db, sealing with box.
func Open(db *pgstore.DB, box Box) *Store { return &Store{db: db, box: box} }

const opTimeout = 10 * time.Second

// Get returns one credential, or "" if it was never set.
//
// AN UNSET CREDENTIAL IS NOT AN ERROR. One nobody has configured is a choice
// -- the AI features are optional, the WhatsApp service may not exist on a
// given deployment -- and making every caller distinguish "absent" from
// "broken" means every caller getting it wrong somewhere. A credential that
// cannot be DECRYPTED is a different matter and is reported.
func (s *Store) Get(key string) (string, error) {
	if !slices.Contains(Keys, key) {
		return "", fmt.Errorf("secrets: %q is not a credential this application uses", key)
	}
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()

	var sealed []byte
	err := s.db.Pool().QueryRow(ctx,
		`SELECT ciphertext FROM secrets WHERE key = $1`, key).Scan(&sealed)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("secrets: reading %s: %w", key, err)
	}
	plain, err := s.box.Open(sealed)
	if err != nil {
		// The underlying error is deliberately not wrapped: it says something
		// like "cipher: message authentication failed", which tells an
		// administrator nothing they can act on. The key is named so they know
		// which of six to re-enter, and the instruction is the useful part.
		return "", fmt.Errorf("secrets: %s cannot be read with the current key; enter it again", key)
	}
	return string(plain), nil
}

// Set stores one credential, sealed.
func (s *Store) Set(key, value, by string) error {
	if !slices.Contains(Keys, key) {
		return fmt.Errorf("secrets: %q is not a credential this application uses", key)
	}
	// An empty value is indistinguishable from "not configured" on the way
	// back out, so storing one would silently mean the opposite of what
	// somebody intended by clearing the field.
	if value == "" {
		return fmt.Errorf("secrets: an empty value is not a credential; delete %s instead", key)
	}
	if s.box == nil {
		return fmt.Errorf("secrets: no key is configured to protect %s with", key)
	}
	sealed, err := s.box.Seal([]byte(value))
	if err != nil {
		return fmt.Errorf("secrets: %s could not be protected: %w", key, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()
	if _, err := s.db.Pool().Exec(ctx, `
		INSERT INTO secrets (key, ciphertext, updated_by, updated_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (key) DO UPDATE SET
		    ciphertext = EXCLUDED.ciphertext,
		    updated_by = EXCLUDED.updated_by,
		    updated_at = EXCLUDED.updated_at`, key, sealed, by); err != nil {
		return fmt.Errorf("secrets: storing %s: %w", key, err)
	}
	return nil
}
