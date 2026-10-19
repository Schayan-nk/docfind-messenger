package find

import (
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Every /api/ route, discovered from the routing table itself rather than
// listed here.
//
// The list this replaces had eight entries and was written when the service
// had eight endpoints. It now has sixty-three, twenty of which reach a
// database connection an administrator configured and thirteen of which run
// SQL that an administrator typed -- and none of the fifty-five added since
// were covered, because nothing made adding one to the list compulsory. A
// route registered outside its guard would have been an unauthenticated
// endpoint that no test complained about.
//
// So the routes are read out of server.go. A route added tomorrow is in this
// test the moment it is registered, and the only way to keep it out is to put
// it in publicAPI below and say why.
var routeRegistration = regexp.MustCompile(`mux\.HandleFunc\("([A-Z]+) (/api/[^"]*)"`)

// publicAPI is every /api/ route that may answer an anonymous caller, and the
// reason it may.
//
// Every one is the sign-in flow itself, which cannot require a session: most
// are how you get one, and /api/me is how the page discovers it has none.
// Adding to this map is how a route is exempted, which means it is a decision
// somebody writes down rather than an omission.
var publicAPI = map[string]string{
	"/api/login": "issues the session; requiring one would be circular",
	// The password's challenge is the credential here, checked by
	// VerifyChallenge; a session cannot be required before one exists.
	"/api/login/second-step": "finishes a two-step sign-in; its challenge token is the credential",
	// Mails the code again for that same challenge, to the account's own
	// address only, rate-limited (twostepmail.go).
	"/api/login/second-step/resend": "mails a sign-in code again; the challenge token is the credential",
	// A passkey sign-in: the options are a random challenge, and the
	// passkey's signature over it is the credential (passkeyroutes.go).
	"/api/login/passkey/options": "a challenge for a passkey sign-in; carries nothing about anybody",
	"/api/login/passkey":         "signs in with a passkey; its signature is the credential",
	"/api/logout":                "ends a session, and must work even with a stale or absent one",
	// A forgotten password (passwordreset.go): the first texts a code to the
	// phone already on the account and answers the same for any name; the
	// second takes that code, which is the credential. Both rate-limited.
	"/api/login/forgot": "texts a reset code to the account's own phone; answers alike for every name",
	"/api/login/reset":  "sets a forgotten password; the texted code is the credential",
	"/api/me":           "how the page asks whether anyone is signed in; answers 'nobody' anonymously",
	"/api/first-admin":  "creates the first account on an empty instance, guarded by there being no users",
}

func discoverAPIRoutes(t *testing.T) map[string][]string {
	t.Helper()
	// server.go sits beside this file, and `go test` runs in the package
	// directory. Reading it is the point: the routing table is the thing being
	// audited, so the test must look at the real one.
	src, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("cannot read the routing table: %v", err)
	}
	routes := map[string][]string{}
	for _, m := range routeRegistration.FindAllStringSubmatch(string(src), -1) {
		method, path := m[1], m[2]
		routes[path] = append(routes[path], method)
	}
	if len(routes) < 40 {
		t.Fatalf("only %d /api/ routes were found in server.go; the registration pattern "+
			"has changed and this test is no longer reading the real routing table", len(routes))
	}
	return routes
}

// No /api/ route answers a caller with no session, except the sign-in flow.
//
// Runtime, not a reading of the source: a guard can be present in the
// registration and still not refuse. Each route is called with a valid Origin
// so that what rejects it is the SESSION check and not the cross-origin one --
// otherwise a route could lose its guard entirely and this test would still
// pass on the CSRF refusal of a POST.
func TestNoAPIRouteAnswersWithoutASession(t *testing.T) {
	s, _, _, _ := accountsTestServer(t)
	routes := discoverAPIRoutes(t)

	paths := make([]string, 0, len(routes))
	for p := range routes {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	checked := 0
	for _, path := range paths {
		if why, ok := publicAPI[path]; ok {
			if strings.TrimSpace(why) == "" {
				t.Errorf("%s is exempted from needing a session with no reason given", path)
			}
			continue
		}
		for _, method := range routes[path] {
			body := ""
			if method == "POST" || method == "PUT" {
				body = "{}"
			}
			rec := asUser(t, s, method, path, "", body)
			if rec.Code != http.StatusForbidden && rec.Code != http.StatusUnauthorized {
				t.Errorf("%s %s answered an anonymous caller with %d; every route that is not "+
					"the sign-in flow must refuse one (401 or 403). If this route is meant to be "+
					"public, add it to publicAPI with the reason.", method, path, rec.Code)
			}
			checked++
		}
	}
	// A test that checked nothing must not pass. The same rule the audit script
	// learned: nought of nought is vacuously true and reads like a clean bill.
	if checked < 40 {
		t.Fatalf("only %d route/method pairs were actually exercised", checked)
	}
	t.Logf("%d route/method pairs refused an anonymous caller", checked)
}

// Every route that is not on the public list is registered inside a guard.
//
// The runtime test above proves a route refuses anonymity; this proves the
// refusal is the permission system rather than something incidental that
// happens to return 403 today -- a handler that fails on a missing parameter,
// say, which would stop refusing the moment the parameter became optional.
func TestEveryAPIRouteIsRegisteredInsideAGuard(t *testing.T) {
	// Wrapped calls rejoined first: see the note on `continuation` in
	// permroutes_test.go for why a per-line scan would otherwise fail a route
	// for being formatted rather than for being ungated.
	src := routingTable(t)
	// requireBuilder and requireAnnouncer are guards like the others: each
	// authenticates first and then checks its own allow-list, so a route
	// wrapped in either is not open. They are named here rather than matched
	// by a "require" prefix because the prefix would also accept something
	// called requireNothing.
	// requirePermission is the only real gate now: requirePerm, requireUser
	// and guard.Require are deleted. The four legacy allow-list wrappers are
	// still guards -- each authenticates and then checks its own list -- and
	// are named rather than matched by a "require" prefix, which would also
	// accept something called requireNothing.
	//
	// NOTE that "requirePerm" as a substring used to match "requirePermission"
	// by accident, so this test passed for the wrong reason during the
	// migration. Spelling it out removes that luck.
	// One gate. The four named-user allow-list wrappers that used to be listed
	// here -- requireBuilder, requireAnnouncer, requireDocsAdmin,
	// requireMailAdmin -- are deleted, and their lists are imported as grants
	// at start-up instead.
	guards := []string{
		"requirePermission", "guard.RequireAuth",
	}

	var ungated []string
	for _, line := range strings.Split(src, "\n") {
		m := routeRegistration.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		path := m[2]
		if _, ok := publicAPI[path]; ok {
			continue
		}
		guarded := false
		for _, g := range guards {
			if strings.Contains(line, g) {
				guarded = true
				break
			}
		}
		if !guarded {
			ungated = append(ungated, strings.TrimSpace(line))
		}
	}
	for _, line := range ungated {
		t.Errorf("this route is registered without a permission guard:\n    %s\n"+
			"  Wrap it in requirePermission -- or, if it is "+
			"genuinely public, add it to publicAPI with the reason.", line)
	}
}

// The routes that reach a configured database connection all demand the
// settings permission, not merely a session.
//
// These are the ones where getting the level wrong costs most: the builder
// runs SQL an administrator typed, against a connection whose password the
// service holds. A viewer who could reach them would inherit both.
// Every builder route is behind the allow-list gate, no exceptions.
