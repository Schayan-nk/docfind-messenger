package find

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Schayan-nk/docfind-messenger/internal/audit"
	"github.com/Schayan-nk/docfind-messenger/internal/auth"
	"github.com/Schayan-nk/docfind-messenger/internal/webauthn"
)

// Passkeys: signing in with the device's own lock instead of a password and
// a code (internal/webauthn checks the cryptography; auth/passkeys.go keeps
// them on the account).
//
// OFF UNLESS CONFIGURED. A passkey is bound to a host NAME served over HTTPS
// -- browsers refuse an IP address and plain http outright -- so nothing is
// offered until DOCFIND_PASSKEY_RPID names the host docfind is reached by.
//
// A passkey sign-in requires the person to be VERIFIED by the device (face,
// fingerprint or PIN), which is why it stands in for both the password and
// the second step. Registering one asks for the password again, like every
// other change to how an account signs in.

const passkeyCeremony = 2 * time.Minute

// UsePasskeys turns passkeys on for rp; nil leaves them off.
func (s *Server) UsePasskeys(rp *webauthn.Relying) { s.passkeyRP = rp }

func (s *Server) passkeysOn() bool { return s.passkeyRP != nil && s.AuthEnabled() }

type pendingPasskey struct {
	user  string // "" for a sign-in, the account for a registration
	until time.Time
}

// newPasskeyChallenge issues a challenge for one ceremony.
func (s *Server) newPasskeyChallenge(user string) ([]byte, error) {
	now := time.Now()
	s.passkeyChallenges.Range(func(k, v any) bool {
		if now.After(v.(pendingPasskey).until) {
			s.passkeyChallenges.Delete(k)
		}
		return true
	})
	ch, err := webauthn.NewChallenge()
	if err != nil {
		return nil, err
	}
	s.passkeyChallenges.Store(webauthn.Encode(ch), pendingPasskey{user: user, until: now.Add(passkeyCeremony)})
	return ch, nil
}

// takePasskeyChallenge finds the challenge the browser answered, and spends
// it: each is good for one ceremony, by the account it was issued to.
func (s *Server) takePasskeyChallenge(clientData []byte, user string) ([]byte, bool) {
	var cd struct {
		Challenge string `json:"challenge"`
	}
	if json.Unmarshal(clientData, &cd) != nil || cd.Challenge == "" {
		return nil, false
	}
	v, ok := s.passkeyChallenges.LoadAndDelete(cd.Challenge)
	if !ok {
		return nil, false
	}
	p := v.(pendingPasskey)
	if time.Now().After(p.until) || p.user != user {
		return nil, false
	}
	raw, err := webauthn.Decode(cd.Challenge)
	return raw, err == nil
}

func (s *Server) passkeysOff(w http.ResponseWriter) bool {
	if s.passkeysOn() {
		return false
	}
	http.Error(w, `{"error":"کلید عبور روی این سرور راه‌اندازی نشده است."}`, http.StatusNotFound)
	return true
}

func decodeFields(fields ...*string) ([][]byte, bool) {
	out := make([][]byte, len(fields))
	for i, f := range fields {
		b, err := webauthn.Decode(*f)
		if err != nil || len(b) == 0 || len(b) > 64<<10 {
			return nil, false
		}
		out[i] = b
	}
	return out, true
}

// ---------------------------------------------------------------- sign-in

func (s *Server) handlePasskeyLoginOptions(w http.ResponseWriter, r *http.Request) {
	if s.passkeysOff(w) {
		return
	}
	ch, err := s.newPasskeyChallenge("")
	if err != nil {
		http.Error(w, `{"error":"انجام نشد."}`, http.StatusInternalServerError)
		return
	}
	// No allowCredentials: the passkey itself says whose it is (a discoverable
	// credential), so the page does not have to ask for a user name first.
	writeJSON(w, map[string]any{"publicKey": map[string]any{
		"challenge": webauthn.Encode(ch), "rpId": s.passkeyRP.ID,
		"userVerification": "required", "timeout": passkeyCeremony.Milliseconds(),
	}})
}

