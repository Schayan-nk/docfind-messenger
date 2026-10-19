package chat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Sticker and GIF packs (SPEC 6.4, 8.8). DocFind administrators make packs
// and add files (routes behind the settings permission); every signed-in
// person may see them and send from them. No GIF comes from an outside
// service: the GIF search looks only at the packs' own keywords.

// Pack is one sticker or GIF pack with its items.
type Pack struct {
	ID    int64       `json:"id"`
	Kind  string      `json:"kind"`
	Title string      `json:"title"`
	Items []*PackItem `json:"items"`
}

// PackItem is one sticker or GIF.
type PackItem struct {
	ID       int64  `json:"id"`
	Pack     int64  `json:"pack"`
	Kind     string `json:"kind"`
	Media    int64  `json:"media"`
	Format   string `json:"format"`
	Mime     string `json:"mime"`
	Emoji    string `json:"emoji,omitempty"`
	Keywords string `json:"keywords,omitempty"`
	W        int    `json:"w,omitempty"`
	H        int    `json:"h,omitempty"`
}

// StickerReq is a "send_sticker" frame (stickers and GIFs both).
type StickerReq struct {
	Chat    int64  `json:"chat"`
	CID     string `json:"cid"`
	Topic   int64  `json:"topic"`
	Silent  bool   `json:"silent"`
	ReplyTo int64  `json:"reply_to"`
	Item    int64  `json:"item"`
}

// SendSticker sends a pack item. It counts as media for a group's rights.
func (s *Service) SendSticker(ctx context.Context, user string, r StickerReq, except *conn) (*Message, error) {
	it, err := s.Store.PackItem(ctx, r.Item)
	if err != nil {
		return nil, ErrNoItem
	}
	// The file travels in the extra too: a device that catches up after a
	// reconnect gets the message as logged, before its media was looked up.
	extra, _ := json.Marshal(map[string]any{"item": it.ID, "format": it.Format, "emoji": it.Emoji, "w": it.W, "h": it.H, "media": it.Media, "mime": it.Mime})
	return s.sendKind(ctx, user, r.Chat, r.Topic, r.CID, r.Silent, r.ReplyTo, ActSendMedia, Outgoing{Kind: it.Kind, MediaID: it.Media, Extra: extra}, except)
}

// Packs is GET /api/chat/packs?kind=sticker|gif.
func (a *API) Packs(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	if kind != "sticker" && kind != "gif" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "نوع بسته نامعتبر است."})
		return
	}
	list, err := a.Svc.Store.Packs(r.Context(), kind)
	if err != nil {
		writeErr(w, err)
		return
	}
	u, _ := a.Svc.users.Get(a.User(r))
	writeJSON(w, http.StatusOK, map[string]any{"packs": list, "manage": u != nil && u.Role == "admin"})
}

