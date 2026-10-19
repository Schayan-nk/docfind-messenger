package find

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"sync"
)

// versionedAssetPaths names every reference, inside an embedded page or
// stylesheet, to another embedded asset whose bytes are fixed for the life of
// a build. Each is rewritten by versionAssetLinks to carry that build's
// version in its query string, so handleAsset can answer it with a year-long,
// immutable Cache-Control instead of a conditional GET on every load.
//
// A literal table rather than an HTML/CSS parser: these are compiled-in
// files, not third-party input, and the same technique already stamps the
// service worker's cache name -- see stampWorker in version.go. sw.js is
// deliberately not in this table: it is the one file that must always be
// asked for again, so a build with only a worker change is still noticed.
var versionedAssetPaths = []string{
	"/app.css", "/nav.js", "/push.js", "/notifycenter.js", "/useredit.js", "/datepick.js", "/glassnav.js", "/meganav.js", "/twostep.js", "/passkeys.js",
	"/roles.js", "/favicon.ico", "/apple-touch-icon.png", "/manifest.webmanifest", "/icon.png",
	"/static/fonts/Archivo-latin.woff2", "/static/fonts/Archivo-latin-ext.woff2",
	"/static/fonts/Vazirmatn-Regular.woff2", "/static/fonts/Vazirmatn-Medium.woff2",
	"/static/fonts/Vazirmatn-Bold.woff2",
}

// versionAssetLinks appends "?v=<version>" to every quoted reference to a
// versioned asset in body, so href="/app.css" becomes
// href="/app.css?v=1.2.3" and a font's url("/static/fonts/...") gets the same
// treatment.
//
// Applied once, at route registration -- to every page handlePage serves and
// to the stylesheet before /app.css is registered -- not per request: the
// content is as fixed for the process's life as the ETag handleAsset computes
// from it.
func versionAssetLinks(body []byte, version string) []byte {
	for _, p := range versionedAssetPaths {
		bare := []byte(`"` + p + `"`)
		stamped := []byte(`"` + p + "?v=" + version + `"`)
		body = bytes.ReplaceAll(body, bare, stamped)
	}
	return body
}

// isVersionedRequest reports whether r asked for the exact build this server
// is running, via the "v" query string versionAssetLinks adds.
//
// Checked against the running build rather than merely "is v present": a link
// left over from a build this server no longer is falls back to the ordinary
// revalidated path like any unversioned request, rather than being told to
// cache content that may no longer match what the URL implies.
//
// /sw.js is refused outright, no matter what query string turns up on it. It
// is deliberately absent from versionedAssetPaths, but that only stops a page
// from ever *linking* it with a "v" -- this stops a request that adds one by
// hand, or a stray cache from a future build, from doing the one thing this
// file must never do. See stampWorker's reasoning in version.go.
func isVersionedRequest(r *http.Request) bool {
	if r.URL.Path == "/sw.js" {
		return false
	}
	return r.URL.Query().Get("v") == assetStamp()
}

// assetStamp is what a versioned link carries: the release, and a short hash
// of every asset such a link can point at.
//
// The release alone is not enough. A year-long immutable cache is only safe
// if the URL changes whenever the bytes do, and the version string does not
// always: every local build is "dev", and a fix rebuilt under the same
// release number would otherwise be hidden from every browser that loaded
// the first build -- for a year. The hash makes the URL follow the content.
var assetStamp = sync.OnceValue(func() string {
	h := sha256.New()
	// Every file in versionedAssetPaths: one left out here is served immutable
	// for a year under a stamp its own change does not move.
	// TestEveryVersionedAssetIsInTheStamp.
	for _, b := range [][]byte{
		styleSheet, navJS, pushJS, notifyCenterJS, userEditJS, datePickJS, glassNavJS, megaNavJS, twoStepJS, passkeysJS,
		rolesJS,
		faviconICO, appleTouchIconPNG, manifestJSON, iconPNG,
	} {
		h.Write(b)
	}
	_ = fs.WalkDir(staticFS, "static/fonts", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if b, rerr := staticFS.ReadFile(p); rerr == nil {
				h.Write(b)
			}
		}
		return nil
	})
	return buildVersion + "-" + hex.EncodeToString(h.Sum(nil))[:12]
})
