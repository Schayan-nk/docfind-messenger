package find

import (
	"net/http"
	"net/url"
	"strings"
)

// Sending somebody to the sign-in page BEFORE a page is served, not after.
//
// THE COMPLAINT: opening docfind with nothing cached showed the home page --
// empty panels, a greeting with no name -- for a moment, and then jumped to
// the sign-in page. Every page was served to anybody, and it was the page's
// own script that asked /api/me who was there and only then went to /login.
// On a cold start that is a full page load, a stylesheet, a font and a round
// trip later: long enough to see.
//
// The server knows at the first byte whether the request carries a session,
// so it answers a page request without one with a redirect and no page at
// all. The pages' own check stays, for the one case this cannot see -- a
// session that expires while a page is open.
//
// The service worker passes the redirect straight through: it only ever
// stores a 200, and a navigation may be answered with a redirect.

// signedInPage serves page to a signed-in reader and redirects anybody else
// to the sign-in page, carrying where they were going.
func (s *Server) signedInPage(page http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.AuthEnabled() || s.hasSession(r) {
			page(w, r)
			return
		}
		pageRedirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()))
	}
}

// signedOutPage is the other half: the sign-in page, asked for by somebody
// already signed in, sends them on to where they were going instead of
// flashing a password box they do not need.
func (s *Server) signedOutPage(page http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.AuthEnabled() && s.hasSession(r) {
			pageRedirect(w, r, localPath(r.URL.Query().Get("next")))
			return
		}
		page(w, r)
	}
}

func (s *Server) hasSession(r *http.Request) bool {
	if s.guard == nil {
		return false
	}
	_, ok := s.guard.Authenticate(r)
	return ok
}

// pageRedirect answers with a 303 that no cache may keep: the same address
// is a page for one reader and a redirect for the next.
func pageRedirect(w http.ResponseWriter, r *http.Request, to string) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Add("Vary", "Cookie")
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// localPath returns next if it is a path on this server, and "/" otherwise --
// the server-side twin of the sign-in page's safeNext, so a crafted
// ?next=https://elsewhere cannot turn the redirect into a way off the site.
func localPath(next string) string {
	next = strings.TrimSpace(next)
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") {
		return "/"
	}
	u, err := url.Parse(next)
	if err != nil || u.Scheme != "" || u.Host != "" {
		return "/"
	}
	return u.RequestURI()
}
