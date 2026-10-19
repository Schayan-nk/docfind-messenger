package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/text/encoding/htmlindex"

	"github.com/Schayan-nk/docfind-messenger/internal/netguard"
)

// Link previews (SPEC 6.3, security check 8). The server fetches the page,
// never the browser, and only from the public internet: not this machine,
// not the office network, not a cloud metadata address -- checked on the
// name before the request, on every redirect, and on the address actually
// connected to (a name that resolves differently the second time, DNS
// rebinding, still lands on the dial check). Six seconds, 512 KB of page,
// 2 MB of picture. No address is logged: a link is part of a message.

var previewTransport = &http.Transport{
	Proxy:                  nil, // direct, so the dial check sees the real address
	DialContext:            (&net.Dialer{Timeout: 4 * time.Second, Control: netguard.Control(netguard.Public)}).DialContext,
	TLSHandshakeTimeout:    4 * time.Second,
	ResponseHeaderTimeout:  5 * time.Second,
	MaxResponseHeaderBytes: 32 << 10,
	DisableCompression:     false,
}

var previewClient = &http.Client{
	Timeout:   6 * time.Second,
	Transport: previewTransport,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return errors.New("too many redirects")
		}
		if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
			return netguard.ErrRefused
		}
		return netguard.CheckHost(req.Context(), req.URL.Hostname(), netguard.Public)
	},
}

// previewSlots caps how many previews are fetched at once.
var previewSlots = make(chan struct{}, 4)

// Preview is what a bubble shows under (or above) a link.
type Preview struct {
	URL   string `json:"url"`
	Site  string `json:"site,omitempty"`
	Title string `json:"title,omitempty"`
	Desc  string `json:"desc,omitempty"`
	Image int64  `json:"image,omitempty"` // a chat_media row (kind "preview")
	Above bool   `json:"above,omitempty"`
}

// fetchPreview reads a page's title, description, site name and picture.
func (s *Service) fetchPreview(ctx context.Context, raw string) (*Preview, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return nil, netguard.ErrRefused
	}
	body, final, err := previewGet(ctx, u.String(), "text/html,application/xhtml+xml", 512<<10, true)
	if err != nil {
		return nil, err
	}
	p := parsePreview(body.data, body.charset)
	p.URL = raw
	if p.Site == "" {
		p.Site = strings.TrimPrefix(final.Hostname(), "www.")
	}
	if p.Title == "" && p.Desc == "" {
		return nil, errors.New("nothing to show")
	}
	if img := p.imageURL; img != "" && s.Media != nil {
		if iu, err := final.Parse(img); err == nil && (iu.Scheme == "https" || iu.Scheme == "http") {
			if b, _, err := previewGet(ctx, iu.String(), "image/*", 2<<20, false); err == nil {
				if id, err := s.keepPreviewImage(ctx, b.data); err == nil {
					p.Image = id
				}
			}
		}
	}
	return &p.Preview, nil
}

type fetched struct {
	data    []byte
	charset string
}

func previewGet(ctx context.Context, raw, accept string, limit int64, wantHTML bool) (*fetched, *url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, nil, err
	}
	if err := netguard.CheckHost(ctx, u.Hostname(), netguard.Public); err != nil {
		return nil, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; DocFind link preview)")
	req.Header.Set("Accept", accept)
	req.Header.Set("Accept-Language", "fa,en;q=0.8")
	resp, err := previewClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, errors.New(resp.Status)
	}
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if wantHTML && !strings.Contains(ct, "html") {
		return nil, nil, errors.New("not a page")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return nil, nil, err
	}
	cs := ""
	if i := strings.Index(ct, "charset="); i >= 0 {
		cs = strings.Trim(ct[i+8:], `"' ;`)
	}
	return &fetched{data: data, charset: cs}, resp.Request.URL, nil
}

type parsed struct {
	Preview
	imageURL string
}

var (
	reTitle  = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	reMeta   = regexp.MustCompile(`(?is)<meta\s[^>]*>`)
	reAttr   = regexp.MustCompile(`(?is)([a-z][a-z0-9:_-]*)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))`)
	reMetaCS = regexp.MustCompile(`(?is)<meta[^>]+charset\s*=\s*["']?([a-z0-9_-]+)`)
)

