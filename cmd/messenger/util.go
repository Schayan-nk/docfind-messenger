package main

import (
	"os"
	"path/filepath"
)

// cleanups is what to undo on the way out, last added first.
type cleanups []func()

func (c *cleanups) add(fn func()) {
	if fn != nil {
		*c = append(*c, fn)
	}
}

func (c cleanups) run() {
	for i := len(c) - 1; i >= 0; i-- {
		c[i]()
	}
}

// cacheDir returns somewhere writable for the log, preferring the user's
// local cache directory.
func cacheDir() string {
	if d, err := os.UserCacheDir(); err == nil {
		return filepath.Join(d, "docfind-messenger")
	}
	return "."
}

// dataFilePath puts a state file next to the accounts file, which is where
// this application keeps the state it must not lose.
func dataFilePath(usersFile, name string) string {
	if usersFile == "" {
		return name
	}
	return filepath.Join(filepath.Dir(usersFile), name)
}
