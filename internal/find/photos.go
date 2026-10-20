package find

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Schayan-nk/docfind-messenger/internal/auth"
)

// Several pictures per account (asked for on 2026-10-19, with Telegram's
// profile as the model): every picture somebody sets is kept, newest first,
// and the newest is the one drawn everywhere. A profile opens into the whole
// set -- a full-width picture, a strip to move between them, a viewer.
//
// Each picture is kept twice: a sharp square of up to galleryFull pixels,
// fetched only by the profile's full-width view and the viewer, and the small
// square every list and avatar draws, so a chat list costs no more than it did.
//
//	<avatarDir>/<hash>.jpg              the current picture, small (avatarPath):
//	                                    what every other route still reads
//	<avatarDir>/g/<hash>/<id>.jpg       one picture, sharp
//	<avatarDir>/g/<hash>/<id>.s.jpg     the same picture, small
//
// <id> is when it was set, in milliseconds, thirteen digits: the names sort in
// the order the pictures were set, and the newest is the current one. Making
// an older picture current again gives it a new id, the way Telegram moves it
// to the front.

const (
	// galleryFull is the sharp copy's side: a phone's full width at 2.5x.
	galleryFull = 1024
	// gallerySmall is the side drawn in lists, at up to 58 CSS pixels on a 3x
	// screen.
	gallerySmall = 160
	// galleryMax is how many are kept; setting another lets the oldest go.
	galleryMax = 10
)

// galleryID is a picture's id: thirteen digits, so it can never name a path.
var galleryID = regexp.MustCompile(`^\d{13}$`)

// galleryPhoto is one picture as the API lists it.
type galleryPhoto struct {
	ID string    `json:"id"`
	At time.Time `json:"at"`
}

// errNoPhoto is a picture id that is not in the gallery.
var errNoPhoto = errors.New("no such picture")

// galleryDir is one account's pictures, named from a hash of the account as
// avatarPath is, for the same reasons.
func (s *Server) galleryDir(username string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(username))))
	return filepath.Join(s.avatarDir, "g", hex.EncodeToString(sum[:16]))
}

func (s *Server) galleryFile(username, id string, small bool) string {
	name := id + ".jpg"
	if small {
		name = id + ".s.jpg"
	}
	return filepath.Join(s.galleryDir(username), name)
}

