package auth

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// ErrReadOnly is returned when the database is unreachable and the application
// is serving accounts from its snapshot.
//
// TYPED, so a route can turn it into one honest message instead of leaking a
// driver error onto a page. The alternative -- accepting the write and
// reconciling later -- is how two stores drift, and the drift is discovered by
// somebody whose password change silently did not happen.
var ErrReadOnly = errors.New("auth: the database is unavailable, so accounts cannot be changed")

// fallback wraps a backend with a local snapshot of the accounts.
//
// IT BUYS EXACTLY ONE THING: docfind starts, and people sign in, while the
// database is down. That matters because accounts are the one store whose
// absence stops everything -- an unreachable reporting database costs you the
// reports page, an unreachable account store costs you the door.
//
// IT DELIBERATELY DOES NOT BUY WRITE AVAILABILITY. Queueing writes to
// reconcile when the database returns sounds generous and is how two stores
// drift apart; the drift is then discovered by the person whose password
// change quietly did not take. A refusal is worse for one request and better
// for everybody.
//
// AND IT FAILS CLOSED. The snapshot is only believed if it is SIGNED -- an
// HMAC under a key derived from the session key, so somebody able to write
// the file but not read that key cannot hand themselves an administrator
// account -- and RECENT: older than snapshotMaxAge and it is refused, because
// a month-old copy still lets in the people removed since. A snapshot that is
// refused is reported, and the application does not open accounts at all
// rather than open them on something it cannot trust. Sign-ins made from a
// snapshot are emergency sessions: see SignEmergency and Can.
type fallback struct {
	primary Backend
	path    string
	key     []byte

	mu       sync.RWMutex
	degraded bool
}

// snapshotMaxAge is how old a snapshot may be and still be used to sign
// people in. The snapshot is rewritten on every successful load and every
// account change, so a live system's is never more than hours old; a week
// covers a database down over a long weekend and no more.
const snapshotMaxAge = 7 * 24 * time.Hour

// snapshotFile is the snapshot as written to disk.
type snapshotFile struct {
	Version   int             `json:"version"`
	WrittenAt time.Time       `json:"written_at"`
	Accounts  json.RawMessage `json:"accounts"`
	MAC       string          `json:"mac"`
}

// NewFallback returns a Backend that falls back to a snapshot at path, signed
// and verified with key. A nil key means no snapshot can ever be trusted.
func NewFallback(primary Backend, path string, key []byte) Backend {
	return &fallback{primary: primary, path: path, key: key}
}

// SnapshotKey derives the snapshot's key from the session-signing key, so the
// two are never the same bytes used for two purposes.
func SnapshotKey(sessionKey []byte) []byte {
	if len(sessionKey) == 0 {
		return nil
	}
	m := hmac.New(sha256.New, sessionKey)
	m.Write([]byte("docfind accounts snapshot v1"))
	return m.Sum(nil)
}

func (f *fallback) Describe() string {
	if f.isDegraded() {
		return f.primary.Describe() + ": دسترس‌ناپذیر؛ فقط خواندن از نسخهٔ پشتیبان"
	}
	return f.primary.Describe()
}

// Healthy is false in degraded mode, which is what stops a page offering a
// control that would be refused.
func (f *fallback) Healthy() bool {
	return !f.isDegraded() && f.primary.Healthy()
}

// Degraded reports whether accounts are being served from the snapshot --
// which is what makes a sign-in an emergency session.
func (f *fallback) Degraded() bool { return f.isDegraded() }

// Load tries the primary and reads the snapshot when it cannot.
//
// This is also the recovery path: a successful load clears the degraded flag,
// so a transient outage does not leave the application read-only until
// somebody restarts it.
func (f *fallback) Load() ([]*User, error) {
	users, err := f.primary.Load()
	if err == nil {
		f.setDegraded(false)
		// Refreshed on load as well as on write, so a snapshot exists even on
		// a deployment where nothing has been changed since the database
		// arrived.
		if len(users) > 0 {
			_ = f.writeSnapshot(users)
		}
		return users, nil
	}

	snap, serr := f.readSnapshot(time.Now())
	if serr != nil {
		// Both gone. REPORTED rather than returning an empty set: an empty
		// account store is indistinguishable from a fresh install, and a fresh
		// install asks the next visitor to create the first administrator.
		// Naming both halves matters, or somebody debugs the wrong one.
		return nil, fmt.Errorf(
			"auth: the account database is unreachable (%v), and its snapshot at %s could not be used (%v)",
			err, f.path, serr)
	}
	f.setDegraded(true)
	return snap, nil
}

