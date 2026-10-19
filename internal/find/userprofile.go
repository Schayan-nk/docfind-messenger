package find

import (
	"errors"
	"net/http"

	"github.com/Schayan-nk/docfind-messenger/internal/auth"
)

// The profile fields the users page edits, for whoever manages accounts.

// actor is who made the request, for the audit trail.
func actor(r *http.Request) string {
	if me := auth.UserFrom(r); me != nil {
		return me.Username
	}
	return "?"
}

// handleUserProfile is PUT /api/users/profile: a person's names, addresses,
// personnel code, and whether their next sign-in asks for a new password.
func (s *Server) handleUserProfile(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		auth.Profile
	}
	if err := readJSON(r, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, `{"error":"درخواست نامعتبر است."}`)
		return
	}
	if err := s.guard.Users.SetProfile(req.Username, req.Profile); err != nil {
		if errors.Is(err, auth.ErrNoSuchUser) {
			writeUserError(w, err)
			return
		}
		writeJSONError(w, http.StatusBadRequest, `{"error":`+jsonQuote("ذخیره نشد: "+err.Error())+`}`)
		return
	}
	s.auditChange(actor(r), "user.profile", "user:"+req.Username, nil)
	u, _ := s.guard.Users.Get(req.Username)
	writeJSON(w, map[string]any{"ok": true, "user": u.Public()})
}
