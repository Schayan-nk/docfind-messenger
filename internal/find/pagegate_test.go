package find

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// The complaint: on a cold start the home page appeared, empty, before the
// jump to sign-in. A page request with no session now gets a redirect and no
// page at all.
func TestASignedOutVisitorGetsNoPageOnlyARedirect(t *testing.T) {
	s, _, _, _ := accountsTestServer(t)
	for _, path := range []string{"/messages", "/settings", "/settings/account", "/messages?x=1"} {
		rec := asUser(t, s, "GET", path, "", "")
		if rec.Code != http.StatusSeeOther {
			t.Errorf("%s answered %d, want 303", path, rec.Code)
			continue
		}
		loc := rec.Header().Get("Location")
		if !strings.HasPrefix(loc, "/login?next=") {
			t.Errorf("%s redirected to %q", path, loc)
		}
		if strings.Contains(rec.Body.String(), "<main") {
			t.Errorf("%s sent the page along with the redirect", path)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
			t.Errorf("%s redirect is cacheable: %q", path, cc)
		}
	}
	// Where they were going survives the trip.
	rec := asUser(t, s, "GET", "/messages?x=1", "", "")
	if loc := rec.Header().Get("Location"); loc != "/login?next=%2Fmessages%3Fx%3D1" {
		t.Errorf("next = %q", loc)
	}
}

func TestASignedInReaderGetsThePage(t *testing.T) {
	s, _, viewer, _ := accountsTestServer(t)
	rec := asUser(t, s, "GET", "/messages", viewer, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<html") {
		t.Errorf("the messages page answered %d", rec.Code)
	}
}

// The other direction: the sign-in page does not flash at somebody already
// signed in, and its ?next= cannot send them off the site.
func TestTheSignInPageSendsASignedInReaderOn(t *testing.T) {
	s, _, viewer, _ := accountsTestServer(t)
	for next, want := range map[string]string{
		"/messages":             "/messages",
		"https://evil.example/": "/",
		"//evil.example/x":      "/",
		"/\\evil.example":       "/",
		"":                      "/",
	} {
		rec := asUser(t, s, "GET", "/login?next="+url.QueryEscape(next), viewer, "")
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != want {
			t.Errorf("next=%q: %d to %q, want %q", next, rec.Code, rec.Header().Get("Location"), want)
		}
	}
	if rec := asUser(t, s, "GET", "/login", "", ""); rec.Code != http.StatusOK {
		t.Errorf("the sign-in page answered %d to somebody signed out", rec.Code)
	}
}
