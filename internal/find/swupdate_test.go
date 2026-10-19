package find

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

// Nothing takes over until somebody asks for it.
//
// This is the contract the whole feature rests on, and it is one line in sw.js:
// install must NOT call skipWaiting. With it, a newly installed worker seizes
// every open page the moment it finishes installing, and the reader is left on
// old HTML served by a new worker -- the pairing that put the reports page on
// "در حال بارگذاری گزارش‌ها…" when a release changed the CSP. Without it the new
// worker waits, and the button on the notice is what lets it through.
//
// The two calls are easy to confuse, so both directions are checked: skipWaiting
// must be reachable ONLY from the message handler, and clients.claim must stay,
// because it does a different job -- it is what lets a FIRST install control the
// page that installed it, where there is no predecessor to displace.
func TestNoWorkerTakesOverUnasked(t *testing.T) {
	s := string(serviceWorkerJS)

	install := strings.Index(s, "addEventListener('install'")
	message := strings.Index(s, "addEventListener('message'")
	if install < 0 {
		t.Fatal("sw.js has no install handler")
	}
	if message < 0 {
		t.Fatal("sw.js has no message handler, so a waiting worker can never be let " +
			"through and an update only lands when every tab is closed")
	}

	// The install handler ALONE -- up to its own closing brace, not up to the
	// next handler. Cutting at the activate handler instead swept in the
	// message handler that sits between them, whose whole job is to call
	// skipWaiting, and reported the feature as the bug it prevents.
	installBody := s[install:]
	if i := strings.Index(installBody, "\n});"); i > 0 {
		installBody = installBody[:i]
	}
	if strings.Contains(installBody, "self.skipWaiting()") {
		t.Error("install calls skipWaiting again, so a new worker seizes every open page " +
			"the instant it installs -- old HTML under a new worker, with nobody asked. " +
			"That is the state that took the reports page down")
	}

	// And the one place it IS allowed must be gated on the page's request.
	messageBody := s[message:]
	if i := strings.Index(messageBody, "\n});"); i > 0 {
		messageBody = messageBody[:i]
	}
	if !strings.Contains(messageBody, "SKIP_WAITING") || !strings.Contains(messageBody, "self.skipWaiting()") {
		t.Error("the message handler no longer lets a waiting worker through on request, " +
			"so pressing the update button cannot actually update anything")
	}

	// claim is a different decision and must survive.
	if !strings.Contains(s, "self.clients.claim()") {
		t.Error("clients.claim() is gone; a first install would no longer control the page " +
			"that installed it, so that first visit gets no offline shell and no " +
			"notifications until a reload")
	}
}

// An update that is ready gets noticed, however it became ready.
//
// There are two ways a worker ends up waiting, and only one of them announces
// itself:
//
//   - it installs while this page is open, which fires updatefound and then a
//     statechange to "installed";
//   - it installed during an EARLIER visit and is still waiting now, in which
//     case both of those events happened to a page that no longer exists.
//
// The second was the easy one to miss. Without asking the registration directly
// at start-up, a waiting update stays invisible until another release lands
// behind it -- so the reader is told about version three while version two sits
// there unmentioned.
//
// Both routes have to check for an existing controller. An "installed" worker
// with no controller is a first install, which activates by itself and is
// nobody's news.
func TestAReadyUpdateIsNoticedEitherWay(t *testing.T) {
	s := string(navJS)

	if !strings.Contains(s, "reg.waiting && navigator.serviceWorker.controller") {
		t.Error("nothing asks the registration whether an update is ALREADY waiting, so " +
			"one that installed during an earlier visit is never announced -- it stays " +
			"invisible until a further release lands behind it")
	}
	if !strings.Contains(s, "addEventListener('updatefound'") {
		t.Error("nothing listens for updatefound, so an update that installs while the " +
			"page is open is never announced")
	}
	if !strings.Contains(s, "fresh.state === 'installed' && navigator.serviceWorker.controller") {
		t.Error("the installed worker is announced without checking for a controller, so a " +
			"first install is reported as an update to somebody who has not used the app yet")
	}
	// And a way to ask on demand, for somebody who does not want to wait out
	// the half-hour interval.
	if !strings.Contains(s, "window.docfindUpdate") {
		t.Error("there is no way to check for an update on demand, so the only route is to " +
			"wait for the interval")
	}
}

