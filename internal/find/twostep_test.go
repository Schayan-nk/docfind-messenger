package find

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func decode(t *testing.T, body string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("not JSON: %s", body)
	}
	return m
}

func sessionCookie(rec *httptest.ResponseRecorder) bool {
	for _, c := range rec.Result().Cookies() {
		if strings.Contains(c.Name, "docfind_token") && c.Value != "" && c.MaxAge >= 0 {
			return true
		}
	}
	return false
}
