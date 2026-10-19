package find

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Schayan-nk/docfind-messenger/internal/audit"
	"github.com/Schayan-nk/docfind-messenger/internal/auth"
)

// TokenMaxAge bounds how long an issued sign-in token stays valid.
//
// Deliberately short for a signed token: it is the ceiling on how long a
// stolen one is useful, and the only limit that applies when nothing has
// happened to bump the account's token version.
const TokenMaxAge = 12 * time.Hour

// AuthEnabled reports whether this server is gating requests behind accounts.
//
// When no account store is configured the server keeps its original
// loopback-and-token behaviour, so running it as a desktop tool on one machine
// is unchanged.
func (s *Server) AuthEnabled() bool { return s.guard != nil }

// NeedsFirstAdmin reports whether the account store is empty and the app
// should be asking for its first administrator instead of a password.
func (s *Server) NeedsFirstAdmin() bool {
	return s.AuthEnabled() && s.guard.Users.Count() == 0
}

// mutating wraps a state-changing handler with an origin check, so a signed-in
// user's browser cannot be made to act by another site.
func (s *Server) mutating(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth.SameOrigin(r) {
			http.Error(w, `{"error":"درخواست از یک سایت دیگر آمده بود و پذیرفته نشد."}`, http.StatusForbidden)
			return
		}
		// A change sent twice -- a queued offline write retried after its
		// answer was lost -- is done once (idempotency.go).
		s.idempotent(w, r, next)
	}
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// handleLogin signs a user in.
//
// Failures are deliberately uniform: the same message and status whether the
// account is unknown, the password is wrong, or the account is disabled.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.AuthEnabled() {
		http.Error(w, `{"error":"حساب‌های کاربری روی این سرور فعال نیست."}`, http.StatusNotFound)
		return
	}
	var req loginRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		http.Error(w, `{"error":"درخواست نامعتبر است."}`, http.StatusBadRequest)
		return
	}
	// Audited under the name that was TRIED: a run of failures against one
	// account is exactly what the trail is for, and nobody is signed in yet
	// to be the actor. Clipped, because it is whatever was typed.
	tried := clipRunes(strings.TrimSpace(req.Username), 64)
	audit.NoteActor(r.Context(), tried)
	audit.Note(r.Context(), "user:"+tried, nil)

	ip := s.guard.ClientIP(r)
	// Normalised the way the account store matches names, so "Boss" and
	// "BOSS" share one counter -- they are one account.
	userKey, ipKey := auth.LoginKey(req.Username), auth.IPKey(ip)
	for _, k := range []string{userKey, ipKey} {
		if blocked, wait := s.guard.Throttle.Blocked(k); blocked {
			w.Header().Set("Retry-After", itoaSeconds(wait))
			http.Error(w, `{"error":"تلاش ناموفق زیاد بود؛ کمی بعد دوباره تلاش کنید."}`, http.StatusTooManyRequests)
			return
		}
	}

	u, err := s.guard.Users.Authenticate(req.Username, req.Password)
	if err != nil {
		s.guard.Throttle.Fail(userKey)
		s.guard.Throttle.Fail(ipKey)
		// The lock this failure set, said now rather than on the next try:
		// otherwise the last wrong password reads like any other.
		if blocked, wait := s.guard.Throttle.Blocked(userKey); blocked {
			w.Header().Set("Retry-After", itoaSeconds(wait))
			http.Error(w, `{"error":"گذرواژه چند بار نادرست وارد شد و حساب برای `+
				faMinutes(wait)+` قفل شد."}`, http.StatusTooManyRequests)
			return
		}
		http.Error(w, `{"error":"نام کاربری یا گذرواژه نادرست است."}`, http.StatusUnauthorized)
		return
	}
	// The policy's duties, while the password is in hand (securitypolicy.go).
	u = s.signInDuties(u, req.Password)
	// Two steps for this account: the password was right, and that is ALL
	// this answer says -- a challenge for the code, not a session. The
	// account's failure counter is left as it is until the code is right, so
	// wrong codes count against it (twostep.go).
	if s.guard.Users.SecondFactorOn(u.Username) {
		s.askSecondStep(w, r, u)
		return
	}
	// The ACCOUNT's counter only. Clearing the address's too let anybody with
	// one working account reset it between guesses at everybody else's.
	s.guard.Throttle.Succeed(userKey)
	s.issueSession(w, r, u, nil)
}