// The press is what performs the update.
//
// Not a reload button with a polite label. The order is: tell the waiting worker
// to take over, wait for it to have done so, and only then reload -- reloading
// first would pull the new HTML down while the old worker was still serving it,
// which is the same mismatch the other way round.
func TestTheButtonPerformsTheUpdate(t *testing.T) {
	s := string(navJS)

	i := strings.Index(s, "function showUpdateBar")
	if i < 0 {
		t.Fatal("the update notice is gone")
	}
	body := s[i:]

	if !strings.Contains(body, "postMessage({ type: 'SKIP_WAITING' })") {
		t.Error("the button no longer asks the waiting worker to take over, so pressing " +
			"it reloads the page onto the SAME version and the notice comes back")
	}
	// The reload must be driven by controllerchange, not fired alongside the
	// message. A reload in the same click, before the handover, races it.
	cc := strings.Index(s, "addEventListener('controllerchange'")
	if cc < 0 {
		t.Fatal("nothing listens for controllerchange, so nothing reloads once the new " +
			"worker has taken over and the page sits on old HTML under it")
	}
	ccBody := s[cc:]
	if j := strings.Index(ccBody, "\n  });"); j > 0 {
		ccBody = ccBody[:j]
	}
	if !strings.Contains(ccBody, "accepting") || !strings.Contains(ccBody, "location.reload()") {
		t.Error("controllerchange does not reload after an accepted handover, so the press " +
			"swaps the worker and leaves the old HTML in front of the reader")
	}

	// A handover that never completes must not strand somebody on a disabled
	// button. The reload alone is still most of the benefit.
	if !strings.Contains(body, "if (accepting) location.reload()") {
		t.Error("there is no fallback if the worker never activates; the button would stay " +
			"disabled and the page would never move")
	}
}

// A first install is not an update -- and everything after it is.
//
// clients.claim() means that on a first-ever visit the page's controller goes
// from null to a worker and controllerchange fires -- an INSTALL. The notice
// listens to that same event as a backstop, so without a guard every new reader
// would be told a new version had arrived before they had used the application
// once.
//
// The guard has to swallow exactly ONE change, and this test exists because the
// first version of it swallowed them all. It read the controller into a const at
// load: a page opened for the first time captured false, and that false then
// stood for the rest of the page's life, so a deployment arriving an hour later
// was suppressed as well. It was found by measuring, not reading -- the shell
// cache moved from v5 to v6, the old cache was dropped, the page was claimed by
// the new worker, and nothing was said.
//
// And it fails worst on the page that matters most: somebody's first page of the
// day is the page they leave open all day, which is the tab this exists for.
//
// So the flag has to be reassignable, and the handler has to set it. A const
// cannot express "the first one only".
func TestOnlyTheFirstClaimIsTreatedAsAnInstall(t *testing.T) {
	s := string(navJS)

	capture := strings.Index(s, "let controlled = !!navigator.serviceWorker.controller")
	if capture < 0 {
		t.Fatal("nav.js no longer tracks whether the page is controlled with a reassignable " +
			"flag. If this went back to a const read once at load, a page whose first " +
			"visit installed the worker will never report an update again -- and that is " +
			"the page somebody leaves open all day")
	}

	listener := strings.Index(s, "addEventListener('controllerchange'")
	if listener < 0 {
		t.Fatal("nav.js no longer listens for controllerchange, so a deployment that " +
			"takes over an open page says nothing and the reader is left on stale HTML")
	}
	if capture > listener {
		t.Error("the controller is read after the controllerchange listener is attached; " +
			"it must be read at load, because inside the handler there is always a " +
			"controller and the check would pass in both cases")
	}

	// The handler must both return early AND record that the claim happened.
	// Returning without recording is the bug above; recording without returning
	// announces the install.
	body := s[listener:]
	if i := strings.Index(body, "});"); i > 0 {
		body = body[:i]
	}
	if !strings.Contains(body, "if (!controlled)") {
		t.Error("controllerchange no longer swallows the install's own claim, so every " +
			"new reader is told the app updated before they had used it once")
	}
	if !strings.Contains(body, "controlled = true") {
		t.Error("controllerchange never records that the first claim happened, so the flag " +
			"stays false and every later update is swallowed too -- the exact regression " +
			"this test was written for")
	}
	if !strings.Contains(body, "showUpdateBar()") {
		t.Error("controllerchange no longer raises the notice at all")
	}
}

