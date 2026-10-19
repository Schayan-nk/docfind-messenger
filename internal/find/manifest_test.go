package find

import (
	"encoding/json"
	"strings"
	"testing"
)

// The manifest, checked against the server that has to serve everything it
// names.
//
// A web manifest fails silently and completely. A browser that cannot parse it,
// or that follows one of its icons to a 404, does not report an error anywhere
// a developer will see -- it simply declines to offer the install, and the only
// symptom is somebody saying the app "can't be added to the home screen any
// more". There is no test the browser runs that this file cannot fail, so these
// are the checks that would otherwise be nobody's job.
type webManifest struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	ShortName       string `json:"short_name"`
	Lang            string `json:"lang"`
	Dir             string `json:"dir"`
	StartURL        string `json:"start_url"`
	Scope           string `json:"scope"`
	Display         string `json:"display"`
	BackgroundColor string `json:"background_color"`
	ThemeColor      string `json:"theme_color"`
	LaunchHandler   struct {
		ClientMode string `json:"client_mode"`
	} `json:"launch_handler"`
	Icons []struct {
		Src     string `json:"src"`
		Sizes   string `json:"sizes"`
		Type    string `json:"type"`
		Purpose string `json:"purpose"`
	} `json:"icons"`
	Shortcuts []struct {
		Name      string `json:"name"`
		ShortName string `json:"short_name"`
		URL       string `json:"url"`
	} `json:"shortcuts"`
}

func parseManifest(t *testing.T) webManifest {
	t.Helper()
	var m webManifest
	if err := json.Unmarshal(manifestJSON, &m); err != nil {
		t.Fatalf("the manifest is not valid JSON, so every browser ignores it entirely "+
			"and the app is not installable on any platform: %v", err)
	}
	return m
}

// Every address the manifest names is one this server answers.
//
// This is the failure that costs the most and shows the least. An icon that
// 404s does not degrade the install prompt, it suppresses it: Chrome requires
// a fetchable icon before it will offer to install at all. A shortcut pointing
// at a route that no longer exists puts a dead entry in the long-press menu on
// somebody's home screen, which is worse than not offering the shortcut.
//
// So each one is fetched from the real handler rather than matched against the
// routing table as a string -- a route can be registered and still refuse to
// answer, and it is the answer that matters here.
func TestEveryAddressInTheManifestIsServed(t *testing.T) {
	m := parseManifest(t)
	s, _, _, _ := accountsTestServer(t)

	if len(m.Icons) == 0 {
		t.Fatal("the manifest declares no icons, so no browser will offer to install the app")
	}
	for _, ic := range m.Icons {
		rec := getAsset(t, s, ic.Src, "", "")
		if rec.Code != 200 {
			t.Errorf("icon %s answers %d; Chrome will not offer to install an app whose "+
				"icon it cannot fetch, and it reports nothing when it declines", ic.Src, rec.Code)
			continue
		}
		if n := rec.Body.Len(); n == 0 {
			t.Errorf("icon %s is served empty", ic.Src)
		}
	}

	for _, sc := range m.Shortcuts {
		// The fragment is the page's business, not the server's; what has to
		// exist is the document it hangs off.
		path := sc.URL
		if i := strings.IndexByte(path, '#'); i >= 0 {
			path = path[:i]
		}
		if path == "" {
			path = "/"
		}
		rec := getAsset(t, s, path, "", "")
		// A redirect is fine -- several of these are behind a sign-in, and
		// being sent to the sign-in page is the correct answer for somebody
		// who pressed a shortcut while signed out. A 404 is not: that is a
		// dead entry in the menu on somebody's home screen.
		if rec.Code == 404 || rec.Code >= 500 {
			t.Errorf("shortcut %q points at %s, which answers %d -- a dead entry in the "+
				"long-press menu on every installed home screen", sc.Name, path, rec.Code)
		}
		// Chrome shows short_name in the menu and falls back to name, so a
		// shortcut with neither is an unlabelled row.
		if strings.TrimSpace(sc.Name) == "" && strings.TrimSpace(sc.ShortName) == "" {
			t.Errorf("shortcut to %s has no name", sc.URL)
		}
		// Outside the scope a shortcut opens in the browser rather than in the
		// app, which is the one thing installing it was meant to stop.
		if !strings.HasPrefix(sc.URL, m.Scope) {
			t.Errorf("shortcut %q (%s) is outside the manifest scope %q, so it opens in a "+
				"browser tab instead of the installed app", sc.Name, sc.URL, m.Scope)
		}
	}
}

