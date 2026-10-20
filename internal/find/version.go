package find

import (
	"encoding/json"
	"sort"
	"strings"
)

// buildVersion is the release this binary was built as, set by main.
//
// It exists for one job: stamping the service worker, so that every release
// ships a worker whose bytes differ from the last one's. See stampWorker.
var buildVersion = "dev"

// SetVersion records the build's version string. Called by main before the
// server starts serving, because the worker's bytes -- and therefore its ETag
// -- are computed once when the routes are built.
func SetVersion(v string) {
	if v = strings.TrimSpace(v); v != "" {
		buildVersion = v
	}
}

// binaryHash is the SHA-256 of the running executable, set by main at start.
//
// Shown to administrators beside the version so the build that is running can
// be compared with the build that was tested -- the same figure the install
// script checks. "" when main could not read its own file.
var binaryHash string

// SetBinaryHash records the executable's hash.
func SetBinaryHash(h string) { binaryHash = strings.TrimSpace(h) }

// cachePlaceholder is the literal in sw.js that carries the shell cache name.
//
// It is a real, working value rather than a token like __VERSION__ so that
// sw.js stays valid JavaScript on its own: it can be read, linted and reasoned
// about without knowing that something rewrites it on the way out.
const cachePlaceholder = "docfind-shell-v6"

// stampWorker puts the build version into the service worker.
//
// Two problems, one fix.
//
// The first is that an update is only ever noticed when the WORKER's bytes
// change. docfind 5.4.5 changed the indexer and the search path and nothing
// else; sw.js was byte-identical to 5.4.4's, so no browser saw a new worker,
// nothing went into the waiting state, and no reader was told anything. The
// release had shipped and the notice -- which had been written, tested and
// deployed specifically to say so -- stayed silent, because it is wired to the
// worker changing rather than to the application changing.
//
// The second is that the shell cache name was a hand-maintained constant, and
// the comment above it asked whoever edited the shell to remember to bump it.
// That was bumped by hand twice and would eventually be forgotten, leaving
// somebody offline holding an old shell.
//
// Naming the cache after the build solves both: the bytes differ every release,
// so every release is noticed; and the cache is new every release, so the
// activate handler drops the previous one without anybody remembering to ask.
//
// It also writes in the list of files the worker keeps (workerAssets), so the
// list is always this build's own and the worker's bytes change when it does.
func stampWorker(js []byte, version string) []byte {
	out := strings.ReplaceAll(string(js), cachePlaceholder, "docfind-shell-"+version)
	list, _ := json.Marshal(workerAssets())
	return []byte(strings.Replace(out, precachePlaceholder, "const PRECACHE = "+string(list)+";", 1))
}

// precachePlaceholder is the literal in sw.js the list replaces: valid on its
// own (an empty list keeps nothing beyond the pages), like cachePlaceholder.
const precachePlaceholder = "const PRECACHE = [];"

// sharedScripts are the scripts the pages share besides app.css and nav.js
// (which versionedAssetPaths already names).
var sharedScripts = []string{
	"/push.js", "/notifycenter.js", "/datepick.js", "/glassnav.js", "/meganav.js", "/twostep.js",
	"/passkeys.js", "/useredit.js", "/roles.js", "/invites.js", "/photoedit.js", "/photoview.js", "/setglass.css",
}

// workerAssets is every file the pages are made of -- scripts, styles,
// fonts, icons -- which the service worker keeps at install, so a phone whose
// network stalls still opens the application from its own copy (2026-10-19:
// an iPhone's connections stopped getting through for six minutes, and the
// worker, waiting on the network for files it had never kept, showed nothing).
// Sorted, so the same build always writes the same worker.
func workerAssets() []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, p := range versionedAssetPaths {
		add(p)
	}
	for _, p := range sharedScripts {
		add(p)
	}
	for _, m := range []map[string][]byte{chatScripts, chatStyles, chatExtraScripts} {
		for p := range m {
			add(p)
		}
	}
	sort.Strings(out)
	return out
}
