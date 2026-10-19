package find

import (
	"encoding/json"
	"net/http"

	"github.com/Schayan-nk/docfind-messenger/internal/audit"
	"github.com/Schayan-nk/docfind-messenger/internal/auth"
)

// Every account has a mobile number (asked for on 1405/07/08): where the SMS
// panel sends that person's notifications, and their sign-in code when SMS is
// their second step. A person sets their own on the settings page; whoever
// manages accounts sets anybody's in the user editor.

// applyPhone sets an account's number when the request carries one, and
// answers the request itself when that fails. ok is false when it answered.
func (s *Server) applyPhone(w http.ResponseWriter, r *http.Request, u *auth.User, phone *string) (*auth.User, bool) {
	if phone == nil {
		return u, true
	}
	if err := s.guard.Users.SetPhone(u.Username, *phone); err != nil {
		writeJSONError(w, http.StatusBadRequest, `{"error":"شمارهٔ همراه درست نیست؛ مثل ۰۹۱۲۱۲۳۴۵۶۷ بنویسید."}`)
		return nil, false
	}
	audit.Note(r.Context(), "user:"+u.Username, map[string]any{"phone": "changed"})
	fresh, err := s.guard.Users.Get(u.Username)
	if err != nil {
		writeUserError(w, err)
		return nil, false
	}
	return fresh, true
}

// handleSetUserPhone sets anybody's number, for whoever manages accounts:
// {"username": "...", "phone": "..."}. Allowed even where SMS is that
// person's second step -- the lost-phone case is an administrator's to fix --
// and audited like every change to an account.
func (s *Server) handleSetUserPhone(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Phone    string `json:"phone"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, `{"error":"درخواست نامعتبر است."}`)
		return
	}
	u, err := s.guard.Users.Get(req.Username)
	if err != nil {
		writeUserError(w, err)
		return
	}
	if fresh, ok := s.applyPhone(w, r, u, &req.Phone); ok {
		writeJSON(w, map[string]any{"user": fresh.Public()})
	}
}

// handleSetOwnPhone sets the signed-in person's number.
//
// NOT while SMS is their second step: the number IS the second step then, and
// a session left open on a desk must not be enough to move it to another
// phone. Turning two-step off (which asks for the password and a code) comes
// first, the same rule the email method follows for its address.
func (s *Server) handleSetOwnPhone(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r)
	if u == nil {
		writeJSONError(w, http.StatusUnauthorized, `{"error":"برای ادامه وارد شوید."}`)
		return
	}
	var req struct {
		Phone string `json:"phone"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, `{"error":"درخواست نامعتبر است."}`)
		return
	}
	if s.guard.Users.SecondFactorMethod(u.Username) == auth.MethodSMS {
		writeJSONError(w, http.StatusConflict,
			`{"error":"این شماره مرحلهٔ دوم ورود شماست؛ برای عوض کردن آن اول ورود دومرحله‌ای را خاموش کنید."}`)
		return
	}
	cur, err := s.guard.Users.Get(u.Username)
	if err != nil {
		writeUserError(w, err)
		return
	}
	if fresh, ok := s.applyPhone(w, r, cur, &req.Phone); ok {
		writeJSON(w, map[string]any{"phone": fresh.Phone})
	}
}
