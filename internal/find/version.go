package find

import "strings"

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
func stampWorker(js []byte, version string) []byte {
	return []byte(strings.ReplaceAll(string(js), cachePlaceholder, "docfind-shell-"+version))
}
