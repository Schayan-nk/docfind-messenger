package find

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Schayan-nk/docfind-messenger/internal/auth"
	"github.com/Schayan-nk/docfind-messenger/internal/pgstore"
)

// Editing what a role gets by default (asked for on 1405/07/12): the manager
// and viewer templates, kept as one document in app_settings and put in force
// the moment they are saved (auth/roletemplates.go). The permission matrix
// already reads the templates from /api/permissions; this adds the edit.
//
// And roles of one's own (asked for the same day: "add roles, not only مدیر
// and بیننده"): a name in customRolesKey, a template in roleTemplatesKey like
// any edited role, assignable to a user like the built-in ones. A role cannot
// be deleted while anybody has it -- the answer says how many do.

const (
	roleTemplatesKey = "role_templates"
	customRolesKey   = "custom_roles"
)

// UseRoleTemplates reads the made roles and the edited templates and puts
// them in force -- the roles first, so a user who has one is valid from the
// first request.
func (s *Server) UseRoleTemplates(st appSettingsStore) {
	s.roleTplStore = st
	if st == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var made map[string]string
	if err := st.Get(ctx, customRolesKey, &made); err != nil {
		if !errors.Is(err, pgstore.ErrNoSetting) {
			logReportError("custom roles: reading them", err)
		}
	} else {
		auth.SetCustomRoles(toRoleNames(made))
	}
	var saved map[string][]string
	if err := st.Get(ctx, roleTemplatesKey, &saved); err != nil {
		if !errors.Is(err, pgstore.ErrNoSetting) {
			logReportError("role templates: reading them", err)
		}
		return
	}
	m := map[auth.Role][]string{}
	for k, ids := range saved {
		m[auth.Role(k)] = ids
	}
	auth.SetRoleTemplates(m)
}

func toRoleNames(m map[string]string) map[auth.Role]string {
	out := map[auth.Role]string{}
	for k, v := range m {
		out[auth.Role(k)] = v
	}
	return out
}

// editedTemplates is the role_templates document as it stands: every role
// whose template was edited, made roles included.
func editedTemplates(cat *auth.Catalogue) map[string][]string {
	cur := map[string][]string{}
	for _, e := range auth.EditableRoleList() {
		if auth.IsEdited(e) {
			cur[string(e)] = auth.TemplateFor(cat, e)
		}
	}
	return cur
}

// putTemplates saves the role_templates document and puts it in force.
func (s *Server) putTemplates(ctx context.Context, cur map[string][]string, by string) error {
	if err := s.roleTplStore.Set(ctx, roleTemplatesKey, cur, by); err != nil {
		return err
	}
	m := map[auth.Role][]string{}
	for k, ids := range cur {
		m[auth.Role(k)] = ids
	}
	auth.SetRoleTemplates(m)
	return nil
}

// putCustomRoles saves the made roles and puts them in force.
func (s *Server) putCustomRoles(ctx context.Context, made map[auth.Role]string, by string) error {
	doc := map[string]string{}
	for r, n := range made {
		doc[string(r)] = n
	}
	if err := s.roleTplStore.Set(ctx, customRolesKey, doc, by); err != nil {
		return err
	}
	auth.SetCustomRoles(made)
	return nil
}

func (s *Server) roleStoreReady(w http.ResponseWriter) (*auth.Catalogue, bool) {
	if s.roleTplStore == nil {
		writeJSONError(w, http.StatusNotFound, `{"error":"برای نگه‌داشتن نقش‌ها، پایگاه دادهٔ PostgreSQL لازم است."}`)
		return nil, false
	}
	cat := s.Permissions()
	if cat == nil {
		writeJSONError(w, http.StatusServiceUnavailable, `{"error":"فهرست دسترسی‌ها هنوز آماده نیست."}`)
		return nil, false
	}
	return cat, true
}

func actorOf(r *http.Request) string {
	if me := auth.UserFrom(r); me != nil {
		return me.Username
	}
	return "?"
}

