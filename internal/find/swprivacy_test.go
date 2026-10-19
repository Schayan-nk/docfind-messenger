package find

import (
	"net/http"
	"strings"
	"testing"
)

// What a browser keeps for somebody, and what it lets go of when they leave.
//
// A phone or a front-desk PC passes between people. The API was already
// no-store and the worker never touched /api/, but the worker kept every page
// it was shown under its full address -- a list of which customer, report and
// case the last person opened -- and signing out left localStorage (starred
// mail, the cases already announced) for the next person.

// Signing out tells the browser to drop the origin's cache and storage, and
// NOT its cookies -- that directive reaches every site on the registrable
// domain, and would sign somebody out of the automation system next door.
func TestSigningOutClearsWhatTheBrowserKept(t *testing.T) {
	s, admin, _, _ := accountsTestServer(t)
	rec := asUser(t, s, "POST", "/api/logout", admin, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("sign-out answered %d", rec.Code)
	}
	csd := rec.Header().Get("Clear-Site-Data")
	for _, want := range []string{`"cache"`, `"storage"`} {
		if !strings.Contains(csd, want) {
			t.Errorf("Clear-Site-Data = %q, missing %s", csd, want)
		}
	}
	if strings.Contains(csd, "cookies") || strings.Contains(csd, "*") {
		t.Errorf("Clear-Site-Data = %q clears cookies, which reach beyond this origin", csd)
	}
}

// The worker keeps the application and nothing else.
func TestTheWorkerKeepsOnlyTheShell(t *testing.T) {
	js := string(serviceWorkerJS)
	fetchAt := strings.Index(js, "addEventListener('fetch'")
	if fetchAt < 0 {
		t.Fatal("sw.js has no fetch handler")
	}
	body := js[fetchAt:]
	if end := strings.Index(body, "// ===="); end > 0 {
		body = body[:end]
	}
	apiAt := strings.Index(body, "url.pathname.startsWith('/api/')) return")
	respondAt := strings.Index(body, "event.respondWith(")
	if apiAt < 0 || respondAt < 0 || apiAt > respondAt {
		t.Error("the fetch handler no longer leaves /api/ alone before answering")
	}
	for _, must := range []struct{ code, why string }{
		{"SHELL_PATHS.has(url.pathname)", "only the shell's own paths are kept"},
		{"c.put(url.pathname, copy)", "kept under the path, not the address with its query"},
		{"!res.redirected", "a redirect to the sign-in page is not kept under the page's name"},
		{"no-store", "a response the server marked no-store is not kept"},
		{"url.pathname === '/api/logout'", "signing out empties the cache as it goes by"},
	} {
		if !strings.Contains(body, must.code) {
			t.Errorf("sw.js fetch handler lost %q: %s", must.code, must.why)
		}
	}
	if strings.Contains(body, "c.put(req,") {
		t.Error("the worker stores responses under the full request again")
	}
}

// The page does the same on the way out, for a browser that ignores
// Clear-Site-Data -- and it is the shared sign-out wrapper that does it, so no
// page can forget.
func TestThePageForgetsThisBrowserOnSignOut(t *testing.T) {
	js := string(navJS)
	at := strings.Index(js, "async function forgetThisBrowser()")
	if at < 0 {
		t.Fatal("nav.js has no forgetThisBrowser")
	}
	fn := js[at:]
	if end := strings.Index(fn, "\n  }\n"); end > 0 {
		fn = fn[:end]
	}
	for _, must := range []string{"localStorage.clear()", "sessionStorage.clear()", "caches.delete"} {
		if !strings.Contains(fn, must) {
			t.Errorf("forgetThisBrowser does not call %s", must)
		}
	}
	wrap := strings.Index(js, "o.onSignOut = async () => {")
	call := strings.Index(js, "await forgetThisBrowser();")
	orig := strings.Index(js, "original();")
	if wrap < 0 || call < wrap || orig < call {
		t.Error("the sign-out wrapper does not forget this browser before signing out")
	}
}
