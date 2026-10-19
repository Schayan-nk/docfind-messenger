package find

import (
	"path/filepath"
	"testing"
)

// /static is the one route where a caller-supplied string reaches a filesystem
// path. Everywhere else an id is a key into the index, produced by the walk.
// So the path validation is the part tested hardest.

func TestStaticRelPathRefusesEscapes(t *testing.T) {
	hostile := []string{
		"/static/../server.go",
		"/static/../../go.mod",
		"/static/vendor/../../../go.mod",
		"/static/..",
		"/static/./x.svg",
		"/static//vendor/lucide/sprite.svg",
		`/static/..\server.go`,
		`/static/vendor\lucide\sprite.svg`,
		"/static/C:/Windows/win.ini",
		"/static/.env",
		"/static/vendor/.git/config",
		"/static/",
		"/staticx/foo.svg",
		"/notstatic/foo.svg",
	}
	for _, p := range hostile {
		if rel, ok := staticRelPath(p); ok {
			t.Errorf("staticRelPath(%q) accepted, returning %q", p, rel)
		}
	}

	good := map[string]string{
		"/static/vendor/lucide/sprite.svg":   "vendor/lucide/sprite.svg",
		"/static/vendor/lottie/robot.lottie": "vendor/lottie/robot.lottie",
		"/static/a.png":                      "a.png",
	}
	for p, want := range good {
		rel, ok := staticRelPath(p)
		if !ok || rel != want {
			t.Errorf("staticRelPath(%q) = (%q, %v), want (%q, true)", p, rel, ok, want)
		}
	}
}

func TestWithinDir(t *testing.T) {
	root := filepath.Clean("C:/app/static")
	inside := []string{"C:/app/static/a.svg", "C:/app/static/vendor/b.js"}
	outside := []string{"C:/app/other.txt", "C:/app/static/../x", "C:/"}
	for _, p := range inside {
		if !withinDir(root, filepath.Clean(p)) {
			t.Errorf("withinDir(%q) = false, want true", p)
		}
	}
	for _, p := range outside {
		if withinDir(root, filepath.Clean(p)) {
			t.Errorf("withinDir(%q) = true, want false", p)
		}
	}
}
