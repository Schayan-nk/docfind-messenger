package config

import (
	"os"
	"path/filepath"
	"testing"
)

// Which side wins, and that the choice is deliberate.
//
// The default -- environment beats file -- is what lets an operator correct a
// deployed box without editing a file that may be under version control. The
// override exists because that rule has one bad case: a stale variable in the
// Windows machine environment cannot be removed for a running scheduled task,
// since the Task Scheduler service caches its copy of the environment block
// until the machine restarts. A corrected .env was being silently ignored and
// the only way out was a reboot.
func TestWhichSideWins(t *testing.T) {
	write := func(t *testing.T) string {
		t.Helper()
		p := filepath.Join(t.TempDir(), ".env")
		body := "DOCFIND_TEST_SET=from-file\nDOCFIND_TEST_UNSET=from-file\n"
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}

	t.Run("by default the environment wins", func(t *testing.T) {
		p := write(t)
		t.Setenv("DOCFIND_TEST_SET", "from-environment")
		os.Unsetenv("DOCFIND_TEST_UNSET")
		if err := LoadDotEnv(p); err != nil {
			t.Fatal(err)
		}
		if got := os.Getenv("DOCFIND_TEST_SET"); got != "from-environment" {
			t.Errorf("a variable already set became %q; the file must not overwrite it, "+
				"or an operator loses the way to correct a box without editing a "+
				"checked-in file", got)
		}
		if got := os.Getenv("DOCFIND_TEST_UNSET"); got != "from-file" {
			t.Errorf("an unset variable became %q, want the file's value", got)
		}
		os.Unsetenv("DOCFIND_TEST_UNSET")
	})

	t.Run("ForceDotEnv makes the file win", func(t *testing.T) {
		p := write(t)
		t.Setenv("DOCFIND_TEST_SET", "from-environment")
		os.Unsetenv("DOCFIND_TEST_UNSET")
		if err := ForceDotEnv(p); err != nil {
			t.Fatal(err)
		}
		if got := os.Getenv("DOCFIND_TEST_SET"); got != "from-file" {
			t.Errorf("the forced load left %q in place; the whole point is to beat a "+
				"stale machine variable that cannot be deleted without a restart", got)
		}
		if got := os.Getenv("DOCFIND_TEST_UNSET"); got != "from-file" {
			t.Errorf("an unset variable became %q, want the file's value", got)
		}
		os.Unsetenv("DOCFIND_TEST_UNSET")
	})

	// A missing file must not wipe the environment under either rule: the
	// settings may legitimately live only in the machine environment, which is
	// how this was deployed before the file existed.
	t.Run("a missing file changes nothing", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "nope.env")
		t.Setenv("DOCFIND_TEST_SET", "from-environment")
		if err := ForceDotEnv(missing); err == nil {
			t.Error("a missing file returned no error; the caller cannot tell it read nothing")
		}
		if got := os.Getenv("DOCFIND_TEST_SET"); got != "from-environment" {
			t.Errorf("a missing file changed the environment to %q", got)
		}
	})
}
