package find

import (
	"encoding/json"
	"net/http"
	"testing"
)

// The metrics are for administrators: route timings and job errors describe
// how the service is built and where it is struggling.
func TestMetricsAreForAdministratorsOnly(t *testing.T) {
	s, admin, viewer, _ := accountsTestServer(t)
	if rec := asUser(t, s, "GET", "/api/admin/metrics", viewer, ""); rec.Code != http.StatusForbidden {
		t.Errorf("a viewer got %d", rec.Code)
	}
	if rec := asUser(t, s, "GET", "/api/admin/metrics", "", ""); rec.Code != http.StatusUnauthorized &&
		rec.Code != http.StatusForbidden {
		t.Errorf("an anonymous caller got %d", rec.Code)
	}
	// A request first, so there is a route to report.
	asUser(t, s, "GET", "/api/users", admin, "")
	rec := asUser(t, s, "GET", "/api/admin/metrics", admin, "")
	if rec.Code != 200 {
		t.Fatalf("an administrator got %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Routes []struct {
			Route string  `json:"route"`
			Count int64   `json:"count"`
			P95   float64 `json:"p95_ms"`
		} `json:"routes"`
		Runtime map[string]any `json:"runtime"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range out.Routes {
		if r.Route == "GET /api/users" && r.Count >= 1 {
			found = true
		}
	}
	if !found {
		t.Errorf("the earlier request is not in the route figures: %+v", out.Routes)
	}
	if out.Runtime["goroutines"] == nil || out.Runtime["uptime_s"] == nil {
		t.Errorf("runtime figures missing: %v", out.Runtime)
	}
}
