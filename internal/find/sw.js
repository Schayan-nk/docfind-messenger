// Service worker for the installed app.
//
// Scope is deliberately narrow. This caches the application shell -- the pages
// and the vendored scripts listed below -- so the app opens on a phone with no
// signal instead of showing a browser error page. It never caches an API
// response or a document: those are business data behind a login, and a copy
// sitting in a phone's cache directory is a copy outside the access control
// this application exists to enforce.
//
// It also owns the notifications. See the notificationclick handler at the
// foot of this file for why that has to be here rather than on the page.
//
// Bump CACHE when the shell changes; the activate handler drops older ones.
//
// v6: nav.js, app.css and offline.html all changed in the release that added
// the update notice. Network-first hides most of the cost of forgetting -- a
// reachable server always wins -- but it is exactly the reader with no server
// who would have been left holding the old shell, which is the one case this
// cache exists for.
//
// Only the shell's own addresses are kept, and by path alone. Before, the
// worker kept whatever page was opened, query string and all, so the
// cache's list of keys was a record of what the last person looked at. The name below is replaced with
// the build version when served (version.go), so the release carrying this
// drops those caches as it activates.
const CACHE = 'docfind-shell-v6';

// The messenger's notifications (chatsw.js): one per chat with a count, and
// «پاسخ» and «خوانده شد» on it. A server without the messenger answers 404,
// which must not stop this worker installing.
try { importScripts('/chatsw.js'); } catch (_) { }

// The page shown when a navigation fails and nothing in the cache matches.
//
// Before this, the fallback was the home page: somebody offline who asked for
// one page silently got another, with no indication that anything had gone
// wrong -- and then watched its panels fail one by one as their requests
// died. A page that says "you are offline" is a smaller
// thing than a page pretending to be somewhere else.
const OFFLINE = '/offline';

const SHELL = [
  '/',
  '/login',
  '/messages',
  '/settings',
  OFFLINE,
  // The shared stylesheet and navigation: without these the offline shell
  // renders as unstyled markup, which looks broken rather than offline.
  '/app.css',
  '/nav.js',
  '/icon.png',
  '/favicon.ico',
  '/manifest.webmanifest',
];

// Everything else the pages are made of -- every script, style, font and icon
// this build serves -- written in by the server as it serves this file
// (stampWorker in version.go), and kept at install. Without it a phone whose
// network stalled could open no more than the bare pages: on 2026-10-19 an
// iPhone's connections stopped getting through for six minutes and the app
// showed nothing at all, because the scripts had never been kept.
const PRECACHE = [];

self.addEventListener('install', event => {
  event.waitUntil(
    caches.open(CACHE)
      // addAll fails the whole install if any single entry 404s, which would
      // leave the app with no worker at all; each is added independently so
      // one missing file cannot cost the rest. A page that answered with a
      // redirect -- installed while signed out, it is the sign-in page -- is
      // not kept under the page's name.
      .then(c => Promise.all(SHELL.concat(PRECACHE).map(url => fetch(url)
        .then(res => (res.ok && !res.redirected) ? c.put(url, res) : null)
        .catch(() => {}))))
  );
  // NOTE: no skipWaiting() here, and that is the point.
  //
  // It used to be called unconditionally, which meant a newly installed worker
  // seized control of every open page the instant it finished installing. The
  // page in front of somebody was then old HTML being served by a new worker --
  // and that is not a theoretical pairing. A release tightened the Content-
  // Security-Policy, the already-open reports page kept its old markup, the new
  // policy refused to run that markup's inline script, and it sat on
  // "در حال بارگذاری گزارش‌ها…" until somebody was told to reload.
  //
  // So the new worker now WAITS. It has installed, it has its caches, and it
  // does nothing until the page asks it to take over -- which the page only
  // does when somebody presses the button on the update notice. See the message
  // handler below, and the matching half in nav.js.
  //
  // The cost of this is honest and worth stating: an update that nobody accepts
  // is an update that does not happen. Somebody who ignores the notice stays on
  // the old version until they close every tab of the application. That is the
  // trade -- no surprise swaps underneath people, in exchange for updates that
  // wait to be wanted.
});

// The page asking this worker to take over.
//
// The only way a waiting worker ever activates, other than every tab closing.
// It is sent by nav.js when somebody presses "بارگذاری دوباره" on the update
// notice, so the swap happens at a moment they chose, immediately before the
// reload that brings the matching HTML down.
self.addEventListener('message', event => {
  if (event.data && event.data.type === 'SKIP_WAITING') self.skipWaiting();
});

// clients.claim() stays, even though skipWaiting() went.
//
// The two are easy to confuse and do different jobs. skipWaiting decides
// WHETHER a new worker displaces the old one; claim decides whether, once a
// worker is active, it takes charge of pages that loaded before it existed.
//
// Dropping claim as well would only hurt the FIRST visit: the page that
// installed the worker would finish its life uncontrolled, so that first visit
// gets no offline shell and no notifications until a reload. There is nothing
// to be surprised by there -- no older worker is being displaced, because there
// is no older worker.
self.addEventListener('activate', event => {
  event.waitUntil(
    caches.keys()
      .then(keys => Promise.all(keys.filter(k => k !== CACHE).map(k => caches.delete(k))))
      .then(() => self.clients.claim())
  );
});

