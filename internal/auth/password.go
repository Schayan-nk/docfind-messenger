// Package auth provides user accounts, password hashing, sessions and
// role-based access control for the document browser.
//
// This exists because the browser is moving off a loopback-only listener with
// a single shared token and onto the public internet, where "the only thing
// protecting it is that you must already be on this machine" stops being true.
package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Password hashing parameters.
//
// PBKDF2-HMAC-SHA256 rather than argon2id, which would otherwise be the first
// choice: this repository is deliberately built with no network access and
// vendors every dependency, and pbkdf2 is in the standard library as of Go
// 1.24 while argon2 is not. The iteration count follows OWASP's 2023 guidance
// for this construction.
const (
	pbkdf2Iterations = 600_000
	pbkdf2KeyLength  = 32
	saltLength       = 16
	hashScheme       = "pbkdf2-sha256"
)

// ErrMalformedHash means a stored hash could not be parsed, so no password can
// ever match it.
var ErrMalformedHash = errors.New("auth: stored password hash is malformed")

// HashPassword derives a verifier for a plaintext password.
//
// The encoded form carries its own parameters -- scheme, iterations, salt --
// so the cost can be raised later without invalidating existing accounts: an
// old hash still verifies against the parameters it was written with.
func HashPassword(plain string) (string, error) {
	salt := make([]byte, saltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: reading random salt: %w", err)
	}
	key, err := pbkdf2.Key(sha256.New, plain, salt, pbkdf2Iterations, pbkdf2KeyLength)
	if err != nil {
		return "", fmt.Errorf("auth: deriving password hash: %w", err)
	}
	return strings.Join([]string{
		hashScheme,
		strconv.Itoa(pbkdf2Iterations),
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	}, "$"), nil
}

// VerifyPassword reports whether plain matches an encoded hash.
//
// The comparison is constant time so that a wrong password cannot be
// distinguished from a nearly-right one by how long the check took.
func VerifyPassword(encoded, plain string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != hashScheme {
		return false, ErrMalformedHash
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter <= 0 {
		return false, ErrMalformedHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false, ErrMalformedHash
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return false, ErrMalformedHash
	}
	got, err := pbkdf2.Key(sha256.New, plain, salt, iter, len(want))
	if err != nil {
		return false, fmt.Errorf("auth: deriving password hash: %w", err)
	}
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// NeedsRehash reports whether a stored hash was written with weaker
// parameters than HashPassword now uses -- another scheme, fewer iterations,
// a shorter salt or key -- and should be replaced the next time the password
// is known, which is at sign-in.
//
// It matters for more than strength. The unknown-user path costs a CURRENT
// hash; an account left on an older, cheaper one answers measurably faster,
// and that difference is exactly what tells an attacker which names are real.
//
// Checked 2026-09-23: all six live accounts are pbkdf2-sha256 at 600,000
// iterations, which is OWASP's figure for this construction, so today this
// finds nothing. It is what makes the NEXT increase apply to everybody
// without a password reset.
func NeedsRehash(encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != hashScheme {
		return true
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter < pbkdf2Iterations {
		return true
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil || len(salt) < saltLength {
		return true
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[3])
	return err != nil || len(key) < pbkdf2KeyLength
}

// dummyHash is verified against when a login names an account that does not
// exist, so that a request for an unknown user costs the same as one for a
// real user. Without it, the response time alone tells an attacker which
// usernames are worth guessing passwords for.
var dummyHash, _ = HashPassword("this password matches no account")

// VerifyUnknownUser burns the same work a real verification would, and always
// fails. Callers use it on the "no such user" path.
func VerifyUnknownUser(plain string) {
	_, _ = VerifyPassword(dummyHash, plain)
}
