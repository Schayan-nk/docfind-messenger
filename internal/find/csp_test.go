package find

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// The Content-Security-Policy, and the outage that decided its shape.
//
// These pages carry their script inline, so the policy has to permit that
// somehow. There are two ways: 'unsafe-inline', which permits ANY inline
// script including an injected one, or a hash naming the exact script.
//
// The hash version was written, shipped, and taken back out. It is better
// security and it broke the live site completely: the reports page sat on its
// "loading" placeholder for every user, because the document the browser
// parsed carried a 200,391-byte script while the policy authorised the hash of
// a 204,792-byte one. Something between the server and the browser altered the
// HTML -- and a hash policy does not degrade when that happens, it refuses.
// With no console error, because a blocked script cannot report being blocked.
//
// So the tests below assert 'unsafe-inline' deliberately. That is not a
// standard being lowered by accident; it is a weakness held knowingly, in
// preference to a page that does nothing.
//
// The way back to hashes is to send the policy INSIDE the document, as a
// <meta http-equiv="Content-Security-Policy">, so that the policy and the
// bytes it describes travel together and cannot desynchronise. A stale or
// rewritten document then carries its own matching policy.
// TestNoPageUsesInlineEventHandlers below is kept because it is the
// precondition for that work: a hash cannot cover onclick="".
var pageRoutes = []string{
	"/", "/login", "/dashboard", "/settings", "/evaluation", "/documents", "/builder",
	"/monitoring", "/bpm",
}

func cspOf(t *testing.T, s *Server, path string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s returned %d, so its policy cannot be checked", path, rec.Code)
	}
	return rec.Header().Get("Content-Security-Policy")
}

var scriptSrcOf = regexp.MustCompile(`script-src([^;]*)`)

// Anything with no script of its own keeps the strict policy.
//
// The stylesheet, the icons, the vendored bundles, a document being
// downloaded: none of them has an inline script, so none of them needs the
// relaxation the pages need, and they do not get it.
func TestNonPageResponsesGetTheStrictPolicy(t *testing.T) {
	s, _, _, _ := accountsTestServer(t)
	for _, p := range []string{"/app.css", "/nav.js", "/favicon.ico"} {
		csp := cspOf(t, s, p)
		if !strings.Contains(csp, "script-src 'self';") {
			t.Errorf("%s does not get the strict script-src; it carries no inline script and "+
				"has no reason to permit one. policy: %s", p, csp)
		}
	}
}
