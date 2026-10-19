package find

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Schayan-nk/docfind-messenger/internal/auth"
)

func idemRequest(s *Server, next http.HandlerFunc, session, key string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/api/reminders", strings.NewReader(`{}`))
	if session != "" {
		r.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: session})
	}
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	w := httptest.NewRecorder()
	s.idempotent(w, r, next)
	return w
}

// A write sent again under the same key -- its first answer lost on the way
// back -- is done once and answered the same; another person's session, or no
// key at all, is a separate write; a failure is not remembered.
func TestIdempotentWritesHappenOnce(t *testing.T) {
	s := &Server{}
	var runs atomic.Int32
	ok := func(w http.ResponseWriter, r *http.Request) {
		n := runs.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":` + string(rune('0'+n)) + `}`))
	}
	first := idemRequest(s, ok, "sess-a", "k1")
	again := idemRequest(s, ok, "sess-a", "k1")
	if runs.Load() != 1 || again.Code != http.StatusCreated || again.Body.String() != first.Body.String() ||
		again.Header().Get("Idempotent-Replay") != "true" || again.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("repeat ran the write again or answered differently: runs=%d %d %q", runs.Load(), again.Code, again.Body.String())
	}
	if idemRequest(s, ok, "sess-b", "k1"); runs.Load() != 2 {
		t.Error("another session's key replayed this session's answer")
	}
	idemRequest(s, ok, "sess-a", "")
	idemRequest(s, ok, "sess-a", "")
	if runs.Load() != 4 {
		t.Errorf("writes without a key were collapsed: runs=%d", runs.Load())
	}

	var fails atomic.Int32
	bad := func(w http.ResponseWriter, r *http.Request) {
		fails.Add(1)
		http.Error(w, `{"error":"no"}`, http.StatusBadGateway)
	}
	idemRequest(s, bad, "sess-a", "k2")
	if idemRequest(s, bad, "sess-a", "k2"); fails.Load() != 2 {
		t.Error("a failed write was remembered instead of tried again")
	}
	if w := idemRequest(s, ok, "sess-a", strings.Repeat("x", 101)); w.Code != http.StatusBadRequest {
		t.Errorf("an over-long key answered %d", w.Code)
	}
}

// A change queued offline carries its key from the moment it is made, and the
// account that made it; the flush sends only the signed-in account's own, so
// a session that ran out and a colleague signing in on the same browser does
// not send one person's changes as another's.
func TestOutboxSendsOnlyAsItsOwner(t *testing.T) {
	nav := readPage(t, "nav.js")
	for _, want := range []string{
		"'Idempotency-Key': key",
		"key, label: label || 'تغییر', at: Date.now(), owner });",
		"filter(it => it.owner && it.owner === who)",
		"localStorage.setItem('docfind.account', o.account.username)",
	} {
		if !strings.Contains(nav, want) {
			t.Errorf("nav.js no longer has %q", want)
		}
	}
}

// Two sends of the same write arriving together -- a flush racing a retry --
// run it once: the second waits for the first and is given its answer.
func TestIdempotentDuplicatesInFlightWait(t *testing.T) {
	s := &Server{}
	var runs atomic.Int32
	release := make(chan struct{})
	slow := func(w http.ResponseWriter, r *http.Request) {
		runs.Add(1)
		<-release
		w.Write([]byte(`{"ok":true}`))
	}
	var wg sync.WaitGroup
	answers := make([]*httptest.ResponseRecorder, 2)
	for i := range answers {
		wg.Add(1)
		go func() { defer wg.Done(); answers[i] = idemRequest(s, slow, "sess", "same") }()
	}
	time.Sleep(100 * time.Millisecond)
	close(release)
	wg.Wait()
	if runs.Load() != 1 {
		t.Fatalf("the write ran %d times", runs.Load())
	}
	for _, a := range answers {
		if a.Code != http.StatusOK || a.Body.String() != `{"ok":true}` {
			t.Errorf("answer %d %q", a.Code, a.Body.String())
		}
	}
}
