package auth

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(filepath.Join(tempDir(t), "users.json"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestCreateAndAuthenticate(t *testing.T) {
	s := newStore(t)
	if _, err := s.Create("r.ahmadi", "Shayan", "a-good-password", RoleAdmin); err != nil {
		t.Fatalf("Create: %v", err)
	}

	u, err := s.Authenticate("r.ahmadi", "a-good-password")
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if u.Role != RoleAdmin {
		t.Errorf("role = %q, want admin", u.Role)
	}
	if u.LastLogin.IsZero() {
		t.Error("a successful sign-in did not record LastLogin")
	}

	if _, err := s.Authenticate("r.ahmadi", "wrong"); !errors.Is(err, ErrBadCredential) {
		t.Errorf("wrong password gave %v, want ErrBadCredential", err)
	}
}

// A wrong password and an account that does not exist must be indistinguishable
// from the outside, or the login form becomes a way to enumerate staff.
func TestUnknownUserAndWrongPasswordLookTheSame(t *testing.T) {
	s := newStore(t)
	if _, err := s.Create("real.user", "Real", "password-one", RoleViewer); err != nil {
		t.Fatal(err)
	}
	_, errUnknown := s.Authenticate("ghost.user", "password-one")
	_, errWrong := s.Authenticate("real.user", "not-the-password")

	if !errors.Is(errUnknown, ErrBadCredential) || !errors.Is(errWrong, ErrBadCredential) {
		t.Fatalf("errors differ: unknown=%v wrong=%v", errUnknown, errWrong)
	}
}

// Names differing only by case would otherwise become two accounts that look
// identical everywhere they are listed.
func TestUsernamesAreCaseInsensitive(t *testing.T) {
	s := newStore(t)
	if _, err := s.Create("Admin.User", "Admin", "password-one", RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create("admin.user", "Impostor", "password-two", RoleAdmin); !errors.Is(err, ErrUserExists) {
		t.Fatalf("creating a case variant gave %v, want ErrUserExists", err)
	}
	if _, err := s.Authenticate("ADMIN.USER", "password-one"); err != nil {
		t.Errorf("could not sign in with a different case: %v", err)
	}
}

func TestInvalidUsernamesAndRolesAreRefused(t *testing.T) {
	s := newStore(t)
	for _, name := range []string{"ab", "has space", "sql';--", "../../etc/passwd", strings.Repeat("x", 65)} {
		if _, err := s.Create(name, "X", "password-one", RoleViewer); !errors.Is(err, ErrInvalidName) {
			t.Errorf("Create(%q) gave %v, want ErrInvalidName", name, err)
		}
	}
	if _, err := s.Create("fine.name", "X", "password-one", Role("superuser")); !errors.Is(err, ErrInvalidRole) {
		t.Errorf("an unknown role was accepted: %v", err)
	}
}

// Locking every administrator out of the system leaves no way back in short of
// hand-editing JSON on the server, so the store refuses.
func TestCannotRemoveTheLastAdmin(t *testing.T) {
	s := newStore(t)
	if _, err := s.Create("only.admin", "Only Admin", "password-one", RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create("a.viewer", "Viewer", "password-two", RoleViewer); err != nil {
		t.Fatal(err)
	}

	if err := s.Delete("only.admin"); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("deleting the last admin gave %v, want ErrLastAdmin", err)
	}
	if _, err := s.Update("only.admin", "Only Admin", RoleViewer, false); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("demoting the last admin gave %v, want ErrLastAdmin", err)
	}
	if _, err := s.Update("only.admin", "Only Admin", RoleAdmin, true); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("disabling the last admin gave %v, want ErrLastAdmin", err)
	}

	// With a second admin present, the first may go.
	if _, err := s.Create("second.admin", "Second", "password-three", RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("only.admin"); err != nil {
		t.Errorf("could not delete an admin while another remains: %v", err)
	}
}

// Anything crossing the process boundary must not carry the password hash.
func TestPublicOmitsPasswordHash(t *testing.T) {
	s := newStore(t)
	u, err := s.Create("some.user", "Some User", "password-one", RoleViewer)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range u.Public() {
		if strings.Contains(strings.ToLower(k), "password") {
			t.Errorf("Public() exposed field %q", k)
		}
		if s, ok := v.(string); ok && strings.Contains(s, "pbkdf2") {
			t.Errorf("Public() leaked the hash in field %q", k)
		}
	}
}

// The file holds password hashes, so it must not be world readable, and a
// reopened store must see the same accounts.
func TestStorePersistsAndIsNotWorldReadable(t *testing.T) {
	path := filepath.Join(tempDir(t), "nested", "users.json")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create("persist.me", "Persist", "password-one", RoleManager); err != nil {
		t.Fatal(err)
	}

	again, err := OpenStore(path)
	if err != nil {
		t.Fatalf("reopening the store: %v", err)
	}
	if again.Count() != 1 {
		t.Fatalf("reopened store has %d accounts, want 1", again.Count())
	}
	if _, err := again.Authenticate("persist.me", "password-one"); err != nil {
		t.Errorf("could not sign in after reopening: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Meaningful on Unix; Windows reports 0666 regardless, so only assert
	// where the bits are real.
	if mode := info.Mode().Perm(); mode&0o077 != 0 && os.PathSeparator == '/' {
		t.Errorf("account file mode is %o, want no group or world access", mode)
	}
}

func TestSetPasswordChangesTheCredential(t *testing.T) {
	s := newStore(t)
	if _, err := s.Create("rotate.me", "Rotate", "old-password", RoleViewer); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPassword("rotate.me", "new-password"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate("rotate.me", "old-password"); !errors.Is(err, ErrBadCredential) {
		t.Error("the old password still worked")
	}
	if _, err := s.Authenticate("rotate.me", "new-password"); err != nil {
		t.Errorf("the new password did not work: %v", err)
	}
}

func TestThrottleLocksOutAfterRepeatedFailures(t *testing.T) {
	th := NewThrottle(3, time.Minute, 50*time.Millisecond)
	const key = "user:someone"

	for i := 0; i < 3; i++ {
		if blocked, _ := th.Blocked(key); blocked {
			t.Fatalf("locked out early, after %d failures", i)
		}
		th.Fail(key)
	}
	blocked, wait := th.Blocked(key)
	if !blocked {
		t.Fatal("not locked out after reaching the limit")
	}
	if wait <= 0 {
		t.Error("lockout reported no remaining time")
	}

	time.Sleep(70 * time.Millisecond)
	if blocked, _ := th.Blocked(key); blocked {
		t.Error("still locked out after the lockout expired")
	}

	// A success clears the record.
	th.Fail(key)
	th.Succeed(key)
	if blocked, _ := th.Blocked(key); blocked {
		t.Error("still throttled after a successful sign-in")
	}
}
