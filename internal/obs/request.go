// Package obs is docfind's observability: the application log, the per-request
// record, and the latency figures built from it.
//
// WHY IT EXISTS. Until 5.21.0 the server wrote to stdout and the scheduled task
// that ran it threw stdout away. A fault could only be diagnosed from
// its side effects -- which file changed, which timestamp moved -- and a slow
// page could not be diagnosed at all, because nothing recorded how long
// anything took. This package is the record.
//
// THE RULE IT FOLLOWS is the one the docxgen logger already states: log what
// happened, never what was in it. A route PATTERN, never the path -- the path
// can carry a document name, which is business data. Never a query string --
// the desktop build can carry its token in ?t=. Never a body, a password, a
// key, a token or a prompt.
package obs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"sync/atomic"
	"time"
)

// Kind is a category of time spent outside the handler's own code.
type Kind int

const (
	// DB is a query against any database: PostgreSQL, a reporting MySQL, a
	// SQL Server.
	DB Kind = iota
	// External is an HTTP call to anything that is not an AI provider: the
	// SMS gateway, the internet probes, another web service.
	External
	// AI is a call to a language model.
	AI
	// Mail is a POP3 session.
	Mail
	numKinds
)

func (k Kind) String() string {
	switch k {
	case DB:
		return "db"
	case External:
		return "ext"
	case AI:
		return "ai"
	case Mail:
		return "mail"
	}
	return "other"
}

// Request is what is known about one HTTP request while it runs.
//
// It travels in the context as a POINTER, and that is the point: the auth
// layer and the permission layer each replace the *http.Request with a copy
// carrying a new context, so anything they stored by value would be invisible
// to the middleware that started the request. A pointer planted at the top is
// shared by every copy made below it.
type Request struct {
	ID    string
	Start time.Time

	mu   sync.Mutex
	user string

	// Per kind: how many calls are in flight, when the current in-flight
	// span began, and the total span time. Guarded by mu.
	active [numKinds]int
	since  [numKinds]time.Time
	nanos  [numKinds]time.Duration
	count  [numKinds]atomic.Int64
}

type ctxKey struct{}

// NewRequest starts the record for one request.
func NewRequest() *Request {
	return &Request{ID: NewID(), Start: time.Now()}
}

// NewID is 16 hex characters from the system's secure random source.
//
// Not a UUID: nothing needs one, and a shorter ID is easier to read out over
// the phone when somebody reports the number shown on an error page.
func NewID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand does not fail on any platform this runs on; if it ever
		// does, a time-derived ID still correlates the log lines, which is
		// all an ID is for here.
		return hex.EncodeToString([]byte(time.Now().Format("150405.000000")))[:16]
	}
	return hex.EncodeToString(b[:])
}

// With returns a context carrying rq.
func With(ctx context.Context, rq *Request) context.Context {
	return context.WithValue(ctx, ctxKey{}, rq)
}

// From returns the request record in ctx, or nil.
//
// Every helper below is nil-safe, so code that runs both inside and outside a
// request -- a background job calling the same query function -- needs no
// branches.
func From(ctx context.Context) *Request {
	if ctx == nil {
		return nil
	}
	rq, _ := ctx.Value(ctxKey{}).(*Request)
	return rq
}

// RequestID returns the ID of the request in ctx, or "".
func RequestID(ctx context.Context) string {
	if rq := From(ctx); rq != nil {
		return rq.ID
	}
	return ""
}

// SetUser records who made the request, once they are known.
func SetUser(ctx context.Context, username string) {
	rq := From(ctx)
	if rq == nil {
		return
	}
	rq.mu.Lock()
	rq.user = username
	rq.mu.Unlock()
}

// User returns who made the request, or "".
func (rq *Request) User() string {
	if rq == nil {
		return ""
	}
	rq.mu.Lock()
	defer rq.mu.Unlock()
	return rq.user
}

// Track starts timing one outside call and returns the function that stops it.
//
//	defer obs.Track(ctx, obs.DB)()
//
// WALL TIME WITH ANYTHING IN FLIGHT, not a sum. The figure answers "how long
// was this request waiting on the database", and two ways of summing get that
// wrong: a query method that calls another query method would be counted
// twice, and three queries run in parallel would report three times the wait.
// So the clock runs while at least one call of the kind is open, and the
// count says how many calls there were.
func Track(ctx context.Context, k Kind) func() {
	rq := From(ctx)
	if rq == nil || k < 0 || k >= numKinds {
		return func() {}
	}
	rq.count[k].Add(1)
	rq.mu.Lock()
	if rq.active[k] == 0 {
		rq.since[k] = time.Now()
	}
	rq.active[k]++
	rq.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			rq.mu.Lock()
			rq.active[k]--
			if rq.active[k] == 0 {
				rq.nanos[k] += time.Since(rq.since[k])
			}
			rq.mu.Unlock()
		})
	}
}

// Spent returns the time with a call of this kind in flight, and how many
// calls there were.
func (rq *Request) Spent(k Kind) (time.Duration, int64) {
	if rq == nil || k < 0 || k >= numKinds {
		return 0, 0
	}
	rq.mu.Lock()
	d := rq.nanos[k]
	if rq.active[k] > 0 {
		// Still open (a streaming response): count up to now.
		d += time.Since(rq.since[k])
	}
	rq.mu.Unlock()
	return d, rq.count[k].Load()
}