// handleSaveRoleTemplate is PUT /api/permissions/role {"role", "ids"} or
// {"role", "reset": true} to go back to the role's default.
func (s *Server) handleSaveRoleTemplate(w http.ResponseWriter, r *http.Request) {
	cat, ok := s.roleStoreReady(w)
	if !ok {
		return
	}
	var req struct {
		Role  string   `json:"role"`
		IDs   []string `json:"ids"`
		Reset bool     `json:"reset"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, `{"error":"درخواست نامعتبر است."}`)
		return
	}
	role := auth.Role(req.Role)
	if !auth.RoleEditable(role) {
		writeJSONError(w, http.StatusBadRequest, `{"error":"این نقش ویرایش‌شدنی نیست؛ مدیر ارشد همیشه همه‌چیز را دارد."}`)
		return
	}
	if !req.Reset {
		if bad, ok := auth.CheckTemplate(cat, req.IDs); !ok {
			writeJSONError(w, http.StatusBadRequest, `{"error":`+jsonQuote("این دسترسی در پیش‌فرض نقش نمی‌تواند باشد: "+bad)+`}`)
			return
		}
	}
	// The whole document, every edited role: the current ones plus this.
	cur := editedTemplates(cat)
	if req.Reset {
		delete(cur, req.Role)
	} else {
		ids := append([]string(nil), req.IDs...)
		sort.Strings(ids)
		cur[req.Role] = ids
	}
	by := actorOf(r)
	if err := s.putTemplates(r.Context(), cur, by); err != nil {
		logReportError("role templates: saving", err)
		writeJSONError(w, http.StatusInternalServerError, `{"error":"ذخیره انجام نشد."}`)
		return
	}
	s.auditChange(by, "permissions.role", "role:"+req.Role, map[string]any{"reset": req.Reset, "count": len(req.IDs)})
	writeJSON(w, map[string]any{"ok": true, "role": req.Role, "template": auth.TemplateFor(cat, role), "edited": auth.IsEdited(role)})
}

// builtInRoleNames are the names the built-in roles go by on the screen; a
// made role may not take one of them.
var builtInRoleNames = []string{"مدیر ارشد", "مدیر", "بیننده"}

// cleanRoleName checks a made role's name: one to forty characters, not a
// built-in role's, not another made role's (other than except's own).
func cleanRoleName(name string, except auth.Role) (string, string) {
	name = strings.Join(strings.Fields(name), " ")
	if n := len([]rune(name)); n == 0 || n > 40 {
		return "", "نام نقش باید یک تا چهل نویسه باشد."
	}
	for _, b := range builtInRoleNames {
		if name == b {
			return "", "این نام از آنِ یک نقش اصلی است."
		}
	}
	for r, n := range auth.CustomRoles() {
		if r != except && strings.EqualFold(n, name) {
			return "", "نقشی با این نام هست."
		}
	}
	return name, ""
}

func newRoleID() auth.Role {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return auth.Role(auth.CustomRolePrefix + hex.EncodeToString(b))
}

// handleCreateRole is POST /api/permissions/roles {"name"}: a new role, which
// starts with what بیننده has now.
func (s *Server) handleCreateRole(w http.ResponseWriter, r *http.Request) {
	cat, ok := s.roleStoreReady(w)
	if !ok {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, `{"error":"درخواست نامعتبر است."}`)
		return
	}
	name, bad := cleanRoleName(req.Name, "")
	if bad != "" {
		writeJSONError(w, http.StatusBadRequest, `{"error":`+jsonQuote(bad)+`}`)
		return
	}
	id := newRoleID()
	made := auth.CustomRoles()
	made[id] = name
	by := actorOf(r)
	cur := editedTemplates(cat)
	cur[string(id)] = auth.TemplateFor(cat, auth.RoleViewer)
	// The template first: a role in force with no template would hold
	// بیننده's default, which is what it is given anyway -- but saved the
	// other way round, a failure between the two writes would leave a name
	// with no template, and that is the order that cannot surprise anybody.
	if err := s.putTemplates(r.Context(), cur, by); err != nil {
		logReportError("custom roles: saving the template", err)
		writeJSONError(w, http.StatusInternalServerError, `{"error":"ذخیره انجام نشد."}`)
		return
	}
	if err := s.putCustomRoles(r.Context(), made, by); err != nil {
		logReportError("custom roles: saving", err)
		writeJSONError(w, http.StatusInternalServerError, `{"error":"ذخیره انجام نشد."}`)
		return
	}
	s.auditChange(by, "permissions.role.create", "role:"+string(id), map[string]any{"name": name})
	writeJSON(w, map[string]any{"ok": true, "id": id, "name": name, "template": auth.TemplateFor(cat, id)})
}

// handleRenameRole is PATCH /api/permissions/roles {"id", "name"}.
func (s *Server) handleRenameRole(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.roleStoreReady(w); !ok {
		return
	}
	var req struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := readJSON(r, &req); err != nil || !auth.IsCustomRole(auth.Role(req.ID)) {
		writeJSONError(w, http.StatusBadRequest, `{"error":"این نقش پیدا نشد."}`)
		return
	}
	name, bad := cleanRoleName(req.Name, auth.Role(req.ID))
	if bad != "" {
		writeJSONError(w, http.StatusBadRequest, `{"error":`+jsonQuote(bad)+`}`)
		return
	}
	made := auth.CustomRoles()
	made[auth.Role(req.ID)] = name
	by := actorOf(r)
	if err := s.putCustomRoles(r.Context(), made, by); err != nil {
		logReportError("custom roles: renaming", err)
		writeJSONError(w, http.StatusInternalServerError, `{"error":"ذخیره انجام نشد."}`)
		return
	}
	s.auditChange(by, "permissions.role.rename", "role:"+req.ID, map[string]any{"name": name})
	writeJSON(w, map[string]any{"ok": true, "id": req.ID, "name": name})
}

// roleHolders is how many accounts have a role.
func (s *Server) roleHolders(role auth.Role) int {
	n := 0
	if s.guard != nil && s.guard.Users != nil {
		for _, u := range s.guard.Users.List() {
			if u.Role == role {
				n++
			}
		}
	}
	return n
}

// handleDeleteRole is DELETE /api/permissions/roles?id=: refused while anybody
// has the role, so nobody is ever left holding a role that is not there.
func (s *Server) handleDeleteRole(w http.ResponseWriter, r *http.Request) {
	cat, ok := s.roleStoreReady(w)
	if !ok {
		return
	}
	id := auth.Role(strings.TrimSpace(r.URL.Query().Get("id")))
	if !auth.IsCustomRole(id) {
		writeJSONError(w, http.StatusBadRequest, `{"error":"فقط نقش‌های ساخته‌شده حذف‌شدنی‌اند."}`)
		return
	}
	if n := s.roleHolders(id); n > 0 {
		writeJSONError(w, http.StatusConflict, `{"error":`+jsonQuote(faDigits(strconv.Itoa(n))+" کاربر این نقش را دارند؛ پیش از حذف، نقششان را عوض کنید.")+`}`)
		return
	}
	made := auth.CustomRoles()
	delete(made, id)
	by := actorOf(r)
	if err := s.putCustomRoles(r.Context(), made, by); err != nil {
		logReportError("custom roles: deleting", err)
		writeJSONError(w, http.StatusInternalServerError, `{"error":"حذف انجام نشد."}`)
		return
	}
	cur := editedTemplates(cat)
	delete(cur, string(id))
	if err := s.putTemplates(r.Context(), cur, by); err != nil {
		// The role is gone and nobody has it; a template left behind holds
		// nothing (auth.templateOf), so this is noted, not failed.
		logReportError("custom roles: dropping the template", err)
	}
	s.auditChange(by, "permissions.role.delete", "role:"+string(id), nil)
	writeJSON(w, map[string]any{"ok": true})
}
