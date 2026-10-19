package find

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A handler panic must become a 500 that says nothing, not a dropped
// connection and not a leaked stack trace.

func TestPanicBecomesA500(t *testing.T) {
	h := recoverPanics(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom: C:\\Archive\\Documents\\secret.docx contains 'PRIVATE'")
	}))

	rec := httptest.NewRecorder()
	// If recovery fails this call panics and the test fails loudly, which is
	// the behaviour we want from the test either way.
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/search?q=x", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status %d, want 500", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type %q, want JSON", ct)
	}

	body := rec.Body.String()
	// The panic value carried a path and a document fragment. Neither may
	// appear in the response; a panic message routinely contains a file path,
	// a SQL fragment or a slice of document text.
	for _, leak := range []string{"boom", "secret.docx", "PRIVATE", "C:\\", "goroutine", ".go:"} {
		if strings.Contains(body, leak) {
			t.Errorf("response leaked %q: %s", leak, body)
		}
	}
	if !strings.Contains(body, "خطای داخلی سرور") {
		t.Errorf("expected a Persian error sentence, got %q", body)
	}
}

// ErrAbortHandler is the documented way a handler asks for its connection to
// be dropped. Converting it into a 500 would break that contract.
func TestAbortHandlerIsNotConvertedToA500(t *testing.T) {
	h := recoverPanics(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(http.ErrAbortHandler)
	}))

	var got any
	func() {
		defer func() { got = recover() }()
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	}()
	if got != http.ErrAbortHandler {
		t.Errorf("ErrAbortHandler was swallowed (recovered %v); it must pass through", got)
	}
}

// Once the handler has begun writing there is no status line left to replace,
// so recovery must not append to a body the client is already reading.
func TestPanicAfterWritingDoesNotCorruptTheBody(t *testing.T) {
	h := recoverPanics(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"results":[]}`))
		panic("late failure")
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/search", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("status %d; the 200 was already sent and must stand", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "خطای داخلی") {
		t.Errorf("the error sentence was appended to an already-written body: %s", rec.Body.String())
	}
}

// The whole point is that the process survives. A panicking route must not
// stop the next request from being served.
func TestServerKeepsServingAfterAPanic(t *testing.T) {
	var hits int
	h := recoverPanics(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path == "/boom" {
			panic("nil map write")
		}
		w.Write([]byte("ok"))
	}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/boom", nil))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/fine", nil))

	if rec.Body.String() != "ok" {
		t.Errorf("the request after a panic returned %q", rec.Body.String())
	}
	if hits != 2 {
		t.Errorf("handler ran %d times, want 2", hits)
	}
}

// The wrapper must not swallow Flush. /api/reports/export streams a CSV and
// depends on it; a wrapper that dropped it would turn a streaming download
// into one buffered entirely in memory, which is exactly the request this app
// least wants to buffer.
func TestRecoveryWrapperForwardsFlush(t *testing.T) {
	var flushed bool
	h := recoverPanics(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, ok := w.(http.Flusher)
		if !ok {
			t.Error("the handler can no longer see an http.Flusher")
			return
		}
		w.Write([]byte("row\n"))
		f.Flush()
		flushed = true
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/reports/export", nil))
	if !flushed {
		t.Error("Flush was not reachable through the recovery wrapper")
	}
}
