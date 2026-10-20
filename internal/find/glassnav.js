// The glass dock: the navigation on a phone or a touch tablet.
//
// A floating capsule of the places people open most -- the messages and
// the settings -- plus "more", with a round search button of the same
// material beside it. The active place sits in an inner
// capsule that slides to whatever is pressed, so the answer to a tap is
// immediate even while the next page is still loading; where the browser
// supports cross-document view transitions the capsule then glides into its
// place on the new page rather than jumping.
//
// "More" opens a sheet with everything else this account may open, from the
// same permission-checked list the menu drawer is built from (nav.js hands it
// over as window.DocfindNav). Nothing here decides who may see what.
//
// On a desk -- wide screen, fine pointer -- none of this is mounted: the
// drawer becomes a floating glass sidebar instead (app.css). The CSS decides
// which; this file only builds the dock and leaves it hidden where it does
// not belong.
(function () {
  'use strict';
  const N = window.DocfindNav;
  if (!N || document.querySelector('.glass-dock')) return;
  const { visible, opts } = N;
  const has = href => visible.some(d => d.href === href);
  const icon = name => '<svg class="ic" aria-hidden="true"><use href="#i-' + name + '"/></svg>';
  const fa = n => Number(n).toLocaleString('fa-IR');
  const reduce = window.matchMedia('(prefers-reduced-motion: reduce)');

  // ------------------------------------------------------------ the dock
  // Only what the menu shows: the messenger is a permission (page:chat).
  const items = [
    has('/messages') && { href: '/messages', icon: 'message-square', label: 'پیام‌ها', match: p => p === '/' || p.startsWith('/messages') },
    { href: '/settings', icon: 'settings', label: 'تنظیمات', match: p => p.startsWith('/settings') },
    { more: true, icon: 'layout-grid', label: 'بیشتر' },
  ].filter(Boolean);

  const here = location.pathname;
  let active = items.findIndex(it => it.match && it.match(here));
  // Anywhere else is somewhere in "more", and the capsule says so.
  if (active < 0) active = items.length - 1;

  const dock = document.createElement('div');
  dock.className = 'glass-dock';
  const nav = document.createElement('nav');
  nav.className = 'glass-nav';
  nav.setAttribute('aria-label', 'پیمایش اصلی');
  const pill = document.createElement('span');
  pill.className = 'glass-nav-pill';
  pill.setAttribute('aria-hidden', 'true');
  nav.appendChild(pill);

  const els = items.map((it, i) => {
    const el = document.createElement(it.more ? 'button' : 'a');
    el.className = 'glass-nav-item' + (it.more ? ' glass-nav-more' : '');
    if (it.more) {
      el.type = 'button';
      el.setAttribute('aria-haspopup', 'dialog');
      el.setAttribute('aria-expanded', 'false');
      el.setAttribute('aria-controls', 'glassMore');
      el.addEventListener('click', openMore);
    } else {
      el.href = it.href;
      el.addEventListener('click', e => {
        if (e.ctrlKey || e.metaKey || e.shiftKey || e.button) return;
        if (i === active && location.pathname === it.href) { e.preventDefault(); window.scrollTo({ top: 0, behavior: reduce.matches ? 'auto' : 'smooth' }); return; }
        moveTo(i); // the answer to the tap, before the next page arrives
      });
    }
    if (i === active) {
      el.classList.add('on');
      if (!it.more) el.setAttribute('aria-current', 'page');
    }
    el.innerHTML = '<span class="glass-nav-icon">' + icon(it.icon) + '</span><span class="glass-nav-label"></span>' +
      (it.badge ? '<span class="glass-nav-badge" hidden></span>' : '');
    el.querySelector('.glass-nav-label').textContent = it.label;
    nav.appendChild(el);
    return el;
  });

  const search = document.createElement('button');
  search.type = 'button';
  search.className = 'glass-nav-search';
  search.setAttribute('aria-label', 'جست‌وجو در پیام‌ها و صفحه‌ها');
  search.setAttribute('aria-keyshortcuts', 'Control+K');
  search.innerHTML = icon('search');
  // A page with a search of its own (the messenger's) takes the button.
  search.addEventListener('click', () => (window.DocfindPageSearch ? window.DocfindPageSearch() : N.openPalette()));

  dock.append(nav, search);
  // Played once per visit to the application, not on every page: a dock
  // that rises again on each navigation stops looking like part of the
  // device and starts looking like a page loading.
  let first = false;
  try { first = !sessionStorage.getItem('docfind.dock'); sessionStorage.setItem('docfind.dock', '1'); } catch (_) {}
  if (first) {
    dock.classList.add('entering');
    dock.addEventListener('animationend', () => dock.classList.remove('entering'), { once: true });
  }
  document.body.appendChild(dock);
  document.body.classList.add('has-glassnav');

  // The capsule is placed by transform alone: no layout is animated. Its
  // width is the item width, the same for every item, so it changes only
  // when the dock itself is resized.
  function place(i, animate) {
    const el = els[i];
    if (!el || !el.offsetWidth) return;
    if (!animate) pill.classList.add('still');
    pill.style.width = el.offsetWidth + 'px';
    nav.style.setProperty('--px', el.offsetLeft + 'px');
    nav.style.setProperty('--hx', (el.offsetLeft + el.offsetWidth / 2) + 'px');
    if (!animate) requestAnimationFrame(() => requestAnimationFrame(() => pill.classList.remove('still')));
  }
  function moveTo(i) {
    els.forEach((el, j) => el.classList.toggle('on', j === i));
    place(i, true);
  }
  place(active, false);
  if (window.ResizeObserver) new ResizeObserver(() => place(els.findIndex(el => el.classList.contains('on')), false)).observe(nav);

  // ------------------------------------------------------------ scrolling
  // Reading down a page, the dock steps back -- smaller, labels folded --
  // and it comes back the moment the reader scrolls up or stops. One passive
  // listener, one frame of work at most per frame.
  let lastY = window.scrollY, ticking = false, settle = 0;
  window.addEventListener('scroll', () => {
    if (ticking) return;
    ticking = true;
    requestAnimationFrame(() => {
      ticking = false;
      const y = window.scrollY, dy = y - lastY;
      if (Math.abs(dy) > 6) {
        dock.classList.toggle('is-min', dy > 0 && y > 120 && !dock.contains(document.activeElement));
        lastY = y;
      }
      clearTimeout(settle);
      settle = setTimeout(() => dock.classList.remove('is-min'), 900);
    });
  }, { passive: true });

  // ------------------------------------------------------------ more
  let sheet = null;
  const moreBtn = els[els.length - 1];

  function tiles() {
    const out = [];
    const seen = new Set(items.map(it => it.href).filter(Boolean));
    const add = (t) => { if (t.href && seen.has(t.href)) return; if (t.href) seen.add(t.href); out.push(t); };
    // What people come to "more" for first.
    add({ group: 'میان‌بُرها', icon: 'bell', label: 'اعلان‌ها', run: () => window.DocfindBell && window.DocfindBell.open(), bell: true, when: !!window.DocfindBell });
    for (const g of N.groups) {
      const list = g.solo ? [g] : g.items;
      const title = g.solo ? 'سامانه' : g.name;
      for (const d of list) add({ group: title, icon: d.icon, label: d.short || d.name, desc: d.desc, href: d.href });
    }
    if (!seen.has('/settings')) add({ group: 'سامانه', icon: 'settings', label: 'تنظیمات من', href: '/settings' });
    // Install, where the browser can (nav.js docfindA2HS): the prompt on
    // Android and desktop, the three steps on an iPhone.
    if (window.docfindA2HS && window.docfindA2HS.eligible()) {
      add({ group: 'سامانه', icon: 'square-plus', label: 'نصب برنامه', run: () => window.docfindA2HS.show() });
    }
    const kept = out.filter(t => t.when !== false);
    // A group the dock has already taken everything else from would be a
    // heading over one lonely tile. It joins the shortcuts.
    const size = {};
    for (const t of kept) size[t.group] = (size[t.group] || 0) + 1;
    for (const t of kept) if (size[t.group] === 1) t.group = 'میان‌بُرها';
    const order = [...new Set(kept.map(t => t.group))];
    return kept.sort((a, b) => order.indexOf(a.group) - order.indexOf(b.group));
  }

  function buildSheet() {
    sheet = document.createElement('dialog');
    sheet.id = 'glassMore';
    sheet.className = 'glass-sheet';
    sheet.setAttribute('aria-labelledby', 'glassMoreTitle');
    sheet.innerHTML = '<div class="glass-sheet-grip" aria-hidden="true"></div>' +
      '<div class="glass-sheet-head"><h2 id="glassMoreTitle">همهٔ بخش‌ها</h2>' +
      '<button type="button" class="glass-sheet-close" aria-label="بستن">' + icon('x') + '</button></div>' +
      '<div class="glass-sheet-body"></div>' +
      '<div class="glass-sheet-foot"><button type="button" class="btn btn-sm btn-ghost" data-drawer>فهرست کامل با توضیح هر بخش</button></div>';
    const body = sheet.querySelector('.glass-sheet-body');
    let group = '', grid = null, i = 0;
    for (const t of tiles()) {
      if (t.group !== group) {
        group = t.group;
        const h = document.createElement('h3');
        h.textContent = group;
        grid = document.createElement('div');
        grid.className = 'glass-tiles';
        body.append(h, grid);
      }
      const el = document.createElement(t.href ? 'a' : 'button');
      el.className = 'glass-tile';
      el.style.setProperty('--i', i++);
      if (t.href) {
        el.href = t.href;
        if (t.href === location.pathname + location.hash || t.href === location.pathname) el.setAttribute('aria-current', 'page');
        // A link to somewhere on this very page moves there and closes the
        // sheet; the browser would otherwise do nothing
        // visible behind the sheet.
        el.addEventListener('click', () => closeMore(true));
      } else {
        el.type = 'button';
        el.addEventListener('click', () => { closeMore(true); t.run(); });
      }
      el.innerHTML = '<span class="glass-tile-icon">' + icon(t.icon) + (t.bell ? '<span class="glass-nav-badge" hidden></span>' : '') + '</span><span class="glass-tile-label"></span>';
      el.querySelector('.glass-tile-label').textContent = t.label;
      if (t.desc) el.title = t.desc;
      grid.appendChild(el);
    }
    sheet.querySelector('.glass-sheet-close').addEventListener('click', () => closeMore());
    sheet.querySelector('[data-drawer]').addEventListener('click', () => { closeMore(true); N.openDrawer(); });
    // A press on the dimmed page outside the sheet closes it, as a native
    // sheet does. The dialog's own box is the sheet, so a click whose target
    // is the dialog itself landed on the backdrop.
    sheet.addEventListener('click', e => { if (e.target === sheet) closeMore(); });
    sheet.addEventListener('cancel', e => { e.preventDefault(); closeMore(); });
    document.body.appendChild(sheet);
    paintBell();
  }

  function paintBell() {
    const tile = sheet && sheet.querySelector('.glass-tile .glass-nav-badge');
    if (!tile || !bell) return;
    const n = (bell.unread || 0) + (bell.critical || 0);
    tile.hidden = !n;
    tile.textContent = n > 99 ? '۹۹+' : fa(n);
    tile.classList.toggle('crit', !!bell.critical);
    tile.closest('.glass-tile').setAttribute('aria-label', 'اعلان‌ها' + (n ? '، ' + fa(n) + ' خوانده‌نشده' : ''));
  }
  let bell = null;
  window.addEventListener('bell:summary', e => { bell = e.detail || null; paintBell(); });

  function openMore() {
    if (!sheet) buildSheet();
    if (sheet.open) return closeMore();
    sheet.classList.remove('closing');
    sheet.showModal();
    moreBtn.setAttribute('aria-expanded', 'true');
    dock.classList.remove('is-min');
    const first = sheet.querySelector('.glass-tile');
    if (first) first.focus({ preventScroll: true });
  }

  // Closing plays the sheet out, then closes the dialog; with reduced motion,
  // or when leaving the page anyway, it closes at once.
  function closeMore(now) {
    if (!sheet || !sheet.open) return;
    moreBtn.setAttribute('aria-expanded', 'false');
    const done = () => { sheet.classList.remove('closing'); if (sheet.open) sheet.close(); moreBtn.focus({ preventScroll: true }); };
    if (now || reduce.matches) return done();
    sheet.classList.add('closing');
    let finished = false;
    const finish = () => { if (!finished) { finished = true; done(); } };
    sheet.addEventListener('animationend', finish, { once: true });
    setTimeout(finish, 320); // in case the animation never runs
  }

  window.DocfindDock = { openMore, closeMore };
})();
