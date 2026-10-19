package auth

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Schayan-nk/docfind-messenger/internal/obs"
)

type ctxKey int

const (
	userKey ctxKey = iota
	sidKey
)

// Guard ties the account store and the token signer to HTTP handlers.
type Guard struct {
	Users    *Store
	Signer   *Signer
	Throttle *Throttle
	// Sessions records where each account is signed in, for the account screen
	// and for showing an administrator who is using the application. It never
	// takes part in deciding whether a request is authenticated -- see the
	// note on the type -- so it may be nil and everything still works.
	Sessions *Sessions
	// TrustProxyHeaders makes the client IP come from X-Forwarded-For.
	// Off by default: when the app is reachable directly, a caller can put
	// whatever they like in that header and defeat the login throttle by
	// pretending to be a new address on every attempt. Turn it on only when
	// a reverse proxy is genuinely in front and is rewriting the header.
	TrustProxyHeaders bool
	// policy is the sign-in policy (policy.go); the default until SetPolicy.
	policy policyHolder
}

// Policy is the sign-in policy in force.
func (g *Guard) Policy() Policy { return g.policy.get() }

// SetPolicy puts a sign-in policy in force, the throttle's lockout included.
func (g *Guard) SetPolicy(p Policy) {
	g.policy.set(p)
	p = g.policy.get()
	g.Throttle.Configure(p.LockoutAttempts, time.Duration(p.LockoutMinutes)*time.Minute, p.LockoutDoubles)
}

// UserFrom returns the signed-in user carried on a request, if any.
func UserFrom(r *http.Request) *User {
	u, _ := r.Context().Value(userKey).(*User)
	return u
}

// WithUser attaches a user to a request context.
//
// The counterpart of UserFrom, and exported for the same reason it is: a
// caller that can read the user off a request should be able to construct one,
// which is what lets a permission decision be tested without minting a token
// and going round through HTTP.
func WithUser(r *http.Request, u *User) *http.Request {
	// Named on the request's log record as well. This is the one place every
	// authenticated request passes through, so it is the one place that needs
	// to say who made it -- the access line is written further out, by code
	// that never sees the user.
	if u != nil {
		obs.SetUser(r.Context(), u.Username)
	}
	return r.WithContext(context.WithValue(r.Context(), userKey, u))
}

// SessionIDFrom returns the identifier of the sign-in this request belongs to,
// or "" for a token minted before sessions were recorded.
func SessionIDFrom(r *http.Request) string {
	s, _ := r.Context().Value(sidKey).(string)
	return s
}

// Authenticate resolves the request's token to a live user and attaches it.
//
// Two checks, and both matter. The signature proves the token is ours and has
// not expired. The account is then re-read and its token version compared, so
// a token minted before someone was disabled, demoted or had their password
// changed is refused -- that comparison is the whole reason a signed token can
// be withdrawn here at all.
func (g *Guard) Authenticate(r *http.Request) (*http.Request, bool) {
	token := tokenFrom(r, IsTLS(r, g.TrustProxyHeaders))
	if token == "" {
		return r, false
	}
	claims, err := g.Signer.Verify(token)
	if err != nil {
		return r, false
	}
	u, err := g.Users.Get(claims.Subject)
	if err != nil || u.Disabled {
		return r, false
	}
	if claims.Version != u.TokenVersion {
		return r, false
	}
	// An account still on its temporary password is signed in for one thing
	// only: choosing its own. Everything else answers as if signed out, which
	// sends the page back to the sign-in screen, and that screen asks for the
	// new password (it reads update_required from /api/me).
	if u.MustChangePassword && !mustChangeAllows(r) {
		return r, false
	}
	// The same for a policy that requires two steps, until the account has
	// one: the sign-in page offers the setup (two_step_required on /api/me).
	if g.policy.get().RequireTwoStep && !HasTwoStep(u) && !mustChangeAllows(r) && !twoStepSetupAllows(r) {
		return r, false
	}
	// Carried from the token onto this request's copy of the account, where
	// Can reads it. Never stored: it describes the session, not the person.
	u.Emergency = claims.Emergency
	// Recorded only after every check has passed, so a rejected token never
	// appears in the list of places somebody is signed in.
	g.Sessions.Touch(claims.SID, u.Username, r, g.TrustProxyHeaders)
	r = r.WithContext(context.WithValue(r.Context(), sidKey, claims.SID))
	return WithUser(r, u), true
}

// mustChangeAllows is what an account that must change its password may
// still reach: who it is, the change, and signing out.
func mustChangeAllows(r *http.Request) bool {
	switch r.Method + " " + r.URL.Path {
	case "GET /api/me", "POST /api/account/password", "POST /api/logout":
		return true
	}
	return false
}

// twoStepSetupAllows is what setting up a second step needs: the two-step
// card's routes and the passkey card's.
func twoStepSetupAllows(r *http.Request) bool {
	p := r.URL.Path
	if strings.Contains(p, "..") {
		return false
	}
	switch {
	case r.Method == http.MethodGet && (p == "/api/account/two-step" || p == "/api/account/passkeys"):
		return true
	case r.Method == http.MethodPost && (strings.HasPrefix(p, "/api/account/two-step/") ||
		p == "/api/account/passkeys" || p == "/api/account/passkeys/options"):
		return true
	}
	return false
}

