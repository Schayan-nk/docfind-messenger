package find

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Schayan-nk/docfind-messenger/internal/audit"
	"github.com/Schayan-nk/docfind-messenger/internal/auth"
)

// Forgotten passwords (asked for on 1405/07/15). From the sign-in page,
// somebody types their username and a six-digit code is texted to the mobile
// number on that account; the code and a new password then set it.
//
//   - The code goes by SMS only, to the number already on the account. The
//     page never asks for a number: a number typed there would let anybody
//     route somebody else's code to their own phone.
//   - The first answer is the same whatever was typed -- an unknown name, a
//     disabled account, one with no phone, a code asked for too soon -- and
//     it takes as long, because the text is sent in the background. Otherwise
//     the page would tell an outsider which usernames exist.
//   - Every request counts against the address it came from (ResetAddressKey),
//     whether a code went or not, so nobody can spend the panel's credit
//     texting codes to a list of names. Each account also has the code
//     store's own limits: one every thirty seconds, five in all.
//   - Wrong codes count against the account (ResetKey) and the address, like
//     wrong passwords; five against one code and it is gone.
//   - Setting the password signs nobody in. It signs every device out, and
//     the person then signs in as usual -- with their second step, if they
//     have one, so a texted code alone never opens an account that needs two.
//
// Codes are the shared store's (twostepmail.go): hashed, ten minutes, in memory.

// passwordResetOn is whether the sign-in page offers it: the SMS panel is
// what carries the code, so without one there is nothing to offer.
func (s *Server) passwordResetOn() bool { return s.smsConfig().Enabled() }

// resetCodeKey is where a reset code is kept: the account as the store
// normalises it, so "Reader" and "reader " ask for and spend one code.
func resetCodeKey(username string) string { return "reset:" + auth.LoginKey(username) }

const resetSentNote = "اگر این نام کاربری در docfind شمارهٔ همراه ثبت‌شده‌ای داشته باشد، " +
	"کدی شش‌رقمی به آن پیامک شد. کد تا ۱۰ دقیقه معتبر است."

// handleForgotPassword is POST /api/login/forgot {"username"}: texts a code.
func (s *Server) handleForgotPassword(w http.ResponseWriter, r *http.Request) {
	if !s.AuthEnabled() {
		http.Error(w, `{"error":"حساب‌های کاربری روی این سرور فعال نیست."}`, http.StatusNotFound)
		return
	}
	var req struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, `{"error":"درخواست نامعتبر است."}`)
		return
	}
	// A fact about the server, not about any account: safe to say.
	if !s.passwordResetOn() {
		writeJSONError(w, http.StatusServiceUnavailable,
			`{"error":"بازیابی گذرواژه با پیامک روی این سرور راه‌اندازی نشده است؛ از مدیر سیستم بخواهید گذرواژه‌تان را عوض کند."}`)
		return
	}
	name := strings.TrimSpace(req.Username)
	if name == "" {
		writeJSONError(w, http.StatusBadRequest, `{"error":"نام کاربری را بنویسید."}`)
		return
	}
	tried := clipRunes(name, 64)
	audit.NoteActor(r.Context(), tried)
	audit.Note(r.Context(), "user:"+tried, nil)

	addrKey := auth.ResetAddressKey(s.guard.ClientIP(r))
	if blocked, wait := s.guard.Throttle.Blocked(addrKey); blocked {
		w.Header().Set("Retry-After", itoaSeconds(wait))
		writeJSONError(w, http.StatusTooManyRequests,
			`{"error":"از این دستگاه کد زیادی خواسته شد؛ کمی بعد دوباره تلاش کنید."}`)
		return
	}
	s.guard.Throttle.Fail(addrKey)
	s.textResetCode(name)
	writeJSON(w, map[string]any{"ok": true, "note": resetSentNote})
}

