package find

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Map tiles for the location messages (chatplace.js), fetched by this server
// and cached on its disk.
//
// Through the server rather than straight from the provider for three
// reasons: the pages keep their strict Content-Security-Policy (img-src
// 'self'); a phone on mobile data talks only to this server; and the provider
// sees one cached client rather than every page view -- which is also what
// OpenStreetMap's tile policy asks for. Signed-in readers only, so this is not
// an open proxy.
//
// DOCFIND_MAP_TILES is the provider's URL template with {z}, {x} and {y}; the
// default is OpenStreetMap's, and "off" turns the map off (a location is then
// drawn on a plain grid with its coordinates). DOCFIND_MAP_CONTACT, a URL or
// an email address, goes in the User-Agent the provider sees, as the tile
// policy asks.

const defaultTileURL = "https://tile.openstreetmap.org/{z}/{x}/{y}.png"

type tileState struct {
	template string
	agent    string
	dir      string
	client   *http.Client
	mu       sync.Mutex
	inflight map[string]*sync.WaitGroup
}

// UseMapTiles turns /maptile on with tiles cached under dir.
func (s *Server) UseMapTiles(dir string) {
	t := strings.TrimSpace(os.Getenv("DOCFIND_MAP_TILES"))
	if strings.EqualFold(t, "off") {
		return
	}
	if t == "" {
		t = defaultTileURL
	}
	agent := "docfind-messenger/" + buildVersion + " (location messages"
	if c := strings.TrimSpace(os.Getenv("DOCFIND_MAP_CONTACT")); c != "" && !strings.ContainsAny(c, "\r\n()") {
		agent += "; +" + c
	}
	s.tiles = &tileState{template: t, agent: agent + ")", dir: dir,
		client: &http.Client{Timeout: 15 * time.Second}, inflight: map[string]*sync.WaitGroup{}}
}

const tileTTL = 14 * 24 * time.Hour

// handleMapTile is GET /maptile/{z}/{x}/{y}.
func (s *Server) handleMapTile(w http.ResponseWriter, r *http.Request) {
	if s.tiles == nil {
		http.NotFound(w, r)
		return
	}
	z, ez := strconv.Atoi(r.PathValue("z"))
	x, ex := strconv.Atoi(r.PathValue("x"))
	y, ey := strconv.Atoi(strings.TrimSuffix(r.PathValue("y"), ".png"))
	if ez != nil || ex != nil || ey != nil || z < 0 || z > 19 || x < 0 || y < 0 || x >= 1<<z || y >= 1<<z {
		http.NotFound(w, r)
		return
	}
	body, err := s.tiles.get(r.Context(), z, x, y)
	if err != nil {
		http.Error(w, "tile unavailable", http.StatusBadGateway)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "image/png")
	h.Set("Cache-Control", "private, max-age=86400")
	h.Set("X-Content-Type-Options", "nosniff")
	w.Write(body)
}

func (t *tileState) path(z, x, y int) string {
	sum := sha256.Sum256([]byte(t.template))
	return filepath.Join(t.dir, hex.EncodeToString(sum[:4]), strconv.Itoa(z), strconv.Itoa(x), strconv.Itoa(y)+".png")
}

// get answers from the disk cache, else fetches once even when many readers
// ask for the same tile at the same moment.
func (t *tileState) get(ctx context.Context, z, x, y int) ([]byte, error) {
	p := t.path(z, x, y)
	if st, err := os.Stat(p); err == nil && time.Since(st.ModTime()) < tileTTL {
		return os.ReadFile(p)
	}
	key := p
	t.mu.Lock()
	if wg, ok := t.inflight[key]; ok {
		t.mu.Unlock()
		wg.Wait()
		return os.ReadFile(p)
	}
	wg := &sync.WaitGroup{}
	wg.Add(1)
	t.inflight[key] = wg
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		delete(t.inflight, key)
		t.mu.Unlock()
		wg.Done()
	}()

	u := strings.NewReplacer("{z}", strconv.Itoa(z), "{x}", strconv.Itoa(x), "{y}", strconv.Itoa(y)).Replace(t.template)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", t.agent)
	resp, err := t.client.Do(req)
	if err != nil {
		if b, rerr := os.ReadFile(p); rerr == nil {
			return b, nil // stale beats nothing while the provider is unreachable
		}
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil || resp.StatusCode != http.StatusOK || imageType(body) == "" {
		if b, rerr := os.ReadFile(p); rerr == nil {
			return b, nil
		}
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err == nil {
		tmp := p + ".tmp"
		if os.WriteFile(tmp, body, 0o600) == nil {
			_ = os.Rename(tmp, p)
		}
	}
	return body, nil
}
