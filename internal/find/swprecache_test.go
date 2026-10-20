package find

import (
	"strings"
	"testing"
)

// The worker keeps the whole application, so a phone whose network stalls
// still opens it (2026-10-19: six minutes of an iPhone's connections not
// getting through, and a blank screen, because the worker had never kept the
// scripts the page is made of).

func TestTheWorkerListsEveryFileThePagesAreMadeOf(t *testing.T) {
	js := string(stampWorker(serviceWorkerJS, "9.9.9"))
	if strings.Contains(js, precachePlaceholder) {
		t.Fatal("the file list was not written into the worker")
	}
	for _, p := range []string{"/messages.js", "/chatcore.js", "/chatstate.js", "/chatglass.css", "/chatextras.js",
		"/app.css", "/nav.js", "/photoedit.js", "/static/fonts/Vazirmatn-Regular.woff2"} {
		if !strings.Contains(js, `"`+p+`"`) {
			t.Errorf("the worker does not keep %s", p)
		}
	}
	// Two builds' lists are the same list: the bytes change by version only.
	if a, b := stampWorker(serviceWorkerJS, "1"), stampWorker(serviceWorkerJS, "1"); string(a) != string(b) {
		t.Error("the same build wrote two different workers")
	}
}

// Every file listed is one this server answers: a path that 404s would be
// skipped at install and missing exactly when it is needed.
func TestEveryKeptFileIsServed(t *testing.T) {
	s, _, _, _ := accountsTestServer(t)
	for _, p := range workerAssets() {
		rec := getAsset(t, s, p, "", "")
		if rec.Code != 200 {
			t.Errorf("%s answers %d", p, rec.Code)
			continue
		}
		if rec.Header().Get("X-Docfind-Build") != buildVersion {
			t.Errorf("%s does not say which build it is, so the worker would never keep it", p)
		}
	}
}

// The network gets a few seconds, not for ever, and the worker keeps only its
// own release's bytes.
func TestTheWorkerFallsBackOnASlowNetworkWithoutMixingReleases(t *testing.T) {
	js := string(serviceWorkerJS)
	for _, must := range []struct{ code, why string }{
		{"Promise.race(", "the network is raced against a timer"},
		{"slowUntil", "once slow, the rest of the page comes from the copy at once"},
		{"res.headers.get('X-Docfind-Build') === BUILD", "only this release's bytes are kept"},
		{"SHELL.concat(PRECACHE)", "the files are kept at install"},
	} {
		if !strings.Contains(js, must.code) {
			t.Errorf("sw.js lost %q: %s", must.code, must.why)
		}
	}
}
