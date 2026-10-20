package find

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Schayan-nk/docfind-messenger/internal/auth"
)

// A person's own account: their picture, how they want the application to
// look, their password, and where they are signed in.
//
// Everything here is about the caller, resolved from their token rather than
// from a name in the request. That is the whole security model of this file:
// there is no route that takes "which user" as a parameter except the two
// administrative ones at the bottom, which check the permission explicitly.

// maxAvatarUpload bounds what the server will read from an upload.
//
// Four megabytes is well past any photograph that needs to be shown at 96
// pixels, and it is the point at which refusing is kinder than spending time
// on it. The bound is applied to the reader, not to Content-Length: a client
// can claim any length it likes, so the limit has to be on what is actually
// consumed.
const maxAvatarUpload = 4 << 20

// avatarDisplay is the longest edge the stored image is reduced to.
//
// Stored small rather than stored whole and scaled in the browser. The picture
// is drawn at 40 to 96 pixels in every place it appears, and a staff list of
// forty people each carrying a three-megapixel photograph is several megabytes
// of traffic to draw forty thumbnails. Re-encoding also drops whatever EXIF
// the phone attached, which routinely includes where the photograph was taken.
const avatarDisplay = 256

// handleSetPrefs stores display preferences for the caller.
func (s *Server) handleSetPrefs(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r)
	if u == nil {
		writeJSONError(w, http.StatusUnauthorized, `{"error":"برای ادامه وارد شوید."}`)
		return
	}
	var req auth.Prefs
	if err := readJSON(r, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, `{"error":"درخواست نامعتبر است."}`)
		return
	}
	saved, err := s.guard.Users.SetPrefs(u.Username, req)
	if err != nil {
		writeUserError(w, err)
		return
	}
	writeJSON(w, map[string]any{"prefs": saved})
}

