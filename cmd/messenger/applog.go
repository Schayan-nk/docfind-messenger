package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/Schayan-nk/docfind-messenger/internal/find"
	"github.com/Schayan-nk/docfind-messenger/internal/obs"
)

// The application log, opened when docfind starts as a server.
//
// NOT FOR THE ONE-SHOT COMMANDS. -add-user, -check-db and the rest are run by
// a person at a console, who is reading what they print; capturing their
// output into a file would take it away from the one reader it has.

// stopAppLog flushes and closes the log. A no-op until startAppLog runs, so the
// exit paths can call it unconditionally.
var stopAppLog = func() {}

// logDir decides where the log lives.
//
// Beside the accounts file by default -- data\logs beside users.json,
// next to everything else the server writes -- because that directory is
// already backed up, already permissioned for the service account, and is the
// first place anybody looks. DOCFIND_LOG_DIR overrides it.
func logDir(usersFile string) string {
	if d := strings.TrimSpace(os.Getenv("DOCFIND_LOG_DIR")); d != "" {
		return d
	}
	if usersFile != "" {
		return filepath.Join(filepath.Dir(usersFile), "logs")
	}
	return filepath.Join(cacheDir(), "logs")
}

// envInt reads a positive integer setting, or returns def.
func envInt(name string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err != nil || n <= 0 {
		return def
	}
	return n
}

// startAppLog opens the log, routes stdout, stderr and fatal crashes into it,
// and writes the start-up line.
//
// A failure to open the log is reported and survived. Every release before
// this one ran without it; refusing to start because of one would be a worse
// outage than the missing log ever was.
func startAppLog(usersFile string) {
	dir := logDir(usersFile)
	// A folder per area, a file per day, deleted after a month
	// (DOCFIND_LOG_KEEP_DAYS). See internal/obs/daily.go.
	l, err := obs.Open(obs.Options{
		Dir:         dir,
		KeepDays:    envInt("DOCFIND_LOG_KEEP_DAYS", 30),
		MaxDayBytes: int64(envInt("DOCFIND_LOG_MAX_DAY_MB", 200)) << 20,
	})
	if err != nil {
		logf("THE APPLICATION LOG COULD NOT BE OPENED in %s: %v", dir, err)
		logf("  continuing without it -- requests and errors will not be recorded")
		return
	}

	restore := obs.CaptureStdio(l)
	crash, cerr := l.CaptureCrashes(func(f *os.File) error {
		return debug.SetCrashOutput(f, debug.CrashOptions{})
	})
	if cerr != nil {
		l.Warn("crash output could not be redirected", obs.Fields{"error": cerr.Error()})
	}

	sum := binarySHA256()
	find.SetBinaryHash(sum)
	exe, _ := os.Executable()
	l.Write("start", "info", obs.Fields{
		"version": version,
		"sha256":  sum,
		"pid":     os.Getpid(),
		"go":      runtime.Version(),
		"exe":     exe,
		"log_dir": dir,
	})

	stopAppLog = func() {
		l.Write("stop", "info", obs.Fields{"version": version, "pid": os.Getpid()})
		restore()
		if crash != nil {
			crash.Close()
		}
		l.Close()
	}
}

// binarySHA256 hashes the running executable.
//
// The same figure the install script checks before swapping, recorded at every
// start so the log says which build produced each line -- and so the Security
// Center can show that the binary running is the binary that was tested.
// "" when the file cannot be read, which says so rather than guessing.
func binarySHA256() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	f, err := os.Open(exe)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return strings.ToUpper(hex.EncodeToString(h.Sum(nil)))
}
