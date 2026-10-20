// Mobile navigation, shared by every page.
//
// The toolbar carries a brand, a search field, an action or two, three
// destinations, the signed-in name and a sign-out control. That fits a
// desktop and does not fit a phone: left alone it wrapped into four cramped
// rows of small targets. Below 820px the destinations collapse behind one
// button and open as a sheet.
//
// Built here rather than written into each page three times over, so the
// menu is the same everywhere and a new destination is added in one place.
(function () {
  'use strict';

  // Per-person display settings.
  //
  // Applied from localStorage first and reconciled with the server after,
  // which is the only ordering that avoids a visible jump: the account is
  // fetched over the network, and waiting for it would show every page at the
  // default size for a moment and then resize it under the reader. The server
  // remains the source of truth -- it is what makes the setting follow
  // somebody to their phone -- and the copy in localStorage is a cache of the
  // last known value for this browser.
  const PREFS_KEY = 'docfind.prefs';

  function applyPrefs(p) {
    if (!p) return;
    const root = document.documentElement;
    const scale = Number(p.font_scale);
    // Clamped here as well as on the server: this also runs on whatever is in
    // localStorage, which the person whose browser it is can edit.
    const z = Number.isFinite(scale) && scale >= 85 && scale <= 150 ? scale / 100 : 1;
    root.style.setProperty('--ui-zoom', String(z));
    // The attribute marks the exception. Compact is the stylesheet's
    // default, so the only thing worth saying out loud is that somebody
    // asked for the roomier scale.
    if (p.density === 'comfortable') root.setAttribute('data-density', 'comfortable');
    else root.removeAttribute('data-density');
    if (p.reduce_motion) root.setAttribute('data-motion', 'reduce');
    else root.removeAttribute('data-motion');
  }

  // Exposed so the settings page can apply a change the moment it is made,
  // and so every page can reconcile once /api/me answers.
  window.docfindPrefs = {
    apply: applyPrefs,
    read() {
      try { return JSON.parse(localStorage.getItem(PREFS_KEY) || 'null'); }
      catch (_) { return null; }
    },
    write(p) {
      try { localStorage.setItem(PREFS_KEY, JSON.stringify(p || {})); } catch (_) {}
      applyPrefs(p);
    },
  };

  // Before first paint, from the cache. Wrapped because a browser with site
  // data blocked throws on the read rather than returning null.
  try { applyPrefs(window.docfindPrefs.read()); } catch (_) {}

  // Back to the top, on every page.
  //
  // Built here for the same reason the menu is: the org chart is a screen and
  // a half tall, the analysis page is far longer, and a report can run to a
  // hundred rows -- every one of them needed this, and none of them should
  // carry its own copy.
  //
  // Hidden until there is something to scroll back from. A control that does
  // nothing is worse than no control: it invites a press and teaches the
  // reader that buttons here do not respond.
  function mountTopButton() {
    if (document.querySelector('.totop')) return;
    const b = document.createElement('button');
    b.type = 'button';
    b.className = 'totop';
    b.setAttribute('aria-label', 'بازگشت به بالای صفحه');
    b.title = 'بازگشت به بالای صفحه';
    b.innerHTML = '<svg aria-hidden="true"><use href="#i-arrow-right"/></svg>';
    b.addEventListener('click', () => {
      // Honours the reader's motion preference: an instant jump for anybody
      // who has asked not to be animated.
      const reduce = window.matchMedia('(prefers-reduced-motion: reduce)').matches;
      window.scrollTo({ top: 0, behavior: reduce ? 'auto' : 'smooth' });
      // The tall panes on this site scroll inside themselves, so the window
      // being at the top is not the same as the reader being at the top.
      // The org chart is not among them any more: it is a fixed window with a
      // transform inside it and has no scroll offset to reset.
      document.querySelectorAll('.table-scroll, .scroll').forEach(el => {
        el.scrollTo({ top: 0, behavior: reduce ? 'auto' : 'smooth' });
      });
    });
    document.body.appendChild(b);

    const show = () => {
      const inner = [...document.querySelectorAll('.table-scroll, .scroll')]
        .some(el => el.scrollTop > 260);
      b.classList.toggle('on', window.scrollY > 260 || inner);
    };
    show();
    // passive: this runs on every scroll frame and must never be able to
    // block the scroll it is watching.
    window.addEventListener('scroll', show, { passive: true });
    // The inner panes are created and destroyed as pages render, so the
    // listener goes on the document in the capture phase -- scroll does not
    // bubble, and re-binding on every render would leak handlers.
    document.addEventListener('scroll', show, { passive: true, capture: true });
  }

  // Publishes the header's measured height as --appbar-h, which is what the
  // sticky breadcrumb offsets itself by.
  //
  // Measured rather than assumed: the bar wraps to two rows below the
  // breakpoint, and a hard-coded offset would leave the crumb overlapping it
  // on a phone and floating below a gap on a desktop. ResizeObserver rather
  // than a resize listener, because the bar also changes height when its
  // contents change -- a long user name wrapping is not a window resize.
  function trackAppbarHeight() {
    const bar = document.querySelector('.appbar');
    if (!bar) return;
    const set = () => document.documentElement.style.setProperty(
      '--appbar-h', Math.round(bar.getBoundingClientRect().height) + 'px');
    set();
    if (typeof ResizeObserver === 'function') {
      new ResizeObserver(set).observe(bar);
    } else {
      window.addEventListener('resize', set);
    }
  }

  // PostgreSQL's failure level (pglevel.go): nothing while it is up; a quiet
  // strip while docfind runs from its local copies and refuses some changes;
  // a loud one once that has lasted -- EMERGENCY OFFLINE MODE. Asked once a
  // minute; a page with no session gets a 401 and shows nothing.
  function watchPGLevel() {
    let bar = null;
    const paint = s => {
      const level = (s && s.pg_level) || 1;
      const down = (s && s.degraded) || [];
      if (level < 2 && !down.length) { if (bar) bar.hidden = true; return; }
      if (!bar) {
        bar = document.createElement('div');
        bar.setAttribute('role', 'alert');
        // In the page, under the header, scrolling with it. It used to stick
        // to the top above everything: it pushed the header down and, once
        // scrolled, lay over the header and the menu that opens from it.
        bar.style.cssText = 'position:relative;padding:.35rem 1rem;font-size:13px;line-height:1.7;' +
          'text-align:center;font-weight:600;color:#fff';
        const head = document.querySelector('.appbar');
        if (head) head.after(bar); else document.body.prepend(bar);
      }
      bar.hidden = false;
      // The status bar's other half (R1-5): some other service is away.
      // Named, with what people get instead on hover -- the database's own
      // levels below take precedence when it is the one that is down.
      if (level < 2) {
        bar.style.background = '#5b5f66';
        bar.title = down.map(d => d.label + ': ' + (d.fallback || '')).join('\n');
        bar.textContent = 'در دسترس نیست: ' + down.map(d => d.label).join('، ') +
          '؛ برخی امکانات موقتاً از نسخه‌های ذخیره‌شده کار می‌کند.';
        return;
      }
      bar.title = '';
      const loud = level >= 3;
      bar.style.background = loud ? '#b3261e' : '#7a5c00';
      bar.textContent = loud
        ? 'حالت اضطراری آفلاین: پایگاه‌داده مدتی است در دسترس نیست. پیام‌هایی که روی همین دستگاه مانده خواندنی است؛ فرستادن پیام و تغییر حساب‌ها، دسترسی‌ها و تنظیمات تا برگشتن آن ممکن نیست.'
        : 'پایگاه‌داده موقتاً در دسترس نیست؛ برنامه از نسخه‌های محلی کار می‌کند و برخی تغییرات تا برگشتن آن ممکن نیست.';
    };
    const ask = () => fetch('/api/db-state', { credentials: 'same-origin', cache: 'no-store' })
      .then(r => (r.ok ? r.json() : null)).then(paint).catch(() => {});
    ask();
    setInterval(ask, 60000);
  }

  function start() {
    mountTopButton();
    trackAppbarHeight();
    watchPGLevel();
    // Before the account arrives. buildNav runs this again once it has built
    // the bar and the sheet; this call is for a page that ships its own bar,
    // which would otherwise show whatever the markup marked until /api/me
    // answered.
    markActive();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', start, { once: true });
  } else {
    start();
  }

  // Every destination in the application, with a line saying what is there.
  // A menu that only lists words makes people guess; a menu that says what
  // each place is for answers the question they actually have.
  //
  // A group with one visible child collapses into that child. Otherwise an
  // account given one page of a subject meets a heading that opens to reveal a
  // single entry.
  //
  // icon is a sprite symbol name. It used to be raw path data repeated here;
  // the paths were hand-copied approximations of the same icon family the
  // rest of the app draws, and keeping four of them in sync by hand is how an
  // icon set drifts.
  //
  // pic is the entry's picture in the desk mega menu (meganav.js), a file in
  // static/mega/ (SOURCES.txt says where each came from); a group shows its
  // first entry's until one is pointed at.
  const GROUPS = [
    // The messenger: chats with colleagues, voice and video calls.
    {
      name: 'پیام‌رسان', icon: 'message-square',
      desc: 'گفت‌وگو و تماس با همکاران',
      items: [
        { href: '/messages', name: 'پیام‌ها', desc: 'گفت‌وگوی متنی، تماس صوتی و تصویری با همکاران؛ پیام‌های ذخیره‌شده برای یادداشت',
          icon: 'message-square', pic: 'messages', perm: 'page:chat' },
      ],
    },

    // Everyone's account, security and notifications; for administrators also
    // the users and what each may do.
    {
      name: 'تنظیمات', icon: 'settings',
      desc: 'حساب، امنیت، اعلان‌ها، کاربران و دسترسی‌ها',
      items: [
        { href: '/settings', name: 'تنظیمات', desc: 'حساب، امنیت، اعلان‌ها، کاربران و دسترسی‌ها',
          icon: 'settings', pic: 'settings' },
      ],
    },
  ];

  // The flat list, derived rather than written twice.
  //
  // markActive and the desktop bar both want every destination in one array,
  // and a second hand-maintained copy is a second thing to forget to update.
  const DESTINATIONS = GROUPS.flatMap(g => g.solo ? [g] : g.items);

  // A person's own arrangement (Settings, «نمایش»; internal/auth/menulayout.go):
  // their order and their own groups, over the entries this account may open.
  // Only an arrangement: an href that is not an entry above, or one this
  // account may not open, is not drawn -- and an entry it does not mention (a
  // page added since it was saved) keeps its own place after it, so a new page
  // is never hidden by an old arrangement.
  //
  // Pages a person added under names of their own (asked for on 1405/07/09)
  // are entries like these, drawn where the account may open the page they
  // are on -- the entry above with the same path -- and placed after the
  // rest until the person places them.
  function ownPages(o) {
    const list = o.account && o.account.prefs && Array.isArray(o.account.prefs.menu_pages) ? o.account.prefs.menu_pages : [];
    const out = [];
    for (const p of list) {
      const href = String(p.href || '');
      if (!/^\/(?!\/)[^\s"'<>\\`]*$/.test(href)) continue;
      const path = href.split(/[?#]/)[0];
      const base = DESTINATIONS.find(d => d.href === path) || DESTINATIONS.find(d => d.href.split(/[?#]/)[0] === path);
      if (!base) continue;
      out.push({ href, name: String(p.name || '').replace(/[<>&"'`]/g, ''), desc: 'صفحه‌ای که خودتان افزوده‌اید',
        icon: 'star', needs: base.needs, perm: base.perm });
    }
    return out;
  }
  // The entries a person took out of their own menu with − (asked for on
  // 1405/07/20; internal/auth/menulayout.go). Gone from the menu and the
  // phone's «همه» sheet, still found by the search, and put back with ＋.
  function hiddenHrefs(o) {
    const h = o.account && o.account.prefs && Array.isArray(o.account.prefs.menu_hidden) ? o.account.prefs.menu_hidden : [];
    return new Set(h.map(String));
  }
  function menuGroups(o, allowedAll) {
    const hidden = hiddenHrefs(o);
    const allowed = d => allowedAll(d) && !hidden.has(d.href);
    const layout = o.account && o.account.prefs && Array.isArray(o.account.prefs.menu) ? o.account.prefs.menu : [];
    const mine = ownPages(o);
    if (!layout.length) {
      return GROUPS.map(g => {
        if (g.solo) return allowed(g) ? g : null;
        const items = g.items.filter(allowed);
        return items.length ? Object.assign({}, g, { items }) : null;
      }).filter(Boolean).concat(mine.filter(allowed).map(d => Object.assign({}, d, { solo: true })));
    }
    // The menu's own entries win over a page of one's own at the same address.
    const byHref = new Map(mine.concat(DESTINATIONS).map(d => [d.href, d]));
    const used = new Set();
    const take = h => {
      const d = byHref.get(h);
      if (!d || used.has(h) || !allowed(d)) return null;
      used.add(h);
      return d;
    };
    const clean = s => String(s || '').replace(/[<>&"'`]/g, '');
    const out = [];
    for (const n of layout) {
      if (n.href) {
        const d = take(n.href);
        if (d) out.push(Object.assign({}, d, { solo: true }));
        continue;
      }
      const items = (n.items || []).map(take).filter(Boolean);
      if (items.length) {
        out.push({ name: clean(n.name), icon: /^[a-z0-9-]+$/.test(n.icon || '') ? n.icon : 'list-checks',
          desc: clean(items.map(d => d.short || d.name).join('، ')), items, own: true });
      }
    }
    for (const g of GROUPS) {
      if (g.solo) {
        const d = take(g.href);
        if (d) out.push(d);
        continue;
      }
      const items = g.items.map(d => take(d.href)).filter(Boolean);
      if (items.length) out.push(Object.assign({}, g, { items }));
    }
    for (const p of mine) {
      const d = take(p.href);
      if (d) out.push(Object.assign({}, d, { solo: true }));
    }
    return out;
  }
  // For the arrangement card in Settings: the menu as it comes, and what this
  // account may open, so it offers nothing the menu would not draw.
  window.docfindMenuModel = { GROUPS, allowed: () => true };

  // Takes a sprite symbol name. The sprite is inlined into every page by the
  // server, so this is a same-document reference and works in every engine --
  // an external <use href="sprite.svg#id"> does not render in WebKit, and this
  // app is installed as a PWA on an iPhone.
  const svg = name =>
    `<svg class="ic" aria-hidden="true"><use href="#i-${name}"/></svg>`;

  // Everything interpolated below is a constant from the table above except
  // the display name, which is account data and therefore escaped. Kept as a
  // named function rather than an inline replace so it is obvious at the call
  // site that it happens.
  const esc = s => String(s ?? '').replace(/[&<>"']/g,
    c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));

  let sheet = null, scrim = null, toggle = null, lastFocus = null;

  // ------------------------------------------------------- pinning the menu
  //
  // A sheet that has to be summoned and dismissed for every move costs two
  // presses per navigation, which is the wrong trade for somebody at a desk
  // all day. Pinned, the drawer stays open as a column and the page gives up
  // the width for it -- see the pinned block in app.css, which is also where
  // the decision lives that this only applies above 1100px.
  //
  // Remembered per browser rather than per account. It is a statement about
  // this screen -- pinning a 380px column is right on a monitor and wrong on
  // a laptop -- and the same person moving between the two wants a different
  // answer on each, which a server-side preference could not give them.
  const PIN_KEY = 'docfind.nav.pinned';

  function pinned() {
    try { return localStorage.getItem(PIN_KEY) === '1'; } catch (_) { return false; }
  }

  function applyPin(on) {
    document.body.classList.toggle('nav-pinned', !!on);
    const btn = document.querySelector('.navpin');
    if (btn) {
      btn.setAttribute('aria-pressed', on ? 'true' : 'false');
      const label = on ? 'آزاد کردن فهرست' : 'سنجاق کردن فهرست';
      btn.setAttribute('aria-label', label);
      btn.title = label;
    }
    // A pinned drawer and an open sheet are the same panel in two states, and
    // leaving menu-open set would keep the page's scroll locked behind a
    // drawer that is now part of the layout.
    if (on) document.body.classList.remove('menu-open');
  }

  function setPin(on) {
    try { localStorage.setItem(PIN_KEY, on ? '1' : '0'); } catch (_) {}
    applyPin(on);
  }

  // Before the drawer exists, so the page is not laid out full-width for a
  // frame and then narrowed under the reader. The class is all the CSS needs;
  // the button catches up when buildNav runs.
  if (pinned()) {
    if (document.body) document.body.classList.add('nav-pinned');
    else document.addEventListener('DOMContentLoaded',
      () => document.body.classList.add('nav-pinned'), { once: true });
  }

  function close() {
    document.body.classList.remove('menu-open');
    if (toggle) toggle.setAttribute('aria-expanded', 'false');
    // inert as well as aria-hidden. aria-hidden takes the sheet out of the
    // accessibility tree but leaves its links FOCUSABLE, so Tab walked a
    // keyboard user through a closed drawer full of invisible destinations --
    // which is the one thing aria-hidden on a focusable container is
    // guaranteed to do wrong, and what Lighthouse flags as aria-hidden-focus.
    // inert removes them from the tab order and from hit testing together.
    if (sheet) { sheet.setAttribute('aria-hidden', 'true'); sheet.inert = true; }
    // A filter typed last time would hide most of the menu when it reopens.
    const q = sheet && sheet.querySelector('#navQ');
    if (q && q.value) { q.value = ''; q.dispatchEvent(new Event('input')); }
    // Focus goes back where it came from, or a keyboard user is left at the
    // top of the document with no idea where they are.
    if (lastFocus) { lastFocus.focus(); lastFocus = null; }
  }

  function open() {
    lastFocus = document.activeElement;
    document.body.classList.add('menu-open');
    toggle.setAttribute('aria-expanded', 'true');
    sheet.setAttribute('aria-hidden', 'false');
    sheet.inert = false;
    const first = sheet.querySelector('a, button');
    if (first) first.focus();
  }

  // While the sheet is open it is the only thing on screen, so Tab has to
  // stay inside it -- otherwise focus walks invisibly through the page behind.
  function trapFocus(e) {
    if (e.key !== 'Tab' || !document.body.classList.contains('menu-open')) return;
    const items = [...sheet.querySelectorAll('a, button')].filter(el => el.offsetParent !== null);
    if (!items.length) return;
    const first = items[0], last = items[items.length - 1];
    if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus(); }
    else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus(); }
  }

  // opts: { reports: bool, admin: bool, user: string|null, onSignOut: fn }
  // ----------------------------------------------------------- loading
  //
  // The animation itself is pure CSS and SVG (see .ld in app.css) and needs
  // nothing from here. What this adds is *when* to show it.
  //
  // Nothing appears for the first 300 ms. Most of what this covers -- a
  // search, a cached report -- comes back in single-digit milliseconds, and a
  // loader that flashes up and vanishes reads as a glitch, not as progress.
  // Past 300 ms the wait is long enough that silence reads as a hang.
  //
  // Counted rather than toggled: a report and a search can be in flight at
  // once, and the first to finish must not take the overlay away from the
  // other.
  // The markup comes from the <template id="ldTpl"> the server injects into
  // every page, cloned on demand. There is deliberately no copy of it here:
  // the animation is 1.2 KB of SVG, and a second copy in this file is a second
  // thing to keep in step with app.css.
  const ldClone = () => {
    const t = document.getElementById('ldTpl');
    return t && 'content' in t ? t.content.cloneNode(true) : null;
  };

  // ---- the thought orb ----------------------------------------------------
  //
  // A dotted orb, drawn on a 2D canvas by the thinking-orbs engine vendored
  // at /vendor/thinking-orbs.js (MIT; see the header of that file).
  //
  // IT REPLACES A WEBGL SHADER that lived here -- a sphere raymarched in a
  // fragment shader and lit with a fresnel rim. That shader was good and
  // there are two reasons it is gone anyway.
  //
  // The first is reach. It needed a WebGL context, and where there was none
  // the loader silently fell back to the CSS orbit; this needs a 2D context,
  // which every engine this application runs on has. The animation that
  // people actually see is now the same animation everywhere.
  //
  // The second is that the orb says more. The shader had one state -- busy.
  // This engine has nine, so the chat can show "composing" while it writes
  // and the loader can show "working", and the two read as different
  // activities rather than as the same spinner in two places.
  //
  // WHAT WAS LOST, recorded honestly: the shader took the brand accent from
  // --accent and this engine does not. Its dots are rgba(M,M,M,a) -- one
  // value across all three channels, shaded by depth -- so the orb is
  // monochrome by design and the loader is no longer orange.
  //
  // The upgrade pattern is unchanged: the CSS orbit stays in the markup and
  // is hidden only once an orb is actually running. No 2D context, a reader
  // who asked for less motion, or an engine that never arrives, and the
  // orbit is what they get.

  // ORB_STATE names the engine's nine states this application uses. Kept as
  // names rather than scattered string literals so a typo is one wrong orb
  // rather than a silent fallback -- resolvePreset accepts anything and
  // quietly returns its default for a state it does not know.
  const ORB_STATE = { loading: 'working', writing: 'composing' };

  // The engine is fetched on FIRST USE, not on every page load. It is 21 KB
  // that only matters once something is actually waiting, and the loader does
  // not appear for the first 300 ms anyway -- by which time this has usually
  // arrived. Until it does, the CSS orbit is on screen doing the job.
  let orbEngine = null;
  function loadOrbEngine() {
    if (orbEngine) return orbEngine;
    orbEngine = new Promise(resolve => {
      if (window.ThinkingOrbs) { resolve(window.ThinkingOrbs); return; }
      const s = document.createElement('script');
      s.src = '/vendor/thinking-orbs.js';
      s.async = true;
      // resolve(null) rather than reject on failure: a loader animation is
      // not worth an unhandled rejection in the console of a page that is
      // otherwise working, and every caller already copes with no orb.
      s.onload = () => resolve(window.ThinkingOrbs || null);
      s.onerror = () => resolve(null);
      document.head.appendChild(s);
    });
    return orbEngine;
  }

  // orbDark decides which palette a canvas should use, by looking at what is
  // actually behind it.
  //
  // Not a media query and not a fixed true: this application is dark chrome
  // with CREAM PANELS on the home page, so the same loader component appears
  // on both grounds depending on where it was dropped. Walking up for the
  // first painted background and measuring it answers the question for the
  // place the orb is standing, which is the only place that matters.
  function orbDark(node) {
    for (let n = node; n && n.nodeType === 1; n = n.parentElement) {
      const bg = getComputedStyle(n).backgroundColor || '';
      const m = /rgba?\(\s*([\d.]+)[,\s]+([\d.]+)[,\s]+([\d.]+)(?:[,\s/]+([\d.]+))?/i.exec(bg);
      if (!m) continue;
      if (m[4] !== undefined && parseFloat(m[4]) < 0.25) continue; // see-through: keep looking
      const lum = (0.2126 * +m[1] + 0.7152 * +m[2] + 0.0722 * +m[3]) / 255;
      return lum < 0.5;
    }
    return true; // nothing painted anywhere above: the app's own ground is dark
  }

  // startOrb drives one canvas. Returns a stop function.
  //
  // A port of the engine's own React component, which is where the rAF loop,
  // the device-pixel scaling and the pausing live in the published package.
  // None of that is React-specific and none of it is in the engine bundle.
  function startOrb(cv, state, opt) {
    const o = opt || {};
    let stopped = false, raf = 0, running = false;

    loadOrbEngine().then(TO => {
      if (stopped || !TO || !cv.isConnected) return;
      const ctx = cv.getContext && cv.getContext('2d');
      if (!ctx) return; // no 2D context: the CSS orbit stays

      // RESOLVE AT 20 OR 64 AND NOTHING ELSE.
      //
      // resolvePreset looks the size up in a table and THROWS on a miss --
      // measured across a dozen values, 20 and 64 are the only two that
      // return. They are the package's two documented presets, 20 for an
      // inline mark and 64 for an avatar-sized one, and they carry different
      // particle counts rather than being one drawing scaled.
      //
      // So the CSS box picks the nearer of the two rather than being passed
      // through. The chat's orb is 30px and gets the 20 preset: handing it
      // the 64 one would draw a crowd of particles tuned for a circle twice
      // the area, which reads as a smudge at that size.
      //
      // Wrapped because a throw here happens inside a promise and would
      // surface as an unhandled rejection on a page that is otherwise fine,
      // with the loader silently missing.
      const box = cv.clientWidth || o.size || 64;
      let preset = null;
      try {
        preset = TO.resolvePreset(state, box < 42 ? 20 : 64);
      } catch (e) { return; }
      const draw = preset && TO.MODE_DRAWS[preset.mode];
      if (!draw) return;
      const speed = (preset.speed || 1) * (o.speed || 1);
      const dark = o.dark !== undefined ? o.dark : orbDark(cv);

      // Capped at 2: a loader is decoration over work that is still running,
      // and there is no reading to be done on a dotted sphere that a third
      // device pixel would improve.
      const dpr = Math.min(window.devicePixelRatio || 1, 2);
      let css = 0;
      function frame(tSec) {
        // Sized from the CSS box every frame, because this canvas is laid out
        // by the stylesheet and the box changes with the viewport. Cheap: the
        // assignment only happens when the number actually moved, and setting
        // width on a canvas clears it.
        const w = Math.max(1, Math.round(cv.clientWidth || o.size || 64));
        if (w !== css) {
          css = w;
          cv.width = Math.round(w * dpr);
          cv.height = Math.round(w * dpr);
        }
        ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
        ctx.clearRect(0, 0, css, css);
        draw(ctx, css, tSec, dark, preset.opts);
      }

      // One frame regardless, so a paused or offscreen orb is still a picture
      // rather than a blank square.
      frame((performance.now() / 1000) * speed);
      cv.hidden = false;
      if (cv.parentElement && cv.parentElement.classList) {
        cv.parentElement.classList.add('has-blob');
      }
      if (o.root && o.root.classList) o.root.classList.add('has-blob');

      if (window.matchMedia && matchMedia('(prefers-reduced-motion: reduce)').matches) {
        return; // the single frame above is the whole animation
      }

      function loop() {
        // Stop the moment the node leaves the page. This loader has been left
        // running at the foot of a page before, and an animation nobody can
        // see is a flat battery.
        if (stopped || !cv.isConnected) { running = false; return; }
        frame((performance.now() / 1000) * speed);
        raf = requestAnimationFrame(loop);
      }
      function start() {
        if (running || stopped) return;
        running = true;
        raf = requestAnimationFrame(loop);
      }
      function halt() { running = false; if (raf) cancelAnimationFrame(raf); }

      // Paused while offscreen or while the tab is hidden. Both matter on a
      // phone, and neither is something the reader can see being saved.
      let onScreen = true;
      let io = null;
      if (typeof IntersectionObserver !== 'undefined') {
        io = new IntersectionObserver(es => {
          onScreen = es[es.length - 1].isIntersecting;
          if (onScreen && document.visibilityState !== 'hidden') start(); else halt();
        });
        io.observe(cv);
      } else {
        start();
      }
      const onVis = () => {
        if (document.visibilityState === 'hidden') halt();
        else if (onScreen) start();
      };
      document.addEventListener('visibilitychange', onVis);

      cv._orbStop = () => {
        halt();
        if (io) io.disconnect();
        document.removeEventListener('visibilitychange', onVis);
      };
      if (stopped) cv._orbStop();
    });

    return function stop() {
      stopped = true;
      if (cv._orbStop) { cv._orbStop(); cv._orbStop = null; }
      else if (raf) cancelAnimationFrame(raf);
    };
  }
  window.startOrb = startOrb;
  window.ORB_STATE = ORB_STATE;

  // startBlob upgrades one loader node, and keeps its name because every
  // caller in this application already uses it -- chat.html, loaderNode and
  // ldShow. What is underneath is an orb now rather than a shader.
  function startBlob(root) {
    const cv = root && root.querySelector && root.querySelector('.ld-blob');
    if (!cv) return null;
    return startOrb(cv, ORB_STATE.loading, { root: root });
  }
  window.startBlob = startBlob;

  // Exposed so a page can drop the loader into a panel of its own rather than
  // over the whole screen. The blob starts itself on whatever is handed back,
  // so a caller gets the upgrade without knowing it exists.
  window.loaderNode = function () {
    const frag = ldClone();
    if (!frag) return null;
    // The .ld element survives the append, so it is held here and upgraded a
    // tick later. It has to be a tick later: a canvas inside a fragment has no
    // layout box, and one sized from a zero box draws nothing at all.
    const host = frag.querySelector('.ld');
    if (host) setTimeout(() => { if (host.isConnected) startBlob(host); }, 0);
    return frag;
  };

  const LD_DELAY = 300;
  let ldDepth = 0, ldTimer = null, ldEl = null, ldBlobStop = null;

  function ldShow() {
    if (!ldEl) {
      const art = ldClone();
      if (!art) return; // no template on this page: show nothing rather than throw
      ldEl = document.createElement('div');
      ldEl.className = 'ld-over';
      // aria-busy rather than an alert: this is a state, not an announcement.
      ldEl.setAttribute('aria-busy', 'true');
      ldEl.appendChild(art);
      document.body.appendChild(ldEl);
      // Started only now that the node is in the page and has a box. The
      // overlay is built once and reused, so this runs once too.
      const host = ldEl.querySelector('.ld');
      if (host) ldBlobStop = startBlob(host);
    }
    ldEl.hidden = false;
  }

  // busy(promise) shows the loader if the promise takes longer than 300 ms,
  // and always takes it away again -- including when the promise rejects,
  // which is exactly when an overlay left behind would strand the page.
  window.busy = function busy(work) {
    ldDepth++;
    if (ldTimer === null) ldTimer = setTimeout(() => { ldTimer = null; ldShow(); }, LD_DELAY);

    const done = () => {
      ldDepth = Math.max(0, ldDepth - 1);
      if (ldDepth > 0) return;
      if (ldTimer !== null) { clearTimeout(ldTimer); ldTimer = null; }
      if (ldEl) ldEl.hidden = true;
    };
    return Promise.resolve(work).then(
      v => { done(); return v; },
      e => { done(); throw e; }
    );
  };

  // ------------------------------------------------------------- toast
  //
  // Shared by every page, because it previously lived in ui.html alone: the
  // Persian pages had no toast and the evaluation page used a native alert()
  // instead -- a modal OS dialog captioned with the server's address, which
  // also blocks the page until it is dismissed.
  //
  // The second argument accepts either a boolean, as ui.html has always
  // passed, or a kind: 'ok', 'bad', 'info'.
  // Sprite symbol names, same set as everywhere else.
  const TOAST_ICONS = { ok: 'check', bad: 'alert-triangle', info: 'info' };
  let toastTimer = null;

  // A third argument, {action, run}, adds one button -- an undo, usually --
  // and keeps the toast up twice as long, since deciding to press it takes
  // longer than reading it.
  window.toast = function toast(msg, kind, undo) {
    if (typeof kind !== 'string') kind = kind ? 'bad' : 'ok';
    if (!TOAST_ICONS[kind]) kind = 'ok';

    // Remove the toast already on screen. This used to look for '.dx-toast',
    // a class nothing ever set, so the selector matched nothing -- and since
    // the pending dismissal was cleared just below, the previous toast was
    // left on screen for good, with the new one drawn on top of it.
    document.querySelector('.toast')?.remove();
    clearTimeout(toastTimer);

    const el = document.createElement('div');
    el.className = 'toast rise toast-' + kind;
    // Announced to a screen reader: an error that only appears visually is an
    // error somebody working by keyboard never learns about.
    el.setAttribute('role', kind === 'bad' ? 'alert' : 'status');
    el.setAttribute('aria-live', kind === 'bad' ? 'assertive' : 'polite');

    const ic = document.createElement('div');
    ic.className = 'ic';
    // TOAST_ICONS holds sprite names; svg() builds the reference. The kind was
    // whitelisted above, so this cannot interpolate anything from a message.
    ic.innerHTML = svg(TOAST_ICONS[kind]);

    const text = document.createElement('div');
    text.textContent = msg;   // textContent, never innerHTML: this carries server text

    el.append(ic, text);
    const hasAct = !!(undo && undo.action && typeof undo.run === 'function');
    if (hasAct) {
      const b = document.createElement('button');
      b.type = 'button';
      b.className = 'toast-act';
      b.textContent = undo.action;
      b.addEventListener('click', () => { undo.run(); dismissToast(el); });
      el.appendChild(b);
    }
    document.body.appendChild(el);
    toastTimer = setTimeout(() => dismissToast(el), hasAct ? 8000 : 4000);
    return el;
  };

  function dismissToast(el) {
    if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) { el.remove(); return; }
    el.classList.add('toast-leave');
    el.addEventListener('animationend', () => el.remove(), { once: true });
  }

  // ---------------------------------------------------------- user menu
  //
  // The signed-in person's picture in the header, opening onto the handful of
  // things they change often.
  //
  // Built here rather than in each page for the reason the rest of this file
  // exists: four pages carry an app bar, and four copies of a menu is four
  // places for it to drift. The plain name and the sign-out button it replaces
  // are hidden rather than removed, so a page that never calls this still has
  // a way out.
  //
  // What goes in it is decided by how often somebody needs it, not by what
  // exists. The type size is here because it is the setting people reach for
  // repeatedly and it is two clicks away otherwise; the picture, the password
  // and the session list live on the settings page and are linked, because
  // they are things you do once.

  // Does this destination describe where we are?
  //
  // Compared against path + hash, which is the only way to tell a page's
  // hash routes apart: they are one document as far as the pathname is
  // concerned. A destination carrying a hash must match it exactly; one
  // without matches on the path, and then only if no hash route claimed it
  // first.
  function destMatch(href, path, hash) {
    const cut = href.indexOf('#');
    if (cut >= 0) return path === href.slice(0, cut) && hash === href.slice(cut);
    if (href === '/') return path === '/';
    return path === href || path.startsWith(href + '/');
  }

  // The winner: a hash route beats the page that hosts it, and among the rest
  // the longest matching path wins.
  function activeHref(hrefs) {
    const path = location.pathname, hash = location.hash;
    let best = '', bestScore = -1;
    for (const h of hrefs) {
      if (!destMatch(h, path, hash)) continue;
      const score = (h.includes('#') ? 1000 : 0) + h.length;
      if (score > bestScore) { best = h; bestScore = score; }
    }
    return best;
  }

  // Applied to both navigations at once -- the bar and the phone sheet -- so
  // the two cannot disagree about where the reader is.
  function markActive() {
    const links = [...document.querySelectorAll('.navlinks a[href], .navsheet a.dest[href]')];
    if (!links.length) return;
    const win = activeHref(links.map(a => a.getAttribute('href')));
    for (const a of links) {
      const on = win && a.getAttribute('href') === win;
      a.classList.toggle('navhere', !!on && !a.classList.contains('dest'));
      a.classList.toggle('on', !!on && a.classList.contains('dest'));
      if (on) a.setAttribute('aria-current', 'page');
      else a.removeAttribute('aria-current');
    }
  }

  // A destination can be a hash route, so leaving this to page
  // load would mark the wrong one for as long as the reader stayed on the
  // page. A highlight that has stopped being true is worse than none: it is
  // the one part of the bar somebody trusts without reading it.
  window.addEventListener('hashchange', markActive);

  // ------------------------------------------------------------ the palette
  //
  // Ctrl+K (Cmd+K) on every page: one box that goes anywhere and finds
  // anything the reader may see -- pages, messages, and "what is down
  // right now". Every search goes through the API the pages themselves use,
  // so it can only find what this account is allowed to.
  //
  // The key is matched by its position (KeyK), not its letter: on a Persian
  // layout the same key types «ن», and a shortcut that only worked in
  // English would not work for most of the people using this.
  let palVisible = null;
  let palOpts = {};
  let palItems = [];
  let palActive = 0;
  let palSeq = 0;
  let palTimer = null;

  const palFold = s => String(s || '').toLowerCase()
    .replace(/ي/g, 'ی').replace(/ك/g, 'ک').replace(/‌/g, ' ').replace(/\s+/g, ' ').trim();

  const PAL_COMMANDS = [
    { title: 'وضعیت سرویس‌ها', sub: 'کدام سرویس از کار افتاده و چه چیزی فعلاً در دسترس نیست',
      words: ['وضعیت', 'سرویس', 'status', 'service', 'قطع', 'down', 'سلامت'], run: () => palStatus() },
  ];

  function paletteReady(bar, visible, o) {
    palVisible = visible;
    palOpts = o || {};
    useShortcuts(palOpts.account);
    if (bar && !bar.querySelector('.cmdk-btn')) {
      const b = document.createElement('button');
      b.type = 'button';
      b.className = 'cmdk-btn';
      b.setAttribute('aria-label', 'جست‌وجو و فرمان (Ctrl+K)');
      b.innerHTML = svg('search') + '<span class="cmdk-btn-label">جست‌وجو</span><kbd>Ctrl K</kbd>';
      b.addEventListener('click', openPalette);
      bar.appendChild(b);
    }
  }

  document.addEventListener('keydown', e => {
    if ((e.ctrlKey || e.metaKey) && !e.altKey && e.code === 'KeyK') {
      if (!palVisible) return;
      e.preventDefault();
      openPalette();
    }
  });

  function palDialog() {
    let dlg = document.getElementById('cmdk');
    if (dlg) return dlg;
    dlg = document.createElement('dialog');
    dlg.id = 'cmdk';
    dlg.className = 'cmdk';
    dlg.setAttribute('aria-label', 'جست‌وجو و فرمان');
    dlg.innerHTML =
      '<div class="cmdk-top">' + svg('search') +
      '<input id="cmdkQ" type="search" autocomplete="off" spellcheck="false" role="combobox" ' +
      'aria-expanded="true" aria-controls="cmdkList" aria-autocomplete="list" ' +
      'aria-label="جست‌وجو یا فرمان" placeholder="متن پیام، نام صفحه، یا «وضعیت سرویس‌ها»…"></div>' +
      '<div id="cmdkList" class="cmdk-list" role="listbox" aria-label="نتیجه‌ها"></div>' +
      '<div class="cmdk-foot"><span>↑ ↓ جابه‌جایی</span><span>Enter باز کردن</span><span>Esc بستن</span></div>';
    document.body.appendChild(dlg);
    const q = dlg.querySelector('#cmdkQ');
    q.addEventListener('input', () => palSearch(q.value));
    q.addEventListener('keydown', e => {
      if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
        e.preventDefault();
        if (!palItems.length) return;
        palActive = (palActive + (e.key === 'ArrowDown' ? 1 : -1) + palItems.length) % palItems.length;
        palMark();
      } else if (e.key === 'Enter') {
        e.preventDefault();
        if (palItems[palActive]) palGo(palItems[palActive]);
      }
    });
    dlg.addEventListener('click', e => { if (e.target === dlg) dlg.close(); });
    return dlg;
  }

  // initial: words to start with -- the menu's search box hands its text on.
  // A click handler passes an Event here, which is not a string and so starts
  // empty as before.
  function openPalette(initial) {
    const dlg = palDialog();
    const q = dlg.querySelector('#cmdkQ');
    q.value = typeof initial === 'string' ? initial : '';
    palSearch(q.value);
    if (!dlg.open) dlg.showModal();
    q.focus();
  }
  window.openPalette = openPalette;

  function palGo(it) {
    if (it.run) { it.run(); return; }
    document.getElementById('cmdk').close();
    location.href = it.href;
  }

  // The local part: pages and commands, answered at once; the searches run
  // after a pause in typing and replace only their own groups.
  function palSearch(raw) {
    const q = raw.trim();
    const f = palFold(q);
    const seq = ++palSeq;
    const local = [];
    for (const c of PAL_COMMANDS) {
      if (!f || c.words.some(w => w.includes(f) || f.includes(w)) || palFold(c.title).includes(f)) {
        local.push(Object.assign({ group: 'فرمان‌ها' }, c));
      }
    }
    for (const d of palVisible || []) {
      const hay = palFold([d.name, d.short, d.desc].join(' '));
      if (!f || hay.includes(f)) {
        local.push({ group: 'صفحه‌ها', title: d.name, sub: d.desc || '', href: d.href });
      }
    }
    if (!f) local.splice(9);
    palItems = local;
    palActive = 0;
    palRender();
    clearTimeout(palTimer);
    if (q.length < 2) return;
    palTimer = setTimeout(() => palRemote(q, seq), 180);
  }

  async function palJSON(url) {
    try {
      const r = await fetch(url, { credentials: 'same-origin' });
      return r.ok ? await r.json() : null;
    } catch (_) { return null; }
  }

  // Messages are the one thing here worth asking the server for: the same
  // search as the box above the conversation list (chatsearch.js), and a
  // result opens its conversation (#c<id>, read by messages.js).
  async function palRemote(q, seq) {
    const r = await palJSON('/api/chat/search?limit=6&q=' + encodeURIComponent(q));
    if (seq !== palSeq) return; // a later keystroke has already moved on
    const add = [];
    for (const m of (r && r.messages) || []) {
      const text = String(m.text || '').replace(/\s+/g, ' ').trim();
      if (!text) continue;
      let when = '';
      try {
        when = new Date(m.at).toLocaleString('fa-IR', { day: 'numeric', month: 'long', hour: '2-digit', minute: '2-digit' });
      } catch (_) {}
      add.push({ group: 'پیام‌ها', title: text.length > 90 ? text.slice(0, 90) + '…' : text,
        sub: [m.from_name, when].filter(Boolean).join(' · '), href: '/messages#c' + m.chat });
    }
    palItems = palItems.concat(add);
    palRender();
  }

  const PAL_ORDER = ['فرمان‌ها', 'صفحه‌ها', 'پیام‌ها'];

  function palRender() {
    const list = document.getElementById('cmdkList');
    if (!list) return;
    palItems.sort((a, b) => PAL_ORDER.indexOf(a.group) - PAL_ORDER.indexOf(b.group));
    list.textContent = '';
    if (!palItems.length) {
      const p = document.createElement('p');
      p.className = 'cmdk-empty';
      p.textContent = 'چیزی پیدا نشد. بخشی از متن پیام یا نام صفحه را امتحان کنید.';
      list.appendChild(p);
      return;
    }
    let group = '';
    palItems.forEach((it, i) => {
      if (it.group !== group) {
        group = it.group;
        const h = document.createElement('div');
        h.className = 'cmdk-group';
        h.textContent = group;
        list.appendChild(h);
      }
      const o = document.createElement('div');
      o.className = 'cmdk-item';
      o.id = 'cmdk-o-' + i;
      o.setAttribute('role', 'option');
      const t = document.createElement('b');
      t.textContent = it.title;
      o.appendChild(t);
      if (it.sub) {
        const s = document.createElement('span');
        s.textContent = it.sub;
        o.appendChild(s);
      }
      o.addEventListener('click', () => palGo(it));
      o.addEventListener('mousemove', () => { if (palActive !== i) { palActive = i; palMark(); } });
      list.appendChild(o);
    });
    palMark();
  }

  function palMark() {
    document.querySelectorAll('#cmdkList .cmdk-item').forEach((o, i) => {
      o.setAttribute('aria-selected', String(i === palActive));
      if (i === palActive) {
        o.scrollIntoView({ block: 'nearest' });
        document.getElementById('cmdkQ').setAttribute('aria-activedescendant', o.id);
      }
    });
  }

  // "What is down": the same answer the status strip gives, in words, with
  // what still works.
  async function palStatus() {
    const list = document.getElementById('cmdkList');
    list.textContent = '';
    let d = null, missing = false;
    try {
      const r = await fetch('/api/db-state', { credentials: 'same-origin' });
      if (r.ok) d = await r.json(); else missing = r.status === 404;
    } catch (_) { /* no connection: said below */ }
    const box = document.createElement('div');
    box.className = 'cmdk-status';
    const line = (text, cls) => { const p = document.createElement('p'); if (cls) p.className = cls; p.textContent = text; box.appendChild(p); };
    if (!d) {
      line(missing
        ? 'این نسخه (بدون حساب کاربری) وضعیت سرویس‌ها را نگه نمی‌دارد.'
        : 'وضعیت خوانده نشد؛ ارتباط با سرور برقرار نیست.', missing ? 'muted' : 'bad');
    } else {
      const down = d.degraded || [];
      // pg_level is a number: 1 up, 2 briefly down, 3 emergency offline.
      const pg = Number(d.pg_level) || 1;
      if (pg > 1) line('پایگاه دادهٔ اصلی: ' + (pg >= 3 ? 'حالت اضطراری آفلاین' : 'موقتاً در دسترس نیست'), 'bad');
      if (!down.length && pg < 2) line('✓ همهٔ سرویس‌ها در دسترس‌اند.', 'ok');
      for (const x of down) {
        line((x.state === 'down' ? '● ' : '◐ ') + (x.label || x.name) + (x.fallback ? '؛ ' + x.fallback : ''), x.state === 'down' ? 'bad' : 'warn');
      }
      if ((d.capabilities_off || []).length) line('فعلاً در دسترس نیست: ' + d.capabilities_off.join('، '), 'muted');
    }
    list.appendChild(box);
    palItems = [];
    palActive = 0;
  }

  // ------------------------------------------------ the dock and shortcuts
  //
  // On a phone or a touch tablet the navigation is the glass dock
  // (glassnav.js): home, documents, mail, the assistant and "more", with a
  // round search button beside it. This hands it the same permission-checked
  // destinations the drawer is built from, so the dock can never offer a
  // place the menu would not -- and then loads it. The stylesheet decides
  // where the dock shows; on a desk the drawer is the navigation instead.
  //
  // The script's address is written in double quotes so the server stamps
  // it with the build (cachecontrol.go), which makes it an immutable cached
  // file: on every page after the first the dock is there without a request.
  function mountGlassNav(visible, o, allowed) {
    if (!o.account || window.DocfindNav) return;
    const groups = menuGroups(o, allowed);
    window.DocfindNav = {
      visible, groups, opts: o, openPalette,
      openDrawer: () => { if (toggle) toggle.click(); },
    };
    const s = document.createElement('script');
    s.src = "/glassnav.js";
    s.async = true;
    document.head.appendChild(s);
  }

  // Shortcuts that work on every page. Never while typing: a letter in a
  // text box is a letter. Keys a page already uses are left to it.
  const GOTO = { m: '/messages', t: '/settings' };
  // A person's own shortcuts (Settings, «نمایش»; internal/auth/shortcuts.go):
  // "alt+x" and "g x", read from the account once the menu is built.
  let userAlt = {}, userGo = {}, userList = [];
  function useShortcuts(account) {
    userAlt = {}; userGo = {};
    userList = (account && account.prefs && account.prefs.shortcuts) || [];
    for (const s of userList) {
      const m = /^alt\+([a-z0-9])$/.exec(s.keys) || [];
      const g = /^g ([a-z])$/.exec(s.keys) || [];
      if (m[1]) userAlt[m[1]] = s.href;
      if (g[1]) userGo[g[1]] = s.href;
    }
  }
  window.docfindUseShortcuts = useShortcuts; // the settings card, after a save
  let gAt = 0;
  document.addEventListener('keydown', e => {
    // Alt + a letter or digit, by the key's position so a Persian layout
    // works too. Taken even inside a field: Alt is not typing.
    if (e.altKey && !e.ctrlKey && !e.metaKey && !e.shiftKey && palVisible) {
      const k = e.code.startsWith('Key') ? e.code.slice(3).toLowerCase() : e.code.startsWith('Digit') ? e.code.slice(5) : '';
      if (k && userAlt[k]) {
        e.preventDefault();
        e.stopImmediatePropagation();
        location.href = userAlt[k];
      }
      return;
    }
    if (e.ctrlKey || e.metaKey || e.altKey || !palVisible) return;
    const t = e.target;
    if (t && (t.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName))) return;
    if (document.querySelector('dialog[open]')) return;
    const key = e.code.startsWith('Key') ? e.code.slice(3).toLowerCase() : e.key;
    if (e.key === '?' || (e.shiftKey && e.code === 'Slash')) { e.preventDefault(); showShortcuts(); return; }
    if (key === 'g') { gAt = Date.now(); return; }
    if (gAt && Date.now() - gAt < 1200 && !GOTO[key] && userGo[key]) {
      // A personal one: the page it opens decides who may see it.
      gAt = 0;
      e.preventDefault();
      e.stopImmediatePropagation();
      location.href = userGo[key];
      return;
    }
    if (gAt && Date.now() - gAt < 1200 && GOTO[key]) {
      gAt = 0;
      const href = GOTO[key];
      if (palVisible.some(d => d.href === href.split('#')[0] || d.href === href)) {
        // Taken here, so a page's own key does not act on it as well on
        // the way out.
        e.preventDefault();
        e.stopImmediatePropagation();
        location.href = href;
      }
    }
  });

  // The notification centre is a file of its own (notifycenter.js), added
  // after the menu so it can never hold the page up, and only for somebody
  // signed in: an account is what notifications belong to.
  function mountBell(o) {
    if (!o.account || document.querySelector('script[data-bell]')) return;
    window.DocfindBellOpts = { admin: !!o.admin, user: o.account.username || '' };
    const s = document.createElement('script');
    s.src = "/notifycenter.js"; // stamped with the build, like the dock
    s.async = true;
    s.dataset.bell = '1';
    document.head.appendChild(s);
  }

  function showShortcuts() {
    let d = document.getElementById('kbdHelp');
    if (!d) {
      d = document.createElement('dialog');
      d.id = 'kbdHelp';
      d.className = 'kbd-help';
      d.setAttribute('aria-label', 'میان‌بُرهای صفحه‌کلید');
      // Only the jumps this account can take.
      const names = { '/messages': 'پیام‌ها', '/settings': 'تنظیمات' };
      const rows = [
        ['Ctrl + K', 'جست‌وجو و فرمان در هر صفحه'],
        ['?', 'همین راهنما'],
        ['Esc', 'بستن پنجره یا پنل کناری'],
      ].concat(Object.entries(GOTO).filter(([, href]) => (palVisible || []).some(v => v.href === href))
        .map(([k, href]) => ['g سپس ' + k, names[href] || href]))
        .concat(userList.map(s => [s.keys.replace(/^alt\+/, 'Alt + ').replace(/^g /, 'g سپس '),
          (s.label || s.href) + ' (میان‌بر شما)']));
      d.innerHTML = '<h2>میان‌بُرهای صفحه‌کلید</h2><table></table><button type="button" class="btn">بستن</button>';
      const tb = d.querySelector('table');
      for (const [k, v] of rows) {
        const tr = document.createElement('tr');
        const a = document.createElement('td'); a.innerHTML = '<kbd></kbd>'; a.firstChild.textContent = k;
        const b = document.createElement('td'); b.textContent = v;
        tr.append(a, b);
        tb.appendChild(tr);
      }
      d.querySelector('button').addEventListener('click', () => d.close());
      document.body.appendChild(d);
    }
    d.showModal();
  }

  // ------------------------------------------------ the connection indicator
  //
  // Whether THIS device can reach the server, said in the header on every
  // page: a quiet pill that reads «آفلاین» while it cannot, and a toast when
  // it can again. The status bar says which of the server's own services are
  // down; this says the step before that, the one between the reader and the
  // server, which no server-side check can see.
  //
  // navigator.onLine alone is not enough -- it is true on a wifi whose VPN has
  // dropped -- so a request that fails with a network error also counts, and
  // while offline the same probe offline.html uses is tried every five
  // seconds until one answers: /api/me, because the service worker never
  // answers anything under /api/ from its cache (sw.js) -- a cached file
  // would "answer" with the network still gone. Nothing is polled while
  // online: the page's own requests are the evidence.
  let connDown = false, connTimer = 0;
  function connPill() {
    let p = document.querySelector('.conn-pill');
    const bar = document.querySelector('.appbar');
    if (!p && bar) {
      p = document.createElement('span');
      p.className = 'conn-pill';
      p.setAttribute('role', 'status');
      p.setAttribute('aria-live', 'polite');
      p.hidden = true;
      p.innerHTML = svg('wifi-off') + '<span>آفلاین</span>';
      p.title = 'این دستگاه به سرور نمی‌رسد؛ آنچه روی صفحه است نسخهٔ آخرین بار است.';
      bar.appendChild(p);
    }
    return p;
  }
  function setConn(down) {
    if (down === connDown) return;
    connDown = down;
    document.body.classList.toggle('is-offline', down);
    const p = connPill();
    if (p) p.hidden = !down;
    clearInterval(connTimer);
    if (down) connTimer = setInterval(probeConn, 5000);
    else if (window.toast) window.toast('اتصال برقرار شد.', 'ok');
    window.dispatchEvent(new CustomEvent('conn:change', { detail: { online: !down } }));
  }
  async function probeConn() {
    try {
      // Any answer at all -- even "nobody is signed in" -- means the server
      // was reached.
      await nativeFetch('/api/me', { cache: 'no-store', credentials: 'same-origin' });
      setConn(false);
    } catch (_) { /* still down */ }
  }
  // The page's own requests are the evidence: one that throws a network
  // error (TypeError) means the server was not reached; an answer to an /api/
  // request, even an error status, means it was. Only /api/ counts as back:
  // anything else may have been answered by the service worker's cache while
  // the network is still gone. Wrapped once, and nothing else about fetch
  // changes.
  const nativeFetch = window.fetch.bind(window);
  const fromServer = input => {
    try { return new URL(typeof input === 'string' ? input : input.url, location.href).pathname.startsWith('/api/'); } catch (_) { return false; }
  };
  window.fetch = function (input, init) {
    return nativeFetch(input, init).then(r => { if (connDown && fromServer(input)) setConn(false); return r; }, err => {
      if (err && err.name === 'TypeError') {
        setConn(true);
        // Every page prints the error after its own «... خوانده نشد:», and
        // the browser's words -- "Failed to fetch", "NetworkError when
        // attempting to fetch resource" -- are English and tell the reader
        // nothing. Still a TypeError, so a check on the name holds; the
        // original rides along as the cause.
        throw new TypeError('اتصال به سرور برقرار نیست.', { cause: err });
      }
      throw err;
    });
  };
  window.addEventListener('offline', () => setConn(true));
  window.addEventListener('online', probeConn);
  if (navigator.onLine === false) {
    if (document.body) setConn(true);
    else document.addEventListener('DOMContentLoaded', () => setConn(true), { once: true });
  }
  window.docfindConn = { online: () => !connDown };

  // ------------------------------------------------ the offline outbox
  //
  // A change made while this device cannot reach the server is not lost: it
  // is kept here (at most fifty, oldest dropped) and sent when the connection
  // comes back, or the next time any page opens. Each carries an
  // Idempotency-Key from the moment it is made, so a send whose answer was
  // lost on the way back is recognised by the server (idempotency.go) and done
  // once. Kept in this browser only, and each change carries the account
  // that made it: it is sent only while that same account is signed in, and
  // dropped -- never sent as someone else -- if another signs in here after a
  // session simply ran out. Signing out clears it with everything else.
  const OUTBOX = 'docfind.outbox';
  const outboxLoad = () => { try { return JSON.parse(localStorage.getItem(OUTBOX) || '[]'); } catch (_) { return []; } };
  const outboxSave = q => { try { localStorage.setItem(OUTBOX, JSON.stringify(q.slice(-50))); } catch (_) {} };
  const newKey = () => (window.crypto && crypto.randomUUID ? crypto.randomUUID()
    : Date.now().toString(36) + Math.random().toString(36).slice(2));
  const writeInit = (method, body, key) => ({
    method, credentials: 'same-origin',
    headers: body == null ? { 'Idempotency-Key': key } : { 'Content-Type': 'application/json', 'Idempotency-Key': key },
    body: body == null ? undefined : JSON.stringify(body),
  });

  // docfindSend is fetch for a JSON write that may happen offline: an answer
  // comes back as {ok, status, data}; a write that could not be sent comes
  // back as {queued: true} and goes later.
  window.docfindSend = async function docfindSend(url, method, body, label) {
    const key = newKey();
    let r;
    try { r = await fetch(url, writeInit(method, body, key)); } catch (_) {
      const q = outboxLoad();
      let owner = '';
      try { owner = localStorage.getItem('docfind.account') || ''; } catch (_) {}
      q.push({ url, method, body: body == null ? null : body, key, label: label || 'تغییر', at: Date.now(), owner });
      outboxSave(q);
      if (window.toast) window.toast('اتصال نیست؛ «' + (label || 'تغییر') + '» نگه داشته شد و با برگشت اتصال فرستاده می‌شود.', 'info');
      return { queued: true };
    }
    return { ok: r.ok, status: r.status, data: await r.json().catch(() => ({})) };
  };

  let flushing = false;
  async function flushOutbox() {
    if (flushing || !outboxLoad().length) return;
    flushing = true;
    let sent = 0;
    try {
      // Who is signed in now. Nobody: wait for a sign-in. Somebody else:
      // their session must not carry another person's changes.
      let who = '';
      try {
        const me = await (await nativeFetch('/api/me', { cache: 'no-store', credentials: 'same-origin' })).json();
        who = me && me.user ? me.user.username : '';
      } catch (_) { return; }
      if (!who) return;
      let q = outboxLoad().filter(it => it.owner && it.owner === who);
      outboxSave(q);
      while (q.length) {
        const it = q[0];
        let r;
        try { r = await nativeFetch(it.url, writeInit(it.method, it.body, it.key)); } catch (_) { break; }
        // Not now: the server is struggling, or the session has ended and
        // the write should wait for the next sign-in rather than be dropped.
        if (r.status >= 500 || r.status === 401) break;
        q.shift();
        outboxSave(q);
        if (r.ok) sent++;
        else if (window.toast) {
          const e = await r.json().catch(() => ({}));
          window.toast('«' + it.label + '» فرستاده نشد: ' + (e.error || 'سرور آن را نپذیرفت.'), 'bad');
        }
      }
    } finally { flushing = false; }
    if (sent && window.toast) window.toast(Number(sent).toLocaleString('fa-IR') + ' تغییرِ نگه‌داشته‌شده فرستاده شد.', 'ok');
  }
  window.addEventListener('conn:change', e => { if (e.detail && e.detail.online) flushOutbox(); });
  window.addEventListener('online', () => setTimeout(flushOutbox, 1500));
  setTimeout(flushOutbox, 2500);

  // ------------------------------------------------ the next page, early
  //
  // A page the reader is about to open is fetched while the pointer rests on
  // its link or the finger comes down on it (Speculation Rules, "moderate"),
  // so it is already here when the press lands. Prefetch only, never
  // prerender: a prerendered page would run its scripts -- read the API,
  // send its vitals -- for a page that may never be opened. Only this
  // application's own pages: nothing under /api/ or /static/, no download,
  // nothing that opens elsewhere. Browsers without it lose nothing.
  try {
    if (HTMLScriptElement.supports && HTMLScriptElement.supports('speculationrules') &&
        !document.querySelector('script[type="speculationrules"]')) {
      const rules = document.createElement('script');
      rules.type = 'speculationrules';
      rules.textContent = JSON.stringify({
        prefetch: [{
          source: 'document',
          where: { and: [
            { href_matches: '/*' },
            { not: { href_matches: '/api/*' } },
            { not: { href_matches: '/static/*' } },
            { not: { selector_matches: '[download], [target="_blank"], .no-prefetch' } },
          ] },
          eagerness: 'moderate',
        }],
      });
      document.head.appendChild(rules);
    }
  } catch (_) { /* an older browser: pages load as they always did */ }

  window.markActive = markActive;

  const MENU_PREF_STEP = 5;

  function userMenuMarkup(acct) {
    const name = acct.display_name || acct.username || '';
    const roles = { admin: 'مدیر ارشد', manager: 'مدیر', viewer: 'بیننده' };
    const face = acct.has_avatar
      ? `<img class="um-face" alt="" src="/api/account/photo?u=${encodeURIComponent(acct.username)}">`
      : `<svg class="um-face um-face-ph" aria-hidden="true"><use href="#i-avatar"/></svg>`;
    return `
      <div class="um-head">
        ${face}
        <div class="um-who">
          <b>${esc(name)}</b>
          <span>${esc(roles[acct.role] || acct.role || '')}</span>
        </div>
      </div>
      <div class="um-sep"></div>
      <div class="um-row" role="group" aria-label="اندازه متن">
        <span class="um-label">${svg('type')}اندازه متن</span>
        <div class="um-steps">
          <button type="button" class="um-step" data-fs="-1" aria-label="کوچک‌تر">−</button>
          <output class="um-fs" id="umFs">۱۰۰٪</output>
          <button type="button" class="um-step" data-fs="1" aria-label="بزرگ‌تر">+</button>
        </div>
      </div>
      <button type="button" class="um-item" data-fs="0">
        ${svg('refresh-cw')}<span>اندازه پیش‌فرض</span>
      </button>
      <div class="um-item um-static">
        ${svg('menu')}<span>نمایش فشرده</span>
        <span class="sw sw-sm">
          <input type="checkbox" id="umDensity" data-toggle="density">
          <label for="umDensity" aria-label="نمایش فشرده"></label>
        </span>
      </div>
      <div class="um-item um-static">
        ${svg('sparkles')}<span>انیمیشن</span>
        <span class="sw sw-sm">
          <input type="checkbox" id="umMotion" data-toggle="motion">
          <label for="umMotion" aria-label="انیمیشن"></label>
        </span>
      </div>
      <div class="um-sep"></div>
      <a class="um-item" href="/settings/account">
        ${svg('settings')}<span>تنظیمات حساب</span>
      </a>
      <a class="um-item" href="/settings/security">
        ${svg('monitor')}<span>نشست‌های فعال</span>
      </a>
      <div class="um-sep"></div>
      <button type="button" class="um-item um-danger" data-signout>
        ${svg('log-out')}<span>خروج از حساب</span>
      </button>`;
  }

  function mountUserMenu(acct, onSignOut) {
    const bar = document.querySelector('.appbar');
    if (!bar || !acct || document.querySelector('.um-btn')) return;

    // The plain name and the bare sign-out button are now inside the menu.
    const who = document.getElementById('who');
    if (who) who.setAttribute('hidden', '');
    const oldOut = document.getElementById('signout');
    if (oldOut) oldOut.setAttribute('hidden', '');
    // The document browser names these differently; it predates the shared
    // bar. Hidden here too rather than left showing the name and a sign-out
    // button beside a menu that already offers both.
    const who2 = document.getElementById('navUser');
    if (who2) who2.setAttribute('hidden', '');
    const out2 = document.getElementById('navSignOut');
    if (out2) out2.setAttribute('hidden', '');

    const wrap = document.createElement('div');
    wrap.className = 'um';

    const btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'um-btn';
    btn.setAttribute('aria-haspopup', 'true');
    btn.setAttribute('aria-expanded', 'false');
    btn.setAttribute('aria-label', 'حساب کاربری: ' + (acct.display_name || acct.username || ''));
    btn.innerHTML = acct.has_avatar
      ? `<img class="um-avatar" alt="" src="/api/account/photo?u=${encodeURIComponent(acct.username)}">`
      : `<svg class="um-avatar um-avatar-ph" aria-hidden="true"><use href="#i-avatar"/></svg>`;

    const menu = document.createElement('div');
    menu.className = 'um-menu';
    menu.setAttribute('role', 'menu');
    menu.setAttribute('hidden', '');
    menu.innerHTML = userMenuMarkup(acct);

    wrap.append(btn, menu);
    bar.appendChild(wrap);

    const paint = () => {
      const p = (window.docfindPrefs && window.docfindPrefs.read()) || {};
      const scale = Number(p.font_scale) || 100;
      const out = menu.querySelector('#umFs');
      if (out) out.textContent = new Intl.NumberFormat('fa-IR').format(scale) + '٪';
      const d = menu.querySelector('#umDensity');
      if (d) d.checked = p.density === 'compact';
      // The switch is labelled "animation", so it is on when motion is
      // wanted; the stored field is the opposite of that.
      const m = menu.querySelector('#umMotion');
      if (m) m.checked = !p.reduce_motion;
    };

    // Saved a moment after the last click, so tapping + four times is one
    // request rather than four.
    let saveTimer = null;
    const savePrefs = p => {
      if (window.docfindPrefs) window.docfindPrefs.write(p);
      paint();
      clearTimeout(saveTimer);
      saveTimer = setTimeout(() => {
        fetch('/api/account/prefs', {
          method: 'POST', credentials: 'same-origin',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(p),
        }).catch(() => {});
      }, 500);
    };

    const current = () => {
      const p = (window.docfindPrefs && window.docfindPrefs.read()) || {};
      return {
        font_scale: Number(p.font_scale) || 100,
        density: p.density === 'compact' ? 'compact' : 'comfortable',
        reduce_motion: !!p.reduce_motion,
      };
    };

    let lastFocus = null;
    const isOpen = () => !menu.hasAttribute('hidden');
    const openMenu = () => {
      lastFocus = document.activeElement;
      paint();
      menu.removeAttribute('hidden');
      btn.setAttribute('aria-expanded', 'true');
      const first = menu.querySelector('button, a');
      if (first) first.focus();
    };
    const closeMenu = back => {
      menu.setAttribute('hidden', '');
      btn.setAttribute('aria-expanded', 'false');
      if (back && lastFocus) lastFocus.focus();
      lastFocus = null;
    };

    btn.addEventListener('click', e => {
      e.stopPropagation();
      if (isOpen()) closeMenu(true); else openMenu();
    });

    menu.addEventListener('click', e => {
      const step = e.target.closest('[data-fs]');
      if (step) {
        const dir = Number(step.dataset.fs);
        const p = current();
        // 0 is the reset, which is a value rather than a direction.
        p.font_scale = dir === 0
          ? 100
          : Math.max(85, Math.min(150, p.font_scale + dir * MENU_PREF_STEP));
        savePrefs(p);
        // The menu stays open: somebody adjusting a size wants to press it
        // again and see the result, not reopen the menu between presses.
        return;
      }
      // A checkbox has already flipped itself by the time this runs, so both
      // values are read back off the switches rather than inverted from the
      // stored state -- which would double-toggle and leave the switch and
      // the setting disagreeing.
      if (e.target.closest('.sw')) {
        const p = current();
        const d = menu.querySelector('#umDensity'), m = menu.querySelector('#umMotion');
        p.density = d && d.checked ? 'compact' : 'comfortable';
        p.reduce_motion = !(m && m.checked);
        savePrefs(p);
        return;
      }
      if (e.target.closest('[data-signout]')) {
        closeMenu(false);
        if (onSignOut) onSignOut();
        return;
      }
      // A link closes on its way out, so returning with the back button does
      // not land on an open menu.
      if (e.target.closest('a')) closeMenu(false);
    });

    // Outside and Escape both close. Registered on the document because the
    // menu is not modal -- the page behind it stays usable, so a click on it
    // has to dismiss rather than be swallowed by a scrim.
    document.addEventListener('click', e => {
      if (isOpen() && !wrap.contains(e.target)) closeMenu(false);
    });
    document.addEventListener('keydown', e => {
      if (e.key === 'Escape' && isOpen()) closeMenu(true);
    });

    paint();
  }

  window.mountUserMenu = mountUserMenu;

  // What this browser kept for the person signing out, dropped before they
  // go: everything in localStorage (the offline outbox among it), this
  // tab's session flags, and the offline shell's cache. The server's sign-out
  // answer says the same with Clear-Site-Data; this is for a browser that
  // ignores that header. Preferences come back from the account on the next
  // sign-in, so nothing here is lost that belongs to the browser rather than
  // to them. Bounded, like the push step, so it can never hold up leaving.
  async function forgetThisBrowser() {
    try { localStorage.clear(); } catch (_) {}
    try { sessionStorage.clear(); } catch (_) {}
    try {
      if (window.caches) {
        await Promise.race([
          caches.keys().then(ks => Promise.all(ks.map(k => caches.delete(k)))),
          new Promise(r => setTimeout(r, 1500)),
        ]);
      }
    } catch (_) {}
  }

  // bootNav gives a page the standard menu from the signed-in account, for
  // the pages with no reason of their own to read /api/me. Without it a page
  // had a title and no way anywhere else -- on a phone, installed as an app
  // with no back button, a dead end.
  window.bootNav = async function bootNav() {
    try {
      const r = await fetch('/api/me', { credentials: 'same-origin' });
      const me = await r.json();
      if (me.auth_enabled && !me.user) return;
      window.buildNav({
        admin: !!me.user && me.user.role === 'admin',
        user: me.user ? (me.user.display_name || me.user.username) : null,
        account: me.user,
        onSignOut: () => fetch('/api/logout', { method: 'POST', credentials: 'same-origin' })
          .finally(() => location.replace('/login')),
      });
    } catch (_) { /* the page still works; it only has no menu */ }
  };

  window.buildNav = function buildNav(opts) {
    const o = Object.assign({}, opts || {});
    const bar = document.querySelector('.appbar');
    if (!bar || document.querySelector('.navsheet')) return;

    // Signing out also stops THIS DEVICE receiving the account's
    // notifications. Done here, once, rather than in every page's own
    // onSignOut: a phone handed to somebody else must not go on showing its
    // previous owner's cases on the lock screen, and one page that forgot
    // would be the one it happened on. Bounded to two seconds so a slow
    // network never makes signing out hang.
    if (o.onSignOut) {
      const original = o.onSignOut;
      let leaving = false;
      o.onSignOut = async () => {
        if (leaving) return;
        leaving = true;
        try {
          if (window.DocfindPush) {
            await Promise.race([
              window.DocfindPush.forgetDevice(),
              new Promise(r => setTimeout(r, 2000)),
            ]);
          }
        } catch (_) {}
        await forgetThisBrowser();
        original();
      };
    }

    // The header menu, when the caller passed the account rather than just a
    // display name. Optional so a page that has not been updated still gets
    // the mobile sheet and keeps its own name and sign-out button.
    if (o.account) mountUserMenu(o.account, o.onSignOut);
    // Who last used this browser, for the offline outbox: a page opened
    // offline cannot ask the server, and a queued change must be sent as the
    // person who made it or not at all.
    if (o.account && o.account.username) {
      try { localStorage.setItem('docfind.account', o.account.username); } catch (_) {}
    }

    // No row of destination links in the bar any more; the sheet below is the
    // whole navigation at every width. See the comment on the toggle for why.
    // A page that ships its own row in its markup keeps it -- the document
    // browser's toolbar is in English on purpose -- so this did not have to be
    // changed on every page at once.

    // One navigation, the same at every width.
    //
    // It used to be two: a row of links on a desktop and this button on a
    // phone. That meant the menu a person learned on one device was not the
    // menu they found on the other, and the row could only ever hold the
    // names -- the sheet is where each destination gets a line saying what is
    // there. The button carries the word as well as the bars, because an icon
    // alone is a guess and this is the only way to anywhere.
    toggle = document.createElement('button');
    toggle.type = 'button';
    toggle.className = 'navtoggle';
    toggle.setAttribute('aria-expanded', 'false');
    toggle.setAttribute('aria-controls', 'navsheet');
    // The visible label below is hidden at narrow widths, which leaves this
    // button with three decorative bars and nothing a screen reader can read:
    // it announces "button". Lighthouse failed it for exactly that. The label
    // here is on the button itself, so it survives whatever the stylesheet
    // does to the span.
    toggle.setAttribute('aria-label', 'منو');
    toggle.innerHTML =
      '<span class="bars"><i class="bar"></i><i class="bar"></i><i class="bar"></i></span>' +
      '<span class="navtoggle-label">منو</span>';
    // First in the bar, which in a right-to-left page is the top-right corner
    // -- the same side the drawer opens from, so pressing it moves the panel
    // out from under your own finger rather than across the screen.
    bar.prepend(toggle);

    scrim = document.createElement('div');
    scrim.className = 'navscrim';

    // Each gated destination names what it needs, and the caller says what
    // this account has.
    const has = { admin: o.admin };
    // And each destination's own page permission, where the account says what
    // it holds: an account given only the inbox was shown the whole menu, and
    // every other entry opened a page that refused it. A page that does not
    // pass the account keeps the gates above and nothing more.
    const held = o.account && Array.isArray(o.account.permissions) ? new Set(o.account.permissions) : null;
    const allowed = d => (!d.needs || has[d.needs]) && (!d.perm || !held || held.has(d.perm));
    const visible = DESTINATIONS.filter(allowed);
    paletteReady(bar, visible, o);
    mountGlassNav(visible, o, allowed);
    mountBell(o);

    // The groups, with anything this account cannot reach removed first.
    //
    // A group left with ONE child becomes that child: a heading that opens to
    // reveal a single entry is a click for nothing.
    // A group of the person's own making stays a group even with one entry:
    // they made it so. Read again after every − and ＋ (redrawList).
    window.docfindMenuModel.allowed = allowed;
    const listGroups = () => menuGroups(o, allowed).map(g =>
      !g.solo && !g.own && g.items.length === 1 ? Object.assign({ solo: true }, g.items[0]) : g);
    // − and ＋ need somewhere to keep the choice: an account. Signed out, or a
    // page that passes only a name, the menu is drawn without them.
    const editable = !!(o.account && o.account.prefs);

    // A group is open when the page you are on is inside it, so the menu
    // never hides where you already are behind a collapsed heading.
    //
    // Matched on the PATH, ignoring the hash: a page and its hash routes are
    // one page. The root is compared exactly -- every path starts with "/",
    // so a prefix test on it would open every group at once.
    const path = location.pathname;
    const inGroup = g => !g.solo && g.items.some(d => {
      const p = d.href.split('#')[0];
      return p === '/' ? path === '/' : path.startsWith(p);
    });

    // Each entry is a row: the link, and the − that takes it out of this
    // person's menu (asked for on 1405/07/20). Beside the link rather than in
    // it -- a button inside an <a> is two controls in one, and pressing −
    // would follow the link too. Faint until its row is pointed at, like a
    // site's ✕, so the menu does not read as a column of delete buttons.
    // pic travels with the link, for the mega menu's card (meganav.js).
    const destHTML = d =>
      // No current-page marking here: markActive does it for the bar and the
      // sheet together, once both exist. This used to test
      // here.startsWith(d.href) against the pathname alone, which marked the
      // report list while the reader was on the analysis view.
      `<div class="navrow">
         <a class="dest" href="${d.href}"${d.pic ? ` data-pic="${d.pic}"` : ''}>
           <span class="t">${svg(d.icon)}${d.name}</span>
           <span class="d">${d.desc}</span>
         </a>${editable ? `
         <button type="button" class="nav-x" data-href="${esc(d.href)}" title="برداشتن از فهرست من"
                 aria-label="${esc('برداشتن «' + (d.short || d.name) + '» از فهرست')}">${svg('minus')}</button>` : ''}
       </div>`;

    const groupHTML = g => {
      if (g.solo) return destHTML(g);
      const open = inGroup(g);
      return `<div class="navgroup${open ? ' open' : ''}">
          <button type="button" class="navgroup-head" aria-expanded="${open}">
            ${svg(g.icon)}
            <span class="t">
              <b>${g.name}</b>
              <span class="d">${g.desc}</span>
            </span>
            ${svg('chevron-left')}
          </button>
          <div class="navgroup-body"><div>${g.items.map(destHTML).join('')}</div></div>
        </div>`;
    };

    sheet = document.createElement('nav');
    sheet.className = 'navsheet';
    sheet.id = 'navsheet';
    sheet.setAttribute('aria-label', 'فهرست اصلی');
    sheet.setAttribute('aria-hidden', 'true');
    // Closed at build time, so it starts out of the tab order too -- see
    // close() for why aria-hidden alone is not enough.
    sheet.inert = true;
    sheet.innerHTML =
      `<div class="navsheet-head">
         <img src="/icon.png" alt="" width="30" height="30">
         <span class="brand-name grow">پیام‌رسان</span>
         <button type="button" class="navpin" data-pin aria-pressed="false"
                 aria-label="سنجاق کردن فهرست" title="سنجاق کردن فهرست">
           ${svg('pin')}
         </button>
         <button type="button" class="btn btn-ghost btn-icon" data-close aria-label="بستن">
           ${svg('x')}
         </button>
       </div>
       <div class="navsearch" role="search">
         ${svg('search')}
         <input type="search" id="navQ" autocomplete="off" spellcheck="false"
                placeholder="جست‌وجو در فهرست؛ Enter برای همه‌جا" aria-label="جست‌وجو در فهرست">
       </div>
       <p class="navsearch-none hint" hidden>در فهرست نیست؛ Enter را بزنید تا در همهٔ سامانه جست‌وجو شود.</p>` +
      `<div class="navlist" data-navlist>${listGroups().map(groupHTML).join('')}</div>` +
      (editable ? '<div class="navadd" data-navadd></div>' : '') +
      `<div class="navsheet-foot">
         ${o.user ? `<div class="hint" style="margin:0 0 var(--s3)">وارد شده به نام <b>${esc(o.user)}</b></div>` : ''}
         ${o.onSignOut ? '<button type="button" class="btn" style="width:100%" data-signout>خروج</button>' : ''}
       </div>`;

    document.body.append(scrim, sheet);
    if (editable) mountMenuEdit();

    // − and ＋ (asked for on 1405/07/20: "add + and - beside nav items where
    // user can directly change his nav menu links"). − on any entry, in the
    // sidebar or the mega menu's panel, takes it out of this person's menu
    // (POST /api/account/menu/hidden), with an undo. ＋ at the foot of the
    // list brings back what was taken out, and adds the page you are on under
    // a name of your own (POST /api/account/menu, the same own pages Settings
    // keeps). Each change redraws the list in place, keeping the groups that
    // were open, and tells the mega menu (docfind:navchange).
    function mountMenuEdit() {
      const list = sheet.querySelector('[data-navlist]');
      const host = sheet.querySelector('[data-navadd]');
      let adding = false, problem = '';
      const prefs = o.account.prefs;
      const nameOf = d => d.short || d.name;
      async function post(url, body) {
        const r = await fetch(url, { method: 'POST', credentials: 'same-origin',
          headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
        const d = await r.json().catch(() => ({}));
        if (!r.ok) throw new Error(d.error || 'ذخیره نشد.');
        return d;
      }
      async function setHidden(next) {
        const d = await post('/api/account/menu/hidden', { hidden: next });
        prefs.menu_hidden = Array.isArray(d.hidden) ? d.hidden : [];
        redrawList();
      }
      // What ＋ can bring back: entries taken out that this account may still
      // open (a page since refused is not offered), once each.
      function takenOut() {
        const h = hiddenHrefs(o), seen = new Set();
        return ownPages(o).concat(DESTINATIONS).filter(d => {
          if (!h.has(d.href) || seen.has(d.href) || !allowed(d)) return false;
          seen.add(d.href);
          return true;
        });
      }
      // The page you are on, when the menu does not already lead here and it is
      // a page an own entry may point at (ownPages: a path the menu knows).
      function here() {
        const href = location.pathname + (location.hash && location.hash !== '#' ? location.hash : '');
        if (!/^\/(?!\/)[^\s"'<>\\`]*$/.test(href) || href.length > 200) return null;
        const path = location.pathname;
        if (!DESTINATIONS.some(d => d.href.split(/[?#]/)[0] === path)) return null;
        if (hiddenHrefs(o).has(href) || [...sheet.querySelectorAll('a.dest')].some(a => a.getAttribute('href') === href)) return null;
        const name = (document.title.split('|')[0] || '').trim().slice(0, 40);
        return { href, name };
      }
      function drawAdd(focus) {
        const back = takenOut(), page = here();
        host.textContent = '';
        const btn = document.createElement('button');
        btn.type = 'button';
        btn.className = 'navadd-btn';
        btn.setAttribute('aria-expanded', String(adding));
        btn.innerHTML = svg('plus') + '<span class="t"></span>';
        btn.querySelector('.t').textContent = 'افزودن به فهرست' +
          (back.length ? ' (' + back.length.toLocaleString('fa-IR') + ' برداشته‌شده)' : '');
        btn.addEventListener('click', () => { adding = !adding; problem = ''; drawAdd(adding); });
        host.append(btn);
        if (!adding) return;
        const body = document.createElement('div');
        body.className = 'navadd-body';
        if (page) {
          const f = document.createElement('form');
          f.className = 'navadd-here';
          f.innerHTML = '<label class="hint" for="navAddName">همین صفحه، با نامی که خودتان می‌گذارید</label>' +
            '<div class="navadd-line"><input class="input" id="navAddName" maxlength="40" autocomplete="off" required>' +
            '<button type="submit" class="btn btn-sm btn-primary">افزودن</button></div>';
          f.querySelector('input').value = page.name;
          f.addEventListener('submit', async e => {
            e.preventDefault();
            const go = f.querySelector('button');
            go.disabled = true;
            try {
              const pages = (Array.isArray(prefs.menu_pages) ? prefs.menu_pages : []).concat([{ href: page.href, name: f.querySelector('input').value.trim() }]);
              const d = await post('/api/account/menu', { menu: Array.isArray(prefs.menu) ? prefs.menu : [], pages });
              prefs.menu = d.menu || []; prefs.menu_pages = d.pages || [];
              adding = false; problem = '';
              redrawList();
              window.toast('این صفحه به فهرست شما افزوده شد.', 'ok');
            } catch (err) { problem = err.message; drawAdd(true); }
          });
          body.append(f);
        }
        for (const d of back) {
          const row = document.createElement('div');
          row.className = 'navadd-item';
          const t = document.createElement('span');
          t.innerHTML = svg(/^[a-z0-9-]+$/.test(d.icon || '') ? d.icon : 'star');
          t.append(document.createTextNode(nameOf(d)));
          const plus = document.createElement('button');
          plus.type = 'button';
          plus.className = 'nav-plus';
          plus.title = 'بازگرداندن به فهرست';
          plus.setAttribute('aria-label', 'بازگرداندن «' + nameOf(d) + '» به فهرست');
          plus.innerHTML = svg('plus');
          plus.addEventListener('click', async () => {
            plus.disabled = true;
            try { await setHidden([...hiddenHrefs(o)].filter(h => h !== d.href)); }
            catch (err) { problem = err.message; drawAdd(); }
          });
          row.append(t, plus);
          body.append(row);
        }
        if (!page && !back.length) {
          const p = document.createElement('p');
          p.className = 'hint navadd-none';
          p.textContent = 'هر موردی را که با − از فهرست بردارید، از اینجا برمی‌گردد.';
          body.append(p);
        }
        if (problem) {
          const p = document.createElement('p');
          p.className = 'navsite-err';
          p.textContent = problem;
          body.append(p);
        }
        host.append(body);
        if (focus) { const first = body.querySelector('input, button'); if (first) first.focus(); }
      }
      function redrawList() {
        const open = new Set([...list.querySelectorAll('.navgroup.open .navgroup-head b')].map(b => b.textContent));
        list.innerHTML = listGroups().map(groupHTML).join('');
        for (const g of list.querySelectorAll('.navgroup')) {
          const name = (g.querySelector('.navgroup-head b') || {}).textContent;
          if (!open.has(name)) continue;
          g.classList.add('open');
          g.querySelector('.navgroup-head').setAttribute('aria-expanded', 'true');
        }
        drawAdd();
        markActive();
        if (window.DocfindNav) window.DocfindNav.groups = menuGroups(o, allowed);
        document.dispatchEvent(new CustomEvent('docfind:navchange'));
      }
      // Delegated from the document: the mega menu's panel holds copies of
      // these rows, outside the sheet.
      document.addEventListener('click', async e => {
        const x = e.target.closest('button.nav-x[data-href]');
        if (!x) return;
        e.preventDefault();
        e.stopPropagation();
        const href = x.dataset.href;
        const d = DESTINATIONS.concat(ownPages(o)).find(v => v.href === href);
        const all = [...sheet.querySelectorAll('button.nav-x')];
        const at = all.findIndex(b => b.dataset.href === href);
        const before = [...hiddenHrefs(o)];
        const inSheet = sheet.contains(x);
        x.disabled = true;
        try {
          await setHidden(before.concat([href]));
          // From the keyboard, focus goes on to the entry that took its place
          // (in the mega menu's panel, meganav.js does the same).
          if (!e.detail && inSheet) {
            const next = sheet.querySelectorAll('button.nav-x')[Math.min(at, all.length - 2)];
            if (next) next.focus();
          }
          window.toast('«' + (d ? nameOf(d) : href) + '» از فهرست شما برداشته شد.', 'ok', {
            action: 'بازگرداندن',
            run: () => setHidden(before).catch(err => window.toast(err.message, 'bad')),
          });
        } catch (err) {
          x.disabled = false;
          window.toast(err.message, 'bad');
        }
      }, true);
      drawAdd();
    }

    toggle.addEventListener('click', () =>
      document.body.classList.contains('menu-open') ? close() : open());
    scrim.addEventListener('click', close);
    sheet.querySelector('[data-close]').addEventListener('click', close);

    // The group headings. Delegated from the sheet rather than bound one by
    // one, so a group added to the table above needs nothing here.
    //
    // Nothing is remembered between visits. A menu that reopens with the
    // reader's last shape sounds thoughtful and means the same heading is
    // collapsed on a page it contains, which is the one state it must never
    // be in -- inGroup decides that on every open, from where you are.
    // The search at the head of the menu (asked for on 1405/07/08). Typing
    // narrows the menu to the entries whose name or line match -- folded the
    // palette's way, so ي/ی, ك/ک and the half-space do not matter -- and opens
    // the groups that hold them. Enter hands the words to the palette, which
    // searches the messages too, not only the menu; the down arrow goes to
    // the first match.
    const navQ = sheet.querySelector('#navQ');
    const none = sheet.querySelector('.navsearch-none');
    const matches = a => { const f = palFold(navQ.value); return !f || palFold(a.textContent).includes(f); };
    // An entry is hidden with its row, so its − does not stay behind alone.
    const rowOf = a => a.closest('.navrow') || a;
    navQ.addEventListener('input', () => {
      const f = palFold(navQ.value);
      let any = false;
      const add = sheet.querySelector('[data-navadd]');
      if (add) add.hidden = !!f;
      sheet.querySelectorAll('.navgroup').forEach(g => {
        if (!('wasOpen' in g.dataset)) g.dataset.wasOpen = g.classList.contains('open') ? '1' : '';
        let hit = false;
        g.querySelectorAll('a.dest').forEach(a => { const m = matches(a); rowOf(a).hidden = !m; hit = hit || m; });
        g.hidden = !!f && !hit;
        const open = f ? hit : g.dataset.wasOpen === '1';
        g.classList.toggle('open', open);
        g.querySelector('.navgroup-head').setAttribute('aria-expanded', String(open));
        if (!f) delete g.dataset.wasOpen;
        any = any || hit;
      });
      sheet.querySelectorAll('[data-navlist] > .navrow > a.dest').forEach(a => { const m = matches(a); rowOf(a).hidden = !m; any = any || m; });
      none.hidden = !f || any;
    });
    navQ.addEventListener('keydown', e => {
      if (e.key === 'ArrowDown') {
        const first = [...sheet.querySelectorAll('a.dest')].find(a => a.offsetParent !== null);
        if (first) { e.preventDefault(); first.focus(); }
      } else if (e.key === 'Enter' && navQ.value.trim()) {
        e.preventDefault();
        const words = navQ.value.trim();
        close();
        openPalette(words);
      }
    });

    sheet.addEventListener('click', ev => {
      const head = ev.target.closest('.navgroup-head');
      if (!head || !sheet.contains(head)) return;
      const group = head.parentElement;
      const open = !group.classList.contains('open');
      group.classList.toggle('open', open);
      head.setAttribute('aria-expanded', String(open));
    });

    // The pin. Reflects the stored state onto the button now that it exists,
    // then toggles it. Unpinning leaves the drawer open as an ordinary sheet
    // rather than snapping it shut: the reader asked for a different kind of
    // menu, not for the menu to go away.
    applyPin(pinned());
    sheet.querySelector('[data-pin]').addEventListener('click', () => {
      const on = !document.body.classList.contains('nav-pinned');
      setPin(on);
      if (!on) open();
    });
    const out = sheet.querySelector('[data-signout]');
    if (out && o.onSignOut) out.addEventListener('click', o.onSignOut);

    document.addEventListener('keydown', e => {
      if (e.key === 'Escape') close();
      trapFocus(e);
    });
    // Growing past the breakpoint must not leave the sheet stranded open over
    // a desktop layout that already shows every destination in the bar.
    window.matchMedia('(min-width: 821px)').addEventListener('change', e => { if (e.matches) close(); });

    markActive();

    // The mega menu (meganav.js): a group opening as a panel beside the
    // sidebar. Only on a desk, and fetched only there, after the sheet exists.
    const desk = window.matchMedia('(min-width: 1100px) and (pointer: fine)');
    const mega = () => {
      if (!desk.matches || window.__megaNav) return;
      window.__megaNav = 1;
      const m = document.createElement('script');
      m.src = "/meganav.js";
      m.async = true;
      document.head.appendChild(m);
    };
    mega();
    desk.addEventListener('change', mega);
  };
})();

// ------------------------------------------------ Add to Home Screen, on iOS
//
// iOS has no install prompt. Safari will not offer one, and there is no API to
// ask -- the only way in is Share ▸ Add to Home Screen, which people do not
// find on their own. So the app says where it is, once, and then stops asking.
//
// Shown only when all of these hold, because otherwise it is noise:
//   * this is iOS Safari (Chrome and Android have their own install flow)
//   * the app is not already installed (navigator.standalone)
//   * the reader has not dismissed it before
// It is also only worth showing on a page somebody has reached deliberately,
// so it waits a moment rather than appearing over the first paint.
(function () {
  'use strict';

  const KEY = 'docfind.a2hs.dismissed';
  const DELAY = 2500;

  function isIOS() {
    // iPadOS 13+ reports itself as a Mac, and the only reliable tell is that a
    // Mac has no touch points. Checked this way rather than by version string,
    // which Apple has changed repeatedly.
    const ua = navigator.userAgent;
    const iPhone = /iPad|iPhone|iPod/.test(ua);
    const iPadDesktopUA = /Macintosh/.test(ua) && navigator.maxTouchPoints > 1;
    return iPhone || iPadDesktopUA;
  }

  function isSafari() {
    const ua = navigator.userAgent;
    // On iOS every browser is WebKit, but only Safari can add to the home
    // screen; Chrome and Firefox for iOS carry their own tokens and their
    // share sheets have no such item.
    return !/CriOS|FxiOS|EdgiOS|OPiOS|Chrome/.test(ua);
  }

  function installed() {
    // navigator.standalone is the iOS-specific signal; the media query covers
    // everything else and costs nothing to check.
    return window.navigator.standalone === true ||
      (window.matchMedia && window.matchMedia('(display-mode: standalone)').matches);
  }

  function dismissed() {
    try { return localStorage.getItem(KEY) === '1'; } catch (_) { return false; }
  }

  function remember() {
    try { localStorage.setItem(KEY, '1'); } catch (_) {}
  }

  function show() {
    if (document.querySelector('.a2hs')) return;
    const el = document.createElement('div');
    el.className = 'a2hs';
    el.setAttribute('role', 'complementary');
    el.setAttribute('aria-label', 'افزودن به صفحه اصلی');
    // A fixed literal: nothing here comes from the account or the server.
    el.innerHTML =
      '<div class="a2hs-head">' +
        '<img src="/apple-touch-icon.png" alt="">' +
        '<div class="grow">' +
          '<b>افزودن به صفحه اصلی</b>' +
          '<p class="hint">مثل یک برنامه باز می‌شود: تمام‌صفحه، بدون نوار مرورگر.</p>' +
        '</div>' +
      '</div>' +
      '<ol class="a2hs-steps">' +
        '<li><span class="n">۱</span>' +
          '<svg class="ic" aria-hidden="true"><use href="#i-share"/></svg>' +
          '<span>روی دکمه هم‌رسانی در نوار پایین Safari بزنید.</span></li>' +
        '<li><span class="n">۲</span>' +
          '<svg class="ic" aria-hidden="true"><use href="#i-square-plus"/></svg>' +
          '<span>«Add to Home Screen» را انتخاب کنید.</span></li>' +
        '<li><span class="n">۳</span>' +
          '<svg class="ic" aria-hidden="true"><use href="#i-check"/></svg>' +
          '<span>روی «Add» بزنید. نشان برنامه روی صفحه اصلی می‌آید.</span></li>' +
      '</ol>' +
      '<div class="a2hs-acts">' +
        '<button type="button" class="btn" data-later>بعداً</button>' +
        '<button type="button" class="btn btn-primary" data-done>متوجه شدم</button>' +
      '</div>';
    document.body.appendChild(el);

    // Reserve the height it occupies, so it covers nothing.
    //
    // It is fixed to the bottom, and on the report pages the chart and its
    // caption sit there -- so without this the panel hid them, and the report
    // looked broken rather than the panel looking intrusive. Measured after
    // it is in the document, because its height depends on how the three
    // steps wrap at this width.
    const reserve = () => {
      document.body.style.paddingBottom =
        Math.ceil(el.getBoundingClientRect().height) + 'px';
    };
    reserve();
    const ro = typeof ResizeObserver === 'function' ? new ResizeObserver(reserve) : null;
    if (ro) ro.observe(el);
    else window.addEventListener('resize', reserve);

    const close = forget => {
      // "بعداً" leaves it to reappear on a later visit; "متوجه شدم" means do
      // not ask again. Two buttons because the honest answer to "not now" is
      // different from the honest answer to "stop telling me".
      if (forget) remember();
      if (ro) ro.disconnect(); else window.removeEventListener('resize', reserve);
      document.body.style.paddingBottom = '';
      el.remove();
    };
    el.querySelector('[data-later]').addEventListener('click', () => close(false));
    el.querySelector('[data-done]').addEventListener('click', () => close(true));
  }

  // ------------------------------------------------ everywhere that is not iOS
  //
  // The panel above is iOS Safari only, because iOS has no install prompt and
  // the only way in is a share-sheet item nobody finds. Every other browser
  // fires beforeinstallprompt and can install in one press -- but only if
  // something keeps the event, because the browser's own affordance is an icon
  // in the address bar that people on a phone never see.
  //
  // So: hold the event, offer a button, and use it. Without this the panel's
  // three iOS steps were the app's only install instructions, and on Android
  // they are wrong -- there is no Share button in Chrome's bottom bar to tap.
  let deferred = null;

  window.addEventListener('beforeinstallprompt', e => {
    // Keeping the event is the whole trick: unprevented, the browser shows its
    // own mini-infobar and the event is spent.
    e.preventDefault();
    deferred = e;
    if (!dismissed()) showInstallButton();
  });

  // Nothing to offer once it is installed, and the event never fires again.
  window.addEventListener('appinstalled', () => {
    deferred = null;
    const b = document.querySelector('.a2hs-install');
    if (b) b.remove();
    remember();
  });

  function showInstallButton() {
    if (document.querySelector('.a2hs-install')) return;
    const b = document.createElement('button');
    b.type = 'button';
    b.className = 'btn btn-primary a2hs-install';
    b.textContent = 'نصب برنامه';
    b.addEventListener('click', async () => {
      if (!deferred) { b.remove(); return; }
      deferred.prompt();
      let outcome = 'dismissed';
      try { outcome = (await deferred.userChoice).outcome; } catch (_) {}
      // The event is single-use whatever the answer, so the button goes
      // either way rather than sitting there doing nothing on a second press.
      deferred = null;
      b.remove();
      if (outcome === 'dismissed') remember();
    });
    // The page's own header bar, whatever it calls itself. The chat page has
    // a .chat-bar rather than an .appbar, and before this it fell through to
    // the body -- where an unpositioned button lands wherever the flow ends.
    // On a page laid out to exactly one viewport that is BELOW the fold: the
    // install button added 41px of scroll to a page designed not to scroll,
    // and pushed the message box off the bottom of a phone screen.
    const bar = document.querySelector('.appbar, .chat-bar');
    if (bar) {
      bar.appendChild(b);
      return;
    }
    // Last resort, for a page with no header at all. Floated rather than
    // dropped into the flow, so it cannot change the height of the document
    // it is added to.
    b.classList.add('a2hs-loose');
    document.body.appendChild(b);
  }

  // Exposed so the settings page can offer this on demand -- the only way back
  // for somebody who dismissed it. Either route, depending on the platform.
  window.docfindA2HS = {
    show: () => { if (deferred) showInstallButton(); else show(); },
    eligible: () => !installed() && ((isIOS() && isSafari()) || !!deferred),
  };

  if (!isIOS() || !isSafari() || installed() || dismissed()) return;
  // The messenger asks for this itself, at the top of its list and with the
  // reason that matters there (an iPhone only notifies the Home Screen app);
  // this panel would sit over its message bar.
  setTimeout(() => { if (!document.body.classList.contains('msgr')) show(); }, DELAY);
})();

// --------------------------------------------- the worker, and telling people
//                                                when a new one has taken over
//
// Registration lives here rather than on each page: nav.js is loaded by every
// page inside the application, so a single registration here reaches all of
// them, and no page can be the one that forgot its copy.
//
// Telling somebody a new version has arrived
// ------------------------------------------
// sw.js calls skipWaiting() on install and clients.claim() on activate, so a
// deployment takes over tabs that are already open. That is deliberate -- the
// alternative leaves a retired worker serving a stale shell until every last
// tab is closed -- but it produces a state worth naming: OLD HTML, NEW WORKER.
// The page in front of somebody was built by the previous release and is now
// being served by the current one.
//
// That desync is not hypothetical here. A release tightened the Content-
// Security-Policy; the reports page somebody already had open kept its old
// markup, whose inline script the new policy refused to run, and it sat on
// "در حال بارگذاری گزارش‌ها…" for as long as they left it there. The remedy was
// to tell people to reload, which only reaches the people somebody thinks to
// tell. So the application says it itself.
(function () {
  'use strict';

  if (!('serviceWorker' in navigator)) return;

  // Whether this page is under a worker yet.
  //
  // On a first-ever visit there is no controller: the worker installs, claims
  // the page, and controllerchange fires -- a null-to-worker transition that is
  // an INSTALL, not an update. Announcing that would tell every new reader the
  // application had been updated before they had used it once, so the first
  // such change is swallowed.
  //
  // It is a LET, not a const read once at load, and that distinction is the
  // whole of this comment. Written as a const it was measured doing the wrong
  // thing: a page opened for the first time captured false, the worker claimed
  // it, and the flag then stayed false for the rest of that page's life -- so a
  // real deployment arriving an hour later was swallowed too. Measured, not
  // reasoned: the shell cache went from v5 to v6, the old one was dropped, the
  // page was claimed by the new worker, and it said nothing.
  //
  // And the page that suffers it is the worst possible one. Somebody's first
  // page of the day is the page they leave open all day, which is exactly the
  // tab this feature exists for.
  //
  // So: the first claim is the install and is swallowed; every change after it
  // is a worker REPLACING another, which is an update by definition.
  //
  // Kept as the backstop it now is rather than the main route. sw.js no longer
  // calls skipWaiting, so in the ordinary case a new worker waits and is
  // announced from `updatefound` below, long before any controller changes.
  // This still fires when a worker takes over for the OTHER reason -- every tab
  // of the application was closed and reopened -- and when that is what
  // happened, the page is already fresh and has nothing to say.
  let controlled = !!navigator.serviceWorker.controller;

  // How often an open page asks whether a newer release exists.
  //
  // The calendar is left open all day on a screen nobody touches, and without
  // asking it would go on running the morning's code until something reloaded
  // it. Half an hour is far below a working day and far above anything worth
  // calling polling: it is one conditional GET of sw.js, which answers 304
  // when nothing has changed.
  const CHECK_MS = 30 * 60 * 1000;

  let bar = null;
  // The installed-but-waiting worker: the update itself, held until somebody
  // asks for it. Null when there is nothing to take.
  let pending = null;
  // Set only by the button, so the controllerchange that follows is known to be
  // the one we asked for rather than one to announce.
  let accepting = false;

  navigator.serviceWorker.register('/sw.js').then(reg => {
    // An update that finished installing during an earlier visit is still
    // sitting there now, and nothing will fire to announce it -- the events
    // happened while a previous page was open. Asked directly, once, or a
    // waiting update stays invisible until the next one lands behind it.
    //
    // The controller test distinguishes it from a first install, which also
    // produces a worker but has no predecessor to displace.
    if (reg.waiting && navigator.serviceWorker.controller) offer(reg.waiting);

    // A new worker has appeared and started installing.
    reg.addEventListener('updatefound', () => {
      const fresh = reg.installing;
      if (!fresh) return;
      fresh.addEventListener('statechange', () => {
        // "installed" is the moment it is ready and waiting. With a controller
        // already present this is an UPDATE holding its turn; without one it is
        // the first install, which activates on its own and is nobody's news.
        if (fresh.state === 'installed' && navigator.serviceWorker.controller) {
          offer(fresh);
        }
      });
    });

    // Only while the page is actually being looked at. A backgrounded tab
    // asking is work nobody benefits from, and on a phone it is somebody's
    // battery. Asking again on return covers the laptop shut overnight, where
    // the interval did not fire at all.
    const check = () => {
      if (document.visibilityState === 'visible') reg.update().catch(() => {});
    };
    setInterval(check, CHECK_MS);
    document.addEventListener('visibilitychange', check);

    // Exposed so the settings page can offer "check now" to somebody who does
    // not want to wait out the interval, and so this is testable without
    // reaching into the closure.
    window.docfindUpdate = {
      check: () => reg.update().catch(() => {}),
      pending: () => !!pending,
    };
  }).catch(() => {});

  // A worker took over without being asked.
  //
  // With skipWaiting gone this is no longer how an update arrives, so it means
  // one of two things. Either it is this page's own first install claiming it,
  // or it is the swap we just asked for -- and in that second case the reload
  // below is what brings down the HTML to match.
  navigator.serviceWorker.addEventListener('controllerchange', () => {
    if (accepting) { location.reload(); return; }
    if (!controlled) { controlled = true; return; }  // the install's own claim
    showUpdateBar();
  });

  function offer(worker) {
    pending = worker;
    showUpdateBar();
  }

  function showUpdateBar() {
    if (bar) return;

    const el = document.createElement('div');
    el.className = 'swbar rise';
    // status, not alert. A new version is worth knowing about and is not an
    // error, and assertive would cut across whatever a screen reader is in the
    // middle of reading out.
    el.setAttribute('role', 'status');
    el.setAttribute('aria-live', 'polite');

    const text = document.createElement('span');
    text.className = 'swbar-text';
    text.textContent = 'نسخهٔ تازه‌ای از برنامه آماده است.';

    const go = document.createElement('button');
    go.type = 'button';
    go.className = 'btn btn-primary btn-sm';
    go.textContent = 'به‌روزرسانی';
    // This press is what performs the update. It is not a reload button with a
    // polite label.
    //
    // The waiting worker is told to take over, and the reload happens in the
    // controllerchange handler once it HAS. That order matters: reloading first
    // would fetch the new HTML while the old worker was still serving, which is
    // the same mismatch the other way round.
    go.addEventListener('click', () => {
      go.disabled = true;
      later.disabled = true;
      go.textContent = 'در حال به‌روزرسانی…';
      accepting = true;

      if (pending) {
        pending.postMessage({ type: 'SKIP_WAITING' });
      } else {
        // No worker waiting, so nothing to hand over to. Reached when this
        // notice came from the controllerchange backstop rather than from an
        // update we are holding -- the swap is already done and a reload is the
        // whole of the remaining work.
        location.reload();
        return;
      }

      // If the handover says nothing, go anyway. A worker that fails to
      // activate must not leave somebody looking at a disabled button: the
      // reload alone still brings down the new HTML, which is most of the
      // benefit, and the worker will be taken up on the next visit.
      setTimeout(() => { if (accepting) location.reload(); }, 4000);
    });

    const later = document.createElement('button');
    later.type = 'button';
    later.className = 'btn btn-ghost btn-sm';
    later.textContent = 'بعداً';
    later.addEventListener('click', () => { el.remove(); bar = null; });

    el.append(text, go, later);
    document.body.appendChild(el);
    bar = el;

    // Focus is deliberately NOT moved here. Somebody part-way through a query
    // in the report builder must not have the caret taken off them by a notice
    // they did not ask for; the bar is announced, it is reachable by Tab, and
    // it otherwise leaves them alone. Nor does it time out: an unread notice
    // that removes itself is the same as no notice at all.
  }

})();
