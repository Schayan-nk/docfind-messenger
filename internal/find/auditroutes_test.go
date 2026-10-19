package find

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/Schayan-nk/docfind-messenger/internal/audit"
	"github.com/Schayan-nk/docfind-messenger/internal/pgstore"
)

// memAudit is the audit store in memory.
type memAudit struct {
	mu     sync.Mutex
	events []audit.Event
}

func (m *memAudit) Record(_ context.Context, e audit.Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, e)
	return nil
}

func (m *memAudit) List(_ context.Context, q pgstore.AuditQuery) ([]pgstore.AuditRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []pgstore.AuditRow
	for i := len(m.events) - 1; i >= 0; i-- {
		e := m.events[i]
		if q.Actor != "" && e.Actor != q.Actor {
			continue
		}
		out = append(out, pgstore.AuditRow{ID: int64(i + 1), Event: e})
	}
	return out, nil
}

func (m *memAudit) find(action string) (audit.Event, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.events {
		if e.Action == action {
			return e, true
		}
	}
	return audit.Event{}, false
}

func auditServer(t *testing.T) (*Server, *memAudit, string, string) {
	t.Helper()
	s, admin, viewer, _ := accountsTestServer(t)
	st := &memAudit{}
	s.UseAudit(st)
	return s, st, admin, viewer
}

// A failed sign-in is recorded under the name that was TRIED, as denied.
func TestAFailedSignInIsAuditedUnderTheNameTried(t *testing.T) {
	s, st, _, _ := auditServer(t)
	asUser(t, s, "POST", "/api/login", "", `{"username":"sadeghi","password":"not it"}`)
	e, ok := st.find("POST /api/login")
	if !ok {
		t.Fatal("the sign-in was not audited")
	}
	if e.Actor != "sadeghi" || e.Result != "denied" || e.Target != "user:sadeghi" {
		t.Errorf("event = %+v", e)
	}
}

// A permission change says WHAT changed, and never carries a password.
func TestAPermissionChangeRecordsTheDifference(t *testing.T) {
	s, st, admin, _ := auditServer(t)
	rec := asUser(t, s, "PUT", "/api/permissions/user", admin,
		`{"username":"reader","grants":{"action:notifications":"deny","page:settings":"deny"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("saving grants answered %d: %s", rec.Code, rec.Body.String())
	}
	e, ok := st.find("PUT /api/permissions/user")
	if !ok {
		t.Fatal("the permission change was not audited")
	}
	if e.Actor != "boss" || e.Target != "user:reader" || e.Result != "ok" {
		t.Errorf("event = %+v", e)
	}
	b, _ := json.Marshal(e.Detail)
	if !strings.Contains(string(b), "action:notifications") || !strings.Contains(string(b), "page:settings") {
		t.Errorf("the detail does not say what changed: %s", b)
	}
}

func TestResettingAPasswordNeverRecordsIt(t *testing.T) {
	s, st, admin, _ := auditServer(t)
	asUser(t, s, "POST", "/api/users/password", admin, `{"username":"reader","password":"a brand new secret"}`)
	e, ok := st.find("POST /api/users/password")
	if !ok {
		t.Fatal("the reset was not audited")
	}
	b, _ := json.Marshal(e)
	if strings.Contains(string(b), "brand new secret") {
		t.Fatalf("the password reached the audit trail: %s", b)
	}
	if e.Target != "user:reader" {
		t.Errorf("target %q", e.Target)
	}
}

func TestTheTrailIsForAdministrators(t *testing.T) {
	s, _, admin, viewer := auditServer(t)
	asUser(t, s, "POST", "/api/login", "", `{"username":"ghost","password":"x"}`)
	if rec := asUser(t, s, "GET", "/api/audit", viewer, ""); rec.Code != http.StatusForbidden {
		t.Errorf("a viewer got %d", rec.Code)
	}
	rec := asUser(t, s, "GET", "/api/audit?actor=ghost", admin, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("an administrator got %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Events []pgstore.AuditRow `json:"events"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Events) != 1 || out.Events[0].Actor != "ghost" {
		t.Errorf("events = %+v", out.Events)
	}
	if rec := asUser(t, s, "GET", "/api/audit?since=yesterday", admin, ""); rec.Code != http.StatusBadRequest {
		t.Errorf("a malformed date answered %d", rec.Code)
	}
}

// Reads stay out of the trail; the one thing a search leaves is the request
// log line.
func TestAReadIsNotAudited(t *testing.T) {
	s, st, admin, _ := auditServer(t)
	asUser(t, s, "GET", "/api/docs/list", admin, "")
	st.mu.Lock()
	n := len(st.events)
	st.mu.Unlock()
	if n != 0 {
		t.Errorf("a read produced %d audit events", n)
	}
}
