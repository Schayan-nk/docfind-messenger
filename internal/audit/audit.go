// Package audit records who did what to what, and whether it worked.
//
// WHAT IT IS FOR. The request log answers "what happened to this request";
// the audit log answers "who changed this, and when". Different questions,
// different readers: the audit trail is what an administrator reads after
// "somebody deleted the contract folder" or "why can z.moradi see the
// guarantees report", and it has to be complete, attributable, and beyond the
// reach of the people it records.
//
// COMPLETE BY CONSTRUCTION. Every state-changing request -- every POST, PUT,
// PATCH and DELETE, and the GETs that export or download -- is recorded by
// Middleware, whether or not its handler knows this package exists. A handler
// adds only what it alone knows: the target ("the document it renamed", "the
// account it disabled") through Note. The failure this prevents is the one a
// hand-maintained list always ends in: the route added next month that nobody
// remembered to audit.
//
// NEVER RECORDED: passwords, keys, tokens, session identifiers, mail
// passwords, request bodies, AI prompts. A target names an object ("user:
// sadeghi", "doc:Contracts/x.docx"), never its contents.
//
// TWO COPIES. Every event is written to the application log as kind "audit"
// AND, when it is up, to the audit_events table. The log line is the copy that
// survives the database being down; the table is the one that can be searched
// and cannot be edited by the application.
package audit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"maps"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Schayan-nk/docfind-messenger/internal/obs"
)

// Event is one audited action.
type Event struct {
	// ID is this event's own, given when it is recorded: the table ignores an
	// id it already holds, so an event replayed from the spool (spool.go)
	// after a send whose answer was lost is still one row.
	ID        string         `json:"id,omitempty"`
	At        time.Time      `json:"at"`
	Actor     string         `json:"actor"`            // username; "" when nobody was signed in
	Action    string         `json:"action"`           // "POST /api/docs/rename", or a named action such as "login"
	Target    string         `json:"target,omitempty"` // what it acted on
	Result    string         `json:"result"`           // ok | denied | rejected | error
	Status    int            `json:"status,omitempty"`
	IP        string         `json:"ip,omitempty"`
	UserAgent string         `json:"user_agent,omitempty"`
	RequestID string         `json:"request_id,omitempty"`
	Detail    map[string]any `json:"detail,omitempty"`
}

// Sink stores events. The PostgreSQL store implements it.
type Sink interface {
	Record(ctx context.Context, e Event) error
}

// Recorder writes each event to the log and, when configured, to a sink.
type Recorder struct {
	mu    sync.RWMutex
	sink  Sink
	spool *Spool
}

// UseSpool sets where events the sink refuses wait for it (spool.go). Nil
// means they are in the log only.
func (r *Recorder) UseSpool(sp *Spool) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.spool = sp
	r.mu.Unlock()
}

// ReplaySpool sends the spooled events to the sink now.
func (r *Recorder) ReplaySpool(ctx context.Context) (sent, kept int, err error) {
	if r == nil {
		return 0, 0, nil
	}
	r.mu.RLock()
	sink, sp := r.sink, r.spool
	r.mu.RUnlock()
	if sink == nil || !sp.Pending() {
		return 0, 0, nil
	}
	sent, kept, err = sp.Replay(ctx, sink)
	if sent > 0 || kept > 0 {
		f := obs.Fields{"sent": sent, "kept": kept}
		if err != nil {
			f["error"] = err.Error()
		}
		obs.Default().Info("audit spool replayed into the database", f)
	}
	return sent, kept, err
}

// StartSpoolReplay replays the spool every interval until ctx ends: the
// database coming back is noticed even when nothing is being done.
func (r *Recorder) StartSpoolReplay(ctx context.Context, every time.Duration) {
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				r.ReplaySpool(ctx)
			}
		}
	}()
}

func newEventID() string {
	var b [16]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// NewRecorder returns a recorder with no sink yet: events go to the log only.
func NewRecorder() *Recorder { return &Recorder{} }

// UseSink sets where events are stored. Nil means the log only.
func (r *Recorder) UseSink(s Sink) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.sink = s
	r.mu.Unlock()
}

// Record writes one event. The log copy is written first and always; a
// failure to store it in the database is logged, not returned -- an audit
// write must never fail the action it describes, and the log copy exists.
func (r *Recorder) Record(ctx context.Context, e Event) {
	if e.At.IsZero() {
		e.At = time.Now()
	}
	if e.ID == "" {
		e.ID = newEventID()
	}
	f := obs.Fields{
		"actor": e.Actor, "action": e.Action, "result": e.Result,
		"request_id": e.RequestID, "ip": e.IP, "event_id": e.ID,
	}
	if e.Target != "" {
		f["target"] = e.Target
	}
	if e.Status != 0 {
		f["status"] = e.Status
	}
	if len(e.Detail) > 0 {
		f["detail"] = e.Detail
	}
	level := "info"
	if e.Result != "ok" {
		level = "warn"
	}
	obs.Default().Write("audit", level, f)

	if r == nil {
		return
	}
	r.mu.RLock()
	sink, sp := r.sink, r.spool
	r.mu.RUnlock()
	if sink == nil {
		return
	}
	// A short deadline of its own: the request it describes may already be
	// finishing, and a slow database must not hold the response.
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := sink.Record(sctx, e); err != nil {
		where := "it is in this log"
		if sp != nil {
			if serr := sp.Append(e); serr == nil {
				where = "it is spooled, and goes in when the database is back"
			} else {
				where = "it is in this log; the spool refused it: " + serr.Error()
			}
		}
		obs.Default().Warn("audit event not stored in the database ("+where+")",
			obs.Fields{"error": err.Error(), "request_id": e.RequestID, "action": e.Action, "event_id": e.ID})
		return
	}
	// The database took this one, so it is back: anything waiting follows,
	// in the background.
	if sp.Pending() {
		go r.ReplaySpool(context.Background())
	}
}