// issueSession signs somebody in: the cookie, and the token in the body.
// extra is added to the answer (the second step says how many recovery
// codes are left).
func (s *Server) issueSession(w http.ResponseWriter, r *http.Request, u *auth.User, extra map[string]any) {
	// A fresh identifier for this sign-in, so the account screen can tell one
	// device from another. It is carried in the token and recorded when the
	// first authenticated request arrives -- not here, because the useful
	// fields are the address and the browser of whoever actually uses it.
	//
	// With the database down the account was checked against the snapshot,
	// and the session is an EMERGENCY one: an hour long, and restricted to
	// reading (auth.Can). The snapshot may be missing a change made since it
	// was written, so it is trusted to open the door and no further.
	sign, age := s.guard.Signer.SignSession, s.guard.Signer.MaxAge()
	emergency := s.guard.Users.Degraded()
	if emergency {
		sign, age = s.guard.Signer.SignEmergency, auth.EmergencySessionAge
		audit.Note(r.Context(), "", map[string]any{"emergency": true})
	}
	token, expires, err := sign(u.Username, u.TokenVersion, auth.NewSessionID())
	if err != nil {
		http.Error(w, `{"error":"صدور نشست انجام نشد؛ دوباره تلاش کنید."}`, http.StatusInternalServerError)
		return
	}
	auth.WriteCookie(w, r, token, age, s.guard.TrustProxyHeaders)
	// The token also comes back in the body so a non-browser caller (a script
	// pulling a CSV export, say) can use it as a bearer token without having
	// to manage a cookie jar.
	pub := u.Public()
	pub["two_step_required"] = s.twoStepRequired(u)
	out := map[string]any{
		"user":       pub,
		"token":      token,
		"expires_at": expires.UTC(),
		"emergency":  emergency,
	}
	for k, v := range extra {
		out[k] = v
	}
	writeJSON(w, out)
}

// handleLogout ends the current session, and optionally every other one.
//
// Clearing the cookie ends this browser's session, but the token itself stays
// valid until it expires -- inherent to a signed token, and worth being
// explicit about: anyone who copied it (from a shared machine, a proxy log, a
// script) keeps access for up to the token lifetime. "?all=1" bumps the
// account's token version, which invalidates every token ever issued to them,
// and is the thing to reach for when a session may have been taken.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if !s.AuthEnabled() {
		writeJSON(w, map[string]any{"ok": true})
		return
	}
	everywhere := r.URL.Query().Get("all") == "1"
	// Identify the caller from their own token -- for the audit trail, and
	// because a signed-out request cannot revoke somebody else's sessions.
	var who *auth.User
	if r2, ok := s.guard.Authenticate(r); ok {
		who = auth.UserFrom(r2)
	}
	if who != nil {
		audit.NoteActor(r.Context(), who.Username)
		audit.Note(r.Context(), "user:"+who.Username, map[string]any{"everywhere": everywhere})
	}
	if everywhere {
		if who == nil || s.guard.Users.RevokeTokens(who.Username) != nil {
			everywhere = false
		}
	}
	auth.ClearCookie(w, r, s.guard.TrustProxyHeaders)
	// Everything this browser kept for the person leaving goes with them: the
	// HTTP cache, and the origin's storage -- localStorage (starred mail, the
	// cases already announced), the offline shell's cache and the service
	// worker, whose push subscription would otherwise keep showing their cases
	// to whoever uses this browser next. NOT "cookies": that directive reaches
	// every site on the registrable domain, and would sign somebody out of the
	// automation system on a neighbouring subdomain. nav.js does the same from
	// the page for a browser that ignores this header.
	w.Header().Set("Clear-Site-Data", `"cache", "storage"`)
	writeJSON(w, map[string]any{"ok": true, "revoked_everywhere": everywhere})
}

