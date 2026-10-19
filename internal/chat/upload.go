package chat

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// Chunked, resumable uploads (SPEC 6.4): 512 KB chunks, resume after a lost
// connection, pause and cancel. The page sends each chunk with its offset;
// a chunk sent twice is written over itself, so a retry never corrupts the
// file. The finished file is checked by its first bytes, not its name.

const (
	chunkSize   = 512 << 10
	maxFileSize = 2 << 30
)

var (
	ErrTooBig    = errors.New("chat: file too big")
	ErrBadMedia  = errors.New("chat: file not accepted")
	ErrIncomplet = errors.New("chat: upload not complete")
)

// per-upload locks: one chunk of an upload is written at a time.
var uploadLocks sync.Map

func lockUpload(id string) func() {
	v, _ := uploadLocks.LoadOrStore(id, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// UploadStart is POST /api/chat/uploads {"name","size"}.
func (a *API) UploadStart(w http.ResponseWriter, r *http.Request) {
	me := a.User(r)
	var body struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil || body.Size <= 0 {
		writeErr(w, ErrBadMedia)
		return
	}
	if body.Size > maxFileSize {
		writeErr(w, ErrTooBig)
		return
	}
	if a.Svc.Media == nil {
		writeErr(w, ErrNoStorage)
		return
	}
	if !a.Svc.uploadLimit.allow(me) {
		writeErr(w, ErrRate)
		return
	}
	id := newUUID()
	if err := a.Svc.Store.CreateUpload(r.Context(), me, id, cleanName(body.Name), body.Size); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "chunk": chunkSize, "received": 0})
}