// The worker script can be revalidated, which is the whole reason an open page
// can ever notice a new release.
//
// /sw.js was served with "Cache-Control: no-cache" and no validator. That looks
// like the careful choice and is the opposite of one: no-cache does not mean
// "do not store", it means "store this, but check before each use". A browser
// told to check, and handed nothing to check WITH, cannot ask the question --
// and Chrome resolves that by using what it already has.
//
// Measured: two builds differing only in the worker's cache version were served
// in turn to a page that was already open. A plain fetch from that page saw the
// new script. registration.update() resolved having installed nothing, and the
// old worker stayed active. Every mechanism above this -- the interval, the
// visibility check, the notice itself -- is downstream of this one header, and
// all of it was dead.
//
// The ETag must be the CONTENT's hash, not the build's version: a fix shipped
// without a version bump is still a different worker, and the version string is
// not in this file anyway.
func TestTheWorkerScriptCanBeRevalidated(t *testing.T) {
	s, _, _, _ := accountsTestServer(t)

	rec := getAsset(t, s, "/sw.js", "", "")
	if rec.Code != 200 {
		t.Fatalf("/sw.js answers %d", rec.Code)
	}
	etag := rec.Header().Get("ETag")
	if etag == "" && rec.Header().Get("Last-Modified") == "" {
		t.Fatal("/sw.js is served with no ETag and no Last-Modified, so a browser told " +
			"to revalidate it has nothing to revalidate with -- and a deployed worker " +
			"change can be missed entirely, which is the one kind of staleness this app " +
			"cannot recover from on its own")
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "no-cache") {
		t.Errorf("Cache-Control on /sw.js is %q; it must force revalidation", cc)
	}
	// The scope header has to survive, including on a 304 -- without it a
	// worker served from /sw.js may not control the whole origin.
	if got := rec.Header().Get("Service-Worker-Allowed"); got != "/" {
		t.Errorf("Service-Worker-Allowed is %q, want \"/\"", got)
	}

	// Revalidation actually works: the same tag comes back as 304, and the
	// scope header is still on it.
	again := getAsset(t, s, "/sw.js", "", etag)
	if again.Code != 304 {
		t.Errorf("re-requesting /sw.js with its own ETag answered %d, want 304 -- the "+
			"validator is present but not honoured, so every check re-sends the script",
			again.Code)
	}
	if got := again.Header().Get("Service-Worker-Allowed"); got != "/" {
		t.Errorf("the 304 drops Service-Worker-Allowed (%q), so a revalidated worker "+
			"may lose its scope over the origin", got)
	}

	// And the tag tracks the content. Hashing the embedded bytes here is the
	// point: if the tag were derived from anything else -- a build version, a
	// start time -- two builds with the same version and different workers
	// would share a tag, and the second would never install.
	// Hashed over the bytes actually SERVED, which are the embedded script with
	// the build version stamped into it -- not the raw embedded bytes. An ETag
	// derived from anything other than the response body is not a validator.
	served := stampWorker(serviceWorkerJS, buildVersion)
	sum := sha256.Sum256(served)
	if !strings.Contains(etag, hex.EncodeToString(sum[:8])) {
		t.Errorf("the ETag %q is not derived from the worker script's own bytes, so a "+
			"changed worker shipped under an unchanged version string would revalidate "+
			"as unchanged and never install", etag)
	}
}

// Every release ships a different worker.
//
// This is the property the whole update notice depends on, and it was missing.
// A browser only treats a worker as new when its BYTES differ. docfind 5.4.5
// changed the indexer and the search path and nothing else, so sw.js was
// byte-identical to 5.4.4's: no browser saw an update, nothing entered the
// waiting state, and the notice that had been written and deployed to announce
// exactly this said nothing. The release shipped silently.
//
// Naming the shell cache after the build fixes it, and removes the
// hand-maintained constant that had to be remembered on every shell change --
// which had already been bumped by hand twice.
func TestEveryReleaseShipsADifferentWorker(t *testing.T) {
	a := stampWorker(serviceWorkerJS, "5.4.5")
	b := stampWorker(serviceWorkerJS, "5.4.6")

	if string(a) == string(b) {
		t.Fatal("two different builds produce byte-identical service workers, so a browser " +
			"sees no update and the notice never fires -- which is exactly how 5.4.5 " +
			"shipped without telling anyone")
	}
	if !strings.Contains(string(a), "docfind-shell-5.4.5") {
		t.Error("the shell cache is not named after the build, so it neither changes per " +
			"release nor retires the previous release's cache")
	}
	// The placeholder must be gone from the served bytes, or the stamp silently
	// did nothing and every release would share one cache name again.
	if strings.Contains(string(a), cachePlaceholder) {
		t.Errorf("the placeholder %q survived stamping; the replacement did not match, so "+
			"the cache name is frozen and updates stop being noticed", cachePlaceholder)
	}
	// And the stamped script is still the same worker otherwise -- stamping must
	// not disturb the logic the notice depends on.
	for _, must := range []string{"SKIP_WAITING", "self.clients.claim()", "addEventListener('install'"} {
		if !strings.Contains(string(a), must) {
			t.Errorf("stamping the version lost %q from the worker", must)
		}
	}
}

