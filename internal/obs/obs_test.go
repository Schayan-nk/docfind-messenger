package obs

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// memLogger is a Logger writing into a buffer, and the lines it wrote.
func memLogger() (*Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return &Logger{out: &syncBuffer{b: &buf}}, &buf
}

type syncBuffer struct {
	mu sync.Mutex
	b  *bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func lines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	sc := bufio.NewScanner(bytes.NewReader(buf.Bytes()))
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("a log line is not JSON: %q: %v", sc.Text(), err)
		}
		out = append(out, m)
	}
	return out
}

// ---------------------------------------------------------------- the file

// A folder per area, a file per day: what was asked for.
func TestEachAreaGetsItsOwnFolderAndEachDayItsOwnFile(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	day := time.Now().Format("2006-01-02")
	l.Write("request", "info", Fields{"route": "GET /x"})
	l.Write("audit", "info", Fields{"actor": "boss"})
	l.Write("push", "error", Fields{"msg": "Apple refused"})
	l.Write("calendar", "info", Fields{"msg": "changed"})
	l.Close()

	for section, want := range map[string]string{
		"requests": "GET /x", "audit": "boss", "notifications": "Apple refused",
		"calendar": "changed",
		// Every error also lands in errors\, whatever area it came from.
		"errors": "Apple refused",
	} {
		b, err := os.ReadFile(filepath.Join(dir, section, day+".jsonl"))
		if err != nil {
			t.Errorf("%s\\%s.jsonl: %v", section, day, err)
			continue
		}
		if !strings.Contains(string(b), want) {
			t.Errorf("%s does not hold %q: %s", section, want, b)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "errors", day+".jsonl")); strings.Contains(string(b), "GET /x") {
		t.Error("an info line reached errors\\")
	}
}

// A new day starts a new file; files older than the limit are deleted.
func TestDaysRollOverAndOldOnesAreDeleted(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "requests"), 0o750)
	old := filepath.Join(dir, "requests", "2026-08-01.jsonl")
	recent := filepath.Join(dir, "requests", "2026-09-10.jsonl")
	notOurs := filepath.Join(dir, "requests", "notes.txt")
	clock := time.Date(2026, 9, 23, 23, 59, 0, 0, time.Local)
	d, err := newDailyLog(dir, 30, 0)
	if err != nil {
		t.Fatal(err)
	}
	d.now = func() time.Time { return clock }
	// The files come after the log is opened: opening prunes by the real
	// date, which deleted "recent" once it was a month old (from 10-10).
	for _, p := range []string{old, recent, notOurs} {
		os.WriteFile(p, []byte("x\n"), 0o640)
	}
	d.pruneLocked("2026-09-23")
	d.write("requests", []byte("late\n"))
	clock = clock.Add(2 * time.Minute) // past midnight
	d.write("requests", []byte("early\n"))
	d.close()

	if _, err := os.Stat(old); err == nil {
		t.Error("a file older than a month was kept")
	}
	for _, p := range []string{recent, notOurs} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s was deleted: %v", p, err)
		}
	}
	for day, want := range map[string]string{"2026-09-23": "late", "2026-09-24": "early"} {
		b, _ := os.ReadFile(filepath.Join(dir, "requests", day+".jsonl"))
		if strings.TrimSpace(string(b)) != want {
			t.Errorf("%s holds %q, want %q", day, b, want)
		}
	}
}

