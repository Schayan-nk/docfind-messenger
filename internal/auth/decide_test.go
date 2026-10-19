package auth

import (
	"testing"
)

// The access model.
//
// This is the file where a mistake is a security hole rather than a bug, so
// each rule is stated as its own test with the reason it exists. The three
// that matter most: a deny beats everything including admin, an undeclared
// permission fails CLOSED, and clearing an override returns to inheriting
// rather than to denying.

func user(role Role, grants map[string]string) *User {
	return &User{Username: "u", Role: role, Grants: grants}
}

// THE RULE THAT EARNS ITS KEEP. A permission nobody declared is denied, so a
// route whose permission was never added to the catalogue fails CLOSED.
// Somebody complains within the hour. The opposite failure is silent, and is
// how 51 routes came to be gated on nothing but "signed in".
func TestAnUndeclaredPermissionIsDeniedRatherThanOpen(t *testing.T) {
	cat := NewCatalogue()
	for _, role := range []Role{RoleViewer, RoleManager} {
		if Can(cat, user(role, nil), "action:invented_yesterday") {
			t.Errorf("%s was allowed an undeclared permission", role)
		}
		// Even an explicit allow cannot grant something that does not exist:
		// otherwise a typo in the catalogue plus a typo in a grant would
		// quietly agree with each other.
		u := user(role, map[string]string{"action:invented_yesterday": string(GrantAllow)})
		if Can(cat, u, "action:invented_yesterday") {
			t.Errorf("%s was granted an undeclared permission by an override", role)
		}
	}
	// An administrator is the exception, or a forgotten declaration would
	// lock out the only person who could fix it.
	if !Can(cat, user(RoleAdmin, nil), "action:invented_yesterday") {
		t.Error("an administrator was locked out of an undeclared route")
	}
	// But a deny still beats even that.
	locked := user(RoleAdmin, map[string]string{"action:invented_yesterday": string(GrantDeny)})
	if Can(cat, locked, "action:invented_yesterday") {
		t.Error("a deny did not beat the admin fallback")
	}
}

// AN ADMIN-ONLY ITEM IS NOT REACHABLE BY AN OVERRIDE. These are the
// permissions that would let somebody grant themselves the rest, so granting
// one to a manager has to be impossible rather than merely discouraged.
func TestAdminOnlyItemsCannotBeGrantedAway(t *testing.T) {
	cat := NewCatalogue()
	for _, id := range []string{ActionManageUsers, ActionManageSettings} {
		it, ok := cat.Item(id)
		if !ok {
			t.Fatalf("%s is not in the catalogue", id)
		}
		if !it.AdminOnly {
			t.Errorf("%s is not marked admin-only; it can be granted to anybody", id)
			continue
		}
		u := user(RoleManager, map[string]string{id: string(GrantAllow)})
		if Can(cat, u, id) {
			t.Errorf("a manager was granted %s by an override", id)
		}
	}
}