// Save writes through to the primary, then refreshes the snapshot.
func (f *fallback) Save(users []*User) error {
	if f.isDegraded() {
		// Not even attempted. A refusal is cheaper and clearer than a timeout,
		// and the caller gets one honest message either way.
		return ErrReadOnly
	}
	if err := f.primary.Save(users); err != nil {
		// A write can fail for two very different reasons, and conflating them
		// sends somebody to check the wrong thing. If the primary has gone
		// away, switch to degraded and say so; if it is up and refused this
		// particular write, the real error is the useful one.
		if !f.primary.Healthy() {
			f.setDegraded(true)
			return fmt.Errorf("%w (%v)", ErrReadOnly, err)
		}
		return err
	}
	_ = f.writeSnapshot(users)
	return nil
}

func (f *fallback) isDegraded() bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.degraded
}

func (f *fallback) setDegraded(v bool) {
	f.mu.Lock()
	f.degraded = v
	f.mu.Unlock()
}

// mac is the snapshot's signature over its time and its accounts.
func (f *fallback) mac(writtenAt time.Time, accounts []byte) []byte {
	m := hmac.New(sha256.New, f.key)
	m.Write([]byte("docfind accounts snapshot v1\n"))
	m.Write([]byte(writtenAt.UTC().Format(time.RFC3339Nano)))
	m.Write([]byte("\n"))
	m.Write(accounts)
	return m.Sum(nil)
}

// ReadSnapshot returns the accounts in the signed snapshot at path, verified
// as Load verifies it: signed with key, no older than snapshotMaxAge. For
// recovering what only the snapshot holds -- the permission overrides the
// database did not store before migration 041.
func ReadSnapshot(path string, key []byte, now time.Time) ([]*User, error) {
	return (&fallback{path: path, key: key}).readSnapshot(now)
}

func (f *fallback) readSnapshot(now time.Time) ([]*User, error) {
	if len(f.key) == 0 {
		return nil, fmt.Errorf("there is no key to verify it with")
	}
	raw, err := os.ReadFile(f.path)
	if err != nil {
		return nil, err
	}
	var file snapshotFile
	if err := json.Unmarshal(raw, &file); err != nil || file.Version == 0 {
		// Including the bare list an earlier version wrote: unsigned, so it
		// proves nothing about who wrote it.
		return nil, fmt.Errorf("it is not signed (written by an older docfind, or by hand)")
	}
	// Compacted before checking, so re-indenting the file by hand does not
	// read as tampering -- only a change to what it says does.
	var accounts bytes.Buffer
	if err := json.Compact(&accounts, file.Accounts); err != nil {
		return nil, fmt.Errorf("its accounts are not readable")
	}
	file.Accounts = accounts.Bytes()
	want, err := base64.RawStdEncoding.DecodeString(file.MAC)
	if err != nil || !hmac.Equal(want, f.mac(file.WrittenAt, file.Accounts)) {
		return nil, fmt.Errorf("its signature does not match -- it has been altered")
	}
	if age := now.Sub(file.WrittenAt); age > snapshotMaxAge {
		return nil, fmt.Errorf("it is %d days old; the limit is %d",
			int(age.Hours()/24), int(snapshotMaxAge.Hours()/24))
	}
	var users []*User
	if err := json.Unmarshal(file.Accounts, &users); err != nil {
		return nil, err
	}
	// An empty snapshot is treated as no snapshot, for the same reason a
	// missing one is: an empty list would trip the first-administrator flow.
	if len(users) == 0 {
		return nil, fmt.Errorf("it holds no accounts")
	}
	return users, nil
}

// writeSnapshot replaces the snapshot atomically.
//
// 0600, because it holds password hashes -- the same exposure the accounts
// file has always had. Written to a temporary file and renamed so a crash
// midway cannot leave a truncated snapshot. Not written at all without a key:
// a snapshot nothing can verify would only ever be refused.
func (f *fallback) writeSnapshot(users []*User) error {
	if len(f.key) == 0 {
		return nil
	}
	sorted := make([]*User, len(users))
	copy(sorted, users)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Username < sorted[j].Username })

	accounts, err := json.Marshal(sorted)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	// Compact, not indented: indenting would reformat the signed accounts too.
	raw, err := json.Marshal(snapshotFile{
		Version: 1, WrittenAt: now, Accounts: accounts,
		MAC: base64.RawStdEncoding.EncodeToString(f.mac(now, accounts)),
	})
	if err != nil {
		return err
	}
	if dir := filepath.Dir(f.path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	tmp := f.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, f.path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