// handleChangePassword changes the caller's own password.
//
// The current password is required even though the caller is already signed
// in: a session left open on an unlocked machine would otherwise be enough to
// take an account over, and proving the current password is what stops that.
// Success signs every other device out, because SetPassword raises the token
// version -- which is the correct response to "I am changing my password".
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r)
	if u == nil {
		writeJSONError(w, http.StatusUnauthorized, `{"error":"برای ادامه وارد شوید."}`)
		return
	}
	var req struct {
		Current string `json:"current"`
		Next    string `json:"next"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, `{"error":"درخواست نامعتبر است."}`)
		return
	}
	// The same floor an administrator's reset has. Before accounts were made
	// with a temporary password nothing pressed anybody to come here, so
	// this route never checked; now everybody new does.
	if len(req.Next) < minPasswordLength {
		writeJSONError(w, http.StatusBadRequest, `{"error":"گذرواژهٔ تازه باید دست‌کم ۱۰ نویسه باشد."}`)
		return
	}
	// And whatever the security centre's policy adds (auth/policy.go).
	if broken := s.guard.Policy().Check(u.Username, req.Next); len(broken) > 0 {
		writeUserError(w, policyError(broken))
		return
	}
	// Throttled like the sign-in. The current password is the one thing
	// standing between a session left open and the account itself, and
	// without a limit a stolen session could guess it at leisure.
	pwKey := auth.PasswordKey(u.Username)
	if blocked, wait := s.guard.Throttle.Blocked(pwKey); blocked {
		w.Header().Set("Retry-After", itoaSeconds(wait))
		writeJSONError(w, http.StatusTooManyRequests,
			`{"error":"تلاش ناموفق زیاد بود؛ کمی بعد دوباره تلاش کنید."}`)
		return
	}
	if err := s.guard.Users.ChangePassword(u.Username, req.Current, req.Next); err != nil {
		// A wrong current password is reported as exactly that rather than as
		// a generic failure: the caller has already proved who they are, so
		// there is nothing to be coy about and a vague message would just
		// make them wonder whether the new password was the problem.
		if errors.Is(err, auth.ErrBadCredential) {
			s.guard.Throttle.Fail(pwKey)
			writeJSONError(w, http.StatusForbidden, `{"error":"رمز فعلی درست نیست."}`)
			return
		}
		writeUserError(w, err)
		return
	}
	// The caller's own token is now stale too, so they are signed in again
	// here rather than being bounced to the login screen for doing the right
	// thing. A fresh session id, because this is a new sign-in.
	sid := auth.NewSessionID()
	fresh, err := s.guard.Users.Get(u.Username)
	if err != nil {
		writeUserError(w, err)
		return
	}
	token, expires, err := s.guard.Signer.SignSession(fresh.Username, fresh.TokenVersion, sid)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, `{"error":"ورود مجدد انجام نشد؛ دوباره وارد شوید."}`)
		return
	}
	s.guard.Throttle.Succeed(pwKey)
	s.guard.Sessions.ForgetUser(fresh.Username)
	auth.WriteCookie(w, r, token, time.Until(expires), s.guard.TrustProxyHeaders)
	writeJSON(w, map[string]any{
		"ok":   true,
		"note": "رمز عوض شد. دستگاه‌های دیگر از حساب خارج شدند.",
	})
}

// handleSessions lists where the caller is signed in.
func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r)
	if u == nil {
		writeJSONError(w, http.StatusUnauthorized, `{"error":"برای ادامه وارد شوید."}`)
		return
	}
	here := auth.SessionIDFrom(r)
	list := s.guard.Sessions.For(u.Username)
	out := make([]map[string]any, 0, len(list))
	for _, rec := range list {
		d := auth.Describe(rec.Agent)
		out = append(out, map[string]any{
			"id":        rec.ID,
			"ip":        rec.IP,
			"device":    d.Label,
			"kind":      d.Kind,
			"started":   rec.Started,
			"last_seen": rec.LastSeen,
			// The device reading this, which is the one it must not offer to
			// sign out as if it were somebody else's.
			"current": rec.ID != "" && rec.ID == here,
		})
	}
	writeJSON(w, map[string]any{"sessions": out})
}

// handleSignOutOthers signs every other device out of the caller's account.
//
// Implemented by raising the token version, which invalidates every token
// including the caller's, and then issuing the caller a new one. That is why
// this is "sign out everywhere else" rather than "sign out this one device":
// the registry is not consulted when a request is authenticated -- see the
// note on auth.Sessions -- so removing a row from it would hide a session
// without ending it, which is worse than not offering the button.
func (s *Server) handleSignOutOthers(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r)
	if u == nil {
		writeJSONError(w, http.StatusUnauthorized, `{"error":"برای ادامه وارد شوید."}`)
		return
	}
	if err := s.guard.Users.RevokeTokens(u.Username); err != nil {
		writeUserError(w, err)
		return
	}
	fresh, err := s.guard.Users.Get(u.Username)
	if err != nil {
		writeUserError(w, err)
		return
	}
	sid := auth.NewSessionID()
	token, expires, err := s.guard.Signer.SignSession(fresh.Username, fresh.TokenVersion, sid)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, `{"error":"انجام نشد؛ دوباره وارد شوید."}`)
		return
	}
	s.guard.Sessions.ForgetUser(fresh.Username)
	auth.WriteCookie(w, r, token, time.Until(expires), s.guard.TrustProxyHeaders)
	writeJSON(w, map[string]any{"ok": true, "note": "دستگاه‌های دیگر از حساب خارج شدند."})
}

// ---------------------------------------------------------------- pictures

// avatarPath is where one account's picture lives.
//
// Beside the accounts file, in a directory of its own, named from a hash of
// the account name rather than from the name itself. The name is already
// restricted to letters, digits, dot, dash and underscore, so it would be
// safe to use directly -- the hash is here so that the file name does not
// disclose who works at the company to anybody who can list the directory,
// and so a future change to what a user name may contain cannot turn into a
// path traversal.
func (s *Server) avatarPath(username, ext string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(username))))
	return filepath.Join(s.avatarDir, hex.EncodeToString(sum[:16])+"."+ext)
}

// handleAvatarUpload stores a picture for the caller, or for somebody else if
// the caller administers accounts.
func (s *Server) handleAvatarUpload(w http.ResponseWriter, r *http.Request) {
	me := auth.UserFrom(r)
	if me == nil {
		writeJSONError(w, http.StatusUnauthorized, `{"error":"برای ادامه وارد شوید."}`)
		return
	}
	if s.avatarDir == "" {
		writeJSONError(w, http.StatusNotFound, `{"error":"تصویر پروفایل روی این سرور تنظیم نشده است."}`)
		return
	}

	// Whose picture. Absent means the caller's own; naming somebody else is
	// an administrative act and is checked as one.
	target := strings.TrimSpace(r.URL.Query().Get("u"))
	if target == "" || strings.EqualFold(target, me.Username) {
		target = me.Username
	} else if !s.can(r, auth.ActionManageUsers) {
		writeJSONError(w, http.StatusForbidden, `{"error":"برای این کار دسترسی ندارید."}`)
		return
	}
	subject, err := s.guard.Users.Get(target)
	if err != nil {
		writeUserError(w, err)
		return
	}

	// Read with a hard ceiling. One byte past the limit is read on purpose so
	// that a file exactly at the limit is accepted and anything larger is
	// refused rather than silently truncated into a corrupt image.
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxAvatarUpload+1))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, `{"error":"خواندن تصویر انجام نشد."}`)
		return
	}
	if len(raw) > maxAvatarUpload {
		writeJSONError(w, http.StatusRequestEntityTooLarge,
			`{"error":"حجم تصویر بیش از ۴ مگابایت است."}`)
		return
	}
	if len(raw) == 0 {
		writeJSONError(w, http.StatusBadRequest, `{"error":"تصویری فرستاده نشد."}`)
		return
	}

	// Kept with the earlier ones (photos.go), and drawn everywhere from now on.
	s.galleryMu.Lock()
	p, err := s.galleryAdd(subject.Username, raw)
	s.galleryMu.Unlock()
	if err != nil {
		if errors.Is(err, errNotImage) {
			writeJSONError(w, http.StatusUnsupportedMediaType,
				`{"error":"فقط تصویر JPEG، PNG، GIF یا WebP پذیرفته می‌شود."}`)
			return
		}
		if errors.Is(err, auth.ErrNoSuchUser) {
			writeUserError(w, err)
			return
		}
		writeJSONError(w, http.StatusInternalServerError, `{"error":"ذخیره تصویر انجام نشد."}`)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "username": subject.Username, "photo": p})
}

// errNotImage is a picture or signature whose bytes are not an image.
//
// The type comes from the bytes, never from Content-Type or a file name, and
// what is stored is always re-encoded by this program (photos.go,
// squareJPEG): that is what stops an HTML or SVG payload, or a crafted webp
// or bmp this binary cannot even decode, being kept and served back from our
// own origin as somebody's face.
var errNotImage = errors.New("not an image")

// handleAvatarDelete removes a picture.
func (s *Server) handleAvatarDelete(w http.ResponseWriter, r *http.Request) {
	me := auth.UserFrom(r)
	if me == nil {
		writeJSONError(w, http.StatusUnauthorized, `{"error":"برای ادامه وارد شوید."}`)
		return
	}
	target := strings.TrimSpace(r.URL.Query().Get("u"))
	if target == "" || strings.EqualFold(target, me.Username) {
		target = me.Username
	} else if !s.can(r, auth.ActionManageUsers) {
		writeJSONError(w, http.StatusForbidden, `{"error":"برای این کار دسترسی ندارید."}`)
		return
	}
	subject, err := s.guard.Users.Get(target)
	if err != nil {
		writeUserError(w, err)
		return
	}
	// One picture by its id (the next newest becomes current), or -- with no
	// id, as this route always meant -- every picture.
	s.galleryMu.Lock()
	if id := r.URL.Query().Get("id"); id != "" {
		err = s.galleryDelete(subject.Username, id)
	} else {
		err = s.galleryDeleteAll(subject.Username)
	}
	s.galleryMu.Unlock()
	if errors.Is(err, errNoPhoto) {
		writeJSONError(w, http.StatusNotFound, `{"error":"این تصویر دیگر نیست."}`)
		return
	}
	if err != nil {
		writeUserError(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// handleAvatar serves a picture.
//
// Any signed-in person may see any colleague's picture: it is drawn beside
// their name in the account list and the organisational chart, so restricting
// it further would only break those screens. It is not public -- the route is
// behind authentication like everything else.
func (s *Server) handleAvatar(w http.ResponseWriter, r *http.Request) {
	if s.avatarDir == "" {
		http.NotFound(w, r)
		return
	}
	name := strings.TrimSpace(r.URL.Query().Get("u"))
	if name == "" {
		if me := auth.UserFrom(r); me != nil {
			name = me.Username
		}
	}
	u, err := s.guard.Users.Get(name)
	if err != nil || u.Avatar == "" {
		http.NotFound(w, r)
		return
	}
	// One of the pictures by id (photos.go), small unless size=full; or the
	// current one, sharp, for a profile opened full-width.
	q := r.URL.Query()
	if id := q.Get("id"); id != "" || q.Get("size") == "full" {
		if id == "" {
			s.galleryMu.Lock()
			list, err := s.galleryList(u.Username)
			s.galleryMu.Unlock()
			if err != nil || len(list) == 0 {
				http.NotFound(w, r)
				return
			}
			id = list[0].ID
		}
		s.serveGalleryPhoto(w, r, u.Username, id, q.Get("size") != "full")
		return
	}
	// The extension comes from the account record, and only two values are
	// ever written, so it is checked against that closed set rather than
	// interpolated: a record edited by hand must not be able to name a path.
	if u.Avatar != "png" && u.Avatar != "jpg" {
		http.NotFound(w, r)
		return
	}
	raw, err := os.ReadFile(s.avatarPath(u.Username, u.Avatar))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ct := imageType(raw)
	if ct == "" {
		// Something on disk that is not an image any more. Refused rather
		// than served as octet-stream.
		http.NotFound(w, r)
		return
	}

	sum := sha256.Sum256(raw)
	etag := `"` + hex.EncodeToString(sum[:8]) + `"`
	h := w.Header()
	h.Set("Content-Type", ct)
	h.Set("ETag", etag)
	// Revalidated rather than cached for a day: somebody who changes their
	// picture expects to see the new one, and these are a few kilobytes.
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Content-Type-Options", "nosniff")
	if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.Set("Content-Length", strconv.Itoa(len(raw)))
	_, _ = w.Write(raw)
}

// ------------------------------------------------------ administrative view

// handlePresence tells an administrator who is using the application.
func (s *Server) handlePresence(w http.ResponseWriter, r *http.Request) {
	online := s.guard.Sessions.Online()
	out := make(map[string]any, len(online))
	for name, at := range online {
		out[name] = at
	}
	// Every account's last activity, so the list can say "an hour ago" for
	// the ones that are not online now.
	seen := map[string]any{}
	for _, u := range s.guard.Users.List() {
		if t := s.guard.Sessions.LastSeen(u.Username); !t.IsZero() {
			seen[strings.ToLower(u.Username)] = t
		}
	}
	writeJSON(w, map[string]any{"online": out, "last_seen": seen})
}

// ------------------------------------------------------------- AI settings

// jsonQuote quotes a string for embedding in a hand-built JSON body.
func jsonQuote(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `"خطا"`
	}
	return string(b)
}

// readJSON decodes a request body with a bound on its size.
//
// One megabyte: every body these routes accept is a handful of short fields,
// and an unbounded decode is a way to make the server hold as much memory as
// somebody cares to send.
func readJSON(r *http.Request, dst any) error {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, dst)
}
