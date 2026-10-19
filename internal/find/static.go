package find

import (
	"embed"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// The /static tree: third-party assets that are referenced by path rather than
// compiled into a page.
//
// Everything here is embedded, so a deploy stays a single .exe copy and an
// asset can never be missing at runtime. That matters most for the icon
// sprite: it is chrome on every button and menu, and a sprite served from a
// directory that failed to copy would leave the whole application without
// icons.
//
// One exception is allowed, for assets the operator supplies rather than the
// build: staticOverlay names a directory beside the binary which is consulted
// for any path the embedded tree does not have. That is how a Lottie animation
// can be dropped onto the server without rebuilding.
//
//go:embed static
var staticFS embed.FS

// staticOverlay is the on-disk directory consulted for /static paths that are
// not embedded. Empty disables the overlay.
var staticOverlay = defaultStaticOverlay()

func defaultStaticOverlay() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(exe), "static")
}

// staticContentTypes is a closed list. Anything not named here is refused
// rather than guessed at: this tree is served unauthenticated, and a type
// the browser might run as script or markup has no business being added to it
// by dropping a file in a directory.
var staticContentTypes = map[string]string{
	".svg":    "image/svg+xml",
	".js":     "text/javascript; charset=utf-8",
	".wasm":   "application/wasm",
	".lottie": "application/zip",
	".json":   "application/json; charset=utf-8",
	".png":    "image/png",
	".webp":   "image/webp",
	".woff2":  "font/woff2",
}

// registerStatic mounts the embedded tree and the overlay under /static/.
//
// Every embedded file gets its own exact route, which means it goes through
// handleAsset and inherits the ETag, the 304 and the compress-once behaviour
// already proven there. Go's ServeMux prefers the more specific pattern, so
// those exact routes win over the /static/ prefix that handles the overlay.
func (s *Server) registerStatic(mux *http.ServeMux) {
	_ = fs.WalkDir(staticFS, "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		ct, ok := staticContentTypes[strings.ToLower(path.Ext(p))]
		if !ok {
			// LICENSE files and anything else unrecognised: still served, as
			// text, because a vendored licence must remain readable.
			ct = "text/plain; charset=utf-8"
		}
		body, readErr := staticFS.ReadFile(p)
		if readErr != nil {
			return nil
		}
		mux.HandleFunc("GET /"+p, s.handleAsset(body, ct))
		return nil
	})

	mux.HandleFunc("GET /static/", s.handleStaticOverlay)
}

// handleStaticOverlay serves a /static path that is not embedded, from the
// operator's directory beside the binary.
func (s *Server) handleStaticOverlay(w http.ResponseWriter, r *http.Request) {
	if staticOverlay == "" {
		http.NotFound(w, r)
		return
	}
	rel, ok := staticRelPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	ct, ok := staticContentTypes[strings.ToLower(path.Ext(rel))]
	if !ok {
		// Unlike the embedded tree, an unknown extension here is refused: the
		// contents of this directory are not reviewed at build time.
		http.NotFound(w, r)
		return
	}

	full := filepath.Join(staticOverlay, filepath.FromSlash(rel))
	// Belt and braces: the path was already validated segment by segment, and
	// this re-checks the joined result against the root.
	if !withinDir(staticOverlay, full) {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(full)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "no-cache")
	// ServeContent supplies the validator from the modification time and
	// honours Range, which matters for a multi-megabyte animation.
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
}

// staticRelPath validates a /static/... request path and returns the part
// below "static/".
//
// The id never comes from the index here, unlike the document routes, so this
// is the one place in the application where a caller-supplied string reaches a
// filesystem path. It is checked rather than cleaned: a cleaned path can still
// climb out when the input was crafted, so every segment is inspected and
// anything surprising is refused outright.
func staticRelPath(urlPath string) (string, bool) {
	const prefix = "/static/"
	if !strings.HasPrefix(urlPath, prefix) {
		return "", false
	}
	rel := strings.TrimPrefix(urlPath, prefix)
	if rel == "" || rel != path.Clean(rel) {
		return "", false
	}
	// Windows treats a backslash as a separator; a URL must not carry one.
	if strings.ContainsAny(rel, `\:`) || strings.Contains(rel, "//") {
		return "", false
	}
	for seg := range strings.SplitSeq(rel, "/") {
		if seg == "" || seg == "." || seg == ".." || strings.HasPrefix(seg, ".") {
			return "", false
		}
	}
	return rel, true
}

// withinDir reports whether full resolves inside root.
func withinDir(root, full string) bool {
	rel, err := filepath.Rel(root, full)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
