package find

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Schayan-nk/docfind-messenger/internal/auth"
	"github.com/Schayan-nk/docfind-messenger/internal/pgstore"
)

// The sign-in policy, as the security centre edits it (auth/policy.go says
// what each setting does): one document in app_settings, read at start-up and
// put in force the moment it is saved. And the accounts and addresses locked
// out right now, each with a way to unlock it.

const securityPolicyKey = "security_policy"

// UseSecurityPolicy reads the saved policy and puts it in force.
func (s *Server) UseSecurityPolicy(st appSettingsStore) {
	s.secPolicyMu.Lock()
	s.secPolicyStore = st
	s.secPolicyMu.Unlock()
	if s.guard == nil || st == nil {
		return
	}
	p := auth.DefaultPolicy()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := st.Get(ctx, securityPolicyKey, &p); err != nil && !errors.Is(err, pgstore.ErrNoSetting) {
		logReportError("security policy: reading it", err)
		return
	}
	s.guard.SetPolicy(p)
}

// handleSecurityPolicy is GET /api/admin/security/policy.
func (s *Server) handleSecurityPolicy(w http.ResponseWriter, r *http.Request) {
	s.secPolicyMu.Lock()
	saving := s.secPolicyStore != nil
	s.secPolicyMu.Unlock()
	writeJSON(w, map[string]any{"policy": s.guard.Policy(), "default": auth.DefaultPolicy(), "can_save": saving})
}

// handleSaveSecurityPolicy is PUT /api/admin/security/policy.
func (s *Server) handleSaveSecurityPolicy(w http.ResponseWriter, r *http.Request) {
	s.secPolicyMu.Lock()
	st := s.secPolicyStore
	s.secPolicyMu.Unlock()
	if st == nil {
		writeJSONError(w, http.StatusNotFound, `{"error":"برای نگه‌داشتن سیاست امنیتی، پایگاه دادهٔ PostgreSQL لازم است."}`)
		return
	}
	var p auth.Policy
	if err := readJSON(r, &p); err != nil {
		writeJSONError(w, http.StatusBadRequest, `{"error":"درخواست نامعتبر است."}`)
		return
	}
	p = p.Normalised()
	by := "?"
	if me := auth.UserFrom(r); me != nil {
		by = me.Username
	}
	if err := st.Set(r.Context(), securityPolicyKey, p, by); err != nil {
		logReportError("security policy: saving it", err)
		writeJSONError(w, http.StatusInternalServerError, `{"error":"ذخیره انجام نشد."}`)
		return
	}
	s.guard.SetPolicy(p)
	s.auditChange(by, "security.policy", "app_settings:"+securityPolicyKey, map[string]any{
		"lockout_attempts": p.LockoutAttempts, "lockout_minutes": p.LockoutMinutes,
		"min_length": p.MinLength, "require_two_step": p.RequireTwoStep, "max_age_days": p.MaxAgeDays})
	writeJSON(w, map[string]any{"ok": true, "policy": p})
}

// handleLockouts is GET /api/admin/security/lockouts: what is locked now, with
// each account's name as people know it.
func (s *Server) handleLockouts(w http.ResponseWriter, r *http.Request) {
	list := s.guard.Throttle.Lockouts()
	out := make([]map[string]any, 0, len(list))
	for _, l := range list {
		row := map[string]any{"key": l.Key, "kind": l.Kind, "name": l.Name, "until": l.Until,
			"minutes_left": int(time.Until(l.Until).Minutes()) + 1, "strikes": l.Strikes}
		if l.Kind != "ip" && l.Kind != "resetip" {
			if u, err := s.guard.Users.Get(l.Name); err == nil {
				row["display_name"] = u.DisplayName
			}
		}
		out = append(out, row)
	}
	writeJSON(w, map[string]any{"lockouts": out, "policy": s.guard.Policy()})
}

// unlockableKinds are the throttle's counters an administrator may clear:
// sign-in by account and by address, the password change, and the
// forgotten-password codes by account and by address (passwordreset.go).
var unlockableKinds = map[string]bool{"user": true, "ip": true, "pw": true, "reset": true, "resetip": true}

// handleUnlock is POST /api/admin/security/unlock {"key": "user:x.y"}: the
// lock goes, and with it the count of wrong passwords that led to it.
func (s *Server) handleUnlock(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key string `json:"key"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, `{"error":"درخواست نامعتبر است."}`)
		return
	}
	key := strings.TrimSpace(req.Key)
	kind, name, ok := strings.Cut(key, ":")
	if !ok || name == "" || !unlockableKinds[kind] {
		writeJSONError(w, http.StatusBadRequest, `{"error":"قفل نامعتبر است."}`)
		return
	}
	s.guard.Throttle.Unlock(key)
	// An account's lock and its password-change lock are one person's: both go.
	if kind == "user" {
		s.guard.Throttle.Unlock(auth.PasswordKey(name))
		s.guard.Throttle.Unlock(auth.ResetKey(name))
	}
	by := "?"
	if me := auth.UserFrom(r); me != nil {
		by = me.Username
	}
	s.auditChange(by, "security.unlock", key, nil)
	writeJSON(w, map[string]any{"ok": true})
}

// signInDuties is what the policy asks of somebody who has just proved their
// password: a new one, if this one breaks a rule or has grown too old. Only
// here does the server see a password in the clear, so only here can it ask.
// Returns the account as it now stands.
func (s *Server) signInDuties(u *auth.User, password string) *auth.User {
	p := s.guard.Policy()
	if u.MustChangePassword {
		return u
	}
	if len(p.Check(u.Username, password)) == 0 && !p.Expired(u, time.Now()) {
		return u
	}
	if err := s.guard.Users.SetUpdatePassword(u.Username, true); err != nil {
		logReportError("sign-in: asking "+u.Username+" for a new password", err)
		return u
	}
	if fresh, err := s.guard.Users.Get(u.Username); err == nil {
		return fresh
	}
	return u
}

// twoStepRequired is whether the policy holds u to setting up a second step.
func (s *Server) twoStepRequired(u *auth.User) bool {
	return s.guard != nil && s.guard.Policy().RequireTwoStep && !auth.HasTwoStep(u)
}

// passwordRules is the policy's requirements in words, for the forms that
// ask for a new password.
func (s *Server) passwordRules() []string {
	p := s.guard.Policy()
	rules := p.Check("", "")
	return rules
}

// errPolicy marks a password the policy refuses; its text says which rules.
var errPolicy = errors.New("policy")

type policyErr struct{ msg string }

func (e policyErr) Error() string        { return e.msg }
func (e policyErr) Is(target error) bool { return target == errPolicy }

func policyError(broken []string) error {
	return policyErr{"گذرواژهٔ تازه باید این‌ها را داشته باشد: " + strings.Join(broken, "، ") + "."}
}

// faMinutes says a wait in whole minutes, in Persian.
func faMinutes(d time.Duration) string {
	m := int(d.Minutes() + 0.999)
	if m < 1 {
		m = 1
	}
	const fa = "۰۱۲۳۴۵۶۷۸۹"
	digits := []rune(fa)
	var out []rune
	for _, r := range []rune(strconvItoa(m)) {
		out = append(out, digits[r-'0'])
	}
	return string(out) + " دقیقه"
}

func strconvItoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}