// An open page asks whether a new release exists, and only while it is being
// looked at.
//
// The calendar is a landing page that stays open all day on a screen nobody
// touches. Without asking, it runs the code it loaded that morning until
// something reloads it -- which is how a deployment reaches everyone except
// the people who leave the application open, who are the people using it most.
//
// The visibility condition is not politeness: a phone in somebody's pocket
// waking every half hour to ask is battery spent on nothing.
func TestAnOpenPageChecksForANewRelease(t *testing.T) {
	s := string(navJS)

	if !strings.Contains(s, "reg.update()") {
		t.Fatal("nav.js never calls registration.update(), so a page left open runs the " +
			"release it loaded with until somebody reloads it by hand")
	}
	if !strings.Contains(s, "document.visibilityState === 'visible'") {
		t.Error("the update check is not conditioned on the page being visible, so a " +
			"backgrounded tab -- and a phone in a pocket -- keeps asking for nothing")
	}
	if !strings.Contains(s, "visibilitychange") {
		t.Error("nothing checks on return to the page, so a laptop shut overnight -- " +
			"where the interval never fired -- wakes up on yesterday's release")
	}
}

// The notice offers a way out and does not take one away.
//
// Two failure modes, both of which turn a helpful notice into a harmful one:
//
//   - reloading automatically. The report builder holds a query somebody is
//     part-way through typing; a reload they did not ask for loses it.
//   - timing out. A notice that removes itself while somebody is away from
//     their desk is the same as no notice, and this one exists precisely for
//     the page nobody is looking at.
func TestTheUpdateNoticeLeavesTheDecisionToTheReader(t *testing.T) {
	s := string(navJS)

	i := strings.Index(s, "function showUpdateBar")
	if i < 0 {
		t.Fatal("the update notice is gone; a deployment that takes over an open page " +
			"leaves it on stale HTML with nothing said")
	}
	// The notice's own section, not everything after it.
	//
	// This used to slice to the end of the file, which held only as long as
	// the update notice happened to be last in nav.js. It stopped being last
	// when the PDF export moved here to be shared by every page, and the
	// export's one legitimate timer -- the frame it waits before opening the
	// print dialog -- was reported as the notice doing something it should
	// not. The sections of this file are separated by comment banners, so the
	// next banner is where this one ends.
	bar := s[i:]
	if j := strings.Index(bar, "\n  // ---"); j > 0 {
		bar = bar[:j]
	}

	if !strings.Contains(bar, "location.reload()") {
		t.Error("the notice offers no way to take the update")
	}
	// The notice must not time out -- but "no setTimeout at all" is too blunt a
	// way to say so, because the accept path legitimately arms one: a fallback
	// reload for a handover that never completes. What must never be on a timer
	// is the notice REMOVING itself, which is the thing that would let somebody
	// come back to their desk and find it gone.
	for _, chunk := range strings.Split(bar, "setTimeout(")[1:] {
		if len(chunk) > 160 {
			chunk = chunk[:160]
		}
		if strings.Contains(chunk, "remove(") {
			t.Error("the update notice removes itself on a timer; it is shown to somebody " +
				"who may not be at the screen, which is the case it exists for")
		}
		if !strings.Contains(chunk, "location.reload()") {
			t.Errorf("a timer in the update notice does something other than the fallback "+
				"reload: %q", chunk)
		}
	}
	// Persian, like every other string a reader sees on these pages.
	if !strings.Contains(bar, "نسخهٔ تازه") {
		t.Error("the notice is not in Persian")
	}
	// Announced. A notice that only appears visually is one a screen-reader
	// user never learns about -- and polite rather than assertive, because it
	// is not an error and must not cut across what is being read.
	if !strings.Contains(bar, `'role', 'status'`) || !strings.Contains(bar, `'aria-live', 'polite'`) {
		t.Error("the update notice is not announced politely to a screen reader")
	}

	// The bar needs somewhere to be. Written without this, it inherited no
	// position at all and rendered inline at the very foot of the document,
	// below the fold, where nobody would see it.
	if !bytes.Contains(appCSS, []byte(".swbar {")) {
		t.Error("app.css has no .swbar rule, so the notice renders unpositioned at the " +
			"foot of the document instead of over the page")
	}
	// .rise ends on `transform: none`, so a transform-centred bar jumps out of
	// place the moment the entry animation finishes.
	if css := string(appCSS); strings.Contains(css, ".swbar {") {
		rule := css[strings.Index(css, ".swbar {"):]
		if end := strings.Index(rule, "}"); end > 0 {
			if strings.Contains(rule[:end], "translateX") {
				t.Error(".swbar centres itself with a transform, which .rise wipes to " +
					"`none` when the entry animation ends -- the bar will jump")
			}
		}
	}
}