// A maskable icon, because Android will not make one for you.
//
// Given only "any" icons, Android draws the icon inside a white circle with the
// artwork shrunk into the middle -- the letterboxed look that makes an app
// appear unfinished next to every other icon on the launcher. "maskable" says
// the artwork already carries the safe-zone padding to be cropped to whatever
// shape the launcher uses.
//
// The two purposes are kept as separate entries on purpose. "any maskable" on
// one file tells the browser the same bitmap is correct in both roles, and it
// is not: the padding that makes a good maskable icon makes a small, floating
// favicon.
func TestTheInstalledIconSurvivesAndroidsMask(t *testing.T) {
	m := parseManifest(t)

	var maskable, any int
	for _, ic := range m.Icons {
		p := ic.Purpose
		if p == "" {
			p = "any" // the spec's default
		}
		for _, word := range strings.Fields(p) {
			switch word {
			case "maskable":
				maskable++
			case "any":
				any++
			}
		}
	}
	if maskable == 0 {
		t.Error("no icon is declared maskable, so Android shrinks the artwork into a white " +
			"circle and the app looks unfinished on the launcher")
	}
	if any == 0 {
		t.Error("every icon is maskable-only; there is nothing to use where the full " +
			"bitmap is wanted, such as the task switcher")
	}
	// 512 is the size Android takes for the splash screen and the launcher.
	var has512 bool
	for _, ic := range m.Icons {
		if strings.Contains(ic.Sizes, "512x512") {
			has512 = true
		}
	}
	if !has512 {
		t.Error("no 512x512 icon, which is the one Android uses for the splash screen")
	}
}

// Identity, and what happens to an installed app without it.
//
// With no "id", a browser identifies the installed app by its start_url. That
// works until start_url changes -- and then the installed copy on somebody's
// phone is a DIFFERENT app from the one the manifest now describes: it stops
// receiving updates to its name and icons, and installing again gives them a
// second icon beside the first rather than updating it. Declaring the id makes
// the identity independent of a URL somebody may reasonably want to change.
func TestTheAppIdentityIsPinned(t *testing.T) {
	m := parseManifest(t)

	if strings.TrimSpace(m.ID) == "" {
		t.Error("the manifest declares no id, so the app's identity is its start_url -- " +
			"change that and every installed copy orphans into a separate app")
	}
	// Persian, right-to-left, and it has to say so: without dir the browser
	// lays the name out left-to-right in the install prompt and the launcher.
	if m.Lang == "" || m.Dir == "" {
		t.Error("the manifest does not declare lang and dir, so a Persian name is laid " +
			"out left-to-right in the install prompt")
	}
	if m.Display != "standalone" && m.Display != "fullscreen" && m.Display != "minimal-ui" {
		t.Errorf("display is %q, so the installed app keeps the browser's address bar", m.Display)
	}
	if m.BackgroundColor == "" || m.ThemeColor == "" {
		t.Error("no background or theme colour, so the splash screen flashes white before " +
			"a dark app -- the worst frame of every cold start")
	}
	// start_url must be inside scope, or the app opens outside itself.
	if !strings.HasPrefix(m.StartURL, m.Scope) {
		t.Errorf("start_url %q is outside scope %q", m.StartURL, m.Scope)
	}
}

// Pressing a shortcut uses the window that is already open.
//
// Without this the default sends a shortcut, and anything else that launches
// the app, to a NEW window. Somebody with the calendar already open who presses
// "گزارش‌ها" from the taskbar gets a second copy of the application beside the
// first, and then has two windows polling, two sets of notifications, and no
// way to tell which is which.
//
// navigate-existing rather than focus-existing: the reader named a
// destination, so taking them there is what they asked for. focus-existing
// would surface the window and leave it on whatever page it was already
// showing, which reads as the shortcut having done nothing.
func TestAShortcutReusesTheOpenWindow(t *testing.T) {
	m := parseManifest(t)
	if got := m.LaunchHandler.ClientMode; got != "navigate-existing" {
		t.Errorf("launch_handler.client_mode is %q, want navigate-existing -- otherwise a "+
			"shortcut opens a second copy of the app beside the one already running", got)
	}
}
