package find

import (
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Schayan-nk/docfind-messenger/internal/audit"
	"github.com/Schayan-nk/docfind-messenger/internal/auth"
	"github.com/Schayan-nk/docfind-messenger/internal/chat"
	"github.com/Schayan-nk/docfind-messenger/internal/dependencies"
	"github.com/Schayan-nk/docfind-messenger/internal/obs"
	"github.com/Schayan-nk/docfind-messenger/internal/webauthn"
)

//go:embed favicon.ico
var faviconICO []byte

// The sign-in page and the reports dashboard. Separate documents rather than
// screens inside ui.html: that file carries a full inlined Tailwind build for
// the browser view, and neither of these needs it.
//
//go:embed login.html
var loginHTML []byte

// The page the service worker serves when a navigation cannot reach us.
//
// Served from here as well as cached, so that the worker has something to
// precache in the first place and so that hitting /offline while online shows
// something sensible rather than a 404.
//
//go:embed offline.html
var offlineHTML []byte

//go:embed settings.html
var settingsHTML []byte

// rolesJS edits what each role gets by default (roletemplates.go).
//
//go:embed roles.js
var rolesJS []byte

// Installable-app assets. Without a manifest and an apple-touch-icon, iOS
// Safari's "Add to Home Screen" produces a bookmark with a screenshot for an
// icon rather than something that opens standalone.
//
//go:embed manifest.webmanifest
var manifestJSON []byte

//go:embed sw.js
var serviceWorkerJS []byte

// The template behind the 404 and 500 pages. Both variants are built from it
// once at start-up; see errorpages.go.
//
//go:embed error.html
var errorHTML []byte

// The loading animation: one ring, a sweeping comet and seven orbiting
// particles, in pure CSS and SVG with no script and no image. Kept as its own
// file so exactly one copy exists -- the server injects it into every page,
// rather than it being written into each page for the first paint and again
// into nav.js for later waits. Styled by .ld in app.css.
//
//go:embed loader.html
var loaderHTML []byte

//go:embed icon-192.png
var icon192PNG []byte

//go:embed icon-512.png
var icon512PNG []byte

//go:embed apple-touch-icon.png
var appleTouchIconPNG []byte

// The maskable variant, which the manifest offers alongside the others.
//
// A launcher is entitled to crop a maskable icon to the inner circle -- about
// 80% of the side -- so this one carries the mark at a smaller scale with the
// margin that crop needs. The same artwork in the "any" icons would look
// needlessly small, which is why there are two rather than one.
//
//go:embed icon-maskable.png
var iconMaskablePNG []byte

// The same mark again at header size.
//
//go:embed icon.png
var iconPNG []byte

// The shared stylesheet. Served as its own file rather than inlined into each
// page so the browser fetches it once and every page is guaranteed to be
// looking at the same tokens.
//
//go:embed app.css
var appCSS []byte

// The uiverse-derived components (MIT), kept in their own file because they
// are a distinct body of work with its own attribution -- see the header
// there. Served as part of app.css rather than as a second stylesheet: every
// page would otherwise pay for another request, and these rules are not
// optional on any of them.
//
//go:embed uiverse.css
var uiverseCSS []byte

// styleSheet is the two joined, built once at start-up.
var styleSheet = func() []byte {
	out := make([]byte, 0, len(appCSS)+len(uiverseCSS)+1)
	out = append(out, appCSS...)
	out = append(out, '\n')
	return append(out, uiverseCSS...)
}()

// The shared mobile navigation, for the same reason: one menu, defined once,
// rather than three copies drifting apart.
//
//go:embed nav.js
var navJS []byte

// pushJS is the device-notification client: it subscribes this device to its
// push service and hands the subscription to the server. See push.js.
//
//go:embed push.js
var pushJS []byte

// notifyCenterJS is the bell and notification panel, loaded by nav.js for a
// signed-in account.
//
//go:embed notifycenter.js
var notifyCenterJS []byte

