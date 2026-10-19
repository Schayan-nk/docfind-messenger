package obs

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

// The log on disk: a folder per area, a file per day.
//
//	logs\requests\2026-09-23.jsonl
//	logs\audit\2026-09-23.jsonl
//	logs\notifications\2026-09-23.jsonl
//	logs\errors\2026-09-23.jsonl      every error-level line, from any area
//	...
//
// AS ASKED FOR: "write logs for everything separately, a folder per section,
// and a file per day inside them". It also answers the questions people
// actually ask of a log -- "what happened to the notifications yesterday",
// "show me every error today" -- by opening one file instead of filtering one
// large one.
//
// DELETED AFTER keepDays (a month by default), automatically: checked at
// start-up and each time the date changes. A day's file that passes
// maxDayBytes continues in a numbered part (2026-09-23.2.jsonl), so one noisy
// day cannot fill the drive the archive and the database also live on.

// sectionOf names the folder a kind of line goes in.
func sectionOf(kind string) string {
	switch kind {
	case "request":
		return "requests"
	case "audit":
		return "audit"
	case "push":
		return "notifications"
	case "slow_query", "database":
		return "database"
	case "start", "stop", "app":
		return "app"
	case "stdout", "stderr":
		return "console"
	case "job":
		return "jobs"
	}
	if safeSection.MatchString(kind) {
		return kind // calendar, mail, monitoring, security, backup, ...
	}
	return "app"
}

var safeSection = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,30}$`)

// dayName matches a log file and captures its date.
var dayName = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})(\.\d+)?\.jsonl$`)

type dailyLog struct {
	mu          sync.Mutex
	dir         string
	keepDays    int
	maxDayBytes int64
	now         func() time.Time

	files  map[string]*dayFile // by section
	pruned string              // the day retention last ran for
}

type dayFile struct {
	day  string
	part int
	f    *os.File
	size int64
}

func newDailyLog(dir string, keepDays int, maxDayBytes int64) (*dailyLog, error) {
	if keepDays <= 0 {
		keepDays = 30
	}
	if maxDayBytes <= 0 {
		maxDayBytes = 200 << 20
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	d := &dailyLog{dir: dir, keepDays: keepDays, maxDayBytes: maxDayBytes,
		now: time.Now, files: map[string]*dayFile{}}
	d.mu.Lock()
	d.pruneLocked(d.now().Format("2006-01-02"))
	d.mu.Unlock()
	return d, nil
}

// write appends one line to today's file for section. A line is never split
// across two files.
func (d *dailyLog) write(section string, line []byte) {
	d.mu.Lock()
	defer d.mu.Unlock()
	day := d.now().Format("2006-01-02")
	if d.pruned != day {
		d.pruneLocked(day)
	}
	df := d.files[section]
	if df == nil || df.day != day || (df.size > 0 && df.size+int64(len(line)) > d.maxDayBytes) {
		next := &dayFile{day: day, part: 1}
		if df != nil {
			df.f.Close()
			if df.day == day {
				next.part = df.part + 1
			}
		}
		if err := d.openLocked(section, next); err != nil {
			delete(d.files, section)
			return // a log that cannot write must not stop what it is logging
		}
		d.files[section] = next
		df = next
	}
	n, _ := df.f.Write(line)
	df.size += int64(n)
}

func (d *dailyLog) openLocked(section string, df *dayFile) error {
	dir := filepath.Join(d.dir, section)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	name := df.day + ".jsonl"
	if df.part > 1 {
		name = fmt.Sprintf("%s.%d.jsonl", df.day, df.part)
	}
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	df.f, df.size = f, st.Size()
	return nil
}

// pruneLocked deletes every day file older than keepDays, in every section.
// Only files this log wrote are touched: the name has to be a date.
func (d *dailyLog) pruneLocked(today string) {
	d.pruned = today
	t, err := time.ParseInLocation("2006-01-02", today, time.Local)
	if err != nil {
		return
	}
	cutoff := t.AddDate(0, 0, -d.keepDays).Format("2006-01-02")
	sections, err := os.ReadDir(d.dir)
	if err != nil {
		return
	}
	for _, s := range sections {
		if !s.IsDir() {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(d.dir, s.Name()))
		if err != nil {
			continue
		}
		for _, e := range entries {
			m := dayName.FindStringSubmatch(e.Name())
			if m == nil || m[1] >= cutoff {
				continue
			}
			_ = os.Remove(filepath.Join(d.dir, s.Name(), e.Name()))
		}
	}
}

func (d *dailyLog) close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	var first error
	for s, df := range d.files {
		if err := df.f.Close(); err != nil && first == nil {
			first = err
		}
		delete(d.files, s)
	}
	return first
}