// The shell's own addresses and the files they are made of, by path. Only
// these are ever put in the cache.
const SHELL_PATHS = new Set(SHELL.concat(PRECACHE));

// This worker's build, read off its own cache name, and what a response says
// it is (X-Docfind-Build). A page left open across a release fetches the new
// build's files through the older worker; kept, they would mix two releases in
// the copy a stalled network falls back on, and a module from one with a
// module from the other is a page that does not start.
const BUILD = CACHE.slice('docfind-shell-'.length);

// How long the network gets before this release's own copy answers instead,
// and how long after that everything comes from the copy at once: a page is
// dozens of files, and waiting WAIT for each in turn would take a minute to
// open what the copy opens in a second.
const WAIT = 3000, SLOW = 30000;
let slowUntil = 0;

self.addEventListener('fetch', event => {
  const req = event.request;
  const url = new URL(req.url);
  if (url.origin !== self.location.origin) return;

  // Signing out empties the cache as the request goes by -- untouched, it
  // still goes to the server. The server's answer carries Clear-Site-Data,
  // which does this and more; this is for a browser that ignores it.
  if (req.method === 'POST' && url.pathname === '/api/logout') {
    event.waitUntil(caches.keys().then(ks => Promise.all(ks.map(k => caches.delete(k)))).catch(() => {}));
    return;
  }
  if (req.method !== 'GET') return;

  // Never touch the API or document bytes. Serving a stale report or a
  // document to someone whose access was just revoked is exactly the failure
  // this app must not have, and a cached 401 would be worse still.
  if (url.pathname.startsWith('/api/')) return;

  // Network first, so a reachable server always wins and a release shows at
  // once -- but the network gets WAIT, not for ever. A connection that opens
  // and then hangs (a filtered or failing link) used to leave the app on a
  // blank screen for as long as the phone kept trying; past WAIT this
  // release's own copy answers, and the page connects by itself once the
  // network is back.
  //
  // KEPT ONLY IF IT IS THE SHELL, stored under its path with no query, never
  // when the server said no-store, and only if it is this release's. Anything
  // else is fetched and passed through untouched: the cache must hold the
  // application, not a list of which customer, report or case somebody opened.
  const shell = SHELL_PATHS.has(url.pathname);
  event.respondWith((async () => {
    const network = fetch(req).then(res => {
      const noStore = /no-store/i.test((res && res.headers.get('Cache-Control')) || '');
      const ours = !!res && res.headers.get('X-Docfind-Build') === BUILD;
      if (shell && ours && !noStore && res && res.ok && !res.redirected && res.type === 'basic') {
        const copy = res.clone();
        caches.open(CACHE).then(c => c.put(url.pathname, copy)).catch(() => {});
      }
      return res;
    });
    const kept = shell ? await caches.match(url.pathname, { ignoreSearch: true }) : null;
    if (kept) {
      if (Date.now() < slowUntil) { network.catch(() => {}); return kept; }
      const first = await Promise.race([
        network.then(res => res, () => null),
        new Promise(done => setTimeout(done, WAIT, 'late')),
      ]);
      // A server error is no better than no answer while a copy exists.
      if (first && first !== 'late' && first.status < 500) return first;
      // Slow or gone (not merely an error): the rest of this page from the copy.
      if (first === 'late' || first === null) slowUntil = Date.now() + SLOW;
      network.catch(() => {});
      return kept;
    }
    try {
      return await network;
    } catch (_) {
      // Only a NAVIGATION gets the offline page. A stylesheet or a script
      // that is missing from the cache must fail as itself: handing it an
      // HTML document would have the browser parse markup as CSS, and the
      // page would render unstyled with no clue why.
      if (req.mode === 'navigate') {
        const off = await caches.match(OFFLINE);
        if (off) return off;
      }
      return Response.error();
    }
  })());
});

// ============================================================ notifications

