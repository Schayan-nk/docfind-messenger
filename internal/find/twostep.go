package find

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/Schayan-nk/docfind-messenger/internal/audit"
	"github.com/Schayan-nk/docfind-messenger/internal/auth"
	"github.com/Schayan-nk/docfind-messenger/internal/qr"
	"github.com/Schayan-nk/docfind-messenger/internal/sms"
)

// Two-step sign-in: the password, then six digits from an authenticator app
// or one of the recovery codes (internal/auth/totp.go, secondfactor.go).
//
// THE PASSWORD ALONE BUYS A CHALLENGE, NOT A SESSION: a five-minute token
// (Claims.Purpose "2fa") that the session check refuses and only
// /api/login/second-step accepts. It is good for one sign-in; wrong codes
// count against the ACCOUNT's failure counter, the same one wrong passwords
// feed, so guessing codes locks the account like guessing passwords does.
//
// Setting it up, turning it off and replacing the recovery codes each ask for
// the password again: a session left open on a desk must not be enough to
// take the second step away from its owner or copy their key. An
// administrator can only turn it OFF for somebody -- the lost-phone case --
// never see or set anybody's key.

const twoStepIssuer = "docfind"

// askSecondStep answers a right password on a two-step account.
func (s *Server) askSecondStep(w http.ResponseWriter, r *http.Request, u *auth.User) {
	challenge, _, err := s.guard.Signer.SignChallenge(u.Username, u.TokenVersion)
	if err != nil {
		http.Error(w, `{"error":"ورود انجام نشد؛ دوباره تلاش کنید."}`, http.StatusInternalServerError)
		return
	}
	method := s.guard.Users.SecondFactorMethod(u.Username)
	audit.Note(r.Context(), "", map[string]any{"second_step": "asked", "method": method})
	out := map[string]any{"second_step": true, "challenge": challenge, "method": method}
	if method == auth.MethodEmail || method == auth.MethodSMS {
		// Sent now, keyed by this challenge: the code works for this
		// sign-in and no other. A failed send still hands back the
		// challenge, so a recovery code or "send again" can finish it.
		if c, err := s.guard.Signer.VerifyChallenge(challenge); err == nil {
			to, shown := codeAddress(method, u)
			if err := s.sendCodeVia(r.Context(), method, "login:"+c.SID, to, codeForLogin); err != nil {
				logReportError("second step code", err)
				out["mail_failed"] = true
			} else {
				out["sent_to"] = shown
			}
		}
	}
	writeJSON(w, out)
}

// verifySecondStep checks a code whichever way it arrived: a mailed code
// against the one sent under key, anything else -- an app code, a recovery
// code -- against the account.
func (s *Server) verifySecondStep(username, key, code string) (bool, int, error) {
	if m := s.guard.Users.SecondFactorMethod(username); (m == auth.MethodEmail || m == auth.MethodSMS) && auth.IsSixDigits(code) {
		if _, ok := s.checkCode(key, code); !ok {
			return false, s.guard.Users.RecoveryLeft(username), auth.ErrBadSecondFactor
		}
		return false, s.guard.Users.RecoveryLeft(username), nil
	}
	return s.guard.Users.VerifySecondFactor(username, code, time.Now())
}

