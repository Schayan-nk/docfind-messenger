package auth

import (
	"net/http"
	"time"
)

// SessionCookie carries the signed token over plain HTTP.
const SessionCookie = "docfind_token"

// HostSessionCookie carries it over HTTPS.
//
// The "__Host-" prefix is a promise the BROWSER enforces: a cookie by this
// name is accepted only if it is Secure, has Path=/ and names no Domain -- so
// it can only ever have been set by this exact host. Without it, any other
// site under the same parent domain (app.example.ir, office.example.ir) could
// set a docfind_token for the whole domain, and a browser holding two cookies
// of one name sends both: somebody controlling a sibling site could sign a
// person into an account of their choosing. Only over HTTPS, because the
// prefix requires Secure and a plain-HTTP deployment could not set it at all.
const HostSessionCookie = "__Host-" + SessionCookie

// WriteCookie sets the token cookie on a response.
//
// Secure whenever the request arrived over TLS, so the token is never sent
// back over plain HTTP. HttpOnly keeps it away from page scripts, which is
// the main reason a cookie beats localStorage for this. SameSite=Lax stops
// another site from silently making authenticated requests with it.
func WriteCookie(w http.ResponseWriter, r *http.Request, token string, maxAge time.Duration, trustProxy bool) {
	secure := IsTLS(r, trustProxy)
	name := SessionCookie
	if secure {
		name = HostSessionCookie
		// The unprefixed cookie from before the switch is dropped, so a
		// browser does not go on carrying two sessions.
		expire(w, SessionCookie, true)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(maxAge.Seconds()),
	})
}

// ClearCookie removes the token cookie, under either name.
//
// The flags have to match the ones the cookie was set with, or the browser
// treats it as a different cookie and the original survives the sign-out.
func ClearCookie(w http.ResponseWriter, r *http.Request, trustProxy bool) {
	secure := IsTLS(r, trustProxy)
	expire(w, SessionCookie, secure)
	if secure {
		expire(w, HostSessionCookie, true)
	}
}

func expire(w http.ResponseWriter, name string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// sessionCookieValue is the token from the cookie this connection uses.
//
// Over HTTPS ONLY the host-bound cookie is read. Reading the plain name as a
// fallback would reopen the hole the prefix closes: a sibling site could plant
// docfind_token for somebody signed out, and they would find themselves in
// the planter's account. The cost is paid once -- whoever signed in over
// HTTPS before this change signs in again.
func sessionCookieValue(r *http.Request, tls bool) string {
	name := SessionCookie
	if tls {
		name = HostSessionCookie
	}
	if c, err := r.Cookie(name); err == nil && c.Value != "" {
		return c.Value
	}
	return ""
}

// IsTLS reports whether the request reached us over HTTPS, including when a
// reverse proxy terminated TLS and said so.
//
// X-Forwarded-Proto is only believed when a proxy is configured, for the same
// reason X-Forwarded-For is: on a directly reachable listener anyone can send
// that header. An audit against a running instance confirmed the effect --
// a plain HTTP request carrying "X-Forwarded-Proto: https" got a Secure
// cookie, which the browser then refuses to send back over HTTP, quietly
// breaking that person's session.
func IsTLS(r *http.Request, trustProxy bool) bool {
	if r.TLS != nil {
		return true
	}
	return trustProxy && r.Header.Get("X-Forwarded-Proto") == "https"
}
