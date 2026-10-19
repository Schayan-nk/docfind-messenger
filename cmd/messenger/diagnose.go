package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// logPath is where the start-up transcript goes.
//
// The windowed build cannot print anything, so without this a failure leaves no
// trace at all -- which is exactly how the first network-share attempt failed.
func logPath() string { return filepath.Join(cacheDir(), "docfind.log") }

// logf writes one line to the log file and, when there is one, the console.
func logf(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	fmt.Println(line)

	if err := os.MkdirAll(filepath.Dir(logPath()), 0o750); err != nil {
		return
	}
	f, err := os.OpenFile(logPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s  %s\n", time.Now().Format("2006-01-02 15:04:05"), line)
}