// handleLoginSecondStepResend mails a new sign-in code for a challenge.
func (s *Server) handleLoginSecondStepResend(w http.ResponseWriter, r *http.Request) {
	if !s.AuthEnabled() {
		http.Error(w, `{"error":"حساب‌های کاربری روی این سرور فعال نیست."}`, http.StatusNotFound)
		return
	}
	var req struct {
		Challenge string `json:"challenge"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		http.Error(w, `{"error":"درخواست نامعتبر است."}`, http.StatusBadRequest)
		return
	}
	claims, err := s.guard.Signer.VerifyChallenge(req.Challenge)
	if err != nil || s.challengeSpent(claims.SID) {
		http.Error(w, `{"error":"زمان این مرحله گذشته است؛ دوباره وارد شوید.","restart":true}`, http.StatusUnauthorized)
		return
	}
	u, err := s.guard.Users.Get(claims.Subject)
	method := ""
	if err == nil {
		method = s.guard.Users.SecondFactorMethod(u.Username)
	}
	if err != nil || u.Disabled || u.TokenVersion != claims.Version ||
		(method != auth.MethodEmail && method != auth.MethodSMS) {
		http.Error(w, `{"error":"زمان این مرحله گذشته است؛ دوباره وارد شوید.","restart":true}`, http.StatusUnauthorized)
		return
	}
	to, shown := codeAddress(method, u)
	if err := s.sendCodeVia(r.Context(), method, "login:"+claims.SID, to, codeForLogin); err != nil {
		codeMailError(w, err)
		return
	}
	writeJSON(w, map[string]any{"sent_to": shown})
}

func codeMailError(w http.ResponseWriter, err error) {
	status, msg := http.StatusServiceUnavailable, "کد فرستاده نشد؛ کمی بعد دوباره تلاش کنید یا یک کد پشتیبان وارد کنید."
	switch {
	case errors.Is(err, errCodeTooSoon):
		status, msg = http.StatusTooManyRequests, "کد همین الان فرستاده شد؛ نیم دقیقه صبر کنید."
	case errors.Is(err, errCodeLimit):
		status, msg = http.StatusTooManyRequests, "کد بیش از اندازه فرستاده شد؛ از ابتدا وارد شوید."
	case errors.Is(err, errNoCodeMail):
		msg = "ارسال ایمیل روی این سرور راه‌اندازی نشده است."
	case errors.Is(err, errNoCodeSMS):
		msg = "پنل پیامک روی این سرور تنظیم نشده است."
	default:
		logReportError("second step mail", err)
	}
	body, _ := json.Marshal(map[string]string{"error": msg})
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	w.Write(body)
}

// handleLoginSecondStep finishes a two-step sign-in.
func (s *Server) handleLoginSecondStep(w http.ResponseWriter, r *http.Request) {
	if !s.AuthEnabled() {
		http.Error(w, `{"error":"حساب‌های کاربری روی این سرور فعال نیست."}`, http.StatusNotFound)
		return
	}
	var req struct {
		Challenge string `json:"challenge"`
		Code      string `json:"code"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		http.Error(w, `{"error":"درخواست نامعتبر است."}`, http.StatusBadRequest)
		return
	}
	expired := func() {
		http.Error(w, `{"error":"زمان این مرحله گذشته است؛ دوباره وارد شوید.","restart":true}`, http.StatusUnauthorized)
	}
	claims, err := s.guard.Signer.VerifyChallenge(req.Challenge)
	if err != nil {
		expired()
		return
	}
	audit.NoteActor(r.Context(), claims.Subject)
	audit.Note(r.Context(), "user:"+claims.Subject, nil)

	userKey, ipKey := auth.LoginKey(claims.Subject), auth.IPKey(s.guard.ClientIP(r))
	for _, k := range []string{userKey, ipKey} {
		if blocked, wait := s.guard.Throttle.Blocked(k); blocked {
			w.Header().Set("Retry-After", itoaSeconds(wait))
			http.Error(w, `{"error":"تلاش ناموفق زیاد بود؛ کمی بعد دوباره تلاش کنید."}`, http.StatusTooManyRequests)
			return
		}
	}
	// The account as it is NOW: disabled, or its password changed, since the
	// password was checked means the challenge no longer speaks for it.
	u, err := s.guard.Users.Get(claims.Subject)
	if err != nil || u.Disabled || u.TokenVersion != claims.Version || s.challengeSpent(claims.SID) {
		expired()
		return
	}
	recovery, left, err := s.verifySecondStep(u.Username, "login:"+claims.SID, req.Code)
	switch {
	case errors.Is(err, auth.ErrBadSecondFactor):
		s.guard.Throttle.Fail(userKey)
		s.guard.Throttle.Fail(ipKey)
		audit.Note(r.Context(), "", map[string]any{"second_step": "wrong code"})
		http.Error(w, `{"error":"کد درست نیست."}`, http.StatusUnauthorized)
		return
	case errors.Is(err, auth.ErrRecoveryUnsaved):
		http.Error(w, `{"error":"کد پشتیبان الان پذیرفته نمی‌شود، چون پایگاه داده در دسترس نیست. کد برنامه را وارد کنید."}`,
			http.StatusServiceUnavailable)
		return
	case errors.Is(err, auth.ErrSecondFactorOff):
		expired() // turned off in the meantime: start again
		return
	case err != nil:
		logReportError("second step", err)
		http.Error(w, `{"error":"بررسی کد انجام نشد."}`, http.StatusInternalServerError)
		return
	}
	if !s.spendChallenge(claims.SID, time.Unix(claims.Expires, 0)) {
		expired()
		return
	}
	s.guard.Throttle.Succeed(userKey)
	audit.Note(r.Context(), "", map[string]any{"second_step": "passed", "recovery_code": recovery})
	extra := map[string]any{}
	if recovery {
		extra["recovery_used"] = true
		extra["recovery_left"] = left
	}
	s.issueSession(w, r, u, extra)
}

