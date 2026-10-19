package auth

import "sort"

// The one place that decides whether somebody may do something.
//
// THREE RULES, AND THE ORDER IS THE DESIGN:
//
//  1. A DENY ON THE ACCOUNT WINS over everything, including the role template
//     and including admin. Otherwise "everything except managing users"
//     cannot be expressed for an administrator, and somebody would express it
//     by taking their admin away instead.
//  2. AN ID NOT IN THE CATALOGUE IS DENIED, to everyone but an administrator.
//     This is the rule that earns its keep: a route whose permission was never
//     declared fails CLOSED. Somebody complains within the hour and it gets
//     declared. The opposite failure -- an undeclared route being open to
//     everyone -- is silent, and is how the 51-route gap happened.
//  3. OTHERWISE an explicit allow, then the role template, then no.
//
// Roles survive only as templates. Nothing below consults the role except to
// look up its default set, so there is no second code path where being a
// manager means something on its own.

// GrantState is what an account says about one permission.
type GrantState string

const (
	// GrantAllow adds a permission the template does not give.
	GrantAllow GrantState = "allow"
	// GrantDeny removes one the template does give, and beats everything.
	GrantDeny GrantState = "deny"
)

// Valid reports whether a state is one of the two.
func (g GrantState) Valid() bool { return g == GrantAllow || g == GrantDeny }

// roleTemplates are the default grant sets. Applying a template writes no
// grants: the template is consulted at decision time, so changing a template
// changes everybody who inherits it, which is the point of a template.
//
// ADMIN IS NOT A LIST. It is "everything declared", computed from the
// catalogue, so a newly declared permission is available to an administrator
// the moment it exists -- otherwise every new route would need a line added
// here and would be broken for everybody until somebody remembered.
// NO TEMPLATE LISTS AN EXPLICIT ITEM: those are held by name, per account.
var roleTemplates = map[Role][]string{
	RoleManager: {PageChat, PageSettings, ActionNotifications},
	RoleViewer:  {PageChat, PageSettings, ActionNotifications},
}

// TemplateFor returns the default grant set for a role.
//
// A viewer or manager gets the list above. An administrator gets every
// declared permission except the explicit ones, which is why the catalogue
// is a parameter.
func TemplateFor(cat *Catalogue, r Role) []string {
	if r == RoleAdmin {
		// Everything EXCEPT the explicit items, which no template gives: they
		// belong to named people, and an administrator holding one by template
		// is exactly what this excludes.
		all := cat.IDs()
		out := make([]string, 0, len(all))
		for _, id := range all {
			if it, ok := cat.Item(id); ok && it.Explicit {
				continue
			}
			out = append(out, id)
		}
		return out
	}
	// Explicit items are filtered here too, not only for admin. The list above
	// is meant not to contain any, and this makes that a property of the code
	// rather than of somebody remembering: TemplateFor is what the editing
	// screen labels "the role default", so a template naming an item Can
	// refuses would show a permission as inherited that nobody actually holds.
	tpl := templateOf(r)
	out := make([]string, 0, len(tpl))
	for _, id := range tpl {
		if it, ok := cat.Item(id); ok && it.Explicit {
			continue
		}
		out = append(out, id)
	}
	return out
}

// templateHas is the membership test, without building a slice per request.
func templateHas(cat *Catalogue, r Role, id string) bool {
	if r == RoleAdmin {
		return cat.Has(id)
	}
	for _, have := range templateOf(r) {
		if have == id {
			return true
		}
	}
	return false
}

// Can decides whether u may do id.
//
// A nil user is denied everything: this is called behind the sign-in check,
// and a nil here means a bug in the wiring rather than an anonymous visitor.
// A disabled account is denied everything too, so disabling somebody takes
// effect even where a stale session survives.
func Can(cat *Catalogue, u *User, id string) bool {
	if u == nil || u.Disabled || id == "" {
		return false
	}

	// 0. AN EMERGENCY SESSION IS RESTRICTED. It was issued from the snapshot
	// while the database was down, and the snapshot may be missing a change
	// made since -- a role reduced, a grant withdrawn. So it keeps what its
	// role and grants allow for READING, and loses everything that changes
	// who may do what or that is held only by name: administrator-only items
	// and explicit ones, and anything undeclared, administrators included.
	if u.Emergency {
		it, known := cat.Item(id)
		if !cat.Has(id) || !known || it.AdminOnly || it.Explicit {
			return false
		}
	}

	// 1. An explicit deny beats everything, admin included.
	if state, ok := u.Grants[id]; ok && GrantState(state) == GrantDeny {
		return false
	}

	// 2. Undeclared fails closed, except for an administrator -- who would
	// otherwise be locked out of a route somebody forgot to declare, with no
	// way to grant themselves back in.
	if !cat.Has(id) {
		return u.Role == RoleAdmin
	}

	it, known := cat.Item(id)

	// An admin-only item is never reachable by an override. These are the
	// permissions that would let somebody grant themselves the rest.
	if known && it.AdminOnly {
		return u.Role == RoleAdmin
	}

	// 3. An explicit allow.
	if state, ok := u.Grants[id]; ok && GrantState(state) == GrantAllow {
		return true
	}

	// AN EXPLICIT ITEM IS HELD ONLY BY THAT ALLOW -- no template gives it,
	// including an administrator's. This is how the catalogue says "belongs to
	// named people": the report builder was an allow-list of usernames
	// precisely because the requirement was "these two and no one else, not
	// even other administrators", and a rule of "an administrator holds
	// everything declared" would silently overturn that.
	if known && it.Explicit {
		return false
	}

	// 4. Otherwise the role template.
	return templateHas(cat, u.Role, id)
}

// Effective is every permission u actually holds, sorted.
//
// For /api/me, so a page can hide what the person cannot use. HIDING IS
// COSMETIC: the server check is the gate, and this set exists to stop people
// clicking things that will refuse them, not to enforce anything.
func Effective(cat *Catalogue, u *User) []string {
	// cat may be nil during startup, before the catalogue is installed.
	// Nil-safe rather than nil-checked at every call site, for the same
	// reason Can is: this is security code, and a panic here would be served
	// as a 500 on a page that should simply have shown less.
	if u == nil || u.Disabled || cat == nil {
		return nil
	}
	out := make([]string, 0, len(cat.items))
	for _, it := range cat.items {
		if Can(cat, u, it.ID) {
			out = append(out, it.ID)
		}
	}
	sort.Strings(out)
	return out
}

// SetGrant records an override, or clears it back to the template.
//
// An empty state CLEARS, which is the third state the editing screen needs:
// "allow", "deny" and "whatever the role says". Without a way back to inherit,
// the first click on a checkbox would pin that permission for ever and a later
// change to the template would silently not reach that person.
func (u *User) SetGrant(id string, state GrantState) {
	if u == nil || id == "" {
		return
	}
	if state == "" {
		delete(u.Grants, id)
		if len(u.Grants) == 0 {
			// nil rather than an empty map, so the JSON omits the field and
			// an account with no overrides looks like one.
			u.Grants = nil
		}
		return
	}
	if !state.Valid() {
		return
	}
	if u.Grants == nil {
		u.Grants = map[string]string{}
	}
	u.Grants[id] = string(state)
}

// GrantOf returns the override on this account, or "" for inherit.
func (u *User) GrantOf(id string) GrantState {
	if u == nil {
		return ""
	}
	return GrantState(u.Grants[id])
}
