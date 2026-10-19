package find

import (
	"bytes"
	"net/http"
	"strings"
)

// The 404 and 500 pages.
//
// Until now a mistyped address got Go's default: nineteen bytes of plain text
// reading "404 page not found", in English, unstyled, with no way back into
// the application. A panic got a bare JSON object, which is right for the API
// and wrong in a browser's address bar.
//
// One template serves both. The two variants are built once at start-up rather
// than formatted per request -- they are static bytes, and building them here
// means they go out through handleAsset with the same ETag and compress-once
// treatment as every other page.

// errorPageCSP is the policy for one error page: the page's own, its inline
// script (if any) named by its hash like every other page -- see csp.go.
// These pages are built once at start-up, so their bytes are as fixed as
// the embedded ones.
func errorPageCSP(page []byte) string { return pageCSP(page) }

var (
	notFoundCSP    = errorPageCSP(notFoundPage)
	serverErrorCSP = errorPageCSP(serverErrorPage)
)

// The sentences deliberately say nothing about why. A 500 reaching a browser
// means something failed unexpectedly, and the one thing certain about the
// detail is that the reader cannot act on it -- while a path, a driver message
// or a stack frame is exactly what an attacker would like returned. The
// detail goes to the server log instead.
var (
	notFoundPage = buildErrorPage("404",
		"این صفحه پیدا نشد",
		"نشانی‌ای که باز کردید روی این سرور وجود ندارد. ممکن است نشانی اشتباه تایپ شده باشد یا این صفحه جابه‌جا شده باشد.")

	serverErrorPage = buildErrorPage("500",
		"خطای داخلی سرور",
		"درخواست شما به نتیجه نرسید. موضوع در گزارش خطای سرور ثبت شد؛ چند لحظه بعد دوباره تلاش کنید.")
)

func buildErrorPage(code, title, message string) []byte {
	out := injectAssets(errorHTML)
	for _, r := range []struct{ tok, val string }{
		{"{{CODE}}", code},
		{"{{TITLE}}", title},
		{"{{MESSAGE}}", message},
	} {
		out = bytes.ReplaceAll(out, []byte(r.tok), []byte(r.val))
	}
	return out
}

// wantsHTML reports whether the caller is a browser navigating, rather than
// script calling the API.
//
// Sec-Fetch-Mode is the reliable signal where it exists: a document navigation
// says "navigate", and fetch() says "cors" or "same-origin" no matter what
// Accept header it carries. Accept is the fallback, and the ordering matters --
// fetch() defaults to "*/*", which must not count as wanting HTML.
func wantsHTML(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Mode") {
	case "navigate":
		return true
	case "cors", "same-origin", "no-cors", "websocket":
		return false
	}
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

// writeNotFound answers a missing page, as markup for a browser and as JSON
// for anything else.
func writeNotFound(w http.ResponseWriter, r *http.Request) {
	writeErrorPage(w, r, http.StatusNotFound, notFoundPage, notFoundCSP, `{"error":"این نشانی وجود ندارد."}`)
}

// writeErrorPage sends one of the error pages to a browser, or its JSON body
// to anything else.
func writeErrorPage(w http.ResponseWriter, r *http.Request, status int, page []byte, csp, jsonBody string) {
	if !wantsHTML(r) {
		writeJSONError(w, status, jsonBody)
		return
	}
	h := w.Header()
	h.Set("Content-Security-Policy", csp)
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	w.WriteHeader(status)
	w.Write(page)
}

// writeJSONError sends a JSON error body with a JSON content type.
//
// http.Error cannot be used for this: it overwrites Content-Type with
// text/plain, so a JSON body goes out mislabelled. fetch().json() ignores the
// header and parses it anyway, which is why the rest of this package has got
// away with it, but a panic handler that quietly relabelled its own response
// broke the test that pins the type -- correctly.
func writeJSONError(w http.ResponseWriter, status int, body string) {
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	w.Write([]byte(body + "\n"))
}

// writeServerError answers a failed request the same way.
//
// The caller has already logged whatever actually happened.
func writeServerError(w http.ResponseWriter, r *http.Request) {
	writeErrorPage(w, r, http.StatusInternalServerError, serverErrorPage, serverErrorCSP, `{"error":"خطای داخلی سرور"}`)
}