// spendChallenge records a challenge as used, once; false if it already was.
func (s *Server) spendChallenge(sid string, until time.Time) bool {
	now := time.Now()
	s.spentChallenges.Range(func(k, v any) bool {
		if now.After(v.(time.Time)) {
			s.spentChallenges.Delete(k)
		}
		return true
	})
	_, used := s.spentChallenges.LoadOrStore(sid, until)
	return !used
}

func (s *Server) challengeSpent(sid string) bool {
	_, used := s.spentChallenges.Load(sid)
	return used
}

// ------------------------------------------------------------ own account

// handleTwoStep says where this account stands.
func (s *Server) handleTwoStep(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r)
	if u == nil {
		http.Error(w, `{"error":"برای ادامه وارد شوید."}`, http.StatusUnauthorized)
		return
	}
	cur, err := s.guard.Users.Get(u.Username)
	if err != nil {
		writeUserError(w, err)
		return
	}
	out := map[string]any{
		"on":            cur.SecondFactor.On(),
		"pending":       cur.SecondFactor != nil && !cur.SecondFactor.Enabled,
		"recovery_left": s.guard.Users.RecoveryLeft(u.Username),
		// Whether codes can be mailed at all, so the page offers email only
		// where it would work; and the account's own address, to prefill.
		"email_available": s.codeMail != nil,
		"email":           cur.Email,
		// The same for SMS: whether the panel is set up, and the number.
		"sms_available": s.smsConfig().Enabled(),
		"phone":         cur.Phone,
	}
	if cur.SecondFactor.On() {
		out["method"] = cur.SecondFactor.Via()
	}
	writeJSON(w, out)
}

// handleTwoStepSendCode mails a code to confirm a change to one's own
// email-method account (turning it off).
func (s *Server) handleTwoStepSendCode(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r)
	if u == nil {
		http.Error(w, `{"error":"برای ادامه وارد شوید."}`, http.StatusUnauthorized)
		return
	}
	cur, err := s.guard.Users.Get(u.Username)
	method := s.guard.Users.SecondFactorMethod(u.Username)
	if err != nil || (method != auth.MethodEmail && method != auth.MethodSMS) {
		twoStepError(w, auth.ErrSecondFactorOff)
		return
	}
	to, shown := codeAddress(method, cur)
	if err := s.sendCodeVia(r.Context(), method, "acct:"+u.Username, to, codeForConfirm); err != nil {
		codeMailError(w, err)
		return
	}
	writeJSON(w, map[string]any{"sent_to": shown})
}

// recheckPassword asks for the password again before a change to the second
// step, throttled like changing one's own password.
func (s *Server) recheckPassword(w http.ResponseWriter, u *auth.User, password string) bool {
	key := auth.PasswordKey(u.Username)
	if blocked, wait := s.guard.Throttle.Blocked(key); blocked {
		w.Header().Set("Retry-After", itoaSeconds(wait))
		http.Error(w, `{"error":"تلاش ناموفق زیاد بود؛ کمی بعد دوباره تلاش کنید."}`, http.StatusTooManyRequests)
		return false
	}
	if _, err := s.guard.Users.Authenticate(u.Username, password); err != nil {
		s.guard.Throttle.Fail(key)
		http.Error(w, `{"error":"گذرواژه درست نیست."}`, http.StatusForbidden)
		return false
	}
	s.guard.Throttle.Succeed(key)
	return true
}

type twoStepRequest struct {
	Password string `json:"password"`
	Code     string `json:"code"`
	Email    string `json:"email"`
	Phone    string `json:"phone"`
}

// codeAddress is where an account's codes go and how that is shown: its phone
// for SMS, its email address otherwise.
func codeAddress(method string, u *auth.User) (to, shown string) {
	if method == auth.MethodSMS {
		return u.Phone, maskPhone(u.Phone)
	}
	return u.Email, maskEmail(u.Email)
}

