package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

// A temporary directory whose cleanup does not fail the test on Windows.
//
// t.TempDir() removes its directory when the test ends and FAILS the test if
// it cannot. On Windows a file written moments earlier can still be held
// briefly by something outside this process -- a scanner, the search indexer
// -- and RemoveAll then reports "The directory is not empty".
//
// Measured across eight full-suite runs before any of this changed: four
// failed that way, in four different tests in three packages, with every
// assertion in every one of them passing. The message is always
// "testing.go:1369: TempDir RemoveAll cleanup", never an assertion.
//
// Worth fixing rather than re-running, because a suite that fails half its
// runs for a reason unrelated to the code teaches everybody to re-run it and
// stop reading -- and then the failure that IS real gets the same shrug.
//
// Retried, and a residue is LOGGED rather than failed. Logged rather than
// ignored on purpose: a genuinely leaked file handle would keep the directory
// undeletable on every retry of every run, and that log line is what would
// say so. A scanner holding a file appears once and is gone on the retry.
func tempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "config-"+safeTestName(t.Name())+"-")
	if err != nil {
		t.Fatalf("temporary directory: %v", err)
	}
	t.Cleanup(func() {
		for i := 0; ; i++ {
			if err := os.RemoveAll(dir); err == nil {
				return
			} else if i == 3 {
				t.Logf("could not remove %s: %v -- harmless if this is the only "+
					"run it happens on; a file handle this package failed to "+
					"close would do it every time", dir, err)
				return
			}
			time.Sleep(80 * time.Millisecond)
		}
	})
	return dir
}

// safeTestName makes a test's name usable in a filename.
//
// A subtest's name carries its parent and its case, separated by a slash --
// and MkdirTemp refuses a pattern containing a separator. Learned the hard
// way: without this, a helper written to stop spurious failures caused eight
// real ones the first time it met a table-driven test.
func safeTestName(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}
