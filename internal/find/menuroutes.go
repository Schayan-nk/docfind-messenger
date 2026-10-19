package find

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/Schayan-nk/docfind-messenger/internal/auth"
)

// handleSetMenu is POST /api/account/menu: the caller's own arrangement of the
// menu and the pages they added to it (auth/menulayout.go), or empty ones to
// put it back as it comes. Only ever the caller's: there is no field naming
// whose.
func (s *Server) handleSetMenu(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r)
	if u == nil {
		writeJSONError(w, http.StatusUnauthorized, `{"error":"برای ادامه وارد شوید."}`)
		return
	}
	var req struct {
		Menu  []auth.MenuNode `json:"menu"`
		Pages []auth.MenuPage `json:"pages"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, `{"error":"درخواست نامعتبر است."}`)
		return
	}
	prefs, err := s.guard.Users.SetMenu(u.Username, req.Menu, req.Pages)
	if err != nil {
		_, bad := auth.CleanMenu(req.Menu)
		if bad == nil {
			_, bad = auth.CleanMenuPages(req.Pages)
		}
		if bad != nil {
			writeJSONError(w, http.StatusBadRequest, mustJSON(map[string]string{"error": menuError(bad)}))
			return
		}
		writeUserError(w, err)
		return
	}
	writeJSON(w, map[string]any{"menu": prefs.Menu, "pages": prefs.Pages})
}

// handleSetMenuHidden is POST /api/account/menu/hidden {"hidden": [href…]}: the
// entries the caller took out of their own menu with − (and so, by leaving one
// out, put back with +). Only ever the caller's; empty brings everything back.
func (s *Server) handleSetMenuHidden(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r)
	if u == nil {
		writeJSONError(w, http.StatusUnauthorized, `{"error":"برای ادامه وارد شوید."}`)
		return
	}
	var req struct {
		Hidden []string `json:"hidden"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, `{"error":"درخواست نامعتبر است."}`)
		return
	}
	prefs, err := s.guard.Users.SetMenuHidden(u.Username, req.Hidden)
	if err != nil {
		if _, bad := auth.CleanMenuHidden(req.Hidden); bad != nil {
			writeJSONError(w, http.StatusBadRequest, mustJSON(map[string]string{"error": menuError(bad)}))
			return
		}
		writeUserError(w, err)
		return
	}
	hidden := prefs.Hidden
	if hidden == nil {
		hidden = []string{}
	}
	writeJSON(w, map[string]any{"hidden": hidden})
}

// menuError is a refusal in the page's language.
func menuError(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "twice"):
		return "یک صفحه دو بار در فهرست آمده است."
	case strings.Contains(msg, "40 letters"):
		return "هر نام حداکثر ۴۰ حرف است."
	case strings.Contains(msg, "needs a name"):
		return "هر صفحه‌ای که می‌افزایید نامی می‌خواهد."
	case strings.Contains(msg, "at most"):
		return "فهرست بیش از اندازه بلند است."
	case strings.Contains(msg, "not a page"):
		return "هر مورد باید صفحه‌ای از همین سامانه باشد."
	}
	return "چیدمان فهرست درست نیست."
}