// One noisy day continues in a new part rather than growing without end, and
// a line is never split across two.
func TestABusyDayContinuesInANewPart(t *testing.T) {
	dir := t.TempDir()
	d, err := newDailyLog(dir, 30, 200)
	if err != nil {
		t.Fatal(err)
	}
	line := []byte(strings.Repeat("x", 59) + "\n") // 60 bytes
	for range 10 {
		d.write("requests", line)
	}
	d.close()
	day := time.Now().Format("2006-01-02")
	for _, name := range []string{day + ".jsonl", day + ".2.jsonl", day + ".3.jsonl", day + ".4.jsonl"} {
		st, err := os.Stat(filepath.Join(dir, "requests", name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if st.Size() > 200 || st.Size()%60 != 0 {
			t.Errorf("%s is %d bytes", name, st.Size())
		}
	}
}

func TestReopeningTheLogAppendsRatherThanTruncating(t *testing.T) {
	dir := t.TempDir()
	for i := range 2 {
		d, err := newDailyLog(dir, 30, 0)
		if err != nil {
			t.Fatal(err)
		}
		d.write("app", []byte(fmt.Sprintf("start %d\n", i)))
		d.close()
	}
	b, _ := os.ReadFile(filepath.Join(dir, "app", time.Now().Format("2006-01-02")+".jsonl"))
	if string(b) != "start 0\nstart 1\n" {
		t.Errorf("a restart lost the previous run's log: %q", b)
	}
}

// ---------------------------------------------------------------- the lines

func TestEveryLineIsJSONWithItsTimeKindAndLevel(t *testing.T) {
	l, buf := memLogger()
	l.Write("request", "info", Fields{"route": "GET /x", "status": 200})
	l.Info("hello", nil)
	got := lines(t, buf)
	if len(got) != 2 {
		t.Fatalf("wrote %d lines, want 2", len(got))
	}
	for _, m := range got {
		for _, k := range []string{"ts", "kind", "level"} {
			if _, ok := m[k]; !ok {
				t.Errorf("a line has no %q: %v", k, m)
			}
		}
		if _, err := time.Parse(time.RFC3339Nano, m["ts"].(string)); err != nil {
			t.Errorf("ts is not RFC 3339: %v", m["ts"])
		}
	}
	if got[1]["kind"] != "app" || got[1]["msg"] != "hello" {
		t.Errorf("Info wrote %v", got[1])
	}
}

// A caller cannot forge when a line was written or what kind it is. The
// audit trail and the Security Center both filter on these.
func TestAFieldCannotOverwriteTheTimeOrTheKind(t *testing.T) {
	l, buf := memLogger()
	l.Write("request", "info", Fields{"ts": "1999-01-01", "kind": "audit", "level": "debug"})
	m := lines(t, buf)[0]
	if m["kind"] != "request" || m["level"] != "info" || m["ts"] == "1999-01-01" {
		t.Errorf("a field overwrote the line's own keys: %v", m)
	}
}

// ---------------------------------------------------------------- redaction

func TestCredentialShapedTextIsRedacted(t *testing.T) {
	for _, c := range []struct{ in, mustNot string }{
		{"calling with sk-proj-AbCdEfGhIjKlMnOpQrStUvWx123", "AbCdEfGhIjKlMnOpQrStUvWx123"},
		{"key sk-AbCdEfGhIjKlMnOpQrStUv", "AbCdEfGhIjKlMnOpQrStUv"},
		{"login failed password=hunter2hunter", "hunter2hunter"},
		{`{"api_key": "abc123secretvalue"}`, "abc123secretvalue"},
		{"Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.payload", "eyJhbGciOiJIUzI1NiJ9"},
		{"dsn Report:Passw0rd99@tcp(192.168.77.4:3306)/appdb", "Passw0rd99"},
		{"postgres://docfind:s3cretpw@127.0.0.1:5432/docfind", "s3cretpw"},
	} {
		got := Redact(c.in)
		if strings.Contains(got, c.mustNot) {
			t.Errorf("Redact(%q) = %q, still contains %q", c.in, got, c.mustNot)
		}
	}
}

// The net must not eat ordinary log text.
func TestRedactionLeavesOrdinaryTextAlone(t *testing.T) {
	for _, s := range []string{
		"report guarantees failed: context deadline exceeded",
		"imported the named-user allowlists into permissions for 3 account(s)",
		"token_version bumped for r.ahmadi",
		"mail from p.sadeghi@example.org classified",
		"listening on 0.0.0.0:8300",
	} {
		if got := Redact(s); got != s {
			t.Errorf("Redact changed ordinary text:\n  in  %q\n  out %q", s, got)
		}
	}
}

// ---------------------------------------------------------------- stdout lines

func TestPartialWritesAreJoinedIntoWholeLines(t *testing.T) {
	var got []string
	lw := &lineWriter{emit: func(s string) { got = append(got, s) }}
	lw.Write([]byte("report guar"))
	lw.Write([]byte("antees failed\r\nsecond line\nthi"))
	lw.Write([]byte("rd"))
	lw.Flush()
	want := []string{"report guarantees failed", "second line", "third"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestALineWithoutANewlineCannotGrowForever(t *testing.T) {
	n := 0
	lw := &lineWriter{emit: func(string) { n++ }}
	chunk := bytes.Repeat([]byte("x"), 16<<10)
	for range 10 {
		lw.Write(chunk)
	}
	if n == 0 {
		t.Error("160 KB without a newline was held entirely in memory")
	}
	if lw.buf.Len() > maxLine {
		t.Errorf("the pending buffer is %d bytes, past its %d limit", lw.buf.Len(), maxLine)
	}
}

// What the code prints is what the log records -- including code nobody
// changed. This is the one test that swaps the real os.Stdout, so it must not
// run in parallel with anything.
func TestPrintedOutputReachesTheLog(t *testing.T) {
	l, buf := memLogger()
	restore := CaptureStdio(l)
	fmt.Println("report tenders_open failed: timeout")
	fmt.Fprintln(os.Stderr, "something on stderr")
	restore()

	var sawOut, sawErr bool
	for _, m := range lines(t, buf) {
		if m["kind"] == "stdout" && m["msg"] == "report tenders_open failed: timeout" {
			sawOut = true
		}
		if m["kind"] == "stderr" && m["msg"] == "something on stderr" {
			sawErr = true
		}
	}
	if !sawOut || !sawErr {
		t.Errorf("stdout captured %v, stderr captured %v; log:\n%s", sawOut, sawErr, buf.String())
	}
}

// ---------------------------------------------------------------- requests

func TestEachRequestGetsAnIDAndOneAccessLine(t *testing.T) {
	l, buf := memLogger()
	h := Middleware(l, func(*http.Request) string { return "GET /api/docs/list" }, nil,
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("hello"))
		}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/docs/list?folder=secret-contracts&t=tok123", nil))

	id := rec.Header().Get(HeaderRequestID)
	if len(id) != 16 {
		t.Fatalf("X-Request-ID = %q, want 16 hex characters", id)
	}
	got := lines(t, buf)
	if len(got) != 1 {
		t.Fatalf("wrote %d lines for one request", len(got))
	}
	m := got[0]
	if m["kind"] != "request" || m["request_id"] != id || m["route"] != "GET /api/docs/list" ||
		m["status"].(float64) != 200 || m["bytes"].(float64) != 5 || m["method"] != "GET" {
		t.Errorf("access line = %v", m)
	}
	// The query string never reaches the log: it can carry a token, and a
	// folder name is business data.
	for _, secret := range []string{"secret-contracts", "tok123", "folder="} {
		if strings.Contains(buf.String(), secret) {
			t.Errorf("the log contains %q from the query string: %s", secret, buf.String())
		}
	}
}

// The path is never logged either -- a document's name is in it.
func TestThePathIsNeverLoggedOnlyThePattern(t *testing.T) {
	l, buf := memLogger()
	h := Middleware(l, func(*http.Request) string { return "GET /api/doc/{name}" }, nil,
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/doc/Contract-Nikan-Power.docx", nil))
	if strings.Contains(buf.String(), "Nikan") {
		t.Errorf("a document name reached the log: %s", buf.String())
	}
}

// Who made the request is set deep inside, on a COPY of the request -- which
// is what auth.WithUser does -- and must still reach the access line.
func TestTheUserSetOnACopiedRequestReachesTheLine(t *testing.T) {
	l, buf := memLogger()
	h := Middleware(l, nil, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r2 := r.WithContext(r.Context()) // a copy, as the auth layer makes
		SetUser(r2.Context(), "r.ahmadi")
		defer Track(r2.Context(), DB)()
		time.Sleep(2 * time.Millisecond)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/x", nil))
	m := lines(t, buf)[0]
	if m["user"] != "r.ahmadi" {
		t.Errorf("user = %v, want r.ahmadi", m["user"])
	}
	if n, _ := m["db_n"].(float64); n != 1 {
		t.Errorf("db_n = %v, want 1", m["db_n"])
	}
	if d, _ := m["db_ms"].(float64); d < 1 {
		t.Errorf("db_ms = %v, want the 2 ms spent", m["db_ms"])
	}
}

func TestAnUnmatchedRouteIsLabelledWithoutItsPath(t *testing.T) {
	l, buf := memLogger()
	h := Middleware(l, func(*http.Request) string { return "" }, nil,
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/wp-admin/../../etc/passwd", nil))
	m := lines(t, buf)[0]
	if m["route"] != "GET (no route)" || m["level"] != "warn" {
		t.Errorf("unmatched request logged as %v", m)
	}
	if strings.Contains(buf.String(), "passwd") {
		t.Error("a scanner's probe path reached the log")
	}
}

func TestSuccessfulAssetFetchesAreCountedButNotWritten(t *testing.T) {
	l, buf := memLogger()
	lat := NewLatency(8)
	route := "GET /app.css"
	h := Middleware(l, func(*http.Request) string { return route }, lat,
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/app.css", nil))
	if buf.Len() != 0 {
		t.Errorf("a successful stylesheet fetch was written: %s", buf.String())
	}
	if s := lat.Snapshot(); len(s) != 1 || s[0].Count != 1 {
		t.Errorf("it was not counted either: %v", s)
	}
	// A FAILED asset fetch is worth a line.
	h = Middleware(l, func(*http.Request) string { return route }, nil,
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/app.css", nil))
	if buf.Len() == 0 {
		t.Error("a failing stylesheet fetch was not written")
	}
}

// Streaming responses depend on Flush getting through.
func TestFlushReachesTheRealWriter(t *testing.T) {
	l, _ := memLogger()
	h := Middleware(l, nil, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("row\n"))
		if f, ok := w.(http.Flusher); !ok {
			t.Error("the wrapped writer is not a Flusher")
		} else {
			f.Flush()
		}
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/export", nil))
	if !rec.Flushed {
		t.Error("Flush did not reach the underlying writer")
	}
}

// ---------------------------------------------------------------- latency

func TestPercentilesAreNearestRankOverTheWindow(t *testing.T) {
	lat := NewLatency(100)
	for i := 1; i <= 100; i++ {
		lat.Observe("GET /x", 200, time.Duration(i)*time.Millisecond)
	}
	s := lat.Snapshot()[0]
	if s.P50 != 50 || s.P95 != 95 || s.P99 != 99 || s.Max != 100 || s.Samples != 100 {
		t.Errorf("got p50=%v p95=%v p99=%v max=%v n=%d", s.P50, s.P95, s.P99, s.Max, s.Samples)
	}
	// The window slides: 100 fast requests push the slow ones out.
	for range 100 {
		lat.Observe("GET /x", 200, time.Millisecond)
	}
	if s := lat.Snapshot()[0]; s.P99 != 1 || s.Count != 200 {
		t.Errorf("after the window moved: p99=%v count=%d", s.P99, s.Count)
	}
}

func TestErrorsAreCountedByClass(t *testing.T) {
	lat := NewLatency(10)
	lat.Observe("r", 200, time.Millisecond)
	lat.Observe("r", 404, time.Millisecond)
	lat.Observe("r", 403, time.Millisecond)
	lat.Observe("r", 502, time.Millisecond)
	s := lat.Snapshot()[0]
	if s.Err4xx != 2 || s.Err5xx != 1 || s.Count != 4 {
		t.Errorf("got 4xx=%d 5xx=%d count=%d", s.Err4xx, s.Err5xx, s.Count)
	}
}

func TestTheRouteSetCannotGrowWithoutLimit(t *testing.T) {
	lat := NewLatency(2)
	for i := range maxRoutes + 50 {
		lat.Observe(fmt.Sprintf("GET /r%d", i), 200, time.Millisecond)
	}
	if n := len(lat.Snapshot()); n > maxRoutes+1 {
		t.Errorf("%d routes kept, past the %d ceiling", n, maxRoutes)
	}
}

func TestNilHelpersAreSafe(t *testing.T) {
	var lat *Latency
	lat.Observe("x", 200, time.Second)
	if lat.Snapshot() != nil {
		t.Error("a nil Latency returned figures")
	}
	// A context with no request in it -- a background job calling the same
	// query function a handler does.
	bg := context.Background()
	Track(bg, DB)()
	SetUser(bg, "x")
	if RequestID(bg) != "" {
		t.Error("a context with no request has a request ID")
	}
	var rq *Request
	if rq.User() != "" {
		t.Error("a nil request has a user")
	}
}
