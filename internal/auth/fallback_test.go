package auth

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// deadBackend is a database that cannot be reached.
type deadBackend struct {
	loads  int
	saves  int
	reason string
}

func newDead() *deadBackend {
	return &deadBackend{reason: "dial tcp 192.168.77.13:5432: connection refused"}
}

func (d *deadBackend) Load() ([]*User, error) {
	d.loads++
	return nil, fmt.Errorf("%s", d.reason)
}
func (d *deadBackend) Save([]*User) error {
	d.saves++
	return fmt.Errorf("%s", d.reason)
}
func (d *deadBackend) Healthy() bool    { return false }
func (d *deadBackend) Describe() string { return "PostgreSQL (unreachable)" }

// testSnapKey signs the snapshots these tests write.
var testSnapKey = SnapshotKey([]byte("0123456789abcdef0123456789abcdef"))

func oneUser(name string) []*User {
	return []*User{{Username: name, PasswordHash: "pbkdf2-sha256$x$y", Role: RoleAdmin}}
}

// A live primary is used, and every write refreshes the snapshot -- which is
// what makes the snapshot worth reading later.
func TestALivePrimaryIsUsedAndTheSnapshotIsKeptCurrent(t *testing.T) {
	snap := filepath.Join(tempDir(t), "users.snapshot.json")
	spy := newSpy()
	b := NewFallback(spy, snap, testSnapKey)

	if err := b.Save(oneUser("ali")); err != nil {
		t.Fatal(err)
	}
	if len(spy.saves) != 1 {
		t.Errorf("the primary was not written: %d saves", len(spy.saves))
	}
	if _, err := os.Stat(snap); err != nil {
		t.Fatalf("no snapshot was written: %v", err)
	}
	if !b.Healthy() {
		t.Error("a live primary reports unhealthy")
	}
}

// THE WHOLE PROMISE: with the primary down, reads work from the snapshot.
func TestWithThePrimaryDownReadsComeFromTheSnapshot(t *testing.T) {
	snap := filepath.Join(tempDir(t), "users.snapshot.json")
	// Populate the snapshot through a healthy fallback first.
	if err := NewFallback(newSpy(), snap, testSnapKey).Save(oneUser("ali")); err != nil {
		t.Fatal(err)
	}

	dead := newDead()
	b := NewFallback(dead, snap, testSnapKey)
	loaded, err := b.Load()
	if err != nil {
		t.Fatalf("read failed in degraded mode: %v", err)
	}
	if len(loaded) != 1 || loaded[0].Username != "ali" {
		t.Fatalf("the snapshot did not carry the account: %v", loaded)
	}
	if loaded[0].PasswordHash != "pbkdf2-sha256$x$y" {
		t.Errorf("the hash did not survive the snapshot: %q", loaded[0].PasswordHash)
	}
	if b.Healthy() {
		t.Error("a degraded backend reports healthy")
	}
}

// And writes are REFUSED with a typed error. Accepting a write it cannot store
// is the one thing this must never do: somebody sets a password, is told it
// worked, and can sign in with it only until the process restarts.
func TestWritesAreRefusedInDegradedMode(t *testing.T) {
	snap := filepath.Join(tempDir(t), "users.snapshot.json")
	if err := NewFallback(newSpy(), snap, testSnapKey).Save(oneUser("ali")); err != nil {
		t.Fatal(err)
	}

	dead := newDead()
	b := NewFallback(dead, snap, testSnapKey)
	if _, err := b.Load(); err != nil {
		t.Fatal(err)
	}
	err := b.Save(oneUser("reza"))
	if !errors.Is(err, ErrReadOnly) {
		t.Errorf("a write in degraded mode returned %v, want ErrReadOnly", err)
	}
	// It must not even have tried: a refusal is cheaper and clearer than a
	// timeout, and the caller gets one honest message either way.
	if dead.saves != 0 {
		t.Errorf("the primary was contacted %d time(s) while known to be down", dead.saves)
	}
}

// A dead primary AND no snapshot is a real failure that must be reported.
//
// Returning an empty set here would be the worst possible behaviour: an empty
// account store is indistinguishable from a fresh install, and a fresh install
// asks the next visitor to create the first administrator.
func TestADeadPrimaryWithNoSnapshotIsAnErrorNotAnEmptyStore(t *testing.T) {
	b := NewFallback(newDead(), filepath.Join(tempDir(t), "absent.json"), testSnapKey)
	loaded, err := b.Load()
	if err == nil {
		t.Fatalf("a dead primary with no snapshot loaded %d accounts successfully", len(loaded))
	}
	// The message has to name both halves, or somebody debugs the wrong one.
	for _, want := range []string{"unreachable", "snapshot"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %q: %v", want, err)
		}
	}
}