func (s *Server) handlePasskeyLogin(w http.ResponseWriter, r *http.Request) {
	if s.passkeysOff(w) {
		return
	}
	var req struct {
		ID         string `json:"id"`
		ClientData string `json:"client_data"`
		AuthData   string `json:"authenticator_data"`
		Signature  string `json:"signature"`
		UserHandle string `json:"user_handle"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&req); err != nil {
		http.Error(w, `{"error":"درخواست نامعتبر است."}`, http.StatusBadRequest)
		return
	}
	ipKey := auth.IPKey(s.guard.ClientIP(r))
	if blocked, wait := s.guard.Throttle.Blocked(ipKey); blocked {
		w.Header().Set("Retry-After", itoaSeconds(wait))
		http.Error(w, `{"error":"تلاش ناموفق زیاد بود؛ کمی بعد دوباره تلاش کنید."}`, http.StatusTooManyRequests)
		return
	}
	refuse := func(why string) {
		s.guard.Throttle.Fail(ipKey)
		audit.Note(r.Context(), "", map[string]any{"passkey": "refused", "why": why})
		http.Error(w, `{"error":"ورود با این کلید عبور پذیرفته نشد."}`, http.StatusUnauthorized)
	}
	raw, ok := decodeFields(&req.ID, &req.ClientData, &req.AuthData, &req.Signature)
	if !ok {
		refuse("malformed")
		return
	}
	id, clientData, authData, sig := raw[0], raw[1], raw[2], raw[3]
	challenge, ok := s.takePasskeyChallenge(clientData, "")
	if !ok {
		refuse("no such challenge")
		return
	}
	name, key, ok := s.guard.Users.FindPasskey(strings.TrimSpace(req.UserHandle), id)
	if !ok {
		refuse("unknown passkey")
		return
	}
	audit.NoteActor(r.Context(), name)
	audit.Note(r.Context(), "user:"+name, nil)
	u, err := s.guard.Users.Get(name)
	if err != nil || u.Disabled {
		refuse("account unavailable")
		return
	}
	cred := &webauthn.Credential{ID: key.ID, PublicKey: key.PublicKey, Alg: key.Alg, SignCount: key.SignCount}
	count, err := s.passkeyRP.Assert(cred, challenge, clientData, authData, sig)
	if err != nil {
		refuse(err.Error())
		return
	}
	if err := s.guard.Users.TouchPasskey(name, id, count, time.Now()); err != nil {
		refuse(err.Error())
		return
	}
	// Not the account's password counter: a passkey cannot be guessed, and
	// leaving that counter alone means somebody guessing the password can
	// lock the password out without locking the owner out.
	audit.Note(r.Context(), "", map[string]any{"passkey": "signed in"})
	s.issueSession(w, r, u, map[string]any{"passkey": true})
}

// ------------------------------------------------------------ own account

func (s *Server) handlePasskeys(w http.ResponseWriter, r *http.Request) {
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
	list := make([]map[string]any, 0, len(cur.Passkeys))
	for _, k := range cur.Passkeys {
		item := map[string]any{"id": webauthn.Encode(k.ID), "name": k.Name, "created_at": k.CreatedAt}
		if !k.LastUsed.IsZero() {
			item["last_used"] = k.LastUsed
		}
		list = append(list, item)
	}
	writeJSON(w, map[string]any{"available": s.passkeysOn(), "passkeys": list})
}

func (s *Server) handlePasskeyOptions(w http.ResponseWriter, r *http.Request) {
	if s.passkeysOff(w) {
		return
	}
	u, req, ok := readTwoStep(w, r)
	if !ok || !s.recheckPassword(w, u, req.Password) {
		return
	}
	handle, err := s.guard.Users.PasskeyHandle(u.Username)
	if err != nil {
		passkeyError(w, err)
		return
	}
	ch, err := s.newPasskeyChallenge(u.Username)
	if err != nil {
		passkeyError(w, err)
		return
	}
	cur, _ := s.guard.Users.Get(u.Username)
	exclude := []map[string]any{}
	if cur != nil {
		for _, k := range cur.Passkeys {
			exclude = append(exclude, map[string]any{"type": "public-key", "id": webauthn.Encode(k.ID)})
		}
	}
	params := []map[string]any{}
	for _, alg := range webauthn.Algorithms {
		params = append(params, map[string]any{"type": "public-key", "alg": alg})
	}
	display := u.DisplayName
	if display == "" {
		display = u.Username
	}
	writeJSON(w, map[string]any{"publicKey": map[string]any{
		"challenge":        webauthn.Encode(ch),
		"rp":               map[string]any{"id": s.passkeyRP.ID, "name": "docfind-messenger"},
		"user":             map[string]any{"id": handle, "name": u.Username, "displayName": display},
		"pubKeyCredParams": params,
		"authenticatorSelection": map[string]any{
			"residentKey": "required", "requireResidentKey": true, "userVerification": "required",
		},
		"attestation":        "none",
		"excludeCredentials": exclude,
		"timeout":            passkeyCeremony.Milliseconds(),
	}})
}

func (s *Server) handlePasskeyAdd(w http.ResponseWriter, r *http.Request) {
	if s.passkeysOff(w) {
		return
	}
	u := auth.UserFrom(r)
	if u == nil {
		http.Error(w, `{"error":"برای ادامه وارد شوید."}`, http.StatusUnauthorized)
		return
	}
	var req struct {
		Name        string `json:"name"`
		ClientData  string `json:"client_data"`
		Attestation string `json:"attestation"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&req); err != nil {
		http.Error(w, `{"error":"درخواست نامعتبر است."}`, http.StatusBadRequest)
		return
	}
	raw, ok := decodeFields(&req.ClientData, &req.Attestation)
	if !ok {
		http.Error(w, `{"error":"درخواست نامعتبر است."}`, http.StatusBadRequest)
		return
	}
	challenge, ok := s.takePasskeyChallenge(raw[0], u.Username)
	if !ok {
		http.Error(w, `{"error":"زمان افزودن گذشت؛ دوباره تلاش کنید."}`, http.StatusBadRequest)
		return
	}
	cred, err := s.passkeyRP.Register(challenge, raw[0], raw[1])
	if err != nil {
		audit.Note(r.Context(), "user:"+u.Username, map[string]any{"passkey": "registration refused", "why": err.Error()})
		http.Error(w, `{"error":"این کلید عبور پذیرفته نشد. قفل دستگاه (PIN یا اثر انگشت) باید فعال باشد."}`, http.StatusBadRequest)
		return
	}
	err = s.guard.Users.AddPasskey(u.Username, auth.Passkey{
		ID: cred.ID, PublicKey: cred.PublicKey, Alg: cred.Alg, SignCount: cred.SignCount,
		Name: req.Name, CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		passkeyError(w, err)
		return
	}
	audit.Note(r.Context(), "user:"+u.Username, map[string]any{"passkey": "added", "name": clipRunes(req.Name, 60)})
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handlePasskeyRemove(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID       string `json:"id"`
		Password string `json:"password"`
	}
	u := auth.UserFrom(r)
	if u == nil {
		http.Error(w, `{"error":"برای ادامه وارد شوید."}`, http.StatusUnauthorized)
		return
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		http.Error(w, `{"error":"درخواست نامعتبر است."}`, http.StatusBadRequest)
		return
	}
	if !s.recheckPassword(w, u, req.Password) {
		return
	}
	id, err := webauthn.Decode(req.ID)
	if err != nil || len(id) == 0 {
		http.Error(w, `{"error":"درخواست نامعتبر است."}`, http.StatusBadRequest)
		return
	}
	if err := s.guard.Users.RemovePasskey(u.Username, id); err != nil {
		passkeyError(w, err)
		return
	}
	audit.Note(r.Context(), "user:"+u.Username, map[string]any{"passkey": "removed"})
	writeJSON(w, map[string]any{"ok": true})
}

// handlePasskeyReset is an administrator removing every passkey from an
// account -- a lost or stolen device. Their sessions end too.
func (s *Server) handlePasskeyReset(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.URL.Query().Get("u"))
	audit.Note(r.Context(), "user:"+clipRunes(name, 64), map[string]any{"passkey": "all removed by an administrator"})
	if err := s.guard.Users.RemovePasskey(name, nil); err != nil {
		passkeyError(w, err)
		return
	}
	_ = s.guard.Users.RevokeTokens(name)
	writeJSON(w, map[string]any{"ok": true})
}

func passkeyError(w http.ResponseWriter, err error) {
	status, msg := http.StatusBadRequest, "انجام نشد."
	switch {
	case errors.Is(err, auth.ErrPasskeyExists):
		msg = "این کلید عبور پیش‌تر ثبت شده است."
	case errors.Is(err, auth.ErrTooManyKeys):
		msg = "این حساب بیش از این کلید عبور نمی‌پذیرد؛ یکی را حذف کنید."
	case errors.Is(err, auth.ErrNoSuchPasskey):
		status, msg = http.StatusNotFound, "چنین کلید عبوری نیست."
	case errors.Is(err, auth.ErrNoSuchUser):
		status, msg = http.StatusNotFound, "چنین حسابی وجود ندارد."
	default:
		logReportError("passkey", err)
	}
	body, _ := json.Marshal(map[string]string{"error": msg})
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	w.Write(body)
}
