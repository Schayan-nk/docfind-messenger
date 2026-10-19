package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// StripPort exists because IIS's Application Request Routing appends the
// client's source port to X-Forwarded-For. An IPv6 address is full of colons
// and must survive unchanged; a port must not, because the throttle keys on
// whatever comes back and a port changes on every connection.
func TestStripPort(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"192.168.77.180:63460", "192.168.77.180"},
		{"192.168.77.180", "192.168.77.180"},
		{"::1", "::1"},
		{"[::1]:443", "::1"},
		{"2001:db8::8a2e:370:7334", "2001:db8::8a2e:370:7334"},
		{"[2001:db8::1]:8080", "2001:db8::1"},
		// Not an address at all: returned as it came, for the caller to log.
		{"unknown", "unknown"},
		{"", ""},
	} {
		if got := StripPort(c.in); got != c.want {
			t.Errorf("StripPort(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// The whole point of the change: behind ARR the throttle and the session list
// must see one client, not one per connection.
func TestClientIPDropsTheProxysPort(t *testing.T) {
	g := &Guard{TrustProxyHeaders: true}

	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:50001"
	r.Header.Set("X-Forwarded-For", "192.168.77.180:63460")
	if got := g.ClientIP(r); got != "192.168.77.180" {
		t.Errorf("single entry: got %q, want 192.168.77.180", got)
	}

	// A chain: the left-most is the client, and it too may carry a port.
	r2 := httptest.NewRequest("GET", "/", nil)
	r2.RemoteAddr = "127.0.0.1:50002"
	r2.Header.Set("X-Forwarded-For", "203.0.113.7:1234, 192.168.77.13:80")
	if got := g.ClientIP(r2); got != "203.0.113.7" {
		t.Errorf("chain: got %q, want 203.0.113.7", got)
	}

	// Two requests from the same client on different sockets must key alike,
	// which is what makes the login throttle count a person rather than a
	// connection.
	same := func(port string) string {
		rr := httptest.NewRequest("GET", "/", nil)
		rr.RemoteAddr = "127.0.0.1:1"
		rr.Header.Set("X-Forwarded-For", "198.51.100.4:"+port)
		return g.ClientIP(rr)
	}
	if a, b := same("1111"), same("2222"); a != b {
		t.Errorf("same client on two sockets keyed differently: %q vs %q", a, b)
	}
}

// Without the flag the header is ignored entirely, so a caller cannot choose
// what the session list says about them.
func TestClientIPIgnoresTheHeaderWhenNotTrusted(t *testing.T) {
	g := &Guard{TrustProxyHeaders: false}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "203.0.113.9:44321"
	r.Header.Set("X-Forwarded-For", "10.0.0.1")
	if got := g.ClientIP(r); got != "203.0.113.9" {
		t.Errorf("got %q, want the socket peer 203.0.113.9", got)
	}
}

var _ = http.Handler(nil)
