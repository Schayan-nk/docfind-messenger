package auth

import (
	"net/http/httptest"
	"testing"
)

// An account on its temporary password reaches who it is, the change and
// signing out -- and nothing else, however it asks.
func TestTheUpdatePasswordGateLetsThreeThingsThrough(t *testing.T) {
	for path, want := range map[string]bool{
		"GET /api/me": true, "POST /api/account/password": true, "POST /api/logout": true,
		"GET /api/status": false, "POST /api/me": false, "GET /api/account/password": false,
		"GET /api/users": false, "GET /api/me/../status": false,
	} {
		var method, p string
		for i := range path {
			if path[i] == ' ' {
				method, p = path[:i], path[i+1:]
				break
			}
		}
		r := httptest.NewRequest(method, "http://x"+p, nil)
		if got := mustChangeAllows(r); got != want {
			t.Errorf("%s: allowed=%v, want %v", path, got, want)
		}
	}
}
