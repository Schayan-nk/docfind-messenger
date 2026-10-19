package obs

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

// The application log: one JSON object per line, in logs\<area>\<day>.jsonl
// beside the accounts file -- see daily.go for the layout and retention.
//
// Every line carries its request_id where it has one, so following one request
// across areas -- the access line in requests, the slow query in database,
// the error in errors -- is one search:
//
//	Select-String -Path data\logs\*\2026-09-23.jsonl <request id>
//
// JSON rather than text because the monitoring page and anyone with a shell
// can filter it without guessing at a format.

// Logger writes the application log. The zero value discards everything, so
// tests and the command-line tools need no set-up.
type Logger struct {
	mu    sync.Mutex
	out   io.Writer // every line to one writer: what tests use
	daily *dailyLog
	dir   string
}

// Options configures where and how much.
type Options struct {
	Dir         string // the logs folder; each area gets a subfolder in it
	KeepDays    int    // day files older than this are deleted; default 30
	MaxDayBytes int64  // one area's file for one day continues in a new part past this; default 200 MB
}

var (
	defaultMu sync.RWMutex
	defaultL  = &Logger{}
)

// Open starts the log file and makes it the default logger.
func Open(opt Options) (*Logger, error) {
	d, err := newDailyLog(opt.Dir, opt.KeepDays, opt.MaxDayBytes)
	if err != nil {
		return nil, err
	}
	l := &Logger{daily: d, dir: opt.Dir}
	defaultMu.Lock()
	defaultL = l
	defaultMu.Unlock()
	return l, nil
}

// Default is the logger Open installed, or one that discards.
func Default() *Logger {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultL
}

// Dir is the directory the log lives in, or "" when there is no file.
func (l *Logger) Dir() string {
	if l == nil {
		return ""
	}
	return l.dir
}

// Close flushes and closes the file.
func (l *Logger) Close() error {
	if l == nil || l.daily == nil {
		return nil
	}
	return l.daily.close()
}

// Fields is one log line's payload.
type Fields map[string]any

// Write records one line of the given kind and level.
//
// "ts", "kind" and "level" are always present and always first-class keys; a
// field of the same name in f is overwritten, so a caller cannot forge the
// time or the kind of a line.
func (l *Logger) Write(kind, level string, f Fields) {
	if l == nil || (l.out == nil && l.daily == nil) {
		return
	}
	line := make(map[string]any, len(f)+3)
	for k, v := range f {
		if s, ok := v.(string); ok {
			v = Redact(s)
		}
		line[k] = v
	}
	line["ts"] = time.Now().Format(time.RFC3339Nano)
	line["kind"] = kind
	line["level"] = level

	b, err := json.Marshal(line)
	if err != nil {
		b, _ = json.Marshal(map[string]any{
			"ts": time.Now().Format(time.RFC3339Nano), "kind": kind,
			"level": "error", "msg": "unencodable log line: " + err.Error(),
		})
	}
	b = append(b, '\n')
	if l.out != nil {
		l.mu.Lock()
		_, _ = l.out.Write(b)
		l.mu.Unlock()
	}
	if l.daily != nil {
		section := sectionOf(kind)
		l.daily.write(section, b)
		// Every error, whichever area it belongs to, also in errors\ -- the
		// one folder to open first when something is wrong.
		if level == "error" && section != "errors" {
			l.daily.write("errors", b)
		}
	}
}

// Info, Warn and Error write a free-text line of kind "app".
func (l *Logger) Info(msg string, f Fields) { l.msg("info", msg, f) }
func (l *Logger) Warn(msg string, f Fields) { l.msg("warn", msg, f) }

func (l *Logger) msg(level, msg string, f Fields) {
	if f == nil {
		f = Fields{}
	}
	f["msg"] = msg
	l.Write("app", level, f)
}

// ---------------------------------------------------------------- redaction

// secretPatterns catch a credential that reached a log line by mistake.
//
// A SAFETY NET, NOT A POLICY. Nothing should be logging these in the first
// place, and the tests check the places that could. But the stdout capture
// below records whatever any code printed, including code written before this
// log existed, and one leaked key in a file readable by the service account is
// a key that has to be revoked. So the obvious shapes are scrubbed on the way
// in, where it costs a few regex passes over a line.
var secretPatterns = []*regexp.Regexp{
	// OpenAI and Anthropic style keys.
	regexp.MustCompile(`\b(sk|sk-proj|sk-ant)-[A-Za-z0-9_\-]{16,}`),
	// key=value and key: value for the words that mean a secret.
	regexp.MustCompile(`(?i)\b(password|passwd|pwd|secret|token|api[_-]?key|authorization)(["']?\s*[=:]\s*["']?)([^\s"',;&]+)`),
	// Bearer tokens.
	regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._\-]{12,}`),
	// A DSN's password: user:password@host.
	regexp.MustCompile(`([A-Za-z0-9_.\-]+):([^:@/\s]{3,})@(tcp\(|[A-Za-z0-9.\-]+[:/])`),
}

// Redact replaces anything credential-shaped in s with a placeholder.
func Redact(s string) string {
	if s == "" {
		return s
	}
	// ORDER MATTERS. Bearer runs before key=value: in
	// "Authorization: Bearer <token>" the key=value pattern would otherwise
	// match first, redact the word "Bearer" as if it were the value, and
	// leave the token itself in the line -- which a test caught.
	s = secretPatterns[0].ReplaceAllString(s, "[redacted-key]")
	s = secretPatterns[2].ReplaceAllString(s, "Bearer [redacted]")
	s = secretPatterns[1].ReplaceAllString(s, "${1}${2}[redacted]")
	s = secretPatterns[3].ReplaceAllString(s, "${1}:[redacted]@${3}")
	return s
}

// ------------------------------------------------------------ crash output

// CaptureCrashes sends the Go runtime's own fatal-error output -- the stack
// dump of a crash that no recover() can catch -- to crash.log in the log
// directory, as well as wherever it was going.
//
// Separate from docfind.jsonl because the runtime writes it raw, at the moment
// the process is dying, and it is not JSON. Returns the file so the caller can
// keep it open for the life of the process.
func (l *Logger) CaptureCrashes(setCrashOutput func(*os.File) error) (*os.File, error) {
	if l == nil || l.dir == "" {
		return nil, nil
	}
	f, err := os.OpenFile(filepath.Join(l.dir, "crash.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return nil, err
	}
	if err := setCrashOutput(f); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