// Pressing a notification.
//
// This has to live in the service worker and not on the page, and the reason
// is the whole point of the feature: a notification is read when the
// application is NOT in front of somebody. By then the tab that created it may
// be in the background, on another screen, or gone entirely -- and a click
// handler attached to a page that no longer exists runs nowhere. The worker
// outlives every page it serves, so it is the only thing that can still be
// listening.
//
// An existing window is focused rather than a new one opened. Somebody who
// already has the application open on their calendar does not want a second
// copy of it; they want the one they have, brought forward and pointed at the
// thing the notification was about.
self.addEventListener('notificationclick', event => {
  if (self.chatClick && self.chatClick(event)) return;
  event.notification.close();

  // A press on "بستن" is a dismissal, not a destination. Without this the
  // action button would fall through and open the app, which is the opposite
  // of what somebody pressing "close" asked for.
  if (event.action === 'dismiss') return;

  const target = (event.notification.data && event.notification.data.url) || '/';
  const wanted = new URL(target, self.location.origin);

  event.waitUntil((async () => {
    const windows = await self.clients.matchAll({
      type: 'window',
      // Windows still loading count too, or a press during start-up opens a
      // second copy.
      includeUncontrolled: true,
    });

    for (const client of windows) {
      const here = new URL(client.url);
      if (here.origin !== wanted.origin) continue;
      // Same page: focus it, and move it to the list the notification was
      // about. navigate() would reload and lose whatever was on screen, so
      // the hash is handed over as a message and the page acts on it.
      if (here.pathname === wanted.pathname) {
        if (wanted.hash && client.postMessage) {
          client.postMessage({ type: 'notification-open', hash: wanted.hash });
        }
        if (client.focus) return client.focus();
        return;
      }
    }
    // Nothing open, or nothing on the right page: open it.
    if (self.clients.openWindow) {
      return self.clients.openWindow(wanted.href);
    }
  })());
});

// A notification the reader dismissed rather than pressed.
//
// Nothing to do, and that is deliberate rather than missing: dismissing is not
// a decision about the case, so it must not mark anything read. The handler
// exists so the next person reading this file does not wonder whether one was
// forgotten.
self.addEventListener('notificationclose', () => {});

// Web push: notifications the SERVER sends, which arrive with docfind closed.
//
// This used to be dormant -- the application polled from the page instead, so
// nothing arrived until somebody opened it. The server now watches each
// person's cartable and calendar itself and sends through the device's own push
// service (Apple, Microsoft, Google, Mozilla), which wakes this worker.
//
// THE PAYLOAD IS DECLARATIVE WEB PUSH: {"web_push": 8030, "notification": {...}}.
// On an iPhone with iOS 18.4 or later the system shows it straight from that
// JSON without this code running at all -- which is the point: iOS gives a
// woken worker very little time, and a worker that misses its window used to
// mean a lost notification. Everywhere else the same JSON arrives here and is
// shown by the code below, from the same fields. The older flat shape
// ({title, body, url}) is still read, so nothing sent before this changed is
// lost.
self.addEventListener('push', event => {
  let raw = {};
  try { raw = event.data ? event.data.json() : {}; } catch (_) { raw = {}; }
  const decl = raw && raw.web_push === 8030 && raw.notification ? raw.notification : null;
  if (decl && self.chatPush && self.chatPush(event, decl)) return;
  const payload = decl ? {
    title: decl.title, body: decl.body, tag: decl.tag, silent: decl.silent,
    url: (decl.data && decl.data.url) || decl.navigate,
  } : raw;
  const title = payload.title || 'پیام‌رسان';
  event.waitUntil(self.registration.showNotification(title, {
    body: payload.body || '',
    tag: payload.tag || 'docfind',
    // Quiet hours: it arrives and is listed, but makes no sound.
    silent: !!payload.silent,
    // renotify goes WITH tag, always.
    //
    // A tag makes a second notification replace the first rather than stack,
    // which is what stops ten arrivals becoming ten rows in the shade. But
    // replacement is SILENT by default: no sound, no vibration, no banner.
    // So a second case on the same tag updated the text of a notification
    // nobody was looking at and told the reader nothing -- which is
    // indistinguishable from notifications having stopped working, and is
    // what "it only shows the first alert" looks like from the outside.
    renotify: true,
    icon: '/icon-192.png',
    badge: '/icon-192.png',
    lang: 'fa',
    dir: 'rtl',
    data: { url: payload.url || '/' },
    // Matching the page's own notifications, so a pushed one and a polled one
    // offer the same two buttons rather than behaving differently.
    actions: [
      { action: 'open',    title: 'بازکردن' },
      { action: 'dismiss', title: 'بستن' },
    ],
  }));
});

// The browser replaced this device's subscription on its own.
//
// Browsers do this -- a key rotation, a profile sync, a reinstall of the push
// component -- and the old endpoint then answers 410 Gone, which makes the
// server delete the device. Without this handler notifications would simply
// stop one day, with nobody having switched them off. So the worker subscribes
// again with the same server key and tells the server the new address, using
// the session cookie the browser already holds.
self.addEventListener('pushsubscriptionchange', event => {
  event.waitUntil((async () => {
    try {
      let sub = event.newSubscription;
      if (!sub) {
        const opts = event.oldSubscription && event.oldSubscription.options;
        if (!opts || !opts.applicationServerKey) return;
        sub = await self.registration.pushManager.subscribe({
          userVisibleOnly: true,
          applicationServerKey: opts.applicationServerKey,
        });
      }
      const j = sub.toJSON();
      await fetch('/api/push/devices', {
        method: 'POST',
        credentials: 'same-origin',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ endpoint: j.endpoint, keys: j.keys }),
      });
    } catch (_) {
      // Nothing to show and nobody to show it to. The next time the page is
      // opened, the settings screen notices the device is not registered.
    }
  })());
});
