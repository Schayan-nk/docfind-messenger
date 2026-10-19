package find

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"sync"
)

// Response compression.
//
// Measured before writing any of this: a cold load of the documents screen
// transferred 525 KB of HTML, CSS and JavaScript uncompressed, and gzip takes
// the same bytes to 169 KB -- 68% off, 347 KB saved. On the office LAN that is
// a fraction of a second; over the internet link this application is heading
// for, it is the single largest cost in the page.
//
// The same measurement showed the server's own work is not worth optimising:
// every endpoint answered at 13-15ms against a measured 14.8ms loopback
// round-trip floor, so serving 205 KB of Chart.js cost the same as a one-byte
// reply. The bytes are the bottleneck, not the code that produces them.

// compressible reports whether a content type is worth gzipping.
//
// PNG, ICO and the like are already compressed; running them through gzip
// spends CPU to make them very slightly larger.
func compressible(contentType string) bool {
	ct := strings.ToLower(contentType)
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	ct = strings.TrimSpace(ct)
	switch {
	case strings.HasPrefix(ct, "text/"):
		return true
	case ct == "application/json", ct == "application/javascript",
		ct == "application/manifest+json", ct == "image/svg+xml":
		return true
	case ct == "application/wasm":
		// WebAssembly gzips to roughly a third.
		// Worth compressing, and worth compressing once: it is an embedded
		// asset served through handleAsset, so the cost is paid per process
		// rather than per request.
		return true
	}
	return false
}

// minCompress is the size below which compressing is not worth it. A response
// smaller than roughly one network packet arrives in the same trip either way,
// and gzip adds about 20 bytes of framing.
const minCompress = 900

// gzipPool reuses writers; allocating one per response would make this
// middleware its own small garbage problem under load.
var gzipPool = sync.Pool{
	New: func() any {
		// Level 5, not 9. On this content 9 buys about 3% over 5 and costs
		// roughly three times the CPU -- a bad trade for a server that also
		// answers database queries.
		w, _ := gzip.NewWriterLevel(io.Discard, 5)
		return w
	},
}

// gzipWriter defers the decision to compress until the first write, because
// that is the first moment the handler's Content-Type is known.
type gzipWriter struct {
	http.ResponseWriter
	gz      *gzip.Writer
	status  int
	decided bool
	wrote   bool
	buf     []byte // holds the first write while the decision is pending
}

func (w *gzipWriter) WriteHeader(status int) {
	w.status = status
	// A 304 carries no body, and a websocket upgrade is not ours to touch.
	if status == http.StatusNotModified || status == http.StatusNoContent {
		w.decided, w.gz = true, nil
	}
	if w.decided && w.gz == nil {
		w.ResponseWriter.WriteHeader(status)
		w.wrote = true
	}
}

func (w *gzipWriter) Write(p []byte) (int, error) {
	if !w.decided {
		w.buf = append(w.buf, p...)
		// Wait until there is either enough to judge by, or the handler is
		// clearly finished with a small body.
		if len(w.buf) < minCompress {
			return len(p), nil
		}
		w.decide()
		return len(p), w.flushBuffered()
	}
	if w.gz != nil {
		return w.gz.Write(p)
	}
	return w.ResponseWriter.Write(p)
}

// decide picks compression based on the content type the handler set.
func (w *gzipWriter) decide() {
	w.decided = true
	h := w.ResponseWriter.Header()
	if len(w.buf) < minCompress || !compressible(h.Get("Content-Type")) ||
		h.Get("Content-Encoding") != "" {
		return
	}
	// Content-Length describes the uncompressed body and would now be wrong;
	// the response becomes chunked instead.
	h.Del("Content-Length")
	h.Set("Content-Encoding", "gzip")
	gz := gzipPool.Get().(*gzip.Writer)
	gz.Reset(w.ResponseWriter)
	w.gz = gz
}

func (w *gzipWriter) flushBuffered() error {
	if !w.wrote {
		if w.status == 0 {
			w.status = http.StatusOK
		}
		w.ResponseWriter.WriteHeader(w.status)
		w.wrote = true
	}
	buf := w.buf
	w.buf = nil
	if len(buf) == 0 {
		return nil
	}
	if w.gz != nil {
		_, err := w.gz.Write(buf)
		return err
	}
	_, err := w.ResponseWriter.Write(buf)
	return err
}

// Close finishes the response: flushes anything still buffered and closes the
// compressor. Every path through the middleware must reach this.
func (w *gzipWriter) Close() {
	if !w.decided {
		w.decide()
	}
	_ = w.flushBuffered()
	if w.gz != nil {
		_ = w.gz.Close()
		gzipPool.Put(w.gz)
		w.gz = nil
	}
}

// Flush passes a handler's own flush through, so a streaming CSV export still
// arrives progressively rather than being held until the end.
func (w *gzipWriter) Flush() {
	if !w.decided {
		w.decide()
	}
	_ = w.flushBuffered()
	if w.gz != nil {
		_ = w.gz.Flush()
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// compressResponses gzips text responses for clients that accept it.
func compressResponses(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Caches must not hand a gzipped body to a client that cannot read
		// one. This header is what makes that safe, and it is set BEFORE the
		// gzip check on purpose: the response varies by Accept-Encoding
		// whether or not this particular client sent it, and a cache that
		// stored an identity response without the header would later serve it
		// to a client that would have been given gzip.
		//
		// Set, not Add, and set only here: the asset handlers used to add it
		// as well, so every static response carried the header twice.
		w.Header().Set("Vary", "Accept-Encoding")

		// A WebSocket upgrade (the voice conversation) takes the connection
		// over; wrapped, the handler could not reach it. Browsers send
		// Accept-Encoding on the handshake all the same.
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") ||
			strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			next.ServeHTTP(w, r)
			return
		}

		gw := &gzipWriter{ResponseWriter: w}
		defer gw.Close()
		next.ServeHTTP(gw, r)
	})
}

// precompressed holds a gzipped copy of an embedded asset.
//
// These files never change while the process runs, so compressing each one
// once at first request and serving the bytes thereafter removes the CPU cost
// entirely -- Chart.js is 205 KB and would otherwise be re-compressed on every
// cold load.
type precompressed struct {
	once sync.Once
	body []byte
}

func (p *precompressed) bytes(raw []byte) []byte {
	p.once.Do(func() {
		var buf bytes.Buffer
		// Level 9 here: paid once per process, so the extra few percent is
		// free from then on.
		zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
		if err != nil {
			return
		}
		if _, err := zw.Write(raw); err != nil || zw.Close() != nil {
			return
		}
		p.body = buf.Bytes()
	})
	return p.body
}
