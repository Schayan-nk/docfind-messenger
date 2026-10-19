package find

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"

	"github.com/Schayan-nk/docfind-messenger/internal/audit"
	"github.com/Schayan-nk/docfind-messenger/internal/auth"
)

// The API behind the permission matrix on the settings page.
//
// Three routes, and the split is deliberate:
//
//   - GET  /api/permissions            the catalogue, grouped, plus the role
//     templates -- what CAN be granted.
//   - GET  /api/permissions/user       one account's overrides and what they
//     effectively hold -- both, because the editor has three states to draw.
//   - PUT  /api/permissions/user       replace one account's overrides.
//
// THE EDITOR NEEDS THE OVERRIDES AND THE EFFECT, not one or the other. An
// effective set alone cannot tell "denied" from "the role template does not
// grant it", and those are different boxes with different consequences: the
// first survives a template change and the second does not.

// handlePermissionCatalogue returns what may be granted.
func (s *Server) handlePermissionCatalogue(w http.ResponseWriter, r *http.Request) {
	cat := s.Permissions()
	if cat == nil {
		writeJSON(w, map[string]any{"groups": []any{}, "templates": map[string]any{}})
		return
	}
	// The templates travel too, so the editor can show "this is the role
	// default" beside each box and offer to reset a group back to it.
	templates := map[string][]string{string(auth.RoleAdmin): auth.TemplateFor(cat, auth.RoleAdmin)}
	// The shipped defaults too, and which roles have been edited, for the
	// role editor's "back to the default" (roletemplates.go).
	defaults, edited := map[string][]string{}, map[string]bool{}
	// Every role with its name, built-in and made, and how many have it: the
	// role menus everywhere on the settings page are drawn from this.
	made := auth.CustomRoles()
	roles := []map[string]any{}
	for _, role := range auth.EditableRoleList() {
		templates[string(role)] = auth.TemplateFor(cat, role)
		defaults[string(role)] = auth.DefaultTemplate(role)
		edited[string(role)] = auth.IsEdited(role)
		name, custom := made[role]
		if !custom {
			name = map[auth.Role]string{auth.RoleManager: "مدیر", auth.RoleViewer: "بیننده"}[role]
		}
		roles = append(roles, map[string]any{"id": role, "name": name, "custom": custom, "users": s.roleHolders(role)})
	}
	roles = append(roles, map[string]any{"id": auth.RoleAdmin, "name": "مدیر ارشد", "custom": false, "users": s.roleHolders(auth.RoleAdmin)})
	writeJSON(w, map[string]any{
		"groups":    cat.Groups(),
		"templates": templates,
		"defaults":  defaults,
		"edited":    edited,
		"roles":     roles,
	})
}

// handleUserPermissions returns one account's overrides and their effect.
func (s *Server) handleUserPermissions(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSpace(r.URL.Query().Get("u"))
	if username == "" {
		writeJSONError(w, http.StatusBadRequest, `{"error":"نام کاربری لازم است."}`)
		return
	}
	u, err := s.guard.Users.Get(username)
	if err != nil {
		writeUserError(w, err)
		return
	}
	cat := s.Permissions()
	writeJSON(w, map[string]any{
		"username":  u.Username,
		"role":      string(u.Role),
		"grants":    u.Grants,
		"effective": auth.Effective(cat, u),
	})
}

// setPermissionsRequest replaces the whole override set for one account.
type setPermissionsRequest struct {
	Username string `json:"username"`
	// Grants maps a catalogue ID to "allow" or "deny". An ID ABSENT from this
	// map inherits the role template -- which is why the whole set is sent
	// rather than a list of changes: "remove this override" has no
	// representation in a list of changes, and that is the state the editor
	// most needs to be able to reach.
	Grants map[string]string `json:"grants"`
}

// grantDiff describes a change of grants as three lists of permission ids:
// newly allowed, newly denied, and returned to the role's default.
func grantDiff(before map[string]string, after map[string]auth.GrantState) map[string]any {
	var allowed, denied, cleared []string
	for id, g := range after {
		if before[id] == string(g) {
			continue
		}
		if g == auth.GrantAllow {
			allowed = append(allowed, id)
		} else {
			denied = append(denied, id)
		}
	}
	for id := range before {
		if _, still := after[id]; !still {
			cleared = append(cleared, id)
		}
	}
	slices.Sort(allowed)
	slices.Sort(denied)
	slices.Sort(cleared)
	out := map[string]any{}
	if len(allowed) > 0 {
		out["allowed"] = allowed
	}
	if len(denied) > 0 {
		out["denied"] = denied
	}
	if len(cleared) > 0 {
		out["cleared"] = cleared
	}
	return out
}

// handleSetUserPermissions saves one account's overrides.
func (s *Server) handleSetUserPermissions(w http.ResponseWriter, r *http.Request) {
	var req setPermissionsRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256*1024)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, `{"error":"درخواست نامعتبر است."}`)
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" {
		writeJSONError(w, http.StatusBadRequest, `{"error":"نام کاربری لازم است."}`)
		return
	}

	cat := s.Permissions()
	if cat == nil {
		writeJSONError(w, http.StatusServiceUnavailable,
			`{"error":"فهرست دسترسی‌ها هنوز آماده نیست."}`)
		return
	}

	// An ID that is not in the catalogue is REFUSED rather than stored and
	// ignored. A grant nothing will ever read is worse than an error: the
	// screen would show it saved, and the access it appears to give would
	// never exist.
	grants := make(map[string]auth.GrantState, len(req.Grants))
	for id, state := range req.Grants {
		id = strings.TrimSpace(id)
		if !cat.Has(id) {
			writeJSONError(w, http.StatusBadRequest,
				`{"error":"یکی از دسترسی‌های ارسال‌شده شناخته نشد."}`)
			return
		}
		g := auth.GrantState(strings.TrimSpace(state))
		if !g.Valid() {
			writeJSONError(w, http.StatusBadRequest,
				`{"error":"وضعیت دسترسی باید allow یا deny باشد."}`)
			return
		}
		grants[id] = g
	}

	// NOBODY MAY EDIT THEIR OWN PERMISSIONS, including an administrator.
	// Otherwise the one protection against a mistake -- that somebody else has
	// to agree -- is gone, and an administrator who denies themselves
	// action:manage_users has locked the door with the key inside.
	me := auth.UserFrom(r)
	if me != nil && strings.EqualFold(me.Username, req.Username) {
		writeJSONError(w, http.StatusForbidden,
			`{"error":"دسترسی‌های خودتان را نمی‌توانید تغییر دهید؛ از کاربر مدیر دیگری استفاده کنید."}`)
		return
	}

	// The trail records WHAT changed, not only that something did: "who gave
	// z.moradi the guarantees report" is the question it exists to answer.
	var before map[string]string
	if prev, err := s.guard.Users.Get(req.Username); err == nil {
		before = prev.Grants
	}
	audit.Note(r.Context(), "user:"+req.Username, grantDiff(before, grants))

	if err := s.guard.Users.SetGrants(req.Username, grants); err != nil {
		writeUserError(w, err)
		return
	}
	u, err := s.guard.Users.Get(req.Username)
	if err != nil {
		writeUserError(w, err)
		return
	}
	writeJSON(w, map[string]any{
		"ok":        true,
		"username":  u.Username,
		"grants":    u.Grants,
		"effective": auth.Effective(cat, u),
	})
}