// parsePreview reads Open Graph tags, falling back to <title> and the
// description meta tag. Values are plain text: the page draws them with
// textContent.
func parsePreview(b []byte, charset string) parsed {
	if charset == "" {
		if m := reMetaCS.FindSubmatch(b); m != nil {
			charset = string(m[1])
		}
	}
	if cs := strings.ToLower(charset); cs != "" && cs != "utf-8" && cs != "utf8" {
		if enc, err := htmlindex.Get(cs); err == nil {
			if out, err := enc.NewDecoder().Bytes(b); err == nil {
				b = out
			}
		}
	}
	var p parsed
	meta := map[string]string{}
	for _, tag := range reMeta.FindAll(b, 200) {
		attrs := map[string]string{}
		for _, a := range reAttr.FindAllSubmatch(tag, -1) {
			v := string(a[2]) + string(a[3]) + string(a[4])
			attrs[strings.ToLower(string(a[1]))] = v
		}
		key := strings.ToLower(attrs["property"])
		if key == "" {
			key = strings.ToLower(attrs["name"])
		}
		if key != "" && attrs["content"] != "" {
			if _, seen := meta[key]; !seen {
				meta[key] = attrs["content"]
			}
		}
	}
	text := func(s string, n int) string {
		s = strings.Join(strings.Fields(html.UnescapeString(s)), " ")
		if r := []rune(s); len(r) > n {
			s = string(r[:n]) + "…"
		}
		return s
	}
	p.Title = text(meta["og:title"], 200)
	if p.Title == "" {
		if m := reTitle.FindSubmatch(b); m != nil {
			p.Title = text(string(m[1]), 200)
		}
	}
	p.Desc = text(firstOf(meta["og:description"], meta["description"], meta["twitter:description"]), 300)
	p.Site = text(meta["og:site_name"], 80)
	p.imageURL = strings.TrimSpace(html.UnescapeString(firstOf(meta["og:image"], meta["twitter:image"])))
	return p
}

func firstOf(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// keepPreviewImage stores a preview's picture as a small JPEG.
func (s *Service) keepPreviewImage(ctx context.Context, b []byte) (int64, error) {
	m := &Media{Kind: "preview", Mime: "image/jpeg"}
	if !photoTypes[http.DetectContentType(b)] {
		return 0, ErrBadMedia
	}
	if err := s.thumbs(b, m); err != nil {
		return 0, err
	}
	m.sha = m.thumbSHA // the picture is its own thumbnail
	m.Size = int64(len(b))
	return s.Store.InsertMedia(ctx, m, "", false)
}

// previewLater fetches the preview of a message's first link and puts it on
// the message when it arrives. The message is never held back for it.
func (s *Service) previewLater(chatID, msgID int64, link string, members []string) {
	go func() {
		previewSlots <- struct{}{}
		defer func() { <-previewSlots }()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		p, ok := s.Store.CachedPreview(ctx, link)
		if !ok {
			pv, err := s.fetchPreview(ctx, link)
			if err != nil || pv == nil {
				return
			}
			p, _ = json.Marshal(pv)
			_ = s.Store.KeepPreview(ctx, link, p)
		}
		if up, err := s.Store.SetPreview(ctx, chatID, msgID, p); err == nil && up != nil {
			s.deliver(members, "update", up, nil)
		}
	}()
}

// firstLink is the address of a message's first link, if any.
func firstLink(text string, ents []Entity) string {
	for _, e := range ents {
		switch e.Type {
		case "text_link":
			return e.URL
		case "url":
			u := Slice16(text, e.Offset, e.Offset+e.Length)
			if !strings.Contains(u, "://") {
				u = "https://" + u
			}
			return u
		}
	}
	return ""
}

// PreviewPlace lets the sender take a message's link preview away, or move
// it above or below the text.
func (s *Service) PreviewPlace(ctx context.Context, user string, chatID, msgID int64, on, above bool) error {
	if _, err := s.Store.Role(ctx, chatID, user); err != nil {
		return err
	}
	c, sender, deleted, err := s.Store.msgOwner(ctx, msgID)
	if err != nil || c != chatID {
		return ErrNotMember
	}
	if deleted || sender != user {
		return ErrForbidden
	}
	var p json.RawMessage
	if on {
		cur, err := s.Store.ByIDs(ctx, chatID, user, []int64{msgID})
		if err != nil || len(cur) != 1 || len(cur[0].Preview) == 0 {
			return ErrForbidden
		}
		var pv Preview
		if json.Unmarshal(cur[0].Preview, &pv) != nil {
			return ErrForbidden
		}
		pv.Above = above
		p, _ = json.Marshal(pv)
	}
	up, err := s.Store.SetPreview(ctx, chatID, msgID, p)
	if err != nil || up == nil {
		return err
	}
	members, err := s.Store.Members(ctx, chatID)
	if err != nil {
		return err
	}
	s.deliver(members, "update", up, nil)
	return nil
}

var _ = bytes.MinRead
