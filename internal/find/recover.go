package find

import (
	"fmt"
	"net/http"
	"runtime"
)

// recoverPanics turns a handler panic into a 500 instead of a dropped
// connection.
//
// Without it a single request that reaches a nil dereference kills its
// connection with no status line and no log entry, which makes it both a
// reliable per-request denial of service and invisible afterwards: there is
// nothing recording who sent it. That combination -- cheap to trigger, free of
// evidence -- is why this sits in the chain rather than being treated as a
// nicety.
//
// The stack goes to the server log only. The client is told nothing beyond a
// Persian sentence: a panic message routinely carries a file path, a SQL
// fragment or a slice of document text, and none of that belongs in a
// response body.
func recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Whether anything reached the client is tracked here rather than
		// asked of the writer underneath. Asking was the first attempt and it
		// was quietly wrong: httptest.ResponseRecorder answers no such
		// question, so the check fell through and the error sentence was
		// appended to a body the client was already reading.
		tracked := &panicTracker{ResponseWriter: w}
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			// http.ErrAbortHandler is the documented way a handler says "drop
			// this connection deliberately". Honour it rather than converting
			// it into a 500.
			if rec == http.ErrAbortHandler {
				panic(rec)
			}

			buf := make([]byte, 8<<10)
			buf = buf[:runtime.Stack(buf, false)]
			// Method and path, never the query string: a token can still ride
			// in ?t= on the desktop build.
			fmt.Printf("panic serving %s %s: %v\n%s\n", r.Method, r.URL.Path, rec, buf)

			if tracked.wrote {
				// The status line is long gone. Appending would corrupt the
				// body; dropping the connection is the only honest option.
				return
			}
			// JSON for the API, the 500 page for a browser that was
			// navigating. A bare JSON object in the address bar is the right
			// answer for a fetch() and a poor one for a person.
			writeServerError(tracked, r)
		}()
		next.ServeHTTP(tracked, r)
	})
}

// panicTracker notes whether the handler got as far as sending anything.
//
// It deliberately forwards Flush: /api/reports/export streams a CSV and
// depends on it, and a wrapper that swallowed Flush would turn a streaming
// download into one buffered in memory. Unwrap keeps
// http.NewResponseController working for anything else it needs.
type panicTracker struct {
	http.ResponseWriter
	wrote bool
}

func (t *panicTracker) WriteHeader(status int) {
	t.wrote = true
	t.ResponseWriter.WriteHeader(status)
}

func (t *panicTracker) Write(p []byte) (int, error) {
	t.wrote = true
	return t.ResponseWriter.Write(p)
}

func (t *panicTracker) Flush() {
	if f, ok := t.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (t *panicTracker) Unwrap() http.ResponseWriter { return t.ResponseWriter }