// datePickJS is the Jalali date picker, shared by the report pages and the
// evaluation page.
//
//go:embed datepick.js
var datePickJS []byte

// glassNavJS is the glass dock, nav.js's navigation on a phone or tablet.
//
//go:embed glassnav.js
var glassNavJS []byte

// megaNavJS is the mega menu: on a desk, a group of the glass sidebar opens
// as a panel beside it. nav.js loads it there and nowhere else.
//
//go:embed meganav.js
var megaNavJS []byte

// twoStepJS draws the two-step sign-in card on the settings page.
//
//go:embed twostep.js
var twoStepJS []byte

// passkeysJS is the passkey button on the sign-in page and the passkey card
// on the settings page.
//
//go:embed passkeys.js
var passkeysJS []byte

// Leaflet draws the map a location to share is picked on (chatplace.js).
//
//go:embed vendor/leaflet.js
var leafletJS []byte

//go:embed vendor/leaflet.css
var leafletCSS []byte

// The thought-orb engine, drawn on a 2D canvas: the loader nav.js shows while
// a page waits on the server.
//
//go:embed vendor/thinking-orbs.js
var thinkingOrbsJS []byte

// The person editor on the settings page's users list (useredit.js).
//
//go:embed useredit.js
var userEditJS []byte

// Server is the messenger's HTTP side: its pages, the chat and call API,
// accounts, notifications and the audit trail.
type Server struct {
	token string

	// latency is the recent response time of every route, for the p50/p95/
	// p99 an administrator sees. Built with the server so every request from
	// the first is counted.
	latency *obs.Latency

	// push is device notifications (pushroutes.go), nil when not configured.
	push *pushFeature
	// pushWatch is what the notification watchers last did.
	pushWatch *pushWatchState
	// audit records every change and export (auditroutes.go). Always set: with
	// no database the events still go to the application log.
	audit *audit.Recorder
	// auditStore is where the trail can be searched from; nil without
	// PostgreSQL, and /api/audit then says so.
	auditStore auditStore

	// guard is nil when the server runs in its original single-user desktop
	// mode, where the loopback bind and the start-up token are the whole
	// access model. It is set once accounts are configured, which is what
	// makes the server safe to expose beyond this machine.
	guard *auth.Guard
	// The messenger (chatroutes.go); nil until UseChat.
	chat    *chat.Service
	chatAPI *chat.API
	// tiles is the map-tile cache behind /maptile (maptiles.go); nil draws a
	// shared location on a plain grid.
	tiles *tileState

	// sms is the SMS panel's settings (smssettings.go), nil when there is
	// nowhere to keep them.
	sms *smsState
	// idem remembers the answers to recent writes by their Idempotency-Key,
	// so a retried send is done once (idempotency.go). In memory, bounded.
	idem idemStore
	// perms is the permission catalogue, swapped wholesale. Read on every
	// gated request, so it is an atomic
	// pointer rather than a map somebody edits in place: a data race in the
	// access model is the worst kind this application could have.
	perms atomic.Pointer[auth.Catalogue]
	// avatarDir holds the profile pictures, beside the accounts file. Empty
	// when accounts are off, in which case the picture routes answer 404.
	avatarDir string
	// secPolicyStore keeps the sign-in policy (securitypolicy.go); nil leaves
	// the default in force and the security centre's card read-only.
	secPolicyMu    sync.Mutex
	secPolicyStore appSettingsStore
	// roleTplStore keeps the edited role templates (roletemplates.go).
	roleTplStore appSettingsStore
	// spentChallenges are second-step challenges already used to sign in,
	// until they expire (twostep.go).
	spentChallenges sync.Map
	// codeMail mails second-step codes, and mailedCodes are the ones
	// outstanding (twostepmail.go).
	codeMail    codeSender
	mailedCodes map[string]*mailedCode
	mailedMu    sync.Mutex
	// passkeyRP is the relying party passkeys are bound to; nil keeps them
	// off. passkeyChallenges are the ceremonies in flight (passkeyroutes.go).
	passkeyRP         *webauthn.Relying
	passkeyChallenges sync.Map
	// deps is every dependency's state and circuit breaker. See
	// internal/dependencies.
	deps *dependencies.Registry
	// pg is PostgreSQL's failure level (1 up, 2 briefly down, 3 offline),
	// what the guard refuses changes by and the banner reads. See pglevel.go.
	pg pgLevel
}