// A snapshot with no accounts in it is treated as no snapshot, for the same
// reason: an empty list would trip the first-administrator flow.
func TestAnEmptySnapshotIsTreatedAsNoSnapshot(t *testing.T) {
	snap := filepath.Join(tempDir(t), "users.snapshot.json")
	if err := os.WriteFile(snap, []byte("[]"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFallback(newDead(), snap, testSnapKey).Load(); err == nil {
		t.Error("an empty snapshot was accepted as a usable account store")
	}
}

// RECOVERY. When the database comes back, the next Load must clear the
// degraded flag and writes must work again -- otherwise a transient outage
// leaves the application read-only until somebody restarts it.
func TestItRecoversWhenThePrimaryComesBack(t *testing.T) {
	snap := filepath.Join(tempDir(t), "users.snapshot.json")
	if err := NewFallback(newSpy(), snap, testSnapKey).Save(oneUser("ali")); err != nil {
		t.Fatal(err)
	}

	flaky := &flakyBackend{spy: newSpy(), down: true}
	b := NewFallback(flaky, snap, testSnapKey)

	if _, err := b.Load(); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(b.Save(oneUser("reza")), ErrReadOnly) {
		t.Fatal("expected read-only while down")
	}

	flaky.down = false
	if _, err := b.Load(); err != nil {
		t.Fatalf("load after recovery: %v", err)
	}
	if !b.Healthy() {
		t.Error("still degraded after a successful load")
	}
	if err := b.Save(oneUser("reza")); err != nil {
		t.Errorf("write after recovery failed: %v", err)
	}
}

// A write that fails while the primary still reports healthy returns the REAL
// error, not ErrReadOnly. The two mean different things -- "the database
// refused this" versus "the database is gone" -- and conflating them sends
// somebody to check the wrong thing.
func TestAGenuineWriteFailureIsNotReportedAsReadOnly(t *testing.T) {
	snap := filepath.Join(tempDir(t), "users.snapshot.json")
	spy := newSpy()
	spy.failSave = fmt.Errorf("duplicate key value violates unique constraint")
	b := NewFallback(spy, snap, testSnapKey)

	err := b.Save(oneUser("ali"))
	if err == nil {
		t.Fatal("a refused write succeeded")
	}
	if errors.Is(err, ErrReadOnly) {
		t.Errorf("a constraint violation was reported as read-only: %v", err)
	}
	if !strings.Contains(err.Error(), "duplicate key") {
		t.Errorf("the real reason was lost: %v", err)
	}
}

// The snapshot holds password hashes, so it gets the same permissions the
// accounts file has always had.
func TestTheSnapshotIsNotWorldReadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits are not meaningful on windows; the ACL is inherited from the directory")
	}
	snap := filepath.Join(tempDir(t), "users.snapshot.json")
	if err := NewFallback(newSpy(), snap, testSnapKey).Save(oneUser("ali")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(snap)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("snapshot mode is %o, want 600 -- it holds password hashes", perm)
	}
}

// flakyBackend is a primary that can be taken down and brought back.
type flakyBackend struct {
	spy  *spyBackend
	down bool
}

func (f *flakyBackend) Load() ([]*User, error) {
	if f.down {
		return nil, fmt.Errorf("connection refused")
	}
	return f.spy.Load()
}
func (f *flakyBackend) Save(u []*User) error {
	if f.down {
		return fmt.Errorf("connection refused")
	}
	return f.spy.Save(u)
}
func (f *flakyBackend) Healthy() bool    { return !f.down }
func (f *flakyBackend) Describe() string { return "flaky" }

// ReadSnapshot gives back the permission overrides a save wrote, which is
// what the first start with the database's grants column restores from; and
// it refuses a snapshot signed with another key, as Load does.
func TestReadSnapshotKeepsTheGrants(t *testing.T) {
	snap := filepath.Join(tempDir(t), "users.snapshot.json")
	b := NewFallback(newSpy(), snap, testSnapKey)
	u := oneUser("ali")
	u[0].Grants = map[string]string{"action:notifications": "allow", "page:settings": "deny"}
	if err := b.Save(u); err != nil {
		t.Fatal(err)
	}
	got, err := ReadSnapshot(snap, testSnapKey, time.Now())
	if err != nil || len(got) != 1 || got[0].Grants["action:notifications"] != "allow" || got[0].Grants["page:settings"] != "deny" {
		t.Fatalf("read %+v, %v", got, err)
	}
	if _, err := ReadSnapshot(snap, SnapshotKey([]byte("another key, another key, another")), time.Now()); err == nil {
		t.Error("a snapshot signed with another key was believed")
	}
}
