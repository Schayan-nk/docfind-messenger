package find

import (
	"net/http/httptest"
	"testing"
)

// While PostgreSQL is away: reads and report runs go on; changes to
// accounts, permissions, report definitions and settings, and anything that
// deletes, are refused.
func TestWhatIsRefusedWhileDown(t *testing.T) {
	refused := []string{
		"POST /api/users", "PUT /api/users", "DELETE /api/users",
		"PUT /api/permissions/user", "POST /api/permissions/roles",
		"DELETE /api/account/photo",
	}
	allowed := []string{
		"GET /api/users", "GET /api/permissions", "POST /api/login",
	}
	req := func(line string) (*httptest.ResponseRecorder, bool) {
		var method, path string
		for i := range line {
			if line[i] == ' ' {
				method, path = line[:i], line[i+1:]
				break
			}
		}
		return nil, refusedAtLevel(2, httptest.NewRequest(method, path, nil))
	}
	for _, l := range refused {
		if _, ok := req(l); !ok {
			t.Errorf("%s was allowed while PostgreSQL is down", l)
		}
	}
	for _, l := range allowed {
		if _, ok := req(l); ok {
			t.Errorf("%s was refused while PostgreSQL is down", l)
		}
	}
	if refusedAtLevel(1, httptest.NewRequest("DELETE", "/api/users", nil)) {
		t.Error("level 1 refused a change")
	}
}