// NewServer is a server whose pages and API are gated on token until
// UseAccounts turns accounts on.
func NewServer(token string) *Server {
	return &Server{
		token:     token,
		latency:   obs.NewLatency(0),
		pushWatch: &pushWatchState{},
		audit:     audit.NewRecorder(),
	}
}

// UseAccounts turns on user accounts, roles and token authentication.
//
// Until this is called the server behaves exactly as it always has, so the
// desktop use case is unaffected by any of the access control below.
func (s *Server) UseAccounts(users *auth.Store, signer *auth.Signer, trustProxyHeaders bool) {
	s.guard = &auth.Guard{
		Users:             users,
		Signer:            signer,
		Throttle:          auth.NewThrottle(8, 15*time.Minute, 15*time.Minute),
		TrustProxyHeaders: trustProxyHeaders,
	}
	// So the audit trail records the real visitor IP from the trusted front's
	// X-Forwarded-For tail, matching Guard.ClientIP, rather than the proxy's
	// own address for every action.
	audit.SetTrustProxyHeaders(trustProxyHeaders)
}

// UseSessions records where people are signed in, and where their pictures go.
//
// Both live beside the accounts file, because that is already the directory
// this application treats as its private state: it is created 0700 and holds
// the password hashes and the signing key.
func (s *Server) UseSessions(sessions *auth.Sessions, avatarDir string) {
	if s.guard != nil {
		s.guard.Sessions = sessions
	}
	s.avatarDir = avatarDir
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.registerPages(mux)
	s.registerSignInRoutes(mux)
	s.registerAccountRoutes(mux)
	routeOf := func(r *http.Request) string {
		_, pattern := mux.Handler(r)
		return pattern
	}
	// The request log goes outside everything, so the duration it records is
	// the whole of what the client waited for; the audit trail just inside
	// it, where the signed-in user is known; compression inside the security
	// headers, so every response carries them; panic recovery inside
	// compression, so a panic is caught before a partial body is flushed.
	return obs.Middleware(nil, routeOf, s.latency,
		audit.Middleware(s.audit, routeOf,
			securityHeaders(s.guard != nil && s.guard.TrustProxyHeaders,
				compressResponses(s.degradedGuard(recoverPanics(mux))))))
}

