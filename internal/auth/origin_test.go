package auth

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func post(headers map[string]string, cookie string) *http.Request {
	r := httptest.NewRequest("POST", "http://docfind.example/api/users", nil)
	r.Host = "docfind.example"
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	if cookie != "" {
		r.AddCookie(&http.Cookie{Name: SessionCookie, Value: cookie})
	}
	return r
}

func TestSameOriginEdgeCases(t *testing.T) {
	for _, c := range []struct {
		name    string
		headers map[string]string
		cookie  string
		want    bool
	}{
		{"same origin", map[string]string{"Origin": "http://docfind.example"}, "tok", true},
		{"another site", map[string]string{"Origin": "https://evil.example"}, "tok", false},
		{"a sandboxed frame", map[string]string{"Origin": "null"}, "tok", false},
		// Fetch Metadata outranks a forged-looking-fine Origin.
		{"labelled cross-site", map[string]string{"Origin": "http://docfind.example", "Sec-Fetch-Site": "cross-site"}, "tok", false},
		{"referer only, ours", map[string]string{"Referer": "http://docfind.example/settings"}, "tok", true},
		{"referer only, theirs", map[string]string{"Referer": "https://evil.example/x"}, "tok", false},
		// No signal at all riding a cookie: the forged-request shape.
		{"no signal, cookie", nil, "tok", false},
		{"no signal, fetch metadata says same-origin", map[string]string{"Sec-Fetch-Site": "same-origin"}, "tok", true},
		// No signal, no cookie: a script with its own token has nothing to forge.
		{"no signal, bearer", map[string]string{"Authorization": "Bearer abc"}, "", true},
		{"no signal, nothing", nil, "", true},
	} {
		if got := SameOrigin(post(c.headers, c.cookie)); got != c.want {
			t.Errorf("%s: SameOrigin = %v, want %v", c.name, got, c.want)
		}
	}
}

// Over HTTPS the session goes in the host-bound cookie, the old one is
// dropped, and ONLY the host-bound one is read -- a planted docfind_token
// signs nobody in.
func TestOverHTTPSTheSessionCookieIsHostBound(t *testing.T) {
	r := httptest.NewRequest("POST", "https://docfind.example/api/login", nil)
	r.TLS = &tls.ConnectionState{}
	rec := httptest.NewRecorder()
	WriteCookie(rec, r, "tok", time.Hour, false)
	set := strings.Join(rec.Header().Values("Set-Cookie"), "\n")
	if !strings.Contains(set, HostSessionCookie+"=tok") || !strings.Contains(set, "Secure") {
		t.Errorf("no host-bound secure cookie: %s", set)
	}
	if !strings.Contains(set, SessionCookie+"=; Path=/; Max-Age=0") {
		t.Errorf("the old cookie was not dropped: %s", set)
	}

	planted := httptest.NewRequest("GET", "https://docfind.example/", nil)
	planted.TLS = &tls.ConnectionState{}
	planted.AddCookie(&http.Cookie{Name: SessionCookie, Value: "attackers-token"})
	if got := tokenFrom(planted, true); got != "" {
		t.Errorf("over HTTPS the unprefixed cookie was read: %q", got)
	}
	planted.AddCookie(&http.Cookie{Name: HostSessionCookie, Value: "real"})
	if got := tokenFrom(planted, true); got != "real" {
		t.Errorf("token = %q", got)
	}
}

func TestOverPlainHTTPTheCookieIsUnchanged(t *testing.T) {
	r := httptest.NewRequest("POST", "http://docfind.example/api/login", nil)
	rec := httptest.NewRecorder()
	WriteCookie(rec, r, "tok", time.Hour, false)
	set := strings.Join(rec.Header().Values("Set-Cookie"), "\n")
	if !strings.HasPrefix(set, SessionCookie+"=tok") || strings.Contains(set, "__Host-") {
		t.Errorf("plain HTTP cookie: %s", set)
	}
}
