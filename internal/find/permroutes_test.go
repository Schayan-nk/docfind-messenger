package find

import (
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/Schayan-nk/docfind-messenger/internal/auth"
)

// Every /api/ route names a permission, and the permission is really consulted.
//
// WHY THIS TEST IS THE POINT OF THE WHOLE MODEL. A catalogue and a decision
// function are worth nothing if a route forgets to ask. Measured before this
// work: of 133 registered routes, 9 consulted a permission and 51 were gated on
// nothing but "is signed in" -- so a viewer could call the report-builder API.
// Nothing made adding a route to a guard compulsory, so the gap grew quietly
// with every feature.
//
// There are two halves, and neither is sufficient alone:
//
//   - THE SOURCE HALF proves each route is registered inside a named
//     permission, so a new handler cannot be added ungated without this test
//     naming it.
//   - THE RUNTIME HALF proves the permission is actually enforced, by calling
//     every route as somebody signed in who holds nothing and requiring a
//     refusal. A guard can be present in the registration and still not
//     refuse; only calling it proves otherwise.

var apiRegistration = regexp.MustCompile(`mux\.HandleFunc\("([A-Z]+) (/api/[^"]*)"\s*,\s*(.*)$`)

// ownAccountAPI is every route that acts on the caller's OWN account.
//
// These need no permission beyond being signed in, and giving them one would be
// wrong rather than merely strict: a permission is something an administrator
// grants, and "may change my own password" is not theirs to withhold. Taking it
// away would leave somebody signed in with no way to change their password.
var ownAccountAPI = map[string]string{
	"/api/account/sessions":       "shows where I am signed in; mine by definition",
	"/api/account/prefs":          "my own display settings",
	"/api/account/password":       "changing my own password, which nobody may withhold",
	"/api/account/signout-others": "ending my own other sessions",
	"/api/db-state":               "whether the database is down, for the banner every signed-in page shows",
	"/api/account/photo":          "my own profile picture",
	// Every picture I have set, and making an older one current (photos.go);
	// somebody else's only with manage-users, checked in the handler.
	"/api/account/photos":        "the pictures on a profile, as the picture itself is seen",
	"/api/account/photo/current": "making one of my own pictures the current one",
	"/api/app/android":           "which Android app version is on offer, to anybody signed in",
	// Two-step sign-in is protection on MY account; an administrator withholding
	// it would be withholding my own security. Each change asks for the
	// password again (twostep.go).
	"/api/account/two-step":               "whether my own sign-in takes two steps",
	"/api/account/two-step/begin":         "setting up my own second step, after my password again",
	"/api/account/two-step/confirm":       "turning my own second step on with its first code",
	"/api/account/two-step/disable":       "turning my own second step off, with my password and a code",
	"/api/account/two-step/recovery":      "new recovery codes for my own account, after my password again",
	"/api/account/two-step/email/begin":   "the email way for my own account, after my password again",
	"/api/account/two-step/email/confirm": "proving my own address with the code mailed to it",
	"/api/account/two-step/send-code":     "a code mailed to my own address, to confirm a change",
	"/api/account/two-step/sms/begin":     "the SMS way for my own account, after my password again",
	"/api/account/two-step/sms/confirm":   "proving my own phone with the code texted to it",
	// My own phone, for texts to me; refused while it is my second step
	// (phoneroutes.go), so changing it cannot sidestep the step.
	"/api/account/phone": "my own phone number",
	// My own arrangement of the menu; it reorders what I may open, and the
	// pages I add to it are links the pages themselves still guard.
	"/api/account/menu": "my own arrangement of the menu",
	// The entries I took out of my own menu with −; put back with +.
	"/api/account/menu/hidden": "my own menu, what I took out of it",
	// Passkeys are my own sign-in, like my password.
	"/api/account/passkeys":         "the passkeys on my own account",
	"/api/account/passkeys/options": "starting to add a passkey to my own account, after my password again",
	"/api/account/passkeys/remove":  "removing a passkey from my own account, after my password again",
}

// legacyGatedAPI IS NOW EMPTY, and that is the point of keeping it.
//
// It used to list sixteen routes still behind four named-user allow-lists read
// from configuration -- requireDocsAdmin, requireMailAdmin, requireAnnouncer
// and requireBuilder. They were never part of the original 51-route gap; each
// already restricted. What they could not do is express a difference: one
// allow-list gated reading the saved reports, running an arbitrary query,
// saving a report AND editing a database connection, because it was one list.
//
// All four are gone. Their lists are imported as grants at start-up -- see
// legacygrants.go -- so the people they named keep what they had, and every
// one of those routes now names a permission and is exercised by the runtime
// test below like any other.
//
// Kept as an empty map rather than deleted so that the next route somebody
// wants to exempt has an obvious place to go, with the reason beside it.
var legacyGatedAPI = map[string]string{}