// textResetCode texts a reset code to the account's phone, when there is an
// enabled account with one, and the code store allows another now. In the
// background: the panel's answer takes long enough to tell an outsider which
// names have accounts if the request waited for it. A failure is logged and
// the person asks again.
func (s *Server) textResetCode(name string) {
	u, err := s.guard.Users.Get(name)
	if err != nil || u.Disabled || u.Phone == "" {
		return
	}
	code, err := s.issueCode(resetCodeKey(name), u.Phone)
	if err != nil {
		return
	}
	cfg, phone, text := s.smsConfig(), u.Phone, smsCodeText(codeForReset, code)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := cfg.Send(ctx, []string{phone}, text); err != nil {
			logReportError("password reset sms", err)
		}
	}()
}

// handleResetPassword is POST /api/login/reset {"username","code","password"}:
// the texted code and the new password.
func (s *Server) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	if !s.AuthEnabled() {
		http.Error(w, `{"error":"حساب‌های کاربری روی این سرور فعال نیست."}`, http.StatusNotFound)
		return
	}
	var req struct {
		Username string `json:"username"`
		Code     string `json:"code"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, `{"error":"درخواست نامعتبر است."}`)
		return
	}
	name := strings.TrimSpace(req.Username)
	tried := clipRunes(name, 64)
	audit.NoteActor(r.Context(), tried)
	audit.Note(r.Context(), "user:"+tried, nil)

	// The password is judged before the code is spent, so a password the
	// policy refuses does not cost the person their code.
	if len(req.Password) < minPasswordLength {
		writeJSONError(w, http.StatusBadRequest, `{"error":"گذرواژهٔ تازه باید دست‌کم ۱۰ نویسه باشد."}`)
		return
	}
	if broken := s.guard.Policy().Check(name, req.Password); len(broken) > 0 {
		writeUserError(w, policyError(broken))
		return
	}
	userKey, ipKey := auth.ResetKey(name), auth.IPKey(s.guard.ClientIP(r))
	for _, k := range []string{userKey, ipKey} {
		if blocked, wait := s.guard.Throttle.Blocked(k); blocked {
			w.Header().Set("Retry-After", itoaSeconds(wait))
			writeJSONError(w, http.StatusTooManyRequests,
				`{"error":"تلاش ناموفق زیاد بود؛ کمی بعد دوباره تلاش کنید."}`)
			return
		}
	}
	phone, ok := s.checkCode(resetCodeKey(name), req.Code)
	u, err := s.guard.Users.Get(name)
	// The account must still be the one the code went to: enabled, and with
	// the same phone -- an administrator who changed the number or disabled
	// the account in the last ten minutes did it for a reason.
	if !ok || err != nil || u.Disabled || u.Phone != phone {
		s.guard.Throttle.Fail(userKey)
		s.guard.Throttle.Fail(ipKey)
		writeJSONError(w, http.StatusUnauthorized,
			`{"error":"کد درست نیست یا منقضی شده است. اگر لازم است، کد تازه بخواهید."}`)
		return
	}
	if err := s.guard.Users.ResetPassword(u.Username, req.Password); err != nil {
		if errors.Is(err, auth.ErrSamePassword) {
			writeJSONError(w, http.StatusBadRequest,
				`{"error":"این همان گذرواژهٔ فعلی است. گذرواژهٔ دیگری انتخاب کنید و کد تازه بخواهید."}`)
			return
		}
		writeUserError(w, err)
		return
	}
	// The person proved they hold the account's phone: whatever locked them
	// out by wrong guesses goes, or the new password could not be used yet.
	for _, k := range []string{userKey, auth.LoginKey(u.Username), auth.PasswordKey(u.Username)} {
		s.guard.Throttle.Unlock(k)
	}
	s.guard.Sessions.ForgetUser(u.Username)
	audit.Note(r.Context(), "user:"+u.Username, map[string]any{"by": "sms"})
	writeJSON(w, map[string]any{
		"ok":   true,
		"note": "گذرواژه عوض شد و همهٔ دستگاه‌ها از حساب خارج شدند. اکنون با گذرواژهٔ تازه وارد شوید.",
	})
}
