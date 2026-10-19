package find

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func getWith(t *testing.T, s *Server, path string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("GET", path, nil)
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, r)
	return rec
}

func TestWantsHTML(t *testing.T) {
	cases := []struct {
		name    string
		headers map[string]string
		want    bool
	}{
		// Sec-Fetch-Mode is decisive where the browser sends it.
		{"navigation", map[string]string{"Sec-Fetch-Mode": "navigate"}, true},
		{"navigation without Accept", map[string]string{"Sec-Fetch-Mode": "navigate", "Accept": "*/*"}, true},
		// A fetch() carrying an HTML Accept must still get JSON.
		{"fetch claiming html", map[string]string{"Sec-Fetch-Mode": "cors", "Accept": "text/html"}, false},
		{"same-origin fetch", map[string]string{"Sec-Fetch-Mode": "same-origin", "Accept": "text/html"}, false},
		// Older clients: fall back to Accept.
		{"accept html", map[string]string{"Accept": "text/html,application/xhtml+xml"}, true},
		{"accept star", map[string]string{"Accept": "*/*"}, false},
		{"accept json", map[string]string{"Accept": "application/json"}, false},
		{"no headers at all", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/x", nil)
			for k, v := range c.headers {
				r.Header.Set(k, v)
			}
			if got := wantsHTML(r); got != c.want {
				t.Errorf("wantsHTML = %v, want %v", got, c.want)
			}
		})
	}
}

// An unknown address in a browser gets the Persian 404 page, with a way back
// into the application and nothing of the address it asked for.
func TestUnknownPathServesThe404Page(t *testing.T) {
	s := NewServer("")
	rec := getWith(t, s, "/no/such/page", map[string]string{"Sec-Fetch-Mode": "navigate"})

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("Content-Type %q, want html", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "این صفحه پیدا نشد") {
		t.Error("the Persian title is missing")
	}
	// Go's default, which is what this replaced.
	if strings.Contains(body, "404 page not found") {
		t.Error("still serving Go's default text")
	}
	// A link back into the app is the entire point of having the page.
	if !strings.Contains(body, `href="/messages"`) {
		t.Error("no way back to the application")
	}
	// The requested path must not be reflected by the server. The page fills
	// it in from location.pathname instead, so a crafted URL cannot place
	// markup in the response.
	if strings.Contains(body, "/no/such/page") {
		t.Error("the request path was reflected into the page")
	}
}

// An API caller must keep getting JSON, whatever the page does.
func TestUnknownAPIPathStaysJSON(t *testing.T) {
	s := NewServer("")
	for _, h := range []map[string]string{
		nil,
		{"Accept": "*/*"},
		{"Sec-Fetch-Mode": "cors", "Accept": "text/html"},
	} {
		rec := getWith(t, s, "/api/nope", h)
		if rec.Code != http.StatusNotFound {
			t.Errorf("status %d, want 404", rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
			t.Errorf("Content-Type %q, want JSON (headers %v)", ct, h)
		}
		if strings.Contains(rec.Body.String(), "<html") {
			t.Errorf("markup was sent to an API caller (headers %v)", h)
		}
	}
}

// A panic in a browser navigation should land on the 500 page, and must not
// carry a word of what actually failed.
func TestPanicInANavigationServesThe500Page(t *testing.T) {
	h := recoverPanics(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom: D:\\Archive\\Documents\\secret.docx contains 'PRIVATE'")
	}))

	r := httptest.NewRequest("GET", "/dashboard", nil)
	r.Header.Set("Sec-Fetch-Mode", "navigate")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "خطای داخلی سرور") {
		t.Error("the Persian title is missing")
	}
	for _, leak := range []string{"boom", "secret.docx", "PRIVATE", "D:\\", "goroutine", ".go:", "panic"} {
		if strings.Contains(body, leak) {
			t.Errorf("the 500 page leaked %q", leak)
		}
	}
}
