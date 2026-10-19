package chat

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// What a file is, decided by its first bytes (SPEC 6.4: "check the real file
// type with magic bytes, not only the extension") and what may be sent.

var ErrNoStorage = errors.New("chat: no media folder")

// kindLimit caps each kind; files go up to 2 GB.
var kindLimit = map[string]int64{"photo": 20 << 20, "voice": 50 << 20, "round": 100 << 20}

// execExt are names that run when opened on Windows (or elsewhere).
var execExt = map[string]bool{
	".exe": true, ".dll": true, ".com": true, ".scr": true, ".msi": true, ".msp": true, ".bat": true, ".cmd": true,
	".ps1": true, ".psm1": true, ".vbs": true, ".vbe": true, ".js": true, ".jse": true, ".wsf": true, ".wsh": true,
	".hta": true, ".cpl": true, ".jar": true, ".lnk": true, ".reg": true, ".sh": true, ".apk": true, ".pif": true,
	".gadget": true, ".msc": true, ".inf": true, ".sys": true, ".appx": true, ".msix": true, ".iso": true,
}

// isExecutable: a program by its first bytes, or by its name.
func isExecutable(head []byte, name string) bool {
	switch {
	case bytes.HasPrefix(head, []byte("MZ")), bytes.HasPrefix(head, []byte("\x7fELF")), bytes.HasPrefix(head, []byte("#!")):
		return true
	case len(head) >= 4:
		switch binary.BigEndian.Uint32(head[:4]) {
		case 0xFEEDFACE, 0xFEEDFACF, 0xCEFAEDFE, 0xCFFAEDFE, 0xCAFEBABE:
			return true
		}
	}
	lower := strings.ToLower(name)
	for ext := range execExt {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

var (
	photoTypes = map[string]bool{"image/jpeg": true, "image/png": true, "image/gif": true, "image/webp": true}
	videoTypes = map[string]bool{"video/mp4": true, "video/webm": true}
)

// classify returns the type to store and the kind the file is sent as.
// kind is what the page asked for; a file that is not what it claims is
// refused, and anything "as a file" is a file.
func classify(head []byte, name, kind string, asFile, allowExec bool) (string, string, error) {
	if !allowExec && isExecutable(head, name) {
		return "", "", ErrBadMedia
	}
	mime := http.DetectContentType(head)
	if i := strings.Index(mime, ";"); i >= 0 {
		mime = mime[:i]
	}
	isMP4 := len(head) >= 12 && string(head[4:8]) == "ftyp"
	if asFile {
		kind = "file"
	}
	switch kind {
	case "photo":
		if !photoTypes[mime] {
			return "", "", ErrBadMedia
		}
	case "video", "round":
		if isMP4 && mime == "application/octet-stream" {
			mime = "video/mp4"
		}
		if !videoTypes[mime] {
			return "", "", ErrBadMedia
		}
	case "voice":
		switch {
		case mime == "video/webm" || mime == "audio/webm":
			mime = "audio/webm"
		case mime == "application/ogg" || mime == "audio/ogg":
			mime = "audio/ogg"
		case mime == "video/mp4" || mime == "audio/mp4" || isMP4:
			mime = "audio/mp4"
		case mime == "audio/mpeg":
		default:
			return "", "", ErrBadMedia
		}
	case "file":
		if mime == "text/plain" && !strings.EqualFold(filepath.Ext(name), ".txt") {
			mime = "application/octet-stream"
		}
		if strings.HasPrefix(mime, "text/html") || strings.HasPrefix(mime, "text/xml") {
			mime = "application/octet-stream" // never served as a page
		}
	default:
		return "", "", ErrBadMedia
	}
	return mime, kind, nil
}

// photo strips the metadata that carries a camera's location (EXIF, XMP,
// IPTC) from a JPEG sent as a photo, reads its size and makes its thumbnails.
// A photo sent "as a file" is a file and is left exactly as it was.
func (s *Service) photo(path string, m *Media) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if m.Mime == "image/jpeg" {
		if out, ok := stripJPEGMeta(b); ok && len(out) != len(b) {
			if err := os.WriteFile(path, out, 0o600); err != nil {
				return err
			}
			b = out
		}
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
	if err == nil {
		m.Width, m.Height = cfg.Width, cfg.Height
	}
	if err != nil || cfg.Width*cfg.Height > 60_000_000 {
		return nil // stored, shown without a thumbnail
	}
	return s.thumbs(b, m)
}

// thumbs makes a 320-pixel thumbnail (served) and a 20-pixel one (sent
// inline, for the blur-up while the picture loads).
func (s *Service) thumbs(b []byte, m *Media) error {
	img, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return ErrBadMedia
	}
	if m.Width == 0 {
		m.Width, m.Height = img.Bounds().Dx(), img.Bounds().Dy()
	}
	var big, tiny bytes.Buffer
	if err := jpeg.Encode(&big, shrink(img, 320), &jpeg.Options{Quality: 72}); err != nil {
		return err
	}
	if err := jpeg.Encode(&tiny, shrink(img, 20), &jpeg.Options{Quality: 50}); err != nil {
		return err
	}
	sha, err := s.keepBytes(big.Bytes())
	if err != nil {
		return err
	}
	m.thumbSHA, m.Thumb, m.Mini = sha, true, tiny.Bytes()
	return nil
}

// shrink scales img so its longer side is at most max, averaging each block
// of source pixels (good enough for thumbnails, standard library only).
func shrink(img image.Image, max int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= max && h <= max {
		max = w
		if h > w {
			max = h
		}
	}
	tw, th := max, max
	if w > h {
		th = h * max / w
	} else {
		tw = w * max / h
	}
	tw, th = maxInt(tw, 1), maxInt(th, 1)
	out := image.NewRGBA(image.Rect(0, 0, tw, th))
	for y := 0; y < th; y++ {
		y0, y1 := b.Min.Y+y*h/th, b.Min.Y+maxInt((y+1)*h/th, y*h/th+1)
		for x := 0; x < tw; x++ {
			x0, x1 := b.Min.X+x*w/tw, b.Min.X+maxInt((x+1)*w/tw, x*w/tw+1)
			var r, g, bl, n uint64
			step := maxInt((x1-x0)/4, 1)
			for yy := y0; yy < y1; yy += maxInt((y1-y0)/4, 1) {
				for xx := x0; xx < x1; xx += step {
					cr, cg, cb, _ := img.At(xx, yy).RGBA()
					r, g, bl, n = r+uint64(cr), g+uint64(cg), bl+uint64(cb), n+1
				}
			}
			out.Set(x, y, color.RGBA64{uint16(r / n), uint16(g / n), uint16(bl / n), 0xffff})
		}
	}
	return out
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// stripJPEGMeta drops APP1 (EXIF and XMP, where GPS lives), APP13 (IPTC) and
// comments from a JPEG, keeping everything else byte for byte.
func stripJPEGMeta(b []byte) ([]byte, bool) {
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 {
		return b, false
	}
	out := make([]byte, 0, len(b))
	out = append(out, 0xFF, 0xD8)
	i := 2
	for i+4 <= len(b) {
		if b[i] != 0xFF {
			return b, false
		}
		marker := b[i+1]
		if marker == 0xDA { // start of scan: the rest is image data
			return append(out, b[i:]...), true
		}
		n := int(binary.BigEndian.Uint16(b[i+2 : i+4]))
		if n < 2 || i+2+n > len(b) {
			return b, false
		}
		if marker != 0xE1 && marker != 0xED && marker != 0xFE {
			out = append(out, b[i:i+2+n]...)
		}
		i += 2 + n
	}
	return b, false
}

// uuidV7 is a time-ordered random UUID (RFC 9562), for upload ids.
func uuidV7() string {
	var u [16]byte
	_, _ = rand.Read(u[:])
	ms := uint64(time.Now().UnixMilli())
	u[0], u[1], u[2], u[3], u[4], u[5] = byte(ms>>40), byte(ms>>32), byte(ms>>24), byte(ms>>16), byte(ms>>8), byte(ms)
	u[6] = (u[6] & 0x0f) | 0x70
	u[8] = (u[8] & 0x3f) | 0x80
	x := hex.EncodeToString(u[:])
	return x[0:8] + "-" + x[8:12] + "-" + x[12:16] + "-" + x[16:20] + "-" + x[20:32]
}
