package auth

import (
	"sort"
	"strings"
	"sync/atomic"
)

// The role templates as an administrator has edited them (asked for on
// 1405/07/12: "edit roles on permissions"). roleTemplates in decide.go stays
// the default; an edited role replaces its default here, whole, and a role
// nobody edited keeps it. Administrators are not a list and are not editable:
// "everything declared" is what lets one grant the rest.
//
// Kept by find/roletemplates.go in app_settings and put in force with
// SetRoleTemplates; read at decision time by templateOf, so an edit changes
// everybody with that role on their next request.

var editedTemplates atomic.Pointer[map[Role][]string]

// Roles an administrator made (asked for on 1405/07/12: "add roles, not only
// مدیر and بیننده"). Each is an id the server minted -- CustomRolePrefix and
// eight hex digits, so it can never be taken for a built-in one -- and a
// Persian name; its template is kept with the edited ones (find/
// roletemplates.go), and a new role starts as a copy of بیننده's. A made
// role is as editable as مدیر and بیننده and holds nothing they could not:
// no administrator-only item, none held only by name (CheckTemplate).
var customRoles atomic.Pointer[map[Role]string]

// CustomRolePrefix begins the id of every role an administrator made.
const CustomRolePrefix = "c-"

// SetCustomRoles puts the made roles in force, id -> name. The map is copied.
func SetCustomRoles(m map[Role]string) {
	cp := make(map[Role]string, len(m))
	for r, n := range m {
		if strings.HasPrefix(string(r), CustomRolePrefix) && strings.TrimSpace(n) != "" {
			cp[r] = strings.TrimSpace(n)
		}
	}
	customRoles.Store(&cp)
}

// CustomRoles is the made roles, id -> name: a copy.
func CustomRoles() map[Role]string {
	out := map[Role]string{}
	if m := customRoles.Load(); m != nil {
		for r, n := range *m {
			out[r] = n
		}
	}
	return out
}

// IsCustomRole reports whether r is a role an administrator made.
func IsCustomRole(r Role) bool {
	if m := customRoles.Load(); m != nil {
		_, ok := (*m)[r]
		return ok
	}
	return false
}

// RoleEditable reports whether r's template may be edited: مدیر, بیننده and
// every made role -- never the administrator, who holds everything declared.
func RoleEditable(r Role) bool {
	return r == RoleManager || r == RoleViewer || IsCustomRole(r)
}

// EditableRoleList is every editable role: the built-in two, then the made
// ones by name.
func EditableRoleList() []Role {
	made := CustomRoles()
	ids := make([]Role, 0, len(made))
	for r := range made {
		ids = append(ids, r)
	}
	sort.Slice(ids, func(i, j int) bool { return made[ids[i]] < made[ids[j]] })
	return append([]Role{RoleManager, RoleViewer}, ids...)
}

// SetRoleTemplates puts edited templates in force; a role absent from m keeps
// its default. The lists are copied. A made role's template is taken by its
// id's shape, so the order the two documents are read in does not matter.
func SetRoleTemplates(m map[Role][]string) {
	out := map[Role][]string{}
	for r, ids := range m {
		if r != RoleManager && r != RoleViewer && !strings.HasPrefix(string(r), CustomRolePrefix) {
			continue
		}
		cp := append([]string(nil), ids...)
		sort.Strings(cp)
		out[r] = cp
	}
	editedTemplates.Store(&out)
}

// DefaultTemplate is a role's template as shipped, before any edit. A made
// role's is بیننده's: that is what it starts as.
func DefaultTemplate(r Role) []string {
	if strings.HasPrefix(string(r), CustomRolePrefix) {
		r = RoleViewer
	}
	return append([]string(nil), roleTemplates[r]...)
}

// IsEdited reports whether a role's template has been edited.
func IsEdited(r Role) bool {
	if m := editedTemplates.Load(); m != nil {
		_, ok := (*m)[r]
		return ok
	}
	return false
}

// templateOf is the role's template in force: the edited one, else the default.
func templateOf(r Role) []string {
	if strings.HasPrefix(string(r), CustomRolePrefix) && !IsCustomRole(r) {
		// A made role that is no longer there holds nothing.
		return nil
	}
	if m := editedTemplates.Load(); m != nil {
		if ids, ok := (*m)[r]; ok {
			return ids
		}
	}
	if IsCustomRole(r) {
		return roleTemplates[RoleViewer]
	}
	return roleTemplates[r]
}

// CheckTemplate says whether ids may be a role's template: every one declared,
// none administrator-only, none held only by name. Returns the offending ID.
func CheckTemplate(cat *Catalogue, ids []string) (string, bool) {
	for _, id := range ids {
		it, ok := cat.Item(id)
		if !ok || !cat.Has(id) || it.AdminOnly || it.Explicit {
			return id, false
		}
	}
	return "", true
}