// handleTwoStepSMSBegin starts the SMS method: the password again, and a code
// texted to the number given. The number is not stored until that code comes
// back -- which is what proves it is theirs.
func (s *Server) handleTwoStepSMSBegin(w http.ResponseWriter, r *http.Request) {
	u, req, ok := readTwoStep(w, r)
	if !ok || !s.recheckPassword(w, u, req.Password) {
		return
	}
	phone, good := sms.NormalizePhone(req.Phone)
	if !good {
		http.Error(w, `{"error":"شمارهٔ همراه درست نیست."}`, http.StatusBadRequest)
		return
	}
	if s.guard.Users.SecondFactorOn(u.Username) {
		twoStepError(w, auth.ErrSecondFactorOn)
		return
	}
	if err := s.sendSMSCode(r.Context(), "setup:"+u.Username, phone, codeForSetup); err != nil {
		codeMailError(w, err)
		return
	}
	audit.Note(r.Context(), "user:"+u.Username, map[string]any{"second_step": "sms setup started"})
	writeJSON(w, map[string]any{"sent_to": maskPhone(phone)})
}

// handleTwoStepSMSConfirm finishes it: the texted code proves the number,
// which becomes the account's, and the step is on.
func (s *Server) handleTwoStepSMSConfirm(w http.ResponseWriter, r *http.Request) {
	u, req, ok := readTwoStep(w, r)
	if !ok {
		return
	}
	to, good := s.checkCode("setup:"+u.Username, req.Code)
	if !good {
		twoStepError(w, auth.ErrBadSecondFactor)
		return
	}
	codes, err := s.guard.Users.EnableSMSFactor(u.Username, to, time.Now())
	if err != nil {
		twoStepError(w, err)
		return
	}
	audit.Note(r.Context(), "user:"+u.Username, map[string]any{"second_step": "turned on (sms)"})
	writeJSON(w, map[string]any{"on": true, "recovery_codes": codes})
}

func readTwoStep(w http.ResponseWriter, r *http.Request) (*auth.User, twoStepRequest, bool) {
	var req twoStepRequest
	u := auth.UserFrom(r)
	if u == nil {
		http.Error(w, `{"error":"برای ادامه وارد شوید."}`, http.StatusUnauthorized)
		return nil, req, false
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		http.Error(w, `{"error":"درخواست نامعتبر است."}`, http.StatusBadRequest)
		return nil, req, false
	}
	return u, req, true
}