// registerPages serves the pages and what they load. Each page is a static
// shell that fetches its data from the gated APIs below.
func (s *Server) registerPages(mux *http.ServeMux) {
	// The messenger is the whole application: the root goes to it.
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/messages", http.StatusFound)
	})
	// Every other address nothing serves: the 404 page for a browser, JSON for
	// anything else (errorpages.go). "GET /" is the mux's catch-all.
	mux.HandleFunc("GET /", writeNotFound)
	mux.HandleFunc("GET /favicon.ico", s.handleFavicon())
	// versionAssetLinks stamps the font references inside the stylesheet the
	// same way handlePage stamps a page's references to it; see cachecontrol.go.
	mux.HandleFunc("GET /app.css", s.handleAsset(versionAssetLinks(styleSheet, assetStamp()), "text/css; charset=utf-8"))
	// nav.js loads the dock and the notification centre by address, so it is
	// stamped like a page: those two load as immutable, cached files.
	mux.HandleFunc("GET /nav.js", s.handleAsset(versionAssetLinks(navJS, assetStamp()), "text/javascript; charset=utf-8"))
	mux.HandleFunc("GET /push.js", s.handleAsset(pushJS, "text/javascript; charset=utf-8"))
	mux.HandleFunc("GET /notifycenter.js", s.handleAsset(notifyCenterJS, "text/javascript; charset=utf-8"))
	mux.HandleFunc("GET /datepick.js", s.handleAsset(datePickJS, "text/javascript; charset=utf-8"))
	mux.HandleFunc("GET /glassnav.js", s.handleAsset(glassNavJS, "text/javascript; charset=utf-8"))
	mux.HandleFunc("GET /meganav.js", s.handleAsset(megaNavJS, "text/javascript; charset=utf-8"))
	mux.HandleFunc("GET /twostep.js", s.handleAsset(twoStepJS, "text/javascript; charset=utf-8"))
	mux.HandleFunc("GET /passkeys.js", s.handleAsset(passkeysJS, "text/javascript; charset=utf-8"))
	mux.HandleFunc("GET /vendor/leaflet.js", s.handleAsset(leafletJS, "text/javascript; charset=utf-8"))
	mux.HandleFunc("GET /vendor/leaflet.css", s.handleAsset(leafletCSS, "text/css; charset=utf-8"))
	mux.HandleFunc("GET /vendor/thinking-orbs.js", s.handleAsset(thinkingOrbsJS, "text/javascript; charset=utf-8"))
	mux.HandleFunc("GET /useredit.js", s.handleAsset(userEditJS, "text/javascript; charset=utf-8"))
	mux.HandleFunc("GET /roles.js", s.handleAsset(rolesJS, "text/javascript; charset=utf-8"))
	// Third-party assets referenced by path: the icon sprite and the Lottie
	// runtime. Unauthenticated: library code and chrome, carrying nothing the
	// token protects.
	s.registerStatic(mux)

	// The pages. Each is a static shell that fetches its data from the gated
	// APIs. signedInPage redirects a signed-out reader to sign in first
	// (pagegate.go); the sign-in page itself is the opposite.
	mux.HandleFunc("GET /login", s.signedOutPage(s.handlePage(injectAssets(loginHTML))))
	// Unauthenticated on purpose, like /login: it carries no data, and the
	// moment it is needed there is no server to ask about a session anyway.
	mux.HandleFunc("GET /offline", s.handlePage(offlineHTML))
	mux.HandleFunc("GET /settings", s.signedInPage(s.handlePage(injectAssets(settingsHTML))))
	// A page per category (/settings/security, /settings/users, ...): the same
	// page, which reads the category from the address and builds only it.
	mux.HandleFunc("GET /settings/{section}", s.signedInPage(s.handlePage(injectAssets(settingsHTML))))
	s.routeChat(mux)

	// Installable-app assets.
	mux.HandleFunc("GET /manifest.webmanifest", s.handleAsset(manifestJSON, "application/manifest+json"))
	mux.HandleFunc("GET /sw.js", s.handleServiceWorker())
	mux.HandleFunc("GET /icon.png", s.handleAsset(iconPNG, "image/png"))
	mux.HandleFunc("GET /icon-192.png", s.handleAsset(icon192PNG, "image/png"))
	mux.HandleFunc("GET /icon-512.png", s.handleAsset(icon512PNG, "image/png"))
	mux.HandleFunc("GET /icon-maskable.png", s.handleAsset(iconMaskablePNG, "image/png"))
	mux.HandleFunc("GET /apple-touch-icon.png", s.handleAsset(appleTouchIconPNG, "image/png"))
}