// legacyGateNames are the wrappers legacyGatedAPI routes may use. Empty, for
// the same reason.
var legacyGateNames = []string{}

// registration is one route as server.go registers it.
type registration struct {
	Method string
	Path   string
	Guard  string // everything after the pattern, i.e. the wrapped handler
}

// continuation joins a call that was wrapped across lines.
//
// WHY THIS IS NEEDED: a registration long enough to wrap reads as
//
//	mux.HandleFunc("GET /api/permissions",
//	    s.requirePermission(auth.ActionManageUsers, s.handleCatalogue))
//
// and a per-line scan finds the path with an EMPTY guard -- so a properly
// gated route fails the audit purely for being formatted. That is a trap: the
// obvious fix is to rejoin the line rather than to gate the route, and the
// next person to hit it learns that the test is about layout. Joining every
// continuation first makes the audit about the gate.
var continuation = regexp.MustCompile(`,[ \t]*\r?\n[ \t]*`)

// routingTable is server.go with wrapped calls rejoined.
func routingTable(t *testing.T) string {
	t.Helper()
	src, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("cannot read the routing table: %v", err)
	}
	return continuation.ReplaceAllString(string(src), ", ")
}

// routeGuards reads the real routing table out of server.go.
//
// Reading the source is the point: the routing table is the thing being
// audited, so a list written by hand here would be the very thing that went
// stale last time.
func routeGuards(t *testing.T) []registration {
	t.Helper()
	var out []registration
	for _, line := range strings.Split(routingTable(t), "\n") {
		m := apiRegistration.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		out = append(out, registration{Method: m[1], Path: m[2], Guard: m[3]})
	}
	if len(out) < 30 {
		t.Fatalf("only %d /api/ registrations were found; the pattern has changed "+
			"and this test is no longer reading the real routing table", len(out))
	}
	return out
}

// paths is the set of registered paths, for the "exempted but gone" check.
func pathsOf(regs []registration) map[string]bool {
	out := map[string]bool{}
	for _, r := range regs {
		out[r.Path] = true
	}
	return out
}

// THE SOURCE HALF. Every /api/ route is registered inside a named permission,
// or is on one of the three lists -- each of which carries a reason.
func TestEveryAPIRouteNamesACataloguePermission(t *testing.T) {
	regs := routeGuards(t)
	sort.Slice(regs, func(i, j int) bool {
		if regs[i].Path != regs[j].Path {
			return regs[i].Path < regs[j].Path
		}
		return regs[i].Method < regs[j].Method
	})

	named, exempt := 0, 0
	for _, reg := range regs {
		path, guard := reg.Path, reg.Guard
		{
			if strings.Contains(guard, "s.requirePermission(auth.") {
				named++
				continue
			}
			// Not on the catalogue: it must be on a list, with a reason.
			if why, ok := publicAPI[path]; ok {
				if strings.TrimSpace(why) == "" {
					t.Errorf("%s is public with no reason given", path)
				}
				exempt++
				continue
			}
			if why, ok := ownAccountAPI[path]; ok {
				if !strings.Contains(guard, "guard.RequireAuth") {
					t.Errorf("%s is listed as an own-account route but is not gated on "+
						"being signed in: %s", path, guard)
				}
				if strings.TrimSpace(why) == "" {
					t.Errorf("%s is exempt with no reason given", path)
				}
				exempt++
				continue
			}
			if why, ok := legacyGatedAPI[path]; ok {
				onALegacyGate := false
				for _, name := range legacyGateNames {
					if strings.Contains(guard, name) {
						onALegacyGate = true
						break
					}
				}
				if !onALegacyGate {
					t.Errorf("%s is listed as legacy-gated but uses none of %v: %s",
						path, legacyGateNames, guard)
				}
				if strings.TrimSpace(why) == "" {
					t.Errorf("%s is exempt with no reason given", path)
				}
				exempt++
				continue
			}
			t.Errorf("%s is registered without a catalogue permission:\n  %s\n"+
				"Gate it with s.requirePermission(auth.<Something>, ...), or add it to "+
				"publicAPI / ownAccountAPI / legacyGatedAPI with the reason why not.",
				path, strings.TrimSpace(guard))
		}
	}
	if named < 20 {
		t.Fatalf("only %d route/method pairs name a permission; this test is not "+
			"reading what it thinks it is", named)
	}
	t.Logf("%d route/method pairs name a permission, %d are on a documented list", named, exempt)
}

// Nothing is on two lists, which would make the reason ambiguous and let a
// route be exempted twice over by accident.
func TestNoRouteIsExemptedTwice(t *testing.T) {
	lists := map[string]map[string]string{
		"publicAPI":      publicAPI,
		"ownAccountAPI":  ownAccountAPI,
		"legacyGatedAPI": legacyGatedAPI,
	}
	seen := map[string]string{}
	for name, list := range lists {
		for path := range list {
			if other, dup := seen[path]; dup {
				t.Errorf("%s is on both %s and %s", path, other, name)
			}
			seen[path] = name
		}
	}
	// And nothing is exempted that no longer exists, or the lists become a
	// record of routes somebody deleted.
	registered := pathsOf(routeGuards(t))
	for path := range seen {
		if !registered[path] {
			t.Errorf("%s is exempted but is no longer registered; remove it from %s",
				path, seen[path])
		}
	}
}