// galleryList is username's pictures, newest first. Callers hold galleryMu.
func (s *Server) galleryList(username string) ([]galleryPhoto, error) {
	if err := s.galleryImport(username); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.galleryDir(username))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []galleryPhoto
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".jpg")
		if !ok || e.IsDir() || !galleryID.MatchString(id) {
			continue
		}
		ms, _ := strconv.ParseInt(id, 10, 64)
		out = append(out, galleryPhoto{ID: id, At: time.UnixMilli(ms).UTC()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

// galleryImport brings a picture set before the gallery existed into it, once:
// without this, the first new picture would silently drop the old one. The
// old file is the small copy only, so that is all the sharp view gets.
func (s *Server) galleryImport(username string) error {
	u, err := s.guard.Users.Get(username)
	if err != nil {
		return err
	}
	if u.Avatar != "png" && u.Avatar != "jpg" {
		return nil
	}
	dir := s.galleryDir(username)
	if _, err := os.Stat(dir); err == nil {
		return nil // already a gallery (possibly emptied on purpose)
	}
	path := s.avatarPath(username, u.Avatar)
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil // the record names a file that is gone: nothing to bring in
	}
	at := time.Now()
	if fi, err := os.Stat(path); err == nil {
		at = fi.ModTime()
	}
	full, err := squareJPEG(raw, galleryFull, 88)
	if err != nil {
		return nil
	}
	small, err := squareJPEG(raw, gallerySmall, 84)
	if err != nil {
		return nil
	}
	return s.galleryWrite(username, galleryIDAt(at), full, small)
}

func galleryIDAt(t time.Time) string { return fmt.Sprintf("%013d", t.UnixMilli()) }

// galleryWrite stores both copies of one picture.
func (s *Server) galleryWrite(username, id string, full, small []byte) error {
	if err := os.MkdirAll(s.galleryDir(username), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(s.galleryFile(username, id, true), small, 0o600); err != nil {
		return err
	}
	// The sharp copy last: it is what makes the picture appear in the list.
	return os.WriteFile(s.galleryFile(username, id, false), full, 0o600)
}

// galleryFreeID is the id for a picture set now, moved on past one already
// taken in the same millisecond.
func (s *Server) galleryFreeID(username string) string {
	t := time.Now()
	for {
		id := galleryIDAt(t)
		if _, err := os.Stat(s.galleryFile(username, id, false)); os.IsNotExist(err) {
			return id
		}
		t = t.Add(time.Millisecond)
	}
}

// galleryAdd keeps raw as username's newest picture, and lets the oldest go
// past galleryMax. Callers hold galleryMu.
func (s *Server) galleryAdd(username string, raw []byte) (galleryPhoto, error) {
	// The type from the bytes, never from Content-Type or a file name (see
	// saveAvatar): only what this program re-encodes is ever stored.
	if imageType(raw) == "" {
		return galleryPhoto{}, errNotImage
	}
	if err := s.galleryImport(username); err != nil {
		return galleryPhoto{}, err
	}
	full, err := squareJPEG(raw, galleryFull, 88)
	if err != nil {
		return galleryPhoto{}, errNotImage
	}
	small, err := squareJPEG(raw, gallerySmall, 84)
	if err != nil {
		return galleryPhoto{}, errNotImage
	}
	id := s.galleryFreeID(username)
	if err := s.galleryWrite(username, id, full, small); err != nil {
		return galleryPhoto{}, err
	}
	list, err := s.galleryList(username)
	if err != nil {
		return galleryPhoto{}, err
	}
	for _, old := range list[min(len(list), galleryMax):] {
		s.galleryRemove(username, old.ID)
	}
	if err := s.galleryCurrent(username); err != nil {
		return galleryPhoto{}, err
	}
	ms, _ := strconv.ParseInt(id, 10, 64)
	return galleryPhoto{ID: id, At: time.UnixMilli(ms).UTC()}, nil
}

func (s *Server) galleryRemove(username, id string) {
	_ = os.Remove(s.galleryFile(username, id, false))
	_ = os.Remove(s.galleryFile(username, id, true))
}

// galleryDelete lets one picture go; the next newest becomes current.
func (s *Server) galleryDelete(username, id string) error {
	if !galleryID.MatchString(id) {
		return errNoPhoto
	}
	if err := s.galleryImport(username); err != nil {
		return err
	}
	if _, err := os.Stat(s.galleryFile(username, id, false)); err != nil {
		return errNoPhoto
	}
	s.galleryRemove(username, id)
	return s.galleryCurrent(username)
}

// galleryDeleteAll lets every picture go: the account has none.
func (s *Server) galleryDeleteAll(username string) error {
	if err := s.galleryImport(username); err != nil {
		return err
	}
	list, err := s.galleryList(username)
	if err != nil {
		return err
	}
	for _, p := range list {
		s.galleryRemove(username, p.ID)
	}
	return s.galleryCurrent(username)
}

// galleryPromote makes an older picture the current one, under a new id.
func (s *Server) galleryPromote(username, id string) (galleryPhoto, error) {
	if !galleryID.MatchString(id) {
		return galleryPhoto{}, errNoPhoto
	}
	if err := s.galleryImport(username); err != nil {
		return galleryPhoto{}, err
	}
	list, err := s.galleryList(username)
	if err != nil {
		return galleryPhoto{}, err
	}
	found := false
	for _, p := range list {
		found = found || p.ID == id
	}
	if !found {
		return galleryPhoto{}, errNoPhoto
	}
	if list[0].ID == id {
		return list[0], nil // already current
	}
	fresh := s.galleryFreeID(username)
	if err := os.Rename(s.galleryFile(username, id, true), s.galleryFile(username, fresh, true)); err != nil {
		return galleryPhoto{}, err
	}
	if err := os.Rename(s.galleryFile(username, id, false), s.galleryFile(username, fresh, false)); err != nil {
		return galleryPhoto{}, err
	}
	if err := s.galleryCurrent(username); err != nil {
		return galleryPhoto{}, err
	}
	ms, _ := strconv.ParseInt(fresh, 10, 64)
	return galleryPhoto{ID: fresh, At: time.UnixMilli(ms).UTC()}, nil
}

// galleryCurrent makes the single picture every other route reads the newest
// one in the gallery, or no picture at all once it is empty.
func (s *Server) galleryCurrent(username string) error {
	entries, _ := os.ReadDir(s.galleryDir(username))
	newest := ""
	for _, e := range entries {
		if id, ok := strings.CutSuffix(e.Name(), ".jpg"); ok && galleryID.MatchString(id) && id > newest {
			newest = id
		}
	}
	if newest == "" {
		if err := s.guard.Users.SetAvatar(username, ""); err != nil {
			return err
		}
		for _, ext := range []string{"png", "jpg"} {
			_ = os.Remove(s.avatarPath(username, ext))
		}
		return nil
	}
	small, err := os.ReadFile(s.galleryFile(username, newest, true))
	if err != nil {
		return err
	}
	if err := os.WriteFile(s.avatarPath(username, "jpg"), small, 0o600); err != nil {
		return err
	}
	_ = os.Remove(s.avatarPath(username, "png"))
	return s.guard.Users.SetAvatar(username, "jpg")
}

// squareJPEG is raw cropped to a centred square and reduced to at most side
// pixels (never enlarged), as a JPEG this program wrote.
func squareJPEG(raw []byte, side, quality int) ([]byte, error) {
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	b := src.Bounds()
	n := min(b.Dx(), b.Dy())
	if n <= 0 {
		return nil, errNotImage
	}
	sq := image.Rect(0, 0, n, n).Add(image.Pt(b.Min.X+(b.Dx()-n)/2, b.Min.Y+(b.Dy()-n)/2))
	out := image.NewRGBA(image.Rect(0, 0, min(n, side), min(n, side)))
	if n <= side {
		draw.Draw(out, out.Bounds(), src, sq.Min, draw.Src)
	} else {
		boxScale(out, src, sq)
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, out, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ---------------------------------------------------------------- routes

// photoSubject is whose pictures a request is about: the caller's own, or --
// for a change -- somebody else's when the caller administers accounts.
func (s *Server) photoSubject(w http.ResponseWriter, r *http.Request, change bool) (string, bool) {
	me := auth.UserFrom(r)
	if me == nil {
		writeJSONError(w, http.StatusUnauthorized, `{"error":"برای ادامه وارد شوید."}`)
		return "", false
	}
	if s.avatarDir == "" {
		writeJSONError(w, http.StatusNotFound, `{"error":"تصویر پروفایل روی این سرور تنظیم نشده است."}`)
		return "", false
	}
	target := strings.TrimSpace(r.URL.Query().Get("u"))
	if target == "" || strings.EqualFold(target, me.Username) {
		target = me.Username
	} else if change && !s.can(r, auth.ActionManageUsers) {
		writeJSONError(w, http.StatusForbidden, `{"error":"برای این کار دسترسی ندارید."}`)
		return "", false
	}
	u, err := s.guard.Users.Get(target)
	if err != nil {
		writeUserError(w, err)
		return "", false
	}
	return u.Username, true
}

// handlePhotos is GET /api/account/photos?u=: somebody's pictures, newest
// (current) first. Open to any signed-in person, as the picture itself is.
func (s *Server) handlePhotos(w http.ResponseWriter, r *http.Request) {
	who, ok := s.photoSubject(w, r, false)
	if !ok {
		return
	}
	s.galleryMu.Lock()
	list, err := s.galleryList(who)
	s.galleryMu.Unlock()
	if err != nil {
		logReportError("pictures", err)
		writeJSONError(w, http.StatusInternalServerError, `{"error":"تصویرها خوانده نشد."}`)
		return
	}
	if list == nil {
		list = []galleryPhoto{}
	}
	writeJSON(w, map[string]any{"photos": list})
}

// handlePhotoCurrent is POST /api/account/photo/current?id=: an older picture
// becomes the one drawn everywhere.
func (s *Server) handlePhotoCurrent(w http.ResponseWriter, r *http.Request) {
	who, ok := s.photoSubject(w, r, true)
	if !ok {
		return
	}
	s.galleryMu.Lock()
	p, err := s.galleryPromote(who, r.URL.Query().Get("id"))
	s.galleryMu.Unlock()
	if errors.Is(err, errNoPhoto) {
		writeJSONError(w, http.StatusNotFound, `{"error":"این تصویر دیگر نیست."}`)
		return
	}
	if err != nil {
		logReportError("pictures: current", err)
		writeJSONError(w, http.StatusInternalServerError, `{"error":"انجام نشد."}`)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "photo": p})
}

// serveGalleryPhoto answers GET /api/account/photo?u=&id=[&size=full]: one
// picture by id, which never changes, so it is cached for good.
func (s *Server) serveGalleryPhoto(w http.ResponseWriter, r *http.Request, username, id string, small bool) {
	if !galleryID.MatchString(id) {
		http.NotFound(w, r)
		return
	}
	raw, err := os.ReadFile(s.galleryFile(username, id, small))
	if err != nil || imageType(raw) != "image/jpeg" {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "image/jpeg")
	h.Set("Cache-Control", "private, max-age=31536000, immutable")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Length", strconv.Itoa(len(raw)))
	_, _ = w.Write(raw)
}
