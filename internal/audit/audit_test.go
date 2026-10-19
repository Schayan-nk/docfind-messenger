package audit

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/Schayan-nk/docfind-messenger/internal/obs"
)

type memSink struct {
	mu     sync.Mutex
	events []Event
	fail   bool
}

func (m *memSink) Record(_ context.Context, e Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail {
		return errors.New("database down")
	}
	m.events = append(m.events, e)
	return nil
}

// chain is obs outside audit, as the server builds it, with a handler that
// plays the auth layer's part by naming the user.
func chain(t *testing.T, sink Sink, route string, h http.HandlerFunc) http.Handler {
	t.Helper()
	rec := NewRecorder()
	rec.UseSink(sink)
	routeOf := func(*http.Request) string { return route }
	return obs.Middleware(nil, routeOf, nil, Middleware(rec, routeOf, h))
}

func signedIn(user string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		obs.SetUser(r.Context(), user)
		h(w, r)
	}
}

func TestAChangeIsRecordedWithActorTargetAndResult(t *testing.T) {
	sink := &memSink{}
	h := chain(t, sink, "POST /api/docs/rename", signedIn("n.karimi", func(w http.ResponseWriter, r *http.Request) {
		Note(r.Context(), "doc:Contracts/Nikan.docx", map[string]any{"to": "Nikan-final.docx"})
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/docs/rename", nil))
	if len(sink.events) != 1 {
		t.Fatalf("recorded %d events", len(sink.events))
	}
	e := sink.events[0]
	if e.Actor != "n.karimi" || e.Action != "POST /api/docs/rename" || e.Target != "doc:Contracts/Nikan.docx" ||
		e.Result != "ok" || e.Status != 200 || e.RequestID == "" || e.Detail["to"] != "Nikan-final.docx" {
		t.Errorf("event = %+v", e)
	}
}

func TestARefusalIsRecordedAsDenied(t *testing.T) {
	sink := &memSink{}
	h := chain(t, sink, "DELETE /api/users", signedIn("reader", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("DELETE", "/api/users", nil))
	if len(sink.events) != 1 || sink.events[0].Result != "denied" {
		t.Errorf("events = %+v", sink.events)
	}
}

// A failed sign-in names the account that was TRIED, not "nobody".
func TestAFailedSignInNamesTheAccountTried(t *testing.T) {
	sink := &memSink{}
	h := chain(t, sink, "POST /api/login", func(w http.ResponseWriter, r *http.Request) {
		NoteActor(r.Context(), "sadeghi")
		Note(r.Context(), "user:sadeghi", nil)
		w.WriteHeader(http.StatusUnauthorized)
	})
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/login", nil))
	if len(sink.events) != 1 || sink.events[0].Actor != "sadeghi" || sink.events[0].Result != "denied" {
		t.Errorf("events = %+v", sink.events)
	}
}

func TestReadsAreNotRecordedButExportsAre(t *testing.T) {
	for _, c := range []struct {
		method, route string
		want          bool
	}{
		{"GET", "/api/docs/list", false},
		{"POST", "/api/ask", false},
		{"POST", "/api/reports/run", false},
		{"POST", "/api/mail/message/{uid}/seen", false},
		{"GET", "/api/reports/export", true},
		{"GET", "/api/doc", true},
		{"POST", "/api/users", true},
		{"PUT", "/api/permissions/user", true},
		{"DELETE", "/api/push/devices/{id}", true},
		{"PATCH", "/api/mail/rules/{id}", true},
	} {
		if got := Audited(c.method, c.method+" "+c.route); got != c.want {
			t.Errorf("%s %s audited = %v, want %v", c.method, c.route, got, c.want)
		}
	}
}