// registerSignInRoutes is signing in and out, which cannot require a
// session, and the one route a machine calls.
func (s *Server) registerSignInRoutes(mux *http.ServeMux) {
	// Sessions. /api/me is deliberately open: the sign-in page calls it to
	// find out whether anyone is signed in and whether a first administrator
	// still has to be created. It reveals nothing but that.
	mux.HandleFunc("GET /api/me", s.handleMe)
	mux.HandleFunc("POST /api/login", s.mutating(s.handleLogin))
	// The code after the password, for an account that signs in in two steps
	// (twostep.go). Open like /api/login: its challenge is the credential.
	mux.HandleFunc("POST /api/login/second-step", s.mutating(s.handleLoginSecondStep))
	mux.HandleFunc("POST /api/login/second-step/resend", s.mutating(s.handleLoginSecondStepResend))
	// Signing in with a passkey instead of a password (passkeyroutes.go).
	// Open like /api/login: the passkey's signature is the credential.
	mux.HandleFunc("POST /api/login/passkey/options", s.mutating(s.handlePasskeyLoginOptions))
	mux.HandleFunc("POST /api/login/passkey", s.mutating(s.handlePasskeyLogin))
	// A forgotten password, by a code texted to the account's phone
	// (passwordreset.go). Open like /api/login: the code is the credential.
	mux.HandleFunc("POST /api/login/forgot", s.mutating(s.handleForgotPassword))
	mux.HandleFunc("POST /api/login/reset", s.mutating(s.handleResetPassword))
	mux.HandleFunc("POST /api/logout", s.mutating(s.handleLogout))
	mux.HandleFunc("POST /api/first-admin", s.mutating(s.handleFirstAdmin))
}

