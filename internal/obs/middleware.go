package obs

import (
	"net"
	"net/http"
	"strings"
	"time"
)

// RouteOf names the route a request will be served by, as its registered
// pattern ("GET /api/mail/message/{uid}"), or "" when nothing matches.
//
// A function rather than the mux itself so this package does not care what
// the router is, and so tests can pass a fixed answer.
type RouteOf func(*http.Request) string

// Recorder receives one finished request. The latency figures (metrics.go)
// implement it; so can a test.
type Recorder interface {
	Observe(route string, status int, d time.Duration)
}

// HeaderRequestID is the response header carrying the request's ID.
//
// Sent on every response so the ID can be quoted back. An error page prints
// it, a person reads it out, and the one line in the log that explains the
// failure is found by searching for it.
const HeaderRequestID = "X-Request-ID"

// Middleware starts the record for each request, and writes one access line
// when the response is finished.
//
// WHAT IS NEVER LOGGED, and why each one:
//   - the PATH: it can name a document, and a document's name is business
//     data. The route pattern identifies the endpoint without it.
//   - the QUERY STRING: the desktop build can carry its token in ?t=.
//   - request and response BODIES: every one of them is somebody's data.
//
// Successful requests for static assets are counted but not written: a page
// load fetches a dozen icons and scripts, and writing a line for each would
// bury the requests anybody ever needs to look for.
//
// A nil logger means "whichever is the default when the request arrives", so
// the handler can be built before the log file is opened without losing the
// requests that come after.
func Middleware(l *Logger, route RouteOf, rec Recorder, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l := l
		if l == nil {
			l = Default()
		}
		rq := NewRequest()
		w.Header().Set(HeaderRequestID, rq.ID)
		sw := &statusWriter{ResponseWriter: w}
		r = r.WithContext(With(r.Context(), rq))

		pattern := ""
		if route != nil {
			pattern = route(r)
		}

		defer func() {
			d := time.Since(rq.Start)
			status := sw.status
			if status == 0 {
				// Nothing was written at all: net/http sends 200 for a handler
				// that returns without writing.
				status = http.StatusOK
			}
			label := pattern
			if label == "" {
				// An unmatched request, labelled by method alone. Its path is
				// exactly the thing a scanner varies, so recording it would
				// make every probe a new label and a new line of noise.
				label = r.Method + " (no route)"
			}
			if rec != nil {
				rec.Observe(label, status, d)
			}
			if isQuietAsset(pattern, status) {
				return
			}
			f := Fields{
				"request_id": rq.ID,
				"method":     r.Method,
				"route":      label,
				"status":     status,
				"dur_ms":     ms(d),
				"bytes":      sw.bytes,
				"remote":     remoteHost(r),
			}
			if u := rq.User(); u != "" {
				f["user"] = u
			}
			for k := range numKinds {
				if spent, n := rq.Spent(k); n > 0 {
					f[k.String()+"_ms"] = ms(spent)
					f[k.String()+"_n"] = n
				}
			}
			level := "info"
			switch {
			case status >= 500:
				level = "error"
			case status >= 400:
				level = "warn"
			}
			l.Write("request", level, f)
		}()
		next.ServeHTTP(sw, r)
	})
}

func ms(d time.Duration) float64 {
	return float64(d.Microseconds()) / 1000
}

// isQuietAsset reports a successful static-asset fetch.
func isQuietAsset(pattern string, status int) bool {
	if status >= 400 {
		return false
	}
	p := strings.TrimPrefix(pattern, "GET ")
	switch {
	case strings.HasPrefix(p, "/vendor/"), strings.HasPrefix(p, "/map/"),
		strings.HasPrefix(p, "/static/"), strings.HasPrefix(p, "/icons/"):
		return true
	}
	switch p {
	case "/app.css", "/nav.js", "/chat.js", "/upload.js", "/push.js", "/sw.js",
		"/favicon.ico", "/icon.png", "/icon-192.png", "/icon-512.png",
		"/icon-maskable.png", "/apple-touch-icon.png", "/manifest.webmanifest",
		"/assistant.jpg":
		return true
	}
	return false
}

// remoteHost is the client address without its port.
//
// r.RemoteAddr, not X-Forwarded-For: docfind is reached directly on the LAN,
// and a forwarded header is whatever the client chose to send. If a reverse
// proxy is put in front, this will name the proxy -- which is the honest
// answer until the proxy is trusted by configuration.
func remoteHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// statusWriter notes the status and the number of body bytes sent.
//
// FLUSH IS FORWARDED: the report export streams a CSV and depends on it, and a
// wrapper that swallowed Flush would turn a streamed download into one held in
// memory. Unwrap keeps http.NewResponseController working for the rest.
type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(p)
	w.bytes += int64(n)
	return n, err
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
