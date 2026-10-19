package find

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"sync"
	"time"

	"github.com/Schayan-nk/docfind-messenger/internal/auth"
)

// Idempotent writes, for the offline outbox (nav.js).
//
// A change made while the device was offline is queued in the browser and
// sent when the connection comes back -- and a request that was sent, reached
// the server, and lost its answer on the way back will be sent again. The
// Idempotency-Key header is how the second one is recognised: a successful
// answer is kept for a day under the key and the caller's session, and a
// repeat is given that same answer without doing the work twice. A reminder
// queued on a train is set once, however many tunnels it went through.
//
// Keyed by the session as well as the key -- hashed, never stored -- so one
// person's key can never replay another person's answer. Only successes are
// kept: a failure is worth trying again. Bounded in count and size, and in
// memory: a restart forgets, and the worst a restart can cost is the one
// duplicate this exists to prevent, not a wrong answer.

const (
	idemTTL  = 24 * time.Hour
	idemMax  = 1000
	idemBody = 64 << 10
	// idemWait is how long a duplicate waits for the first of its kind to
	// finish. The two arrive together when a flush races a retry.
	idemWait = 30 * time.Second
)

// idemEntry is one key's answer, or the promise of one: done is closed when
// the request that owns the key has finished, and kept says whether its
// answer was worth keeping.
type idemEntry struct {
	done   chan struct{}
	kept   bool
	status int
	ctype  string
	body   []byte
	at     time.Time
}

type idemStore struct {
	mu    sync.Mutex
	m     map[string]*idemEntry
	order []string
}

// begin returns the entry for k, and whether the caller now owns it and must
// run the request and finish it. Anyone else waits on the entry.
func (st *idemStore) begin(k string, now time.Time) (*idemEntry, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if e, ok := st.m[k]; ok && now.Sub(e.at) <= idemTTL {
		return e, false
	}
	if st.m == nil {
		st.m = map[string]*idemEntry{}
	}
	if _, ok := st.m[k]; !ok {
		st.order = append(st.order, k)
	}
	e := &idemEntry{done: make(chan struct{}), at: now}
	st.m[k] = e
	for len(st.order) > idemMax {
		delete(st.m, st.order[0])
		st.order = st.order[1:]
	}
	return e, true
}

// finish records the owner's answer. One not worth keeping -- a failure, or
// too large -- is forgotten, so the next attempt runs afresh.
func (st *idemStore) finish(k string, e *idemEntry, keep bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	e.kept = keep
	if !keep && st.m[k] == e {
		delete(st.m, k)
	}
	close(e.done)
}

// idemRecorder keeps a copy of what the handler wrote.
type idemRecorder struct {
	http.ResponseWriter
	status int
	buf    bytes.Buffer
	over   bool
}

func (w *idemRecorder) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *idemRecorder) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if !w.over {
		if w.buf.Len()+len(p) > idemBody {
			w.over = true
		} else {
			w.buf.Write(p)
		}
	}
	return w.ResponseWriter.Write(p)
}

// idempotent runs next once per (session, Idempotency-Key). A request
// without the header is untouched.
func (s *Server) idempotent(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		next(w, r)
		return
	}
	if len(key) > 100 {
		http.Error(w, `{"error":"کلید تکرار نامعتبر است."}`, http.StatusBadRequest)
		return
	}
	session := ""
	if c, err := r.Cookie(auth.SessionCookie); err == nil {
		session = c.Value
	}
	sum := sha256.Sum256([]byte(session + "\x00" + r.Method + " " + r.URL.Path + "\x00" + key))
	k := hex.EncodeToString(sum[:])
	e, owner := s.idem.begin(k, time.Now())
	if !owner {
		select {
		case <-e.done:
		case <-time.After(idemWait):
			http.Error(w, `{"error":"همین تغییر هنوز در حال انجام است؛ کمی بعد دوباره بفرستید."}`, http.StatusConflict)
			return
		case <-r.Context().Done():
			return
		}
		if e.kept {
			if e.ctype != "" {
				w.Header().Set("Content-Type", e.ctype)
			}
			w.Header().Set("Idempotent-Replay", "true")
			w.WriteHeader(e.status)
			w.Write(e.body)
			return
		}
		// The first attempt failed and was forgotten: this one is a fresh
		// try, taken through the store again so a third waits on it.
		s.idempotent(w, r, next)
		return
	}
	rec := &idemRecorder{ResponseWriter: w}
	keep := false
	defer func() {
		if keep {
			e.status, e.ctype, e.body = rec.status, w.Header().Get("Content-Type"), rec.buf.Bytes()
		}
		s.idem.finish(k, e, keep)
	}()
	next(rec, r)
	keep = rec.status >= 200 && rec.status < 300 && !rec.over
}