// PackSearch is GET /api/chat/packs/search?kind=gif&q=...: items whose
// keywords or emoji match, in Persian or English spelling.
func (a *API) PackSearch(w http.ResponseWriter, r *http.Request) {
	kind, q := r.URL.Query().Get("kind"), Normalize(strings.TrimSpace(r.URL.Query().Get("q")))
	if (kind != "sticker" && kind != "gif") || q == "" || UTF16Len(q) > 64 {
		writeJSON(w, http.StatusOK, map[string]any{"items": []*PackItem{}})
		return
	}
	items, err := a.Svc.Store.SearchPacks(r.Context(), kind, q, 60)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func packErr(w http.ResponseWriter, status int, text string) {
	writeJSON(w, status, map[string]string{"error": text})
}

// PackCreate is POST /api/chat/packs {"kind","title"} (administrators).
func (a *API) PackCreate(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Kind  string `json:"kind"`
		Title string `json:"title"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&b) != nil {
		packErr(w, http.StatusBadRequest, "درخواست نامعتبر است.")
		return
	}
	title := strings.TrimSpace(CleanText(b.Title))
	if (b.Kind != "sticker" && b.Kind != "gif") || title == "" || UTF16Len(title) > 64 {
		packErr(w, http.StatusBadRequest, "نام بسته باید ۱ تا ۶۴ نویسه باشد.")
		return
	}
	id, err := a.Svc.Store.CreatePack(r.Context(), b.Kind, title, a.User(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pack": &Pack{ID: id, Kind: b.Kind, Title: title, Items: []*PackItem{}}})
}

// PackUpload is POST /api/chat/packs/{id}/items, multipart: file, emoji,
// keywords (administrators). The file is checked by its bytes (lottie.go).
func (a *API) PackUpload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || a.Svc.Media == nil {
		packErr(w, http.StatusBadRequest, "پوشهٔ فایل‌های پیام‌رسان تنظیم نشده است.")
		return
	}
	kind, err := a.Svc.Store.PackKind(ctx, id)
	if err != nil {
		packErr(w, http.StatusNotFound, "این بسته پیدا نشد.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxGIFFile+64<<10)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		packErr(w, http.StatusBadRequest, "فایل بیش از اندازه بزرگ است.")
		return
	}
	defer r.MultipartForm.RemoveAll()
	f, hdr, err := r.FormFile("file")
	if err != nil {
		packErr(w, http.StatusBadRequest, "فایلی فرستاده نشد.")
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxGIFFile+1))
	if err != nil {
		packErr(w, http.StatusBadRequest, "فایل خوانده نشد.")
		return
	}
	format, mime, out, pw, ph, err := checkPackFile(kind, data)
	if err != nil {
		text := "این فایل برای بسته پذیرفته نیست: برچسب WebP، TGS یا WebM و گیف GIF، MP4 یا WebM، در اندازهٔ مجاز."
		if errors.Is(err, ErrLottieCode) {
			text = "این انیمیشن کد یا نشانی بیرونی دارد و پذیرفته نشد."
		}
		packErr(w, http.StatusBadRequest, text)
		return
	}
	emoji := strings.TrimSpace(r.FormValue("emoji"))
	keywords := strings.TrimSpace(CleanText(r.FormValue("keywords")))
	if UTF16Len(emoji) > 16 || UTF16Len(keywords) > 200 {
		packErr(w, http.StatusBadRequest, "شکلک یا کلیدواژه‌ها بیش از اندازه بلند است.")
		return
	}
	sha, err := a.Svc.keepBytes(out)
	if err != nil {
		writeErr(w, err)
		return
	}
	m := &Media{Kind: kind, Mime: mime, Size: int64(len(out)), Width: pw, Height: ph, Name: cleanName(hdr.Filename), sha: sha}
	mid, err := a.Svc.Store.InsertMedia(ctx, m, a.User(r), false)
	if err != nil {
		writeErr(w, err)
		return
	}
	it := &PackItem{Pack: id, Kind: kind, Media: mid, Format: format, Mime: mime, Emoji: emoji, Keywords: keywords, W: pw, H: ph}
	if it.ID, err = a.Svc.Store.AddPackItem(ctx, it, Normalize(keywords+" "+emoji)); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"item": it})
}

// PackDelete is POST /api/chat/packs/{id}/delete; PackItemDelete POST
// /api/chat/packs/items/{id}/delete (administrators). Messages already sent keep
// their file.
func (a *API) PackDelete(w http.ResponseWriter, r *http.Request) {
	a.packRemove(w, r, `DELETE FROM chat_packs WHERE id = $1`)
}
func (a *API) PackItemDelete(w http.ResponseWriter, r *http.Request) {
	a.packRemove(w, r, `DELETE FROM chat_pack_items WHERE id = $1`)
}
func (a *API) packRemove(w http.ResponseWriter, r *http.Request, q string) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		packErr(w, http.StatusNotFound, "پیدا نشد.")
		return
	}
	if _, err := a.Svc.Store.pool.Exec(r.Context(), q, id); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---------------------------------------------------------------- store

const itemCols = `i.id, i.pack_id, p.kind, i.media_id, i.format, m.mime, i.emoji, i.keywords, COALESCE(i.width, 0), COALESCE(i.height, 0)`
const itemFrom = ` FROM chat_pack_items i JOIN chat_packs p ON p.id = i.pack_id JOIN chat_media m ON m.id = i.media_id`

func scanItem(r rowScanner) (*PackItem, error) {
	it := &PackItem{}
	err := r.Scan(&it.ID, &it.Pack, &it.Kind, &it.Media, &it.Format, &it.Mime, &it.Emoji, &it.Keywords, &it.W, &it.H)
	return it, err
}

func collectItems(rows pgx.Rows) ([]*PackItem, error) {
	defer rows.Close()
	out := []*PackItem{}
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// PackItem reads one item.
func (s *Store) PackItem(ctx context.Context, id int64) (*PackItem, error) {
	return scanItem(s.pool.QueryRow(ctx, `SELECT `+itemCols+itemFrom+` WHERE i.id = $1`, id))
}

// PackKind is a pack's kind.
func (s *Store) PackKind(ctx context.Context, id int64) (string, error) {
	var k string
	err := s.pool.QueryRow(ctx, `SELECT kind FROM chat_packs WHERE id = $1`, id).Scan(&k)
	return k, err
}

// Packs lists the packs of one kind with their items.
func (s *Store) Packs(ctx context.Context, kind string) ([]*Pack, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, kind, title FROM chat_packs WHERE kind = $1 ORDER BY id`, kind)
	if err != nil {
		return nil, err
	}
	out := []*Pack{}
	by := map[int64]*Pack{}
	for rows.Next() {
		p := &Pack{Items: []*PackItem{}}
		if err := rows.Scan(&p.ID, &p.Kind, &p.Title); err != nil {
			rows.Close()
			return nil, err
		}
		out, by[p.ID] = append(out, p), p
	}
	rows.Close()
	irows, err := s.pool.Query(ctx, `SELECT `+itemCols+itemFrom+` WHERE p.kind = $1 ORDER BY i.pack_id, i.id`, kind)
	if err != nil {
		return nil, err
	}
	items, err := collectItems(irows)
	for _, it := range items {
		if p := by[it.Pack]; p != nil {
			p.Items = append(p.Items, it)
		}
	}
	return out, err
}

// SearchPacks finds items of kind whose normalised keywords contain q.
func (s *Store) SearchPacks(ctx context.Context, kind, q string, limit int) ([]*PackItem, error) {
	like := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q) + "%"
	rows, err := s.pool.Query(ctx, `SELECT `+itemCols+itemFrom+` WHERE p.kind = $1 AND i.search ILIKE $2 ORDER BY i.id DESC LIMIT $3`, kind, like, limit)
	if err != nil {
		return nil, err
	}
	return collectItems(rows)
}

// CreatePack makes an empty pack.
func (s *Store) CreatePack(ctx context.Context, kind, title, user string) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO chat_packs (kind, title, created_by) VALUES ($1, $2, $3) RETURNING id`, kind, title, user).Scan(&id)
	return id, err
}

// AddPackItem adds a checked file to a pack.
func (s *Store) AddPackItem(ctx context.Context, it *PackItem, search string) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO chat_pack_items (pack_id, media_id, format, emoji, keywords, search, width, height)
		VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, 0), NULLIF($8, 0)) RETURNING id`,
		it.Pack, it.Media, it.Format, it.Emoji, it.Keywords, search, it.W, it.H).Scan(&id)
	return id, err
}