// handleTwoStepBegin makes a new key and shows it: the QR code, and the key
// itself for typing into an app by hand.
func (s *Server) handleTwoStepBegin(w http.ResponseWriter, r *http.Request) {
	u, req, ok := readTwoStep(w, r)
	if !ok || !s.recheckPassword(w, u, req.Password) {
		return
	}
	secret, err := s.guard.Users.BeginSecondFactor(u.Username)
	if err != nil {
		twoStepError(w, err)
		return
	}
	audit.Note(r.Context(), "user:"+u.Username, map[string]any{"second_step": "setup started"})
	uri := auth.TOTPURI(twoStepIssuer, u.Username, secret)
	code, err := qr.Encode(uri)
	if err != nil {
		logReportError("two-step qr", err)
		http.Error(w, `{"error":"کد QR ساخته نشد."}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{
		"qr_svg": code.SVG(),
		// Grouped in fours, the way apps show a key for typing.
		"secret": groupKey(secret),
		"uri":    uri,
	})
}

func groupKey(secret string) string {
	var b strings.Builder
	for i, c := range secret {
		if i > 0 && i%4 == 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(c)
	}
	return b.String()
}

// handleTwoStepConfirm turns the step on with the app's first code and hands
// back the recovery codes, once.
func (s *Server) handleTwoStepConfirm(w http.ResponseWriter, r *http.Request) {
	u, req, ok := readTwoStep(w, r)
	if !ok {
		return
	}
	codes, err := s.guard.Users.ConfirmSecondFactor(u.Username, req.Code, time.Now())
	if err != nil {
		twoStepError(w, err)
		return
	}
	audit.Note(r.Context(), "user:"+u.Username, map[string]any{"second_step": "turned on"})
	writeJSON(w, map[string]any{"on": true, "recovery_codes": codes})
}

// handleTwoStepDisable turns it off: the password, and a code from the app
// or a recovery code -- proof of both halves.
func (s *Server) handleTwoStepDisable(w http.ResponseWriter, r *http.Request) {
	u, req, ok := readTwoStep(w, r)
	if !ok || !s.recheckPassword(w, u, req.Password) {
		return
	}
	if s.guard.Users.SecondFactorOn(u.Username) {
		if _, _, err := s.verifySecondStep(u.Username, "acct:"+u.Username, req.Code); err != nil {
			twoStepError(w, err)
			return
		}
	}
	if err := s.guard.Users.DisableSecondFactor(u.Username); err != nil {
		twoStepError(w, err)
		return
	}
	audit.Note(r.Context(), "user:"+u.Username, map[string]any{"second_step": "turned off"})
	writeJSON(w, map[string]any{"on": false})
}

// handleTwoStepRecovery replaces the recovery codes.
func (s *Server) handleTwoStepRecovery(w http.ResponseWriter, r *http.Request) {
	u, req, ok := readTwoStep(w, r)
	if !ok || !s.recheckPassword(w, u, req.Password) {
		return
	}
	codes, err := s.guard.Users.RenewRecoveryCodes(u.Username)
	if err != nil {
		twoStepError(w, err)
		return
	}
	audit.Note(r.Context(), "user:"+u.Username, map[string]any{"second_step": "new recovery codes"})
	writeJSON(w, map[string]any{"recovery_codes": codes})
}

// handleTwoStepReset is an administrator turning the step off for somebody
// who has lost their phone and their recovery codes. Their sessions end too:
// whoever has the phone should not stay signed in on it.
func (s *Server) handleTwoStepReset(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.URL.Query().Get("u"))
	audit.Note(r.Context(), "user:"+clipRunes(name, 64), map[string]any{"second_step": "reset by an administrator"})
	if err := s.guard.Users.DisableSecondFactor(name); err != nil {
		twoStepError(w, err)
		return
	}
	_ = s.guard.Users.RevokeTokens(name)
	writeJSON(w, map[string]any{"ok": true})
}

func twoStepError(w http.ResponseWriter, err error) {
	status, msg := http.StatusBadRequest, "انجام نشد."
	switch {
	case errors.Is(err, auth.ErrBadSecondFactor):
		status, msg = http.StatusForbidden, "کد درست نیست."
	case errors.Is(err, auth.ErrSecondFactorOn):
		msg = "ورود دومرحله‌ای همین حالا روشن است."
	case errors.Is(err, auth.ErrSecondFactorOff):
		msg = "ورود دومرحله‌ای برای این حساب راه‌اندازی نشده است."
	case errors.Is(err, auth.ErrNoSealer):
		status, msg = http.StatusServiceUnavailable, "ورود دومرحله‌ای روی این سرور در دسترس نیست."
	case errors.Is(err, auth.ErrRecoveryUnsaved):
		status, msg = http.StatusServiceUnavailable, "کد پشتیبان الان پذیرفته نمی‌شود؛ کد برنامه را وارد کنید."
	case errors.Is(err, auth.ErrNoSuchUser):
		status, msg = http.StatusNotFound, "چنین حسابی وجود ندارد."
	default:
		logReportError("two-step", err)
	}
	body, _ := json.Marshal(map[string]string{"error": msg})
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	w.Write(body)
}

// handleTwoStepEmailBegin starts the email method: the password again, and
// a code mailed to the address given. The address is not stored until that
// code comes back -- which is what proves it is theirs.
func (s *Server) handleTwoStepEmailBegin(w http.ResponseWriter, r *http.Request) {
	u, req, ok := readTwoStep(w, r)
	if !ok || !s.recheckPassword(w, u, req.Password) {
		return
	}
	email := strings.TrimSpace(req.Email)
	if a, err := mail.ParseAddress(email); err != nil || a.Address != email {
		http.Error(w, `{"error":"نشانی ایمیل درست نیست."}`, http.StatusBadRequest)
		return
	}
	if s.guard.Users.SecondFactorOn(u.Username) {
		twoStepError(w, auth.ErrSecondFactorOn)
		return
	}
	if err := s.sendCode(r.Context(), "setup:"+u.Username, email, codeForSetup); err != nil {
		codeMailError(w, err)
		return
	}
	audit.Note(r.Context(), "user:"+u.Username, map[string]any{"second_step": "email setup started"})
	writeJSON(w, map[string]any{"sent_to": maskEmail(email)})
}

// handleTwoStepEmailConfirm finishes it: the mailed code proves the address,
// which becomes the account's, and the step is on.
func (s *Server) handleTwoStepEmailConfirm(w http.ResponseWriter, r *http.Request) {
	u, req, ok := readTwoStep(w, r)
	if !ok {
		return
	}
	to, good := s.checkCode("setup:"+u.Username, req.Code)
	if !good {
		twoStepError(w, auth.ErrBadSecondFactor)
		return
	}
	codes, err := s.guard.Users.EnableEmailFactor(u.Username, to, time.Now())
	if err != nil {
		twoStepError(w, err)
		return
	}
	audit.Note(r.Context(), "user:"+u.Username, map[string]any{"second_step": "turned on (email)"})
	writeJSON(w, map[string]any{"on": true, "recovery_codes": codes})
}