// registerAccountRoutes is everything that exists only with accounts:
// managing them, sessions, settings and notifications.
func (s *Server) registerAccountRoutes(mux *http.ServeMux) {
	// Account management: administrators only.
	if s.AuthEnabled() {
		mux.HandleFunc("GET /api/users", s.requirePermission(auth.ActionManageUsers, s.handleListUsers))
		mux.HandleFunc("POST /api/users", s.mutating(s.requirePermission(auth.ActionManageUsers, s.handleCreateUser)))
		mux.HandleFunc("PUT /api/users", s.mutating(s.requirePermission(auth.ActionManageUsers, s.handleUpdateUser)))
		mux.HandleFunc("POST /api/users/password", s.mutating(s.requirePermission(auth.ActionManageUsers, s.handleSetPassword)))
		mux.HandleFunc("PUT /api/users/phone", s.mutating(s.requirePermission(auth.ActionManageUsers, s.handleSetUserPhone)))
		mux.HandleFunc("DELETE /api/users", s.mutating(s.requirePermission(auth.ActionManageUsers, s.handleDeleteUser)))

		// A person's own account. Every one of these resolves who it is
		// acting on from the caller's token, never from the request, so
		// RequireAuth is the whole gate.
		mux.HandleFunc("GET /api/account/sessions", s.guard.RequireAuth(s.handleSessions))
		mux.HandleFunc("POST /api/account/prefs", s.mutating(s.guard.RequireAuth(s.handleSetPrefs)))
		mux.HandleFunc("POST /api/account/phone", s.mutating(s.guard.RequireAuth(s.handleSetOwnPhone)))
		mux.HandleFunc("POST /api/account/menu", s.mutating(s.guard.RequireAuth(s.handleSetMenu)))
		mux.HandleFunc("POST /api/account/menu/hidden", s.mutating(s.guard.RequireAuth(s.handleSetMenuHidden)))
		mux.HandleFunc("POST /api/account/password", s.mutating(s.guard.RequireAuth(s.handleChangePassword)))
		mux.HandleFunc("POST /api/account/signout-others", s.mutating(s.guard.RequireAuth(s.handleSignOutOthers)))
		// The database's failure level, which every page asks once a minute
		// to decide on the offline banner. See pglevel.go.
		mux.HandleFunc("GET /api/db-state", s.guard.RequireAuth(s.handleDBState))
		// The tiles a shared location's map is drawn from (maptiles.go).
		mux.HandleFunc("GET /maptile/{z}/{x}/{y}", s.guard.RequireAuth(s.handleMapTile))

		// Two-step sign-in on one's own account (twostep.go). Each change asks
		// for the password again; an administrator can only turn it off for
		// somebody, never read or set their key.
		mux.HandleFunc("GET /api/account/two-step", s.guard.RequireAuth(s.handleTwoStep))
		mux.HandleFunc("POST /api/account/two-step/begin", s.mutating(s.guard.RequireAuth(s.handleTwoStepBegin)))
		mux.HandleFunc("POST /api/account/two-step/confirm", s.mutating(s.guard.RequireAuth(s.handleTwoStepConfirm)))
		mux.HandleFunc("POST /api/account/two-step/disable", s.mutating(s.guard.RequireAuth(s.handleTwoStepDisable)))
		mux.HandleFunc("POST /api/account/two-step/recovery", s.mutating(s.guard.RequireAuth(s.handleTwoStepRecovery)))
		mux.HandleFunc("POST /api/account/two-step/sms/begin", s.mutating(s.guard.RequireAuth(s.handleTwoStepSMSBegin)))
		mux.HandleFunc("POST /api/account/two-step/sms/confirm", s.mutating(s.guard.RequireAuth(s.handleTwoStepSMSConfirm)))
		mux.HandleFunc("POST /api/account/two-step/email/begin", s.mutating(s.guard.RequireAuth(s.handleTwoStepEmailBegin)))
		mux.HandleFunc("POST /api/account/two-step/email/confirm", s.mutating(s.guard.RequireAuth(s.handleTwoStepEmailConfirm)))
		mux.HandleFunc("POST /api/account/two-step/send-code", s.mutating(s.guard.RequireAuth(s.handleTwoStepSendCode)))
		// Passkeys on one's own account (passkeyroutes.go). Adding and
		// removing one asks for the password again.
		mux.HandleFunc("GET /api/account/passkeys", s.guard.RequireAuth(s.handlePasskeys))
		mux.HandleFunc("POST /api/account/passkeys/options", s.mutating(s.guard.RequireAuth(s.handlePasskeyOptions)))
		mux.HandleFunc("POST /api/account/passkeys", s.mutating(s.guard.RequireAuth(s.handlePasskeyAdd)))
		mux.HandleFunc("POST /api/account/passkeys/remove", s.mutating(s.guard.RequireAuth(s.handlePasskeyRemove)))
		mux.HandleFunc("POST /api/users/passkeys/reset", s.mutating(s.requirePermission(auth.ActionManageUsers, s.handlePasskeyReset)))
		mux.HandleFunc("POST /api/users/two-step/reset", s.mutating(s.requirePermission(auth.ActionManageUsers, s.handleTwoStepReset)))
		// The sign-in policy and the lockouts (securitypolicy.go).
		mux.HandleFunc("GET /api/admin/security/policy", s.requirePermission(auth.ActionManageSettings, s.handleSecurityPolicy))
		mux.HandleFunc("PUT /api/admin/security/policy", s.mutating(s.requirePermission(auth.ActionManageSettings, s.handleSaveSecurityPolicy)))
		mux.HandleFunc("GET /api/admin/security/lockouts", s.requirePermission(auth.ActionManageUsers, s.handleLockouts))
		mux.HandleFunc("POST /api/admin/security/unlock", s.mutating(s.requirePermission(auth.ActionManageUsers, s.handleUnlock)))

		// Pictures. GET is open to any signed-in person because a colleague's
		// face is drawn beside their name; POST and DELETE act on the caller
		// unless a name is given, and giving a name needs PermManageUsers.
		mux.HandleFunc("GET /api/account/photo", s.guard.RequireAuth(s.handleAvatar))
		mux.HandleFunc("POST /api/account/photo", s.mutating(s.guard.RequireAuth(s.handleAvatarUpload)))
		mux.HandleFunc("DELETE /api/account/photo", s.mutating(s.guard.RequireAuth(s.handleAvatarDelete)))
		// Who is using the application: administrative, as it is everybody's
		// activity.
		mux.HandleFunc("GET /api/presence", s.requirePermission(auth.ActionManageUsers, s.handlePresence))

		// Notifications on a person's own devices, delivered while the page is
		// closed (pushroutes.go). Every route is the caller's own devices.
		mux.HandleFunc("GET /api/push/config", s.requirePermission(auth.ActionNotifications, s.handlePushConfig))
		mux.HandleFunc("GET /api/push/devices", s.requirePermission(auth.ActionNotifications, s.handlePushDevices))
		mux.HandleFunc("POST /api/push/devices", s.mutating(s.requirePermission(auth.ActionNotifications, s.handlePushSubscribe)))
		mux.HandleFunc("DELETE /api/push/devices/{id}", s.mutating(s.requirePermission(auth.ActionNotifications, s.handlePushUnsubscribeID)))
		mux.HandleFunc("POST /api/push/unsubscribe", s.mutating(s.requirePermission(auth.ActionNotifications, s.handlePushUnsubscribeEndpoint)))
		mux.HandleFunc("POST /api/push/test", s.mutating(s.requirePermission(auth.ActionNotifications, s.handlePushTest)))
		mux.HandleFunc("GET /api/push/recent", s.requirePermission(auth.ActionNotifications, s.handlePushRecent))
		mux.HandleFunc("GET /api/push/topics", s.requirePermission(auth.ActionNotifications, s.handlePushTopics))
		mux.HandleFunc("POST /api/push/topics", s.mutating(s.requirePermission(auth.ActionNotifications, s.handleSetPushTopics)))
		mux.HandleFunc("POST /api/push/quiet", s.mutating(s.requirePermission(auth.ActionNotifications, s.handleSetPushQuiet)))
		// The sender's queue and failures: administrators only.
		mux.HandleFunc("GET /api/push/status", s.requirePermission(auth.ActionManageSettings, s.handlePushStatus))

		// Latency per route, background jobs and the process's vitals
		// (metricsroutes.go). Administrators only.
		mux.HandleFunc("GET /api/admin/metrics", s.requirePermission(auth.ActionManageSettings, s.handleMetrics))
		// The audit trail. Administrators only: it names who did what.
		mux.HandleFunc("GET /api/audit", s.requirePermission(auth.ActionManageUsers, s.handleAuditList))

		// The permission matrix: reading the catalogue and writing somebody
		// else's grants are both administrative acts.
		mux.HandleFunc("GET /api/permissions",
			s.requirePermission(auth.ActionManageUsers, s.handlePermissionCatalogue))
		mux.HandleFunc("GET /api/permissions/user",
			s.requirePermission(auth.ActionManageUsers, s.handleUserPermissions))
		mux.HandleFunc("PUT /api/permissions/user",
			s.mutating(s.requirePermission(auth.ActionManageUsers, s.handleSetUserPermissions)))
		mux.HandleFunc("PUT /api/users/profile", s.mutating(s.requirePermission(auth.ActionManageUsers, s.handleUserProfile)))
		// What a role gets by default, edited (roletemplates.go), and roles of
		// one's own.
		mux.HandleFunc("PUT /api/permissions/role",
			s.mutating(s.requirePermission(auth.ActionManageUsers, s.handleSaveRoleTemplate)))
		mux.HandleFunc("POST /api/permissions/roles",
			s.mutating(s.requirePermission(auth.ActionManageUsers, s.handleCreateRole)))
		mux.HandleFunc("PATCH /api/permissions/roles",
			s.mutating(s.requirePermission(auth.ActionManageUsers, s.handleRenameRole)))
		mux.HandleFunc("DELETE /api/permissions/roles",
			s.mutating(s.requirePermission(auth.ActionManageUsers, s.handleDeleteRole)))
	}
}