// handleMe reports who is signed in, which the UI uses to decide what to show.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	if !s.AuthEnabled() {
		writeJSON(w, map[string]any{"auth_enabled": false})
		return
	}
	if s.NeedsFirstAdmin() {
		writeJSON(w, map[string]any{"auth_enabled": true, "needs_first_admin": true})
		return
	}
	r2, ok := s.guard.Authenticate(r)
	if !ok {
		// passkeys: whether the sign-in page should offer the passkey button;
		// password_reset: the forgotten-password link (passwordreset.go).
		writeJSON(w, map[string]any{"auth_enabled": true, "user": nil, "passkeys": s.passkeysOn(),
			"password_reset": s.passwordResetOn()})
		return
	}
	u := auth.UserFrom(r2)
	pub := u.Public()
	// Normalised on the way out as well as in, so an account whose prefs were
	// written by an older build still renders at a sane size.
	pub["prefs"] = u.Prefs.Normalised()
	// Which sign-in is asking, so the account screen can mark one row of the
	// session list "this device".
	pub["session_id"] = auth.SessionIDFrom(r2)
	// EVERY PERMISSION THIS ACCOUNT EFFECTIVELY HOLDS, so a page can leave out
	// what the person cannot use. The role template and the overrides are
	// already resolved here, by the same function the server gates on, so the
	// menu and the gate cannot disagree.
	//
	// HIDING IS A COURTESY, NOT THE BOUNDARY. Every one of those routes checks
	// for itself; this only stops people clicking doors that refuse them.
	pub["permissions"] = auth.Effective(s.perms.Load(), u)
	// What the sign-in policy still asks of this account before anything else
	// works (securitypolicy.go), and the rules a new password must meet.
	pub["two_step_required"] = s.twoStepRequired(u)
	pub["password_rules"] = s.passwordRules()
	// emergency tells the pages this session was opened from the snapshot, so
	// they can say why the settings are out of reach.
	writeJSON(w, map[string]any{"auth_enabled": true, "user": pub, "emergency": u.Emergency})
}

type createUserRequest struct {
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Password    string `json:"password"`
	Role        string `json:"role"`
	Disabled    bool   `json:"disabled"`
	// Phone is optional: absent leaves the number as it is, "" clears it.
	Phone *string `json:"phone"`
}

// minPasswordLength is a floor, not a policy. Length is the only property that
// reliably helps; composition rules mostly push people toward "Password1!".
const minPasswordLength = 10

// handleFirstAdmin creates the very first administrator.
//
// Only available while the store is empty, and only over a loopback
// connection: on a public listener an empty store would otherwise let whoever
// arrived first take ownership of the archive.
func (s *Server) handleFirstAdmin(w http.ResponseWriter, r *http.Request) {
	if !s.AuthEnabled() || !s.NeedsFirstAdmin() {
		http.Error(w, `{"error":"مدیر از پیش ساخته شده است."}`, http.StatusConflict)
		return
	}
	if !peerIsLoopback(r) {
		http.Error(w, `{"error":"نخستین مدیر باید از روی خود سرور ساخته شود."}`, http.StatusForbidden)
		return
	}
	var req createUserRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req); err != nil {
		http.Error(w, `{"error":"درخواست نامعتبر است."}`, http.StatusBadRequest)
		return
	}
	if len(req.Password) < minPasswordLength {
		http.Error(w, `{"error":"گذرواژه باید دست‌کم ۱۰ نویسه باشد."}`, http.StatusBadRequest)
		return
	}
	u, err := s.guard.Users.Create(req.Username, req.DisplayName, req.Password, auth.RoleAdmin)
	if err != nil {
		writeUserError(w, err)
		return
	}
	writeJSON(w, map[string]any{"user": u.Public()})
}

// handleListUsers returns every account.
func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	list := s.guard.Users.List()
	out := make([]map[string]any, 0, len(list))
	for _, u := range list {
		out = append(out, u.Public())
	}
	writeJSON(w, map[string]any{"users": out})
}

