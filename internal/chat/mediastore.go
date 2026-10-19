package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// The chat media folder (SPEC phase 0, item 4): where uploaded files live,
// each under its SHA-256 (root/ab/cd/<sha>), never under the name it was sent
// with. DOCFIND_CHAT_MEDIA wins when set; otherwise an administrator chooses
// the folder in the settings page (stored in a small JSON file beside the
// accounts); otherwise the default beside the accounts is used.

// MediaFolder resolves and changes the chat media folder.
type MediaFolder struct {
	mu       sync.RWMutex
	env      string
	setting  string // the JSON file an administrator's choice is kept in
	chosen   string
	fallback string
}

// OpenMediaFolder reads the administrator's choice, if any.
func OpenMediaFolder(fallback, settingFile string) (*MediaFolder, error) {
	m := &MediaFolder{env: strings.TrimSpace(os.Getenv("DOCFIND_CHAT_MEDIA")), setting: settingFile, fallback: fallback}
	raw, err := os.ReadFile(settingFile)
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, fmt.Errorf("chat: reading %s: %w", settingFile, err)
	}
	var v struct {
		Folder string `json:"folder"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("chat: parsing %s: %w", settingFile, err)
	}
	m.chosen = v.Folder
	return m, nil
}

// Root is the folder in use and where the choice came from: "env",
// "setting" or "default".
func (m *MediaFolder) Root() (string, string) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	switch {
	case m.env != "":
		return m.env, "env"
	case m.chosen != "":
		return m.chosen, "setting"
	}
	return m.fallback, "default"
}

// Set stores an administrator's choice. The folder must be an absolute path
// this process can create and write; the environment, when set, stays in
// charge and the choice is refused.
func (m *MediaFolder) Set(dir string) error {
	if m.env != "" {
		return errors.New("DOCFIND_CHAT_MEDIA is set on the server; the folder is chosen there")
	}
	dir = filepath.Clean(strings.TrimSpace(dir))
	if !filepath.IsAbs(dir) {
		return errors.New("the folder must be a full path")
	}
	if err := CheckWritable(dir); err != nil {
		return err
	}
	b, _ := json.Marshal(map[string]string{"folder": dir})
	if err := os.WriteFile(m.setting, b, 0o600); err != nil {
		return err
	}
	m.mu.Lock()
	m.chosen = dir
	m.mu.Unlock()
	return nil
}

// CheckWritable makes dir if needed and proves a file can be written in it.
func CheckWritable(dir string) error {
	if err := os.MkdirAll(filepath.Join(dir, "tmp"), 0o750); err != nil {
		return fmt.Errorf("the folder cannot be made: %w", err)
	}
	p := filepath.Join(dir, "tmp", "write-test")
	if err := os.WriteFile(p, []byte("ok"), 0o600); err != nil {
		return fmt.Errorf("the folder cannot be written: %w", err)
	}
	return os.Remove(p)
}

// pathFor is where a file with this SHA-256 (lower-case hex) is kept.
func (m *MediaFolder) pathFor(sha string) string {
	root, _ := m.Root()
	return filepath.Join(root, sha[:2], sha[2:4], sha)
}

func (m *MediaFolder) partFor(id string) string {
	root, _ := m.Root()
	return filepath.Join(root, "tmp", id+".part")
}

// ------------------------------------------------------------------ rows

// Media is one stored file as the page sees it.
type Media struct {
	ID       int64  `json:"id"`
	Kind     string `json:"kind"`
	Mime     string `json:"mime"`
	Size     int64  `json:"size"`
	Width    int    `json:"w,omitempty"`
	Height   int    `json:"h,omitempty"`
	Duration int    `json:"dur,omitempty"` // milliseconds
	Waveform []int  `json:"wave,omitempty"`
	Name     string `json:"name,omitempty"`
	Thumb    bool   `json:"thumb,omitempty"`
	Mini     []byte `json:"mini,omitempty"` // base64 in JSON: the blur-up picture

	sha, thumbSHA, uploader string
}

const mediaCols = `id, kind, mime, size, COALESCE(width, 0), COALESCE(height, 0), COALESCE(duration_ms, 0), waveform,
	original_name, COALESCE(thumb_sha256, ''), mini, sha256, COALESCE(uploaded_by, '')`

func scanMedia(r pgx.Row) (*Media, error) {
	m := &Media{}
	var wave []byte
	err := r.Scan(&m.ID, &m.Kind, &m.Mime, &m.Size, &m.Width, &m.Height, &m.Duration, &wave, &m.Name, &m.thumbSHA, &m.Mini, &m.sha, &m.uploader)
	for _, b := range wave {
		m.Waveform = append(m.Waveform, int(b))
	}
	m.Thumb = m.thumbSHA != ""
	return m, err
}

// InsertMedia records a stored file.
func (s *Store) InsertMedia(ctx context.Context, m *Media, uploader string, ocrLater bool) (int64, error) {
	wave := make([]byte, 0, len(m.Waveform))
	for _, v := range m.Waveform {
		wave = append(wave, byte(v))
	}
	var up any
	if uploader != "" {
		up = uploader
	}
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO chat_media (sha256, mime, size, kind, width, height, duration_ms, waveform, thumb_sha256, mini, original_name, uploaded_by)
		VALUES ($1, $2, $3, $4, NULLIF($5, 0), NULLIF($6, 0), NULLIF($7, 0), $8, NULLIF($9, ''), $10, $11, $12) RETURNING id`,
		m.sha, m.Mime, m.Size, m.Kind, m.Width, m.Height, m.Duration, nilIfEmpty(wave), m.thumbSHA, nilIfEmpty(m.Mini), m.Name, up).Scan(&id)
	return id, err
}

