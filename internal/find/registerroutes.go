package find

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Schayan-nk/docfind-messenger/internal/audit"
	"github.com/Schayan-nk/docfind-messenger/internal/auth"
)

// Self-registration, invite-only. There is no way to create an account
// without a code, so the public registration page is useless to whoever finds
// it. An administrator mints a code (registerAccountRoutes, below), hands it
// to someone, and the code decides the new account's role. See
// internal/auth/invites.go.

// UseInvites turns self-registration on, keeping the codes in path.
func (s *Server) UseInvites(iv *auth.Invites) { s.invites = iv }

// registrationOn reports whether an account can be self-created here.
func (s *Server) registrationOn() bool { return s.AuthEnabled() && s.invites != nil }

type registerRequest struct {
	Invite      string `json:"invite"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Password    string `json:"password"`
}

// handleRegister creates an account from a valid invite and signs it in.
//
// The form is validated before the code is redeemed, so a weak password or a
// taken name never spends a use. The code itself is answered with one message
// whether it is unknown or used up, so it cannot be probed.
func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if !s.registrationOn() {
		http.Error(w, `{"error":"ثبت‌نام روی این سرور فعال نیست."}`, http.StatusNotFound)
		return
	}
	// Throttled by address, on its own counter so it cannot lock anybody's
	// sign-in and a flood of guesses at codes is slowed.
	key := "register:" + auth.IPKey(s.guard.ClientIP(r))
	if blocked, wait := s.guard.Throttle.Blocked(key); blocked {
		w.Header().Set("Retry-After", itoaSeconds(wait))
		http.Error(w, `{"error":"تلاش زیاد بود؛ کمی بعد دوباره تلاش کنید."}`, http.StatusTooManyRequests)
		return
	}

	var req registerRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req); err != nil {
		http.Error(w, `{"error":"درخواست نامعتبر است."}`, http.StatusBadRequest)
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	if req.DisplayName == "" {
		req.DisplayName = req.Username
	}

	fail := func(msg string, code int) {
		s.guard.Throttle.Fail(key)
		http.Error(w, `{"error":`+jsonQuote(msg)+`}`, code)
	}

	// The name and password first: no side effects, so a bad form never burns
	// a code.
	if !auth.ValidUsername(req.Username) {
		fail("نام کاربری باید ۳ تا ۶۴ نویسهٔ لاتین کوچک، رقم، نقطه، خط یا زیرخط باشد.", http.StatusBadRequest)
		return
	}
	if _, err := s.guard.Users.Get(req.Username); err == nil {
		fail("این نام کاربری گرفته شده است؛ نام دیگری بردارید.", http.StatusConflict)
		return
	}
	if broken := s.guard.Policy().Check(req.Username, req.Password); len(broken) > 0 {
		fail("گذرواژه این شرط‌ها را ندارد: "+strings.Join(broken, "، ")+".", http.StatusBadRequest)
		return
	}

	// The code, consumed only now that the rest is good.
	role, err := s.invites.Redeem(req.Invite)
	if err != nil {
		fail("کد دعوت نادرست است یا دیگر کار نمی‌کند.", http.StatusForbidden)
		return
	}
	u, err := s.guard.Users.Create(req.Username, req.DisplayName, req.Password, role)
	if err != nil {
		s.invites.Refund(req.Invite)
		writeUserError(w, err)
		return
	}
	audit.NoteActor(r.Context(), u.Username)
	audit.Note(r.Context(), "user:"+u.Username, map[string]any{"self_registered": true, "role": string(role)})
	s.guard.Throttle.Succeed(key)
	// Whoever made the code hears about it (signupnotice.go).
	s.announceSignup(u.Username, u.DisplayName, s.invites.Owner(req.Invite))
	// Signed in at once, so registering lands the person in the app rather
	// than back at a sign-in form.
	s.issueSession(w, r, u, nil)
}

// --- the administrator's side: minting and revoking codes.

// mintInviteRequest is what the invites card sends.
type mintInviteRequest struct {
	Role    string `json:"role"`
	MaxUses int    `json:"max_uses"`
	Days    int    `json:"days"`
	Note    string `json:"note"`
}

// handleMintInvite is POST /api/admin/invites. Only a viewer or manager code
// may be minted here: an administrator is never created by a shared link.
func (s *Server) handleMintInvite(w http.ResponseWriter, r *http.Request) {
	if s.invites == nil {
		http.Error(w, `{"error":"ثبت‌نام فعال نیست."}`, http.StatusNotFound)
		return
	}
	var req mintInviteRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		http.Error(w, `{"error":"درخواست نامعتبر است."}`, http.StatusBadRequest)
		return
	}
	role := auth.Role(strings.TrimSpace(req.Role))
	if role == "" {
		role = auth.RoleViewer
	}
	if role == auth.RoleAdmin {
		http.Error(w, `{"error":"کد دعوت مدیر ساخته نمی‌شود؛ مدیر را دستی بسازید."}`, http.StatusBadRequest)
		return
	}
	if !auth.ValidRole(role) {
		http.Error(w, `{"error":"نقش نامعتبر است."}`, http.StatusBadRequest)
		return
	}
	if req.MaxUses < 0 || req.MaxUses > 10000 {
		req.MaxUses = 0
	}
	var ttl time.Duration
	if req.Days > 0 && req.Days <= 365 {
		ttl = time.Duration(req.Days) * 24 * time.Hour
	}
	in, err := s.invites.Mint(role, req.MaxUses, ttl, clipRunes(req.Note, 120), actor(r))
	if err != nil {
		http.Error(w, `{"error":"ساخت کد انجام نشد."}`, http.StatusInternalServerError)
		return
	}
	s.auditChange(actor(r), "invite.mint", "invite:"+in.Code,
		map[string]any{"role": string(role), "max_uses": req.MaxUses, "days": req.Days})
	writeJSON(w, map[string]any{"invite": in, "link": s.registerLink(r, in.Code)})
}

// handleListInvites is GET /api/admin/invites.
func (s *Server) handleListInvites(w http.ResponseWriter, r *http.Request) {
	if s.invites == nil {
		writeJSON(w, map[string]any{"invites": []any{}, "enabled": false})
		return
	}
	writeJSON(w, map[string]any{"invites": s.invites.List(), "enabled": true})
}

// handleRevokeInvite is DELETE /api/admin/invites?code=.
func (s *Server) handleRevokeInvite(w http.ResponseWriter, r *http.Request) {
	if s.invites == nil {
		http.Error(w, `{"error":"ثبت‌نام فعال نیست."}`, http.StatusNotFound)
		return
	}
	code := r.URL.Query().Get("code")
	if err := s.invites.Revoke(code); err != nil {
		if errors.Is(err, auth.ErrNoInvite) {
			http.Error(w, `{"error":"این کد نیست."}`, http.StatusNotFound)
			return
		}
		http.Error(w, `{"error":"لغو انجام نشد."}`, http.StatusInternalServerError)
		return
	}
	s.auditChange(actor(r), "invite.revoke", "invite:"+code, nil)
	writeJSON(w, map[string]any{"ok": true})
}

// registerLink is the shareable address that fills the code in for the person
// who opens it: https://<host>/register?i=<code>.
func (s *Server) registerLink(r *http.Request, code string) string {
	scheme := "https"
	if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") != "https" {
		scheme = "http"
	}
	return scheme + "://" + r.Host + "/register?i=" + code
}