// ---------------------------------------------------------------- notes

type noteKey struct{}

type note struct {
	mu     sync.Mutex
	target string
	detail map[string]any
	skip   bool
	actor  string
}

// Note records what the current request acted on, for its audit event.
//
//	audit.Note(r.Context(), "doc:"+path, nil)
//
// Safe to call from any handler, audited or not; it does nothing outside a
// request the middleware is watching. Detail values must be names and counts,
// never contents.
func Note(ctx context.Context, target string, detail map[string]any) {
	n, _ := ctx.Value(noteKey{}).(*note)
	if n == nil {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if target != "" {
		n.target = target
	}
	if len(detail) > 0 {
		if n.detail == nil {
			n.detail = map[string]any{}
		}
		maps.Copy(n.detail, detail)
	}
}

// NoteActor names who made a request that was made without a session -- the
// sign-in, where the username is what was typed, not who was signed in.
func NoteActor(ctx context.Context, actor string) {
	n, _ := ctx.Value(noteKey{}).(*note)
	if n == nil {
		return
	}
	n.mu.Lock()
	n.actor = actor
	n.mu.Unlock()
}

// ---------------------------------------------------------------- middleware

// RouteOf names the route a request matched, as its registered pattern.
type RouteOf func(*http.Request) string

// readPOSTs are POST routes that read rather than change anything: running a
// report, asking the assistant, previewing a chart. Recording each would bury
// the changes an administrator is looking for under a person's every search.
var readPOSTs = map[string]bool{
	"POST /api/ask":                     true,
	"POST /api/ask/prose":               true,
	"POST /api/reports/run":             true,
	"POST /api/reports/chart":           true,
	"POST /api/reports/monthly":         true,
	"POST /api/analysis/advice":         true,
	"POST /api/analysis/advice/all":     true,
	"POST /api/builder/params":          true,
	"POST /api/evaluation":              true,
	"POST /api/mail/message/{uid}/seen": true,
	"POST /api/browse":                  true,
	"POST /api/open":                    true,
	// Asking the model to classify a message: the inbox does it a dozen at a
	// time on every visit, and it changes no business data.
	"POST /api/mail/message/{uid}/classify": true,
}

// auditedGETs are reads worth recording because data leaves the building
// through them: exports, downloads, a whole document.
var auditedGETs = map[string]bool{
	"GET /api/reports/export":                        true,
	"GET /api/builder/export":                        true,
	"GET /api/attendance/export":                     true,
	"GET /api/doc":                                   true,
	"GET /api/mail/message/{uid}/attachment/{index}": true,
}

// Audited reports whether a request to this route is recorded.
func Audited(method, route string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return !readPOSTs[route]
	case http.MethodGet:
		return auditedGETs[route]
	}
	return false
}

// Middleware records every audited request once its response is written.
//
// It must sit INSIDE obs.Middleware: the actor is read from the request record
// that middleware plants, which the auth layer fills in.
func Middleware(rec *Recorder, route RouteOf, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pattern := ""
		if route != nil {
			pattern = route(r)
		}
		if pattern == "" || !Audited(r.Method, pattern) {
			next.ServeHTTP(w, r)
			return
		}
		n := &note{}
		r = r.WithContext(context.WithValue(r.Context(), noteKey{}, n))
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)

		n.mu.Lock()
		skip, target, actor := n.skip, n.target, n.actor
		detail := maps.Clone(n.detail)
		n.mu.Unlock()
		if skip {
			return
		}
		if actor == "" {
			if rq := obs.From(r.Context()); rq != nil {
				actor = rq.User()
			}
		}
		status := sw.status
		if status == 0 {
			status = http.StatusOK
		}
		rec.Record(r.Context(), Event{
			Actor: actor, Action: pattern, Target: target,
			Result: resultOf(status), Status: status,
			IP: clientIP(r), UserAgent: clip(r.UserAgent(), 200),
			RequestID: obs.RequestID(r.Context()), Detail: detail,
		})
	})
}

func resultOf(status int) string {
	switch {
	case status >= 500:
		return "error"
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return "denied"
	case status >= 400:
		return "rejected"
	}
	return "ok"
}

// trustProxy mirrors the Guard's TrustProxyHeaders so the audit trail records
// the real visitor IP, not the front proxy's. Set once at startup (before any
// request is served) via SetTrustProxyHeaders.
var trustProxy atomic.Bool

// SetTrustProxyHeaders tells the audit logger whether a trusted front
// (a CDN or a reverse proxy) sits in front, so clientIP reads the real
// client from the tail of X-Forwarded-For rather than logging the proxy's own
// address for every actor's every action.
func SetTrustProxyHeaders(on bool) { trustProxy.Store(on) }

func clientIP(r *http.Request) string {
	if trustProxy.Load() {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			// The left-most entry is the original client, matching
			// auth.Guard.ClientIP -- so the audit trail records the real
			// visitor, not the front proxy whose address RemoteAddr would be.
			if i := strings.IndexByte(xff, ','); i > 0 {
				return stripPort(strings.TrimSpace(xff[:i]))
			}
			return stripPort(strings.TrimSpace(xff))
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// stripPort drops a ":port" or "[v6]:port" suffix if present.
func stripPort(s string) string {
	if h, _, err := net.SplitHostPort(s); err == nil {
		return h
	}
	return s
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n]
	}
	return s
}

type statusWriter struct {
	http.ResponseWriter
	status int
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
	return w.ResponseWriter.Write(p)
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
