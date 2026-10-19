package find

import (
	"net/http"

	"github.com/Schayan-nk/docfind-messenger/internal/auth"
)

// The one place a route is gated on a permission.
//
// ONE CHOKEPOINT, AND NOTHING ELSE MAY DECIDE. Before this there were four
// ways a route could be protected -- requirePerm with one of five coarse
// permissions, requireUser (signed in, nothing more), a hardcoded docsAdmins
// allowlist, and requireMailAdmin -- and a fifth way, which was the common
// one: nothing at all. Measured at the time: of 133 registered routes, 9
// consulted a permission and 51 were gated on being signed in and no more.
// Four mechanisms is how the fifth one goes unnoticed.
//
// THE CATALOGUE IS SWAPPED, NOT MUTATED. It carries one item per report and
// the report catalogue is reloaded at runtime, so it is held behind an atomic
// pointer and replaced wholesale. Mutating a map that every request reads
// would be a data race in the access model, which is the worst place in the
// application to have one.

// UsePermissions installs the catalogue. Called again whenever the report
// catalogue is reloaded, so a new report becomes grantable without a restart.
//
// Until this is called there is no catalogue, and Can then denies everything
// to everybody except an administrator -- which is the correct way round for
// the few milliseconds of startup before the reports are read.
func (s *Server) UsePermissions(cat *auth.Catalogue) { s.perms.Store(cat) }

// Permissions is the catalogue in force, or nil before one is installed.
func (s *Server) Permissions() *auth.Catalogue { return s.perms.Load() }

// can decides whether the caller of r may do id.
//
// The single decision function. Every gate below goes through it, and so does
// every place a page is trimmed to what somebody may use, so the server's
// answer and the page's answer cannot drift apart.
func (s *Server) can(r *http.Request, id string) bool {
	if !s.AuthEnabled() {
		// No accounts configured: the deployment is gated by the shared token
		// instead, and there is nobody to hold a permission. Answering true
		// here keeps the single-user and kiosk deployments working exactly as
		// they did, and the token check has already happened by this point.
		return true
	}
	return auth.Can(s.perms.Load(), auth.UserFrom(r), id)
}

// requirePermission gates a handler on a catalogue permission.
//
// Replaces bare requireUser on every route that is not genuinely public. When
// accounts are not in use it falls back to the shared-token check, which is
// what requirePerm and requireUser both already did -- the deployment without
// accounts is gated at the door instead.
func (s *Server) requirePermission(id string, next http.HandlerFunc) http.HandlerFunc {
	if !s.AuthEnabled() {
		return s.guardToken(next)
	}
	return s.guard.RequireAuth(func(w http.ResponseWriter, r *http.Request) {
		if !s.can(r, id) {
			forbidden(w)
			return
		}
		next(w, r)
	})
}

// forbidden is the one refusal, in Persian, with nothing in it about what was
// being protected. A refusal that names the permission tells somebody probing
// which route is worth probing.
func forbidden(w http.ResponseWriter) {
	http.Error(w, `{"error":"برای این کار دسترسی ندارید."}`, http.StatusForbidden)
}
