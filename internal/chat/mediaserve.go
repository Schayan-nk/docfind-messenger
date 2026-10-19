package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// Serving stored files (SPEC 6.4, security checks 2 and 7): only to a reader
// who may see them, with HTTP Range for video, and as a download
// ("attachment") for every type the page does not show itself.

// inlineTypes are shown by the page; every other type is a download.
var inlineTypes = map[string]bool{
	"image/jpeg": true, "image/png": true, "image/gif": true, "image/webp": true,
	"video/mp4": true, "video/webm": true,
	"audio/ogg": true, "audio/webm": true, "audio/mpeg": true, "audio/mp4": true,
}

// MediaFile is GET /api/chat/media/{id} (and /thumb for the small picture).
// An id the reader may not open answers exactly like one that does not exist.
func (a *API) MediaFile(w http.ResponseWriter, r *http.Request) {
	me := a.User(r)
	ctx := r.Context()
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 || a.Svc.Media == nil {
		writeErr(w, ErrNotMember)
		return
	}
	if ok, err := a.Svc.Store.MayReadMedia(ctx, me, id); err != nil || !ok {
		writeErr(w, ErrNotMember)
		return
	}
	all, err := a.Svc.Store.MediaByIDs(ctx, []int64{id})
	m := all[id]
	if err != nil || m == nil {
		writeErr(w, ErrNotMember)
		return
	}
	sha, mime, name := m.sha, m.Mime, m.Name
	if strings.HasSuffix(r.URL.Path, "/thumb") {
		if m.thumbSHA == "" {
			writeErr(w, ErrNotMember)
			return
		}
		sha, mime, name = m.thumbSHA, "image/jpeg", "thumb.jpg"
	}
	f, err := os.Open(a.Svc.Media.pathFor(sha))
	if err != nil {
		writeErr(w, ErrNotMember)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		writeErr(w, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", mime)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Cache-Control", "private, max-age=86400")
	disp := "attachment"
	if inlineTypes[mime] && r.URL.Query().Get("download") != "1" {
		disp = "inline"
	}
	if name == "" {
		name = "file"
	}
	h.Set("Content-Disposition", disp+`; filename="`+asciiName(name)+`"; filename*=UTF-8''`+url.PathEscape(name))
	// ServeContent answers Range requests, so a video plays from any point.
	http.ServeContent(w, r, "", st.ModTime(), f)
}

// asciiName is a file name a header can carry for old browsers.
func asciiName(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 32 || r > 126 || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, s)
}

// DocOf is GET /api/chat/chats/{id}/messages/{msg}/doc: the DocFind document
// a message shares, as this reader may see it.
func (a *API) DocOf(w http.ResponseWriter, r *http.Request) {
	chatID, me, ok := a.memberChat(w, r)
	if !ok {
		return
	}
	msg, err := strconv.ParseInt(r.PathValue("msg"), 10, 64)
	if err != nil {
		writeErr(w, ErrNotMember)
		return
	}
	msgs, err := a.Svc.Store.ByIDs(r.Context(), chatID, me, []int64{msg})
	if err != nil || len(msgs) != 1 || msgs[0].DocRef == "" {
		writeErr(w, ErrNotMember)
		return
	}
	a.Svc.dressDocs(me, msgs)
	writeJSON(w, http.StatusOK, map[string]any{"doc": msgs[0].Doc})
}

// dressDocs fills in, for one reader, the documents messages share: the card
// when they may open it, {"denied":true} when they may not.
func (s *Service) dressDocs(viewer string, list []*Message) {
	for _, m := range list {
		if m.DocRef == "" {
			continue
		}
		if s.Docs != nil {
			if card, ok := s.Docs.DocCard(viewer, m.DocRef); ok {
				m.Doc = card
				continue
			}
		}
		m.Doc = map[string]bool{"denied": true}
	}
}

// MediaFolderGet is GET /api/chat/settings/media (administrators): where
// chat files are kept and where that choice came from.
func (a *API) MediaFolderGet(w http.ResponseWriter, r *http.Request) {
	if a.Svc.Media == nil {
		writeErr(w, ErrNoStorage)
		return
	}
	root, from := a.Svc.Media.Root()
	werr := ""
	if err := CheckWritable(root); err != nil {
		werr = err.Error()
	}
	writeJSON(w, http.StatusOK, map[string]any{"folder": root, "from": from, "problem": werr})
}

// MediaFolderSet is POST /api/chat/settings/media {"folder":"D:\\..."}.
func (a *API) MediaFolderSet(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Folder string `json:"folder"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil || a.Svc.Media == nil {
		writeErr(w, ErrNoStorage)
		return
	}
	if err := a.Svc.Media.Set(body.Folder); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "پوشه پذیرفته نشد: " + err.Error()})
		return
	}
	a.MediaFolderGet(w, r)
}

// mediaOf loads one file's details for a message being sent.
func (s *Service) mediaOf(ctx context.Context, id int64) *Media {
	all, err := s.Store.MediaByIDs(ctx, []int64{id})
	if err != nil {
		return nil
	}
	return all[id]
}