// handleCreateUser adds an account.
func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var req createUserRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req); err != nil {
		http.Error(w, `{"error":"درخواست نامعتبر است."}`, http.StatusBadRequest)
		return
	}
	audit.Note(r.Context(), "user:"+clipRunes(req.Username, 64), map[string]any{"role": req.Role})
	if len(req.Password) < minPasswordLength {
		http.Error(w, `{"error":"گذرواژه باید دست‌کم ۱۰ نویسه باشد."}`, http.StatusBadRequest)
		return
	}
	u, err := s.guard.Users.Create(req.Username, req.DisplayName, req.Password, auth.Role(req.Role))
	if err != nil {
		writeUserError(w, err)
		return
	}
	if u, ok := s.applyPhone(w, r, u, req.Phone); ok {
		writeJSON(w, map[string]any{"user": u.Public()})
	}
}

// handleUpdateUser changes a role, display name, enabled state or grants.
//
// Any change that could reduce someone's access ends their live sessions, so
// it takes effect on the next click rather than whenever they happen to sign
// out.
//
// A field the request leaves out keeps its value. The users list's row
// buttons sent only a role and a state, and until 5.51.0 that blanked the
// rest: every "save" or "enable" replaced the person's display name with
// their user name.
func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username    string  `json:"username"`
		DisplayName *string `json:"display_name"`
		Role        string  `json:"role"`
		Disabled    *bool   `json:"disabled"`
		Phone       *string `json:"phone"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req); err != nil {
		http.Error(w, `{"error":"درخواست نامعتبر است."}`, http.StatusBadRequest)
		return
	}
	cur, err := s.guard.Users.Get(req.Username)
	if err != nil {
		writeUserError(w, err)
		return
	}
	name, disabled := cur.DisplayName, cur.Disabled
	if req.DisplayName != nil {
		name = *req.DisplayName
	}
	if req.Disabled != nil {
		disabled = *req.Disabled
	}
	role := req.Role
	if role == "" {
		role = string(cur.Role)
	}
	audit.Note(r.Context(), "user:"+clipRunes(req.Username, 64),
		map[string]any{"role": role, "disabled": disabled})
	u, err := s.guard.Users.Update(req.Username, name, auth.Role(role), disabled)
	if err != nil {
		writeUserError(w, err)
		return
	}
	if u, ok := s.applyPhone(w, r, u, req.Phone); ok {
		writeJSON(w, map[string]any{"user": u.Public()})
	}
}

// handleSetPassword replaces an account's password and signs it out
// everywhere, which is the point of changing a password you think leaked.
func (s *Server) handleSetPassword(w http.ResponseWriter, r *http.Request) {
	var req createUserRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req); err != nil {
		http.Error(w, `{"error":"درخواست نامعتبر است."}`, http.StatusBadRequest)
		return
	}
	if len(req.Password) < minPasswordLength {
		http.Error(w, `{"error":"گذرواژه باید دست‌کم ۱۰ نویسه باشد."}`, http.StatusBadRequest)
		return
	}
	// That the password was reset, and whose -- never the password.
	audit.Note(r.Context(), "user:"+clipRunes(req.Username, 64), nil)
	if err := s.guard.Users.SetPassword(req.Username, req.Password); err != nil {
		writeUserError(w, err)
		return
	}
	// An administrator setting a new password is the way out of a lockout:
	// the person should be able to use it now, not in an hour.
	s.guard.Throttle.Unlock(auth.LoginKey(req.Username))
	writeJSON(w, map[string]any{"ok": true})
}

// handleDeleteUser removes an account.
func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("username")
	audit.Note(r.Context(), "user:"+clipRunes(name, 64), nil)
	if err := s.guard.Users.Delete(name); err != nil {
		writeUserError(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// writeUserError maps an account-store failure onto a status and a sentence
// the person in front of the screen can act on.
//
// It used to send err.Error() straight to the browser, so an administrator who
// tried to remove the last admin account read "auth: refusing to leave the
// system with no active administrator" -- English, on a Persian page, with the
// Go package prefix still attached. Store failures are sentinel errors, so
// each one gets its own sentence and anything unrecognised falls back to a
// generic line rather than forwarding internal text.
func writeUserError(w http.ResponseWriter, err error) {
	// The statuses below are exactly as they were; only the sentence changed.
	status, msg := http.StatusBadRequest, "درخواست انجام نشد."
	switch {
	case errors.Is(err, auth.ErrNoSuchUser):
		status, msg = http.StatusNotFound, "چنین کاربری وجود ندارد."
	case errors.Is(err, auth.ErrUserExists):
		status, msg = http.StatusConflict, "کاربری با این نام از پیش وجود دارد."
	case errors.Is(err, auth.ErrLastAdmin):
		status, msg = http.StatusConflict, "دست‌کم یک مدیر فعال باید در سیستم بماند."
	case errors.Is(err, auth.ErrBadCredential):
		msg = "نام کاربری یا گذرواژه نادرست است."
	case errors.Is(err, auth.ErrDisabled):
		msg = "این حساب غیرفعال است."
	case errors.Is(err, auth.ErrInvalidRole):
		msg = "نقش انتخاب‌شده معتبر نیست."
	case errors.Is(err, errPolicy):
		msg = err.Error()
	case errors.Is(err, auth.ErrSamePassword):
		msg = "گذرواژهٔ تازه باید با گذرواژهٔ فعلی فرق داشته باشد."
	case errors.Is(err, auth.ErrInvalidName):
		msg = "نام کاربری باید ۳ تا ۶۴ نویسه و شامل حرف، رقم، نقطه، خط تیره یا زیرخط باشد."
	}
	body, _ := json.Marshal(map[string]string{"error": msg})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(body)
}

// isLoopback reports whether an address is this machine.
func isLoopback(ip string) bool {
	return ip == "127.0.0.1" || ip == "::1" || ip == "localhost"
}

// peerIsLoopback reports whether the request arrived over a loopback
// connection, judged by the socket peer and nothing else.
//
// Use this, never isLoopback(guard.ClientIP(r)), for a trust decision.
// ClientIP honours the left-most X-Forwarded-For entry when
// -trust-proxy-headers is on, which is correct for logging and throttling but
// is caller-controlled: with that flag set, a remote request carrying
// "X-Forwarded-For: 127.0.0.1" would otherwise pass for local. RemoteAddr is
// where the connection actually came from and cannot be forged on the wire.
func peerIsLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// No port to strip (a synthetic request, or a Unix socket).
		host = r.RemoteAddr
	}
	return isLoopback(host)
}

// handlePage serves one of the embedded HTML pages.
//
// Identical to any other embedded asset: no-cache so an upgrade is picked up
// at once, an ETag so an unchanged page costs a 304 instead of re-sending it,
// and gzipped once per process. It previously had the compression but no
// validator, which meant dashboard.html -- 122 KB of markup -- was
// re-transferred in full on every single reload.
func (s *Server) handlePage(body []byte) http.HandlerFunc {
	// Every reference this page makes to another embedded asset is rewritten
	// to carry the build version, so that asset can be answered with a
	// year-long, immutable Cache-Control instead of a 304 round trip on every
	// load. See versionAssetLinks.
	body = versionAssetLinks(body, assetStamp())
	// The page's own policy, naming the hash of each script it carries, in
	// place of the blanket 'unsafe-inline' the whole service used to send.
	// Computed once here, from the final bytes -- after the sprite and the
	// loader have been injected, because that is what the browser parses.
	csp := pageCSP(body)
	asset := s.handleAsset(body, "text/html; charset=utf-8")
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", csp)
		asset(w, r)
	}
}

// handleAsset serves a static embedded file.
//
// Validated rather than simply cached for a day. These files are compiled into
// the binary, so their content changes exactly when a new build is deployed --
// and a plain "max-age=86400" meant a browser could go on using the previous
// stylesheet for up to a day after an upgrade, showing a half-old interface
// with no way for the user to know why. The ETag is the content's own hash, so
// a request costs a 304 while nothing has changed and picks up a new build the
// moment one lands.
func (s *Server) handleAsset(body []byte, contentType string) http.HandlerFunc {
	sum := sha256.Sum256(body)
	base := hex.EncodeToString(sum[:8])
	// One tag per representation. The compressed and uncompressed forms are
	// different bytes, so they cannot share a tag: a cache holding the gzip
	// body under the plain tag could hand it to a client that never asked for
	// gzip. Vary: Accept-Encoding keeps a correct cache from doing that
	// anyway, which is why this was not visibly broken, but the tag is
	// supposed to identify what was actually sent.
	etagPlain, etagGzip := `"`+base+`"`, `"`+base+`-gz"`
	var gz precompressed
	return func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Type", contentType)
		if isVersionedRequest(r) {
			// The one case where this asset's own bytes cannot go stale under
			// the URL the browser holds: the URL names the build, so a new
			// build gets a new URL and this one never has to be re-asked for.
			h.Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			h.Set("Cache-Control", "no-cache")
		}

		// Compressed once per process rather than per request. These bytes are
		// baked into the binary and cannot change while it runs, so there is
		// nothing to recompute -- and Chart.js alone is 205 KB that would
		// otherwise be gzipped on every cold load.
		var packed []byte
		useGzip := compressible(contentType) && strings.Contains(r.Header.Get("Accept-Encoding"), "gzip")
		if useGzip {
			if packed = gz.bytes(body); len(packed) == 0 {
				useGzip = false
			}
		}

		etag := etagPlain
		if useGzip {
			etag = etagGzip
		}
		h.Set("ETag", etag)
		if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, etag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}

		if useGzip {
			// Vary is set once by compressResponses, which wraps the whole
			// mux; adding it here too put the header on twice.
			h.Set("Content-Encoding", "gzip")
			h.Set("Content-Length", strconv.Itoa(len(packed)))
			w.Write(packed)
			return
		}
		h.Set("Content-Length", strconv.Itoa(len(body)))
		w.Write(body)
	}
}

// handleServiceWorker serves the worker script.
//
// Served with no wider scope than the root it sits at, and -- the part that was
// missing -- with a validator.
//
// It used to set "Cache-Control: no-cache" and nothing else, which reads as
// correct and is not. no-cache does not mean "do not cache": it means "cache
// this, but revalidate before every use". A browser told to revalidate, and
// given neither an ETag nor a Last-Modified to revalidate WITH, has no way to
// ask whether the copy it holds is current -- and Chrome answers that by using
// the copy it holds.
//
// The consequence was measured, not reasoned about. Two builds differing only
// in the worker's cache version were served in turn to an open page; a plain
// fetch from that page saw the new script, and registration.update() resolved
// having installed nothing. The old worker stayed active. That is precisely the
// failure the previous comment here warned about -- "a service worker that a
// browser will not re-check is very hard to replace, because the stale copy is
// the thing deciding whether to fetch a new one" -- arrived at by the header
// that was meant to prevent it.
//
// handleAsset already solves this for every other embedded asset: the ETag is a
// hash of the content, so revalidation costs a 304 while nothing has changed
// and returns the new bytes the moment a build does. There is no reason for the
// one script whose staleness is hardest to recover from to be the one without
// it.
func (s *Server) handleServiceWorker() http.HandlerFunc {
	// Stamped with the build version before the ETag is computed, so a release
	// always ships a worker whose bytes differ from the last one's -- which is
	// the only thing a browser treats as an update. See stampWorker.
	asset := s.handleAsset(stampWorker(serviceWorkerJS, buildVersion),
		"text/javascript; charset=utf-8")
	return func(w http.ResponseWriter, r *http.Request) {
		// Set before handing over, because handleAsset may answer 304 and
		// return without writing a body. The scope header has to be on that
		// answer too: it is what permits a worker served from /sw.js to control
		// the whole origin.
		w.Header().Set("Service-Worker-Allowed", "/")
		asset(w, r)
	}
}

func itoaSeconds(d time.Duration) string {
	s := int(d.Seconds())
	if s < 1 {
		s = 1
	}
	return itoa(s)
}