// THE RUNTIME HALF, AND THE TEETH. Somebody signed in who holds nothing must be
// refused by every gated route.
//
// A guard can be present in the registration and still not refuse -- the
// permission could be one everybody holds, the check could be inverted, the
// catalogue could be missing the ID. Only calling the route proves it.
//
// The user is a viewer with an explicit DENY on every catalogue permission,
// which is why this works without enumerating what a viewer would otherwise
// get: deny beats the template, so the account holds exactly nothing.
func TestSomebodyWhoHoldsNothingIsRefusedEverywhere(t *testing.T) {
	s, _, _, _ := accountsTestServer(t)

	cat := s.Permissions()
	if cat == nil {
		t.Fatal("the test server has no permission catalogue")
	}
	// Deny the lot on the viewer account. Through the store, not by editing
	// what Get returns: that is a copy, deliberately, so editing it would
	// change nothing and this test would pass while proving nothing.
	deny := map[string]auth.GrantState{}
	for _, id := range cat.IDs() {
		deny[id] = auth.GrantDeny
	}
	if err := s.guard.Users.SetGrants("reader", deny); err != nil {
		t.Fatal(err)
	}
	stored, err := s.guard.Users.Get("reader")
	if err != nil {
		t.Fatal(err)
	}
	if got := auth.Effective(cat, stored); len(got) != 0 {
		t.Fatalf("the account still holds %d permission(s): %v", len(got), got)
	}
	token := signFor(t, s.guard.Signer, s.guard.Users, "reader")

	regs := routeGuards(t)
	sort.Slice(regs, func(i, j int) bool {
		if regs[i].Path != regs[j].Path {
			return regs[i].Path < regs[j].Path
		}
		return regs[i].Method < regs[j].Method
	})

	checked, absent := 0, 0
	for _, reg := range regs {
		if _, ok := publicAPI[reg.Path]; ok {
			continue
		}
		if _, ok := ownAccountAPI[reg.Path]; ok {
			continue // mine by definition; a permission would be wrong here
		}
		if _, ok := legacyGatedAPI[reg.Path]; ok {
			continue // refused by its own allow-list, tested where that lives
		}
		// A path with a {placeholder} needs something in it to route.
		target := strings.NewReplacer("{uid}", "1", "{index}", "0", "{id}", "1",
			"{key}", "x").Replace(reg.Path)

		body := ""
		if reg.Method == "POST" || reg.Method == "PUT" || reg.Method == "PATCH" {
			body = "{}"
		}
		rec := asUser(t, s, reg.Method, target, token, body)
		if rec.Code == http.StatusNotFound {
			// Registered in the source but not in THIS server: mail,
			// reporting, the calendar and the assistant are each registered
			// only when configured, and the test server wires none of them.
			// 404 means the route is absent, which says nothing about its
			// guard -- so it is counted separately rather than excused
			// silently.
			absent++
			continue
		}
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s answered %d to somebody who holds no permission at all; "+
				"want 403. The guard is present but not refusing.", reg.Method, target, rec.Code)
		}
		checked++
	}
	// The floor is what stops this passing vacuously if the routing table or
	// the test server ever stops wiring anything at all.
	if checked < 10 {
		t.Fatalf("only %d route/method pairs were actually exercised (%d were absent "+
			"from this server); this test is no longer proving anything", checked, absent)
	}
	t.Logf("%d route/method pairs refused somebody holding nothing; %d not registered "+
		"in this test server", checked, absent)
}

// An administrator reaches the same routes, so the test above is measuring the
// permission and not something that refuses everybody.
//
// Without this, inverting the check or gating every route on an impossible
// permission would make the test above pass and the application unusable.
func TestAnAdministratorIsNotRefusedByTheSameGates(t *testing.T) {
	s, adminTok, _, _ := accountsTestServer(t)
	registered := pathsOf(routeGuards(t))

	// A sample rather than all of them: the ones that answer from local state.
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/users"},
		{"GET", "/api/presence"},
		{"GET", "/api/audit"},
		{"GET", "/api/admin/metrics"},
		{"GET", "/api/admin/security/policy"},
	} {
		if !registered[c.path] {
			t.Errorf("%s is no longer registered; this test is checking a route that does not exist", c.path)
			continue
		}
		rec := asUser(t, s, c.method, c.path, adminTok, "")
		if rec.Code == http.StatusForbidden || rec.Code == http.StatusUnauthorized {
			t.Errorf("%s %s refused an administrator with %d; the gates refuse everybody",
				c.method, c.path, rec.Code)
		}
	}
}
