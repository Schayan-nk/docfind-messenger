package auth

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The snapshot is believed only if it is signed, unaltered and recent. Each
// way it can fail must fail CLOSED: no accounts, and a reason that says which.

func writeGoodSnapshot(t *testing.T) string {
	t.Helper()
	snap := filepath.Join(tempDir(t), "users.snapshot.json")
	if err := NewFallback(newSpy(), snap, testSnapKey).Save(oneUser("ali")); err != nil {
		t.Fatal(err)
	}
	return snap
}

func loadDown(t *testing.T, snap string, key []byte) error {
	t.Helper()
	_, err := NewFallback(newDead(), snap, key).Load()
	return err
}

// Somebody able to write the file cannot make themselves an administrator.
func TestAnAlteredSnapshotIsRefused(t *testing.T) {
	snap := writeGoodSnapshot(t)
	raw, _ := os.ReadFile(snap)
	altered := strings.Replace(string(raw), `\"role\":\"admin\"`, `\"role\":\"viewer\"`, 1)
	if altered == string(raw) {
		// The accounts are embedded as raw JSON; match either encoding.
		altered = strings.Replace(string(raw), `"role":"admin"`, `"role":"viewer"`, 1)
	}
	if altered == string(raw) {
		t.Fatalf("test could not find the role to alter in %s", raw)
	}
	os.WriteFile(snap, []byte(altered), 0o600)
	if err := loadDown(t, snap, testSnapKey); err == nil || !strings.Contains(err.Error(), "altered") {
		t.Errorf("an altered snapshot: %v", err)
	}
}

func TestASnapshotSignedWithAnotherKeyIsRefused(t *testing.T) {
	snap := writeGoodSnapshot(t)
	other := SnapshotKey([]byte("another server's key, 32 bytes!!"))
	if err := loadDown(t, snap, other); err == nil {
		t.Error("a snapshot from another key was accepted")
	}
	if err := loadDown(t, snap, nil); err == nil {
		t.Error("a snapshot was accepted with no key to check it")
	}
}

// The bare list earlier versions wrote proves nothing about who wrote it.
func TestAnUnsignedSnapshotIsRefused(t *testing.T) {
	snap := filepath.Join(tempDir(t), "users.snapshot.json")
	raw, _ := json.Marshal(oneUser("ali"))
	os.WriteFile(snap, raw, 0o600)
	if err := loadDown(t, snap, testSnapKey); err == nil || !strings.Contains(err.Error(), "not signed") {
		t.Errorf("an unsigned snapshot: %v", err)
	}
}

// A copy from last month still lets in everybody removed since.
func TestAnOldSnapshotIsRefused(t *testing.T) {
	snap := writeGoodSnapshot(t)
	f := &fallback{primary: newDead(), path: snap, key: testSnapKey}
	if _, err := f.readSnapshot(time.Now()); err != nil {
		t.Fatalf("a fresh snapshot was refused: %v", err)
	}
	if _, err := f.readSnapshot(time.Now().Add(snapshotMaxAge + time.Hour)); err == nil ||
		!strings.Contains(err.Error(), "days old") {
		t.Errorf("an old snapshot: %v", err)
	}
}

func TestAHandReindentedSnapshotStillVerifies(t *testing.T) {
	snap := writeGoodSnapshot(t)
	raw, _ := os.ReadFile(snap)
	// Whitespace only, the way an editor's "format document" does it.
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, raw, "", "    "); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(snap, pretty.Bytes(), 0o600)
	if err := loadDown(t, snap, testSnapKey); err != nil {
		t.Errorf("reformatting the file broke it: %v", err)
	}
}

func TestSnapshotDegradedIsReported(t *testing.T) {
	snap := writeGoodSnapshot(t)
	b := NewFallback(newDead(), snap, testSnapKey)
	if _, err := b.Load(); err != nil {
		t.Fatal(err)
	}
	d, ok := b.(interface{ Degraded() bool })
	if !ok || !d.Degraded() {
		t.Error("serving from the snapshot is not reported as degraded")
	}
}
