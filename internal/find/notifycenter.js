// The notification centre: a bell in the header with how many notifications
// are unread, and a panel of what arrived -- what needs attention first, then
// today, then everything earlier folded into a one-line digest.
//
// Loaded by nav.js after the menu is built (never before it, so it can never
// delay the page), and only for a signed-in account. Reads the same list the
// push sender writes (/api/push/recent), so a notification that buzzed a
// phone at night is here in the morning.
//
// Priority comes from the server, "important" or "info". "critical" is never
// stored -- it is a service that is down right now, read live from
// /api/db-state and shown to an administrator only.
//
// What a person has read or snoozed is kept in this browser (localStorage,
// cleared on sign-out). It is a reading position, not a record: losing it
// costs nothing but a badge that counts again.
(function () {
  'use strict';
  const bar = document.querySelector('.appbar');
  if (!bar || bar.querySelector('.bell-btn')) return;
  const opts = window.DocfindBellOpts || {};
  const KEY = 'docfind.bell.' + (opts.user || '');
  const DAY = 86400000;
  const icon = name => '<svg class="ic" aria-hidden="true"><use href="#i-' + name + '"/></svg>';
  const faNum = n => Number(n).toLocaleString('fa-IR');

  // ------------------------------------------------------------------ state
  function load() {
    let s = null;
    try { s = JSON.parse(localStorage.getItem(KEY) || 'null'); } catch (_) {}
    if (!s || typeof s !== 'object') {
      // First use: yesterday and before count as read, so the badge starts
      // at what is actually new rather than at fifty.
      s = { since: Date.now() - DAY, read: [], snooze: {} };
    }
    s.read = Array.isArray(s.read) ? s.read.slice(-300) : [];
    s.snooze = s.snooze && typeof s.snooze === 'object' ? s.snooze : {};
    const now = Date.now();
    for (const k of Object.keys(s.snooze)) if (s.snooze[k] <= now) delete s.snooze[k];
    return s;
  }
  let state = load();
  const save = () => { try { localStorage.setItem(KEY, JSON.stringify(state)); } catch (_) {} };
  const isSnoozed = n => !!state.snooze[n.id] && state.snooze[n.id] > Date.now();
  const markRead = ids => { for (const id of ids) if (!state.read.includes(id)) state.read.push(id); save(); };

  // ------------------------------------------------------------------ data
  let items = [];
  let available = true;

  async function fetchJSON(url) {
    try {
      const r = await fetch(url, { credentials: 'same-origin' });
      if (r.status === 404 || r.status === 403) return { missing: true };
      return r.ok ? await r.json() : null;
    } catch (_) { return null; }
  }

  async function refresh() {
    const [recent, db] = await Promise.all([
      fetchJSON('/api/push/recent'),
      opts.admin ? fetchJSON('/api/db-state') : null,
    ]);
    if (recent && recent.missing) {
      // Notifications are off on this server, or this account may not have
      // them: no bell at all rather than one that is always empty.
      available = false;
      btn.hidden = true;
      return;
    }
    const list = (recent && recent.notifications) || [];
    const crit = [];
    if (db && !db.missing) {
      // pg_level is a number (pglevel.go): 1 is up, 2 briefly down, 3 down
      // long enough to be emergency offline mode. Only 2 and 3 are news.
      if (Number(db.pg_level) > 1) {
        crit.push({ id: 'crit-pg', critical: true, title: 'پایگاه دادهٔ اصلی در دسترس نیست',
          body: Number(db.pg_level) >= 3 ? 'حالت اضطراری آفلاین؛ برنامه از نسخه‌های محلی کار می‌کند.' : 'برنامه موقتاً از نسخه‌های محلی کار می‌کند.' });
      }
      for (const x of db.degraded || []) {
        if (x.state !== 'down') continue;
        crit.push({ id: 'crit-' + x.name, critical: true, title: (x.label || x.name) + ' قطع است',
          body: x.fallback ? 'جایگزین: ' + x.fallback : '' });
      }
    }
    items = crit.concat(group(list));
    paintBadge();
    if (panel && !panel.hidden) paint();
  }

  // Several notifications about one thing -- three messages in one chat, a
  // call that rang and was then missed -- are one row with a count, the newest
  // text on top. The tag is what the sender already uses to replace one with
  // the next on a phone.
  function group(list) {
    const out = [];
    const byTag = new Map();
    for (const n of list) {
      const key = n.tag || ('id-' + n.id);
      const have = byTag.get(key);
      if (have) { have.count++; have.ids.push(n.id); continue; }
      const row = Object.assign({ count: 1, ids: [n.id] }, n);
      byTag.set(key, row);
      out.push(row);
    }
    return out;
  }
  // A row is read when its newest notification is older than "mark all
  // read", or every notification in it was opened or ticked.
  const rowRead = r => !r.critical &&
    (Date.parse(r.created_at) < state.since || r.ids.every(id => state.read.includes(id)));

  // ------------------------------------------------------------------ bell
  const btn = document.createElement('button');
  btn.type = 'button';
  btn.className = 'bell-btn';
  btn.setAttribute('aria-haspopup', 'dialog');
  btn.setAttribute('aria-expanded', 'false');
  btn.setAttribute('aria-controls', 'bellPanel');
  btn.innerHTML = icon('bell') + '<span class="bell-count" hidden></span>';
  const count = btn.querySelector('.bell-count');
  const cmdk = bar.querySelector('.cmdk-btn');
  if (cmdk) cmdk.before(btn); else bar.appendChild(btn);

  function paintBadge() {
    const unread = items.filter(r => !rowRead(r) && !isSnoozed(r));
    const crit = unread.some(r => r.critical);
    // For the dock's "more" sheet (glassnav.js), which counts the same way.
    window.dispatchEvent(new CustomEvent('bell:summary', { detail: {
      unread: unread.filter(r => !r.critical).length, critical: unread.filter(r => r.critical).length } }));
    count.hidden = !unread.length;
    count.textContent = unread.length > 99 ? '۹۹+' : faNum(unread.length);
    count.classList.toggle('crit', crit);
    btn.setAttribute('aria-label', unread.length
      ? 'اعلان‌ها، ' + faNum(unread.length) + ' خوانده‌نشده' + (crit ? '، شامل قطعی سرویس' : '')
      : 'اعلان‌ها');
  }

  // ------------------------------------------------------------------ panel
  let panel = null;
  function panelEl() {
    if (panel) return panel;
    panel = document.createElement('section');
    panel.id = 'bellPanel';
    panel.className = 'bellpanel';
    panel.hidden = true;
    panel.setAttribute('role', 'dialog');
    panel.setAttribute('aria-label', 'اعلان‌ها');
    panel.innerHTML = '<div class="bellpanel-head"><h2>اعلان‌ها</h2>' +
      '<button type="button" class="btn btn-sm btn-ghost" data-act="all">' + icon('check') + 'همه خوانده شد</button>' +
      '<a class="btn btn-sm btn-ghost" href="/settings/notifications">تنظیمات</a>' +
      '<button type="button" class="btn btn-icon btn-ghost" data-act="close" aria-label="بستن">' + icon('x') + '</button></div>' +
      '<div class="bellpanel-body"></div>';
    panel.querySelector('[data-act="close"]').addEventListener('click', close);
    panel.querySelector('[data-act="all"]').addEventListener('click', readAll);
    document.body.appendChild(panel);
    return panel;
  }

  function open() {
    panelEl().hidden = false;
    // Under the header wherever the header is: a status banner above it
    // moves it down, and a fixed offset would put the panel over the bell.
    panel.style.top = window.innerWidth >= 600 ? Math.round(bar.getBoundingClientRect().bottom + 6) + 'px' : '';
    document.body.classList.add('bell-open');
    btn.setAttribute('aria-expanded', 'true');
    paint();
    panel.querySelector('h2').setAttribute('tabindex', '-1');
    panel.querySelector('h2').focus();
    refresh();
  }
  function close() {
    if (!panel || panel.hidden) return;
    panel.hidden = true;
    document.body.classList.remove('bell-open');
    btn.setAttribute('aria-expanded', 'false');
    btn.focus();
  }
  btn.addEventListener('click', () => (panel && !panel.hidden ? close() : open()));
  document.addEventListener('keydown', e => { if (e.key === 'Escape' && panel && !panel.hidden) close(); });
  document.addEventListener('click', e => {
    // The toast's undo is part of what the panel just did, not a click away.
    if (panel && !panel.hidden && !panel.contains(e.target) && !btn.contains(e.target) &&
        !(e.target.closest && e.target.closest('.toast'))) close();
  });

  function readAll() {
    const before = state.read.slice();
    const since = state.since;
    markRead(items.filter(r => !r.critical).flatMap(r => r.ids));
    state.since = Date.now();
    save();
    paintBadge();
    paint();
    if (window.toast) {
      window.toast('همه خوانده شد.', 'ok', { action: 'برگرداندن', run: () => {
        state.read = before; state.since = since; save(); paintBadge(); paint();
      } });
    }
  }

  const el = (tag, cls, text) => {
    const e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text != null) e.textContent = text;
    return e;
  };

  function when(iso) {
    const t = Date.parse(iso);
    if (!t) return '';
    const mins = Math.round((Date.now() - t) / 60000);
    if (mins < 1) return 'همین حالا';
    if (mins < 60) return faNum(mins) + ' دقیقه پیش';
    if (mins < 24 * 60) return faNum(Math.round(mins / 60)) + ' ساعت پیش';
    try {
      return new Intl.DateTimeFormat('fa-IR-u-ca-persian', { month: 'long', day: 'numeric', hour: '2-digit', minute: '2-digit' }).format(new Date(t));
    } catch (_) { return new Date(t).toLocaleString('fa-IR'); }
  }

  function paint() {
    const body = panelEl().querySelector('.bellpanel-body');
    body.textContent = '';
    const live = items.filter(r => !isSnoozed(r));
    const snoozed = items.filter(isSnoozed);
    const startOfDay = new Date(); startOfDay.setHours(0, 0, 0, 0);
    const attention = live.filter(r => r.critical || (r.priority === 'important' && !rowRead(r)));
    const rest = live.filter(r => !attention.includes(r));
    const today = rest.filter(r => Date.parse(r.created_at) >= startOfDay.getTime());
    const earlier = rest.filter(r => !today.includes(r));

    if (!items.length) {
      const empty = el('div', 'bell-empty');
      empty.innerHTML = icon('check');
      empty.appendChild(el('p', null, 'اعلانی نیست.'));
      empty.appendChild(el('p', 'hint', 'پیام‌های تازه و تماس‌های از دست رفته اینجا می‌آید.'));
      body.appendChild(empty);
      return;
    }
    section(body, 'نیاز به توجه', attention, 'attn');
    section(body, 'امروز', today);
    if (earlier.length) {
      // The digest: one line saying what the older ones were, opened on
      // request. Nobody reads forty rows from last week one by one.
      const sec = section(body, 'پیش‌تر', [], null);
      const kinds = {};
      for (const r of earlier) kinds[r.kind] = (kinds[r.kind] || 0) + r.count;
      const label = { chat: 'پیام', test: 'آزمایشی' };
      const parts = Object.entries(kinds).map(([k, n]) => faNum(n) + ' ' + (label[k] || k));
      const d = el('details', 'bell-digest');
      d.appendChild(el('summary', null, parts.join('، ')));
      for (const r of earlier) d.appendChild(row(r));
      sec.appendChild(d);
    }
    if (snoozed.length) {
      const sec = section(body, 'به تعویق افتاده', [], null);
      const d = el('details', 'bell-digest');
      d.appendChild(el('summary', null, faNum(snoozed.length) + ' اعلان'));
      for (const r of snoozed) d.appendChild(row(r, true));
      sec.appendChild(d);
    }
  }

  function section(body, title, rows, cls) {
    if (!rows.length && cls !== null) return null;
    const sec = el('section', 'bell-sec' + (cls ? ' ' + cls : ''));
    sec.appendChild(el('h3', null, title));
    for (const r of rows) sec.appendChild(row(r));
    body.appendChild(sec);
    return sec;
  }

  function row(r, snoozedRow) {
    const read = rowRead(r);
    const li = el('div', 'bell-row' + (r.critical ? ' crit' : r.priority === 'important' ? ' imp' : '') + (read ? ' read' : ''));
    const a = el('a', 'bell-main');
    a.href = r.url || '/';
    const t = el('b', null, r.title);
    if (r.count > 1) t.appendChild(el('span', 'bell-n', ' · ' + faNum(r.count) + ' بار'));
    a.appendChild(t);
    if (r.body) a.appendChild(el('span', 'bell-body', r.body));
    const meta = el('small', 'bell-meta', r.critical ? 'همین حالا' : when(r.created_at));
    if (r.quiet) {
      const q = el('span', 'bell-quiet');
      q.innerHTML = icon('bell-off');
      q.appendChild(document.createTextNode('بی‌صدا رسید'));
      meta.appendChild(q);
    }
    a.appendChild(meta);
    a.addEventListener('click', () => { if (!r.critical) markRead(r.ids); });
    li.appendChild(a);
    if (r.critical) return li;

    const acts = el('div', 'bell-acts');
    const act = (name, label, fn) => {
      const b = el('button', 'btn btn-icon btn-ghost');
      b.type = 'button';
      b.setAttribute('aria-label', label);
      b.title = label;
      b.innerHTML = icon(name);
      b.addEventListener('click', e => { e.stopPropagation(); fn(b); });
      acts.appendChild(b);
    };
    if (!read) act('check', 'خوانده شد', () => { markRead(r.ids); paintBadge(); paint(); });
    if (snoozedRow) {
      act('bell', 'برگرداندن از تعویق', () => { delete state.snooze[r.id]; save(); paintBadge(); paint(); });
    } else {
      act('clock', 'یادآوری دوباره', b => snoozeMenu(r, b));
    }
    li.appendChild(acts);
    return li;
  }

  // Snooze: out of the list and the count until then, back on its own.
  function snoozeMenu(r, anchor) {
    const old = document.querySelector('.bell-snooze');
    if (old) old.remove();
    const m = el('div', 'bell-snooze');
    m.setAttribute('role', 'menu');
    const tomorrow = new Date(); tomorrow.setDate(tomorrow.getDate() + 1); tomorrow.setHours(8, 0, 0, 0);
    const choices = [['یک ساعت دیگر', Date.now() + 3600000], ['سه ساعت دیگر', Date.now() + 3 * 3600000], ['فردا ساعت ۸', tomorrow.getTime()]];
    for (const [label, until] of choices) {
      const b = el('button', null, label);
      b.type = 'button';
      b.setAttribute('role', 'menuitem');
      b.addEventListener('click', e => {
        e.stopPropagation();
        state.snooze[r.id] = until;
        save(); m.remove(); paintBadge(); paint();
        if (window.toast) window.toast('به تعویق افتاد.', 'ok');
      });
      m.appendChild(b);
    }
    anchor.parentElement.appendChild(m);
    m.querySelector('button').focus();
  }

  // ------------------------------------------------------------------ timing
  // Every minute while the page is visible, and at once when it comes back:
  // a tab left open overnight is current the moment somebody looks at it.
  refresh();
  setInterval(() => { if (!document.hidden && available) refresh(); }, 60000);
  document.addEventListener('visibilitychange', () => { if (!document.hidden && available) refresh(); });
  window.DocfindBell = { refresh, open };
})();