func nilIfEmpty(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

// MediaByIDs loads files by id.
func (s *Store) MediaByIDs(ctx context.Context, ids []int64) (map[int64]*Media, error) {
	out := map[int64]*Media{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT `+mediaCols+` FROM chat_media WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		m, err := scanMedia(rows)
		if err != nil {
			return nil, err
		}
		out[m.ID] = m
	}
	return out, rows.Err()
}

// MayReadMedia: the uploader, or a member of a chat with a live message that
// carries the file (or shows it as its link preview's picture). Security
// check 2: an id or a hash alone opens nothing.
func (s *Store) MayReadMedia(ctx context.Context, user string, id int64) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM chat_media WHERE id = $1 AND uploaded_by = $2)
		    OR EXISTS (
		        SELECT 1 FROM chat_messages m
		        JOIN chat_members cm ON cm.chat_id = m.chat_id AND cm.user_id = $2 AND cm.role NOT IN ('left', 'banned')
		        WHERE m.deleted_at IS NULL
		          AND (m.media_id = $1 OR (m.preview ->> 'image') = $1::text))
		    OR EXISTS (SELECT 1 FROM chat_pack_items WHERE media_id = $1)`, id, user).Scan(&ok) // sticker and GIF packs are open to everyone (phase 11)
	return ok, err
}

// SetOCR keeps the text read out of a picture or a PDF, for search.
func (s *Store) SetOCR(ctx context.Context, id int64, text string) error {
	_, err := s.pool.Exec(ctx, `UPDATE chat_media SET ocr_text = $2 WHERE id = $1`, id, text)
	return err
}

// ------------------------------------------------------------------ uploads

// Upload is a chunked upload in progress.
type Upload struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Received int64  `json:"received"`
}

const uploadLife = 24 * time.Hour

// CreateUpload starts an upload; at most 8 open at a time per person.
func (s *Store) CreateUpload(ctx context.Context, user, id, name string, size int64) error {
	var open int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM chat_uploads WHERE user_id = $1 AND expires_at > now()`, user).Scan(&open); err != nil {
		return err
	}
	if open >= 8 {
		return ErrRate
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO chat_uploads (id, user_id, name, size, expires_at) VALUES ($1, $2, $3, $4, $5)`,
		id, user, name, size, time.Now().Add(uploadLife))
	return err
}

// UploadOf returns user's upload (ErrNotMember for anybody else's).
func (s *Store) UploadOf(ctx context.Context, user, id string) (*Upload, error) {
	u := &Upload{}
	err := s.pool.QueryRow(ctx, `SELECT id::text, name, size, received FROM chat_uploads WHERE id = $1 AND user_id = $2 AND expires_at > now()`, id, user).
		Scan(&u.ID, &u.Name, &u.Size, &u.Received)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotMember
	}
	return u, err
}

// Advance moves an upload from `from` to `from+n` bytes; false when another
// request moved it first (the caller answers with where it really is).
func (s *Store) Advance(ctx context.Context, user, id string, from, n int64) (bool, error) {
	t, err := s.pool.Exec(ctx, `UPDATE chat_uploads SET received = received + $4 WHERE id = $1 AND user_id = $2 AND received = $3`, id, user, from, n)
	return t.RowsAffected() == 1, err
}

// EndUpload forgets an upload.
func (s *Store) EndUpload(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM chat_uploads WHERE id = $1`, id)
	return err
}

// ExpiredUploads lists uploads nobody finished, to clear their parts.
func (s *Store) ExpiredUploads(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, `DELETE FROM chat_uploads WHERE expires_at < now() RETURNING id::text`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// ------------------------------------------------------------------ previews

// CachedPreview is a stored link preview no older than a week.
func (s *Store) CachedPreview(ctx context.Context, url string) (json.RawMessage, bool) {
	var b []byte
	err := s.pool.QueryRow(ctx, `SELECT preview FROM chat_link_previews WHERE url = $1 AND fetched_at > now() - interval '7 days'`, url).Scan(&b)
	return b, err == nil
}

// KeepPreview stores a fetched preview.
func (s *Store) KeepPreview(ctx context.Context, url string, p json.RawMessage) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO chat_link_previews (url, preview) VALUES ($1, $2)
		ON CONFLICT (url) DO UPDATE SET preview = EXCLUDED.preview, fetched_at = now()`, url, string(p))
	return err
}

// SetPreview puts (or, with nil, takes away) a message's link preview and
// logs it like an edit, so every device redraws the bubble.
func (s *Store) SetPreview(ctx context.Context, chatID, msgID int64, p json.RawMessage) (*Update, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	n, err := execTag(ctx, tx, `UPDATE chat_messages SET preview = $3 WHERE id = $1 AND chat_id = $2 AND deleted_at IS NULL`, msgID, chatID, nullJSON(p))
	if err != nil || n == 0 {
		return nil, err
	}
	pts, err := bump(ctx, tx, chatID)
	if err != nil {
		return nil, err
	}
	var payload any = map[string]any{"id": msgID, "preview": nil}
	if len(p) > 0 {
		payload = map[string]any{"id": msgID, "preview": p}
	}
	up, err := addUpdate(ctx, tx, chatID, pts, "preview", payload)
	if err != nil {
		return nil, err
	}
	return up, tx.Commit(ctx)
}