// securityHeaders sets the response headers a browser needs in order to defend
// this application on its own.
//
// None of these were present before, which an audit against a running instance
// confirmed. The one with immediate practical consequence is framing: without
// it, another site can load /settings in an invisible frame and lure an
// administrator into clicking the controls on it. The rest are depth -- the
// output encoding is what actually stops script injection here, and the policy
// is the second line for the day something slips through it.
// permissionsPolicy: what this site's own pages may ask the browser for (the
// microphone, the camera, sharing the screen, location), and everything
// else off.
const permissionsPolicy = "camera=(self), microphone=(self), display-capture=(self), geolocation=(self), payment=(), usb=(), " +
	"serial=(), hid=(), bluetooth=(), midi=(), interest-cohort=(), browsing-topics=()"

func securityHeaders(trustProxy bool, next http.Handler) http.Handler {
	// The baseline: no inline script at all. The embedded pages, which do carry
	// inline script, replace this with a policy naming the hash of each of
	// their own scripts -- see csp.go and handleAsset. Anything else served
	// from here has no script in it and gets the strict form.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", cspDefault)
		// No API response may be stored by anything between here and the
		// reader. They are per-account by definition -- /api/me is literally
		// "who am I" -- and they went out with no Cache-Control at all and a
		// Vary naming only Accept-Encoding. A shared cache, which is exactly
		// what an office network is apt to have in front of it, could hold one
		// person's answer and hand it to the next person who asked.
		//
		// Set here rather than in each handler because there are sixty-three
		// of them and the one that gets forgotten is the one that matters.
		// no-store rather than Vary: Cookie -- the honest instruction is not
		// "key this by who asked" but "do not keep this".
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store, private")
			h.Set("Pragma", "no-cache")
			h.Add("Vary", "Cookie")
		}
		// frame-ancestors covers modern browsers; this covers the rest.
		h.Set("X-Frame-Options", "DENY")
		// Stops a browser second-guessing the type of a served document and
		// running it as HTML, which matters because the archive holds files
		// this application did not create.
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		// Every powerful feature this application does not use, switched off,
		// so an injected script or a framed page cannot ask for it either.
		// The microphone is used -- the assistant's voice button and the voice
		// conversation -- so this site's own pages may ask for it, and nothing
		// framed or from elsewhere may. It was "()" here, which quietly refused
		// every microphone the application asked for. The camera and screen
		// capture likewise, for the messenger's round video messages and its
		// calls: "camera=()" refused every video message from 6.2.0 to 6.5.1.
		// Location too: the messenger sends a point or a live location (phase
		// 11); the browser still asks the person each time.
		h.Set("Permissions-Policy", permissionsPolicy)
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		// Only over HTTPS: sending HSTS on a plain connection is ignored by
		// browsers, and setting it before the certificate is in place would
		// lock people out the moment it were honoured. IsTLS also counts a
		// connection the trusted front terminated (X-Forwarded-Proto: https) --
		// otherwise, once a CDN terminates TLS and the origin sees plain
		// HTTP, r.TLS is always nil and HSTS would never be sent at all.
		if auth.IsTLS(r, trustProxy) {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// guard checks the token, which the UI sends on every call.
//
// An empty token disables the check. That is a deliberate escape hatch for
// machines where copying a changing token each launch is more friction than the
// protection is worth: the listener is loopback-only either way, so what it
// gives up is protection from *other processes on the same machine*, not from
// the network.
func (s *Server) guardToken(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.token == "" {
			next(w, r)
			return
		}
		got := r.URL.Query().Get("t")
		if got == "" {
			got = r.Header.Get("X-Token")
		}
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

// handleFavicon serves the app's own icon for the browser tab shown when
// WebView2 is missing and the app falls back to an ordinary browser window;
// same-origin, unauthenticated, same reasoning as handleUI.
// An icon is requested on every page load and never changes within a build,
// so it is the one asset most worth answering with a 304. It is not gzipped:
// handleAsset only compresses text, and an .ico is already binary.
func (s *Server) handleFavicon() http.HandlerFunc {
	return s.handleAsset(faviconICO, "image/x-icon")
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// HTML escaping stays on. It was once turned off "to keep Persian
	// readable", which was simply untrue -- the flag only governs < > and &,
	// and non-ASCII is emitted as UTF-8 either way. All it did was let a
	// literal </script> out of the encoder, which is exactly the sequence that
	// matters if this JSON is ever placed inside a page rather than fetched.
	enc := json.NewEncoder(w)
	_ = enc.Encode(v)
}

// writeJSONStatus is writeJSON with a status code, for failures that carry a
// sentence the page should show the reader rather than a bare status. The
// header must be written before the body, or Go sends 200 and the page treats
// the failure as a result.
func writeJSONStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