// tokenFrom reads the token from the session cookie, falling back to an
// Authorization header.
//
// The cookie is the primary transport on purpose. The obvious alternative --
// keeping the token in localStorage and attaching it by script -- puts it
// somewhere any injected script on the page can read it. An HttpOnly cookie
// cannot be read by page scripts at all, which is the stronger position for a
// browser application. The header is accepted so non-browser callers and
// scripted exports have a way in.
func tokenFrom(r *http.Request, tls bool) string {
	if v := sessionCookieValue(r, tls); v != "" {
		return v
	}
	if h := r.Header.Get("Authorization"); h != "" {
		if after, found := strings.CutPrefix(h, "Bearer "); found {
			return strings.TrimSpace(after)
		}
	}
	return ""
}

// RequireAuth rejects anyone not signed in.
func (g *Guard) RequireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r2, ok := g.Authenticate(r)
		if !ok {
			unauthorized(w)
			return
		}
		next(w, r2)
	}
}

// unauthorized is the one place a missing session is refused.
func unauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	http.Error(w, `{"error":"برای ادامه وارد شوید."}`, http.StatusUnauthorized)
}

// ClientIP returns the address a request came from.
//
// The left-most X-Forwarded-For entry is the original client, which is correct
// for a front that appends (IIS/ARR, nginx), so the client it saw is first. SECURITY: this is only safe while the front overwrites or
// strips any client-supplied X-Forwarded-For, and while the origin is reachable
// ONLY through that front -- otherwise a caller can inject a fake left-most
// entry and forge the per-IP throttle key and the audit IP. Behind a CDN, both
// must hold: firewall the origin to the CDN's ranges, and have the CDN strip
// inbound X-Forwarded-For.
func (g *Guard) ClientIP(r *http.Request) string {
	if g.TrustProxyHeaders {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			// Left-most entry is the original client; the rest are proxies.
			if i := strings.IndexByte(xff, ','); i > 0 {
				return StripPort(strings.TrimSpace(xff[:i]))
			}
			return StripPort(strings.TrimSpace(xff))
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// StripPort drops a port from an address, leaving the address alone when
// there is not one.
//
// The specification says an X-Forwarded-For entry is an address, and most
// proxies send exactly that -- but IIS's Application Request Routing appends
// the client's source port, so behind it the header reads
// "192.168.77.180:63460". Taken verbatim that goes in the session list as the
// person's address, which reads as noise, and -- the part that matters -- it
// becomes the key the login throttle counts against. A port changes on every
// connection, so the throttle would have been counting attempts per socket
// rather than per client, and a caller could have retried indefinitely.
//
// Parsed as an address first: an IPv6 address is full of colons and must not
// be mistaken for a host:port pair. "::1" is returned unchanged, "[::1]:443"
// becomes "::1".
func StripPort(addr string) string {
	if addr == "" {
		return addr
	}
	if net.ParseIP(addr) != nil {
		return addr
	}
	if host, _, err := net.SplitHostPort(addr); err == nil && host != "" {
		return host
	}
	return addr
}

// SameOrigin reports whether a state-changing request came from this site.
//
// SameSite=Lax on the session cookie already stops most cross-site posting,
// but it is a browser-side promise; checking the Origin header server-side
// means a browser that does not honour it, or a request that never went
// through one, still cannot ride a signed-in session.
//
// FETCH METADATA FIRST. Every current browser labels a request with where it
// came from; "cross-site" is the one label a legitimate request from this
// application never carries, so it is refused whatever the other headers say.
//
// AND A REQUEST WITH NO SIGNAL AT ALL -- no Origin, no Referer, no
// Sec-Fetch-Site -- is refused when it rides a session COOKIE. A browser sends
// at least one of the three with every state-changing request it makes, so a
// cookie arriving with none of them is exactly the shape a forged request
// takes once the attacker's page has suppressed the Referer. Allowed as
// before when the caller authenticates with a bearer token: nothing sends
// that automatically, so there is nothing to forge.
func SameOrigin(r *http.Request) bool {
	site := r.Header.Get("Sec-Fetch-Site")
	if site == "cross-site" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		// No Origin: some older clients. Fall back to Referer when present.
		ref := r.Header.Get("Referer")
		if ref == "" {
			if site == "same-origin" || site == "none" {
				return true
			}
			return !ridesCookie(r)
		}
		u, err := url.Parse(ref)
		if err != nil {
			return false
		}
		return strings.EqualFold(u.Host, r.Host)
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

// ridesCookie reports whether a request would be authenticated by a session
// cookie rather than an explicit bearer token.
func ridesCookie(r *http.Request) bool {
	if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		return false
	}
	return sessionCookieValue(r, false) != "" || sessionCookieValue(r, true) != ""
}
