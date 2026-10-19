package chat

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// Pack files (SPEC 8.8, 8.14). Every sticker or GIF an administrator adds is
// checked here by its bytes, never by its name:
//   stickers: static WebP (512 KB), animated Lottie .tgs (64 KB gzipped,
//             2 MB as JSON), video WebM (256 KB);
//   GIFs:     GIF, MP4 or WebM (8 MB).
// A Lottie file loses every expression (the JavaScript a Lottie property can
// carry) and is refused if anything that looks like code is still in it, if
// it is too big, has too many layers, or points at a file outside itself
// (security checks 16 and 17). It is kept as plain JSON, already unpacked, so
// no browser has to unpack it and every browser can play it.

var (
	ErrPackFile   = errors.New("chat: not a usable sticker or GIF file")
	ErrLottieCode = errors.New("chat: the animation carries code")
)

const (
	maxWebP      = 512 << 10
	maxTGS       = 64 << 10
	maxLottieRaw = 2 << 20
	maxWebMSt    = 256 << 10
	maxGIFFile   = 8 << 20
	maxLayers    = 1000
	maxLottieFR  = 60
	maxLottieDim = 1024
	maxLottieSec = 10
)

// checkPackFile returns the format, MIME type, the bytes to keep and the size
// in pixels when the file says it.
func checkPackFile(kind string, b []byte) (format, mime string, out []byte, w, h int, err error) {
	switch {
	case len(b) > 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		if kind != "sticker" || len(b) > maxWebP {
			return "", "", nil, 0, 0, ErrPackFile
		}
		w, h = webpSize(b)
		return "webp", "image/webp", b, w, h, nil
	case len(b) > 2 && b[0] == 0x1f && b[1] == 0x8b:
		if kind != "sticker" || len(b) > maxTGS {
			return "", "", nil, 0, 0, ErrPackFile
		}
		out, w, h, err = cleanLottie(b)
		if err != nil {
			return "", "", nil, 0, 0, err
		}
		return "tgs", "application/json", out, w, h, nil
	case len(b) > 4 && bytes.Equal(b[:4], []byte{0x1a, 0x45, 0xdf, 0xa3}):
		if (kind == "sticker" && len(b) > maxWebMSt) || len(b) > maxGIFFile {
			return "", "", nil, 0, 0, ErrPackFile
		}
		return "webm", "video/webm", b, 0, 0, nil
	case len(b) > 10 && (string(b[:6]) == "GIF87a" || string(b[:6]) == "GIF89a"):
		if kind != "gif" || len(b) > maxGIFFile {
			return "", "", nil, 0, 0, ErrPackFile
		}
		return "gif", "image/gif", b, int(binary.LittleEndian.Uint16(b[6:8])), int(binary.LittleEndian.Uint16(b[8:10])), nil
	case len(b) > 12 && string(b[4:8]) == "ftyp":
		if kind != "gif" || len(b) > maxGIFFile {
			return "", "", nil, 0, 0, ErrPackFile
		}
		return "mp4", "video/mp4", b, 0, 0, nil
	}
	return "", "", nil, 0, 0, ErrPackFile
}

// webpSize reads the canvas size of a WebP file (0, 0 when it cannot).
func webpSize(b []byte) (int, int) {
	if len(b) < 30 {
		return 0, 0
	}
	switch string(b[12:16]) {
	case "VP8X":
		w := 1 + (int(b[24]) | int(b[25])<<8 | int(b[26])<<16)
		h := 1 + (int(b[27]) | int(b[28])<<8 | int(b[29])<<16)
		return w, h
	case "VP8 ":
		return int(binary.LittleEndian.Uint16(b[26:28]) & 0x3fff), int(binary.LittleEndian.Uint16(b[28:30]) & 0x3fff)
	case "VP8L":
		v := binary.LittleEndian.Uint32(b[21:25])
		return int(v&0x3fff) + 1, int((v>>14)&0x3fff) + 1
	}
	return 0, 0
}

// cleanLottie unpacks a .tgs file, removes its expressions and checks what is
// left. It returns the clean JSON.
func cleanLottie(gz []byte) ([]byte, int, int, error) {
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return nil, 0, 0, ErrPackFile
	}
	raw, err := io.ReadAll(io.LimitReader(zr, maxLottieRaw+1))
	if err != nil || len(raw) > maxLottieRaw {
		return nil, 0, 0, ErrPackFile
	}
	return cleanLottieJSON(raw)
}

func cleanLottieJSON(raw []byte) ([]byte, int, int, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil || dec.More() {
		return nil, 0, 0, ErrPackFile
	}
	num := func(k string) float64 {
		if n, ok := doc[k].(json.Number); ok {
			f, _ := n.Float64()
			return f
		}
		return -1
	}
	w, h, fr, ip, op := num("w"), num("h"), num("fr"), num("ip"), num("op")
	if _, ok := doc["layers"].([]any); !ok || w <= 0 || h <= 0 || w > maxLottieDim || h > maxLottieDim || fr <= 0 || fr > maxLottieFR || op <= ip || (op-ip)/fr > maxLottieSec {
		return nil, 0, 0, ErrPackFile
	}
	stripExpressions(doc)
	delete(doc, "fonts") // a font list can name files to fetch; stickers draw text as shapes
	delete(doc, "meta")  // who exported it: not drawn
	layers := 0
	if err := checkLottie(doc, &layers, false); err != nil {
		return nil, 0, 0, err
	}
	out, err := json.Marshal(doc)
	return out, int(w), int(h), err
}

// stripExpressions removes every "x" whose value is text: that is where a
// Lottie property keeps its expression.
func stripExpressions(v any) {
	switch t := v.(type) {
	case map[string]any:
		if _, ok := t["x"].(string); ok {
			delete(t, "x")
		}
		for _, c := range t {
			stripExpressions(c)
		}
	case []any:
		for _, c := range t {
			stripExpressions(c)
		}
	}
}

// codeHints are what an expression or a script looks like; none may survive.
var codeHints = []string{"$bm_", "eval(", "function(", "function (", "=>", "<script", "javascript:", "thiscomp", "valueattime"}

// checkLottie refuses code, outside files and too many layers.
func checkLottie(v any, layers *int, inLayers bool) error {
	switch t := v.(type) {
	case map[string]any:
		if inLayers {
			if *layers++; *layers > maxLayers {
				return ErrPackFile
			}
		}
		// An image asset must be embedded (data:), never fetched.
		if p, ok := t["p"].(string); ok && (t["u"] != nil || t["e"] != nil) && !strings.HasPrefix(p, "data:") {
			return ErrLottieCode
		}
		for k, c := range t {
			if err := checkLottie(c, layers, k == "layers"); err != nil {
				return err
			}
		}
	case []any:
		for _, c := range t {
			if err := checkLottie(c, layers, inLayers); err != nil {
				return err
			}
		}
	case string:
		low := strings.ToLower(strings.ReplaceAll(t, " ", ""))
		for _, hint := range codeHints {
			if strings.Contains(low, strings.ReplaceAll(hint, " ", "")) {
				return ErrLottieCode
			}
		}
		if strings.Contains(low, "://") {
			return ErrLottieCode
		}
	}
	return nil
}