// UploadStatus is GET /api/chat/uploads/{id}: how much has arrived, to resume.
func (a *API) UploadStatus(w http.ResponseWriter, r *http.Request) {
	u, err := a.Svc.Store.UploadOf(r.Context(), a.User(r), r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

// UploadChunk is PUT /api/chat/uploads/{id} with Upload-Offset and up to one
// chunk. An offset behind what has arrived is a retry and is accepted as it
// is; one ahead is refused with where the upload really stands.
func (a *API) UploadChunk(w http.ResponseWriter, r *http.Request) {
	me, id := a.User(r), r.PathValue("id")
	ctx := r.Context()
	defer lockUpload(id)()
	u, err := a.Svc.Store.UploadOf(ctx, me, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	off, err := strconv.ParseInt(r.Header.Get("Upload-Offset"), 10, 64)
	if err != nil || off < 0 || off > u.Received {
		writeJSON(w, http.StatusConflict, map[string]any{"received": u.Received})
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 2*chunkSize))
	if err != nil || off+int64(len(data)) > u.Size {
		writeErr(w, ErrBadMedia)
		return
	}
	f, err := os.OpenFile(a.Svc.Media.partFor(u.ID), os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		writeErr(w, err)
		return
	}
	_, werr := f.WriteAt(data, off)
	cerr := f.Close()
	if werr != nil || cerr != nil {
		writeErr(w, errors.Join(werr, cerr))
		return
	}
	if end := off + int64(len(data)); end > u.Received {
		if _, err := a.Svc.Store.Advance(ctx, me, id, u.Received, end-u.Received); err != nil {
			writeErr(w, err)
			return
		}
		u.Received = end
	}
	writeJSON(w, http.StatusOK, map[string]any{"received": u.Received})
}

// UploadCancel is DELETE /api/chat/uploads/{id}.
func (a *API) UploadCancel(w http.ResponseWriter, r *http.Request) {
	me, id := a.User(r), r.PathValue("id")
	defer lockUpload(id)()
	if _, err := a.Svc.Store.UploadOf(r.Context(), me, id); err != nil {
		writeErr(w, err)
		return
	}
	_ = os.Remove(a.Svc.Media.partFor(id))
	_ = a.Svc.Store.EndUpload(r.Context(), id)
	uploadLocks.Delete(id)
	writeJSON(w, http.StatusOK, map[string]bool{"cancelled": true})
}

// finishReq is what the page knows about a file that the server cannot
// measure without ffmpeg: a video's size and length, a voice message's
// waveform, a poster frame.
type finishReq struct {
	Kind     string `json:"kind"` // photo | video | round | voice | file
	AsFile   bool   `json:"as_file"`
	Width    int    `json:"w"`
	Height   int    `json:"h"`
	Duration int    `json:"dur"`
	Waveform []int  `json:"wave"`
	Poster   string `json:"poster"` // base64 JPEG, video and round only
}

// UploadDone is POST /api/chat/uploads/{id}/done: the file is checked, kept
// under its SHA-256 and recorded; the answer is the media row to send.
func (a *API) UploadDone(w http.ResponseWriter, r *http.Request) {
	me, id := a.User(r), r.PathValue("id")
	ctx := r.Context()
	var req finishReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, ErrBadMedia)
		return
	}
	defer lockUpload(id)()
	u, err := a.Svc.Store.UploadOf(ctx, me, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	if u.Received != u.Size {
		writeErr(w, ErrIncomplet)
		return
	}
	m, err := a.Svc.keep(ctx, me, u, req)
	if err != nil {
		writeErr(w, err)
		return
	}
	_ = a.Svc.Store.EndUpload(ctx, id)
	uploadLocks.Delete(id)
	writeJSON(w, http.StatusOK, map[string]any{"media": m})
}

// keep checks the finished part file and moves it to its place.
func (s *Service) keep(ctx context.Context, user string, u *Upload, req finishReq) (*Media, error) {
	part := s.Media.partFor(u.ID)
	head := make([]byte, 512)
	f, err := os.Open(part)
	if err != nil {
		return nil, err
	}
	n, _ := io.ReadFull(f, head)
	f.Close()
	head = head[:n]
	mime, kind, err := classify(head, u.Name, req.Kind, req.AsFile, s.AllowExecutables)
	if err != nil {
		os.Remove(part)
		return nil, err
	}
	if lim := kindLimit[kind]; lim > 0 && u.Size > lim {
		os.Remove(part)
		return nil, ErrTooBig
	}
	m := &Media{Kind: kind, Mime: mime, Size: u.Size, Name: u.Name}
	if kind == "photo" {
		if err := s.photo(part, m); err != nil {
			os.Remove(part)
			return nil, err
		}
	} else {
		if err := s.measured(m, req); err != nil {
			os.Remove(part)
			return nil, err
		}
	}
	sha, size, err := hashFile(part)
	if err != nil {
		return nil, err
	}
	m.sha, m.Size = sha, size
	final := s.Media.pathFor(sha)
	if err := os.MkdirAll(filepath.Dir(final), 0o750); err != nil {
		return nil, err
	}
	if _, err := os.Stat(final); err == nil {
		os.Remove(part) // the same bytes are already kept
	} else if err := os.Rename(part, final); err != nil {
		return nil, err
	}
	id, err := s.Store.InsertMedia(ctx, m, user, false)
	if err != nil {
		return nil, err
	}
	m.ID = id
	if s.OCR != nil && (kind == "photo" || mime == "application/pdf") {
		go s.readText(id, final, mime)
	}
	return m, nil
}

func (s *Service) readText(id int64, path, mime string) {
	text, err := s.OCR(path, mime)
	if err != nil || strings.TrimSpace(text) == "" {
		return
	}
	if len(text) > 200000 {
		text = text[:200000]
	}
	_ = s.Store.SetOCR(context.Background(), id, text)
}

// measured takes what the page measured for a video, round video or voice
// message, within sane bounds, and keeps the poster frame it sent.
func (s *Service) measured(m *Media, req finishReq) error {
	switch m.Kind {
	case "video", "round":
		if req.Width < 0 || req.Height < 0 || req.Width > 8192 || req.Height > 8192 || req.Duration < 0 || req.Duration > 4*3600*1000 {
			return ErrBadMedia
		}
		if m.Kind == "round" && req.Duration > 65000 {
			return ErrBadMedia
		}
		m.Width, m.Height, m.Duration = req.Width, req.Height, req.Duration
		if req.Poster != "" {
			b, err := base64.StdEncoding.DecodeString(req.Poster)
			if err != nil || len(b) > 400<<10 {
				return ErrBadMedia
			}
			return s.thumbs(b, m)
		}
	case "voice":
		if req.Duration <= 0 || req.Duration > 2*3600*1000 || len(req.Waveform) > 100 {
			return ErrBadMedia
		}
		m.Duration = req.Duration
		for _, v := range req.Waveform {
			m.Waveform = append(m.Waveform, min(max(v, 0), 31))
		}
	}
	return nil
}

// hashFile is the SHA-256 and the size of a file.
func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, err
}

// keepBytes stores small bytes (a thumbnail) under their SHA-256.
func (s *Service) keepBytes(b []byte) (string, error) {
	sum := sha256.Sum256(b)
	sha := hex.EncodeToString(sum[:])
	p := s.Media.pathFor(sha)
	if _, err := os.Stat(p); err == nil {
		return sha, nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return "", err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return "", err
	}
	return sha, os.Rename(tmp, p)
}

// cleanName keeps a file name for display only: no path, no control
// characters, at most 200 characters. It is never used as a path.
func cleanName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	name = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, name)
	if r := []rune(name); len(r) > 200 {
		name = string(r[:200])
	}
	return strings.TrimSpace(name)
}

func newUUID() string {
	return uuidV7()
}

// bytesReader exists so tests can hand classify a file's start.
var _ = bytes.NewReader
