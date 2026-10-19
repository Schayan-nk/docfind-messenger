// The mega menu: on a desk, a group of the glass sidebar opens as a panel
// beside it (asked for on 1405/07/19, after useplanes' «Shader Nav», in the
// sidebar's own glass rather than that component's bar).
//
// Pointing at a group -- or pressing it, or the arrow toward the page -- slides
// its links into a glass panel to the left of the sidebar, beside a card with
// a picture of what is there (1405/07/20: "use relevant pics instead of
// animations for mega menu based on hover"; it was a WebGL liquid). The card
// shows the group, and whichever of its links is pointed at or focused: that
// entry's picture, name and line. The pictures are in static/mega/, one per
// entry, named by the entry's pic in nav.js.
//
// Loaded by nav.js only on a desk: wide, with a mouse or a trackpad, the same
// question the pinned sidebar asks. On a phone or a tablet nothing here runs
// and the groups stay accordions. It is also an option, remembered per browser
// like the pin, beside the pin in the sidebar's head: off, the groups open in
// place as they did before. And it steps aside while the menu's search box has
// words in it, because the search opens the matching groups in place.
//
// Nothing here decides what anybody may open. The panel shows copies of the
// rows nav.js already put in the group -- each link and its − -- and those
// are only the ones this account may reach.
(function () {
  'use strict';
  const sheet = document.getElementById('navsheet');
  if (!sheet || sheet.dataset.mega) return;
  sheet.dataset.mega = '1';
  const root = document.documentElement, body = document.body;
  const DESK = window.matchMedia('(min-width: 1100px) and (pointer: fine)');
  const RM = window.matchMedia('(prefers-reduced-motion: reduce)');
  const reduced = () => RM.matches || root.getAttribute('data-motion') === 'reduce';
  const KEY = 'docfind.nav.mega';
  const chosen = () => { try { return localStorage.getItem(KEY) !== '0'; } catch (_) { return true; } };
  const svg = n => '<svg class="ic" aria-hidden="true"><use href="#i-' + n + '"/></svg>';
  const picURL = name => '/static/mega/' + name + '.webp';

  const groups = () => [...sheet.querySelectorAll('.navgroup:not(.navsites)')];
  const headOf = g => g.querySelector('.navgroup-head');
  const nameOf = g => ((headOf(g).querySelector('b') || {}).textContent || '').trim();

  // ------------------------------------------------------------ the panel
  const panel = document.createElement('div');
  panel.className = 'mega';
  panel.id = 'navMega';
  panel.setAttribute('role', 'group');
  panel.hidden = true;
  panel.innerHTML =
    '<div class="mega-links"></div>' +
    '<div class="mega-card" aria-hidden="true"><img class="mega-pic" alt="" decoding="async"><img class="mega-pic" alt="" decoding="async">' +
    '<div class="mega-card-text"><span class="mega-card-ic"></span><b></b><span class="mega-card-d"></span></div></div>';
  body.appendChild(panel);
  const links = panel.querySelector('.mega-links');
  const card = panel.querySelector('.mega-card');
  const pics = [...card.querySelectorAll('.mega-pic')];
  let current = null, openTimer = 0, closeTimer = 0, filling = false;

  // On only where it can be: a desk, the option on, the sidebar showing (or
  // pinned), and nothing typed in the menu's search.
  const active = () => body.classList.contains('nav-mega') && !body.classList.contains('nav-searching');
  const showing = () => body.classList.contains('nav-pinned') || sheet.getAttribute('aria-hidden') !== 'true';

  function markGroups() {
    for (const g of groups()) headOf(g).classList.toggle('has-on', !!g.querySelector('a.dest.on'));
  }

  // Where the sidebar comes to rest, not where it is now: on a desk it is held
  // 12px off the right edge (app.css), and while it slides in its box is still
  // off the screen -- measured then, the panel was placed off it too.
  const sideLeft = () => window.innerWidth - 12 - sheet.offsetWidth;
  function place(g) {
    const h = headOf(g).getBoundingClientRect();
    panel.style.right = (window.innerWidth - sideLeft() + 12) + 'px';
    panel.style.width = Math.min(660, sideLeft() - 36) + 'px';
    const top = Math.max(12, Math.min(h.top - 14, window.innerHeight - panel.offsetHeight - 12));
    panel.style.top = top + 'px';
  }

  // ------------------------------------------------------------ the card
  // Two pictures stacked: the next one is decoded behind the one showing and
  // then faded in over it, so pointing down a list of links never flashes an
  // empty card. A quick run down the list only shows where it stops: a
  // picture that finishes decoding after a later one was asked for is dropped.
  let showingPic = '', want = '';
  function showPic(name) {
    if (!name) { card.classList.remove('has-pic'); showingPic = want = ''; return; }
    if (name === want) return;
    want = name;
    // Back to the one already showing: it is still in front.
    if (name === showingPic) return;
    const back = pics.find(p => !p.classList.contains('on')) || pics[1];
    // Shown once it has loaded; decode() is only waited on briefly, because a
    // decode that never settles must not leave the card on the old picture.
    const loaded = new Promise((resolve, reject) => {
      back.onload = resolve;
      back.onerror = reject;
    });
    back.src = picURL(name);
    loaded.then(() => back.decode ? Promise.race([back.decode().catch(() => {}), new Promise(r => setTimeout(r, 120))]) : null)
      .then(() => {
        if (want !== name) return;
        for (const p of pics) p.classList.toggle('on', p === back);
        card.classList.add('has-pic');
        showingPic = name;
      })
      .catch(() => { if (want === name && !showingPic) card.classList.remove('has-pic'); });
  }
  function describe(icon, title, line, pic) {
    card.querySelector('.mega-card-ic').innerHTML = svg(icon || 'list-checks');
    card.querySelector('b').textContent = title || '';
    card.querySelector('.mega-card-d').textContent = line || '';
    showPic(pic);
  }
  const iconOf = el => { const u = el && el.querySelector('.ic use'); return u ? u.getAttribute('href').replace('#i-', '') : ''; };
  // The group itself: its icon, name and line, and its first entry's picture.
  function describeGroup(g) {
    const head = headOf(g);
    const first = g.querySelector('.navgroup-body a.dest[data-pic]');
    describe(iconOf(head), nameOf(g), (head.querySelector('.d') || {}).textContent,
      first ? first.dataset.pic : '');
  }
  // One link: its own icon, name, line and picture.
  function describeLink(a) {
    const t = a.querySelector('.t'), d = a.querySelector('.d');
    describe(iconOf(a), t ? t.textContent.trim() : '', d ? d.textContent.trim() : '', a.dataset.pic || '');
  }

  function fill(g, quiet) {
    filling = true;
    links.textContent = '';
    g.querySelectorAll('.navgroup-body .navrow').forEach((row, i) => {
      const c = row.cloneNode(true);
      c.hidden = false;
      c.style.setProperty('--i', i);
      links.appendChild(c);
    });
    filling = false;
    describeGroup(g);
    panel.setAttribute('aria-label', nameOf(g));
    // Warm the group's pictures, so pointing down its links finds them ready.
    for (const a of links.querySelectorAll('a.dest[data-pic]')) { const im = new Image(); im.src = picURL(a.dataset.pic); }
    // The links come in once, when the group is opened -- not again for every
    // move of the pointer over it, which is what made them blink and slide
    // in over and over (1405/07/20: "the animation is broken when you hover").
    if (!quiet) { links.classList.remove('in'); void links.offsetWidth; links.classList.add('in'); }
  }

  function open(g, focusFirst) {
    clearTimeout(openTimer); clearTimeout(closeTimer);
    if (!active() || !showing() || sideLeft() < 460) return;
    if (current === g) {
      // Already open: nothing to draw again.
      if (focusFirst) { const a = links.querySelector('a'); if (a) a.focus(); }
      return;
    }
    if (current) headOf(current).setAttribute('aria-expanded', 'false');
    const was = current;
    current = g;
    const head = headOf(g);
    head.setAttribute('aria-expanded', 'true');
    head.setAttribute('aria-controls', 'navMega');
    fill(g);
    if (!was) { panel.hidden = false; panel.classList.remove('out'); }
    place(g);
    if (focusFirst) { const a = links.querySelector('a'); if (a) a.focus(); }
  }

  function close(refocus) {
    clearTimeout(openTimer); clearTimeout(closeTimer);
    if (!current) return;
    const head = headOf(current);
    head.setAttribute('aria-expanded', 'false');
    current = null;
    panel.classList.add('out');
    setTimeout(() => { if (!current) panel.hidden = true; }, reduced() ? 0 : 140);
    if (refocus && head.isConnected) head.focus();
  }
  const later = fn => { clearTimeout(closeTimer); closeTimer = setTimeout(fn, 220); };

  // ------------------------------------------------------- pointing at it
  // Where the pointer has been over the last tenth of a second, to tell a
  // pointer heading for the panel from one stopping on a group: on its way to
  // the panel it crosses the groups below or above the open one, and opening
  // each of those took the panel away from under it.
  const trail = [];
  sheet.addEventListener('pointermove', e => {
    if (e.pointerType !== 'mouse') return;
    const now = performance.now();
    trail.push({ x: e.clientX, y: e.clientY, t: now });
    while (trail.length > 2 && now - trail[0].t > 100) trail.shift();
  }, { passive: true });
  // Heading for the panel: moving toward it (leftward, in this right-to-left
  // menu), on a line that meets its near edge.
  function aiming() {
    if (!current || panel.hidden || trail.length < 2) return false;
    const a = trail[0], b = trail[trail.length - 1];
    const dx = b.x - a.x, dy = b.y - a.y;
    if (dx > -2) return false;
    const r = panel.getBoundingClientRect();
    const k = (r.right - b.x) / dx;
    if (k < 0) return false;
    const y = b.y + dy * k;
    return y >= r.top - 24 && y <= r.bottom + 24;
  }

  // A short wait before opening, so a pointer crossing the sidebar on its way
  // somewhere else does not open every group it passes; a longer one, checked
  // again when it ends, while the pointer is heading for the open panel; and
  // a longer one still before closing, so the gap between the sidebar and the
  // panel can be crossed.
  let overHead = null;
  sheet.addEventListener('pointerover', e => {
    if (e.pointerType !== 'mouse' || !active()) return;
    const head = e.target.closest('.navgroup-head');
    const g = head && head.closest('.navgroup:not(.navsites)');
    overHead = head || null;
    if (g && g === current) {
      // Moving between the parts of the open group's own heading -- its icon,
      // its name, its line -- is not a new arrival.
      clearTimeout(closeTimer); clearTimeout(openTimer);
      return;
    }
    if (g) {
      clearTimeout(closeTimer); clearTimeout(openTimer);
      const wait = !current ? 110 : aiming() ? 260 : 60;
      openTimer = setTimeout(() => { if (overHead === head) open(g); }, wait);
    } else if (current) {
      clearTimeout(openTimer);
      later(() => close());
    }
  });
  sheet.addEventListener('pointerleave', e => {
    if (e.pointerType !== 'mouse') return;
    overHead = null;
    clearTimeout(openTimer);
    if (current) later(() => close());
  });
  panel.addEventListener('pointerenter', () => { clearTimeout(closeTimer); clearTimeout(openTimer); });
  panel.addEventListener('pointerleave', e => {
    if (e.pointerType !== 'mouse') return;
    if (current) describeGroup(current);
    later(() => close());
  });
  // The card follows the link pointed at or focused, and goes back to the
  // group when the pointer leaves the links.
  links.addEventListener('pointerover', e => {
    const a = e.target.closest('a.dest');
    if (a) describeLink(a);
  });
  links.addEventListener('focusin', e => {
    const a = e.target.closest('a.dest');
    if (a) describeLink(a);
  });
  links.addEventListener('pointerleave', () => { if (current) describeGroup(current); });

  // Pressing a group: the panel instead of the accordion. Taken before
  // nav.js's own handler sees it, which would open the group in place.
  sheet.addEventListener('click', e => {
    if (!active()) return;
    const head = e.target.closest('.navgroup-head');
    const g = head && head.closest('.navgroup:not(.navsites)');
    if (!g) return;
    e.preventDefault(); e.stopPropagation();
    // Pointing at it has usually opened it already: a press keeps it open
    // rather than closing what the reader is reaching for.
    if (current !== g) open(g, !e.detail);
    else if (!e.detail) { const a = links.querySelector('a'); if (a) a.focus(); }
  }, true);

  // The keyboard. Left is toward the panel in this right-to-left menu, right
  // is back to the sidebar; up and down walk the panel's links; Escape and Tab
  // past the last link put the panel away.
  sheet.addEventListener('keydown', e => {
    if (!active() || e.key !== 'ArrowLeft') return;
    const head = e.target.closest('.navgroup-head');
    const g = head && head.closest('.navgroup:not(.navsites)');
    if (g) { e.preventDefault(); open(g, true); }
  });
  panel.addEventListener('keydown', e => {
    const all = [...links.querySelectorAll('a')];
    const i = all.indexOf(document.activeElement);
    if (e.key === 'Escape' || e.key === 'ArrowRight') { e.preventDefault(); e.stopPropagation(); close(true); }
    else if (e.key === 'ArrowDown' && all.length) { e.preventDefault(); all[(i + 1) % all.length].focus(); }
    else if (e.key === 'ArrowUp' && all.length) { e.preventDefault(); all[(i - 1 + all.length) % all.length].focus(); }
  });
  panel.addEventListener('focusout', e => {
    // Not while the rows are being replaced after a −: the focused one is
    // among those going, and losing it is not leaving the panel.
    if (filling) return;
    if (current && !panel.contains(e.relatedTarget) && e.relatedTarget !== headOf(current)) close();
  });
  document.addEventListener('pointerdown', e => {
    if (current && !panel.contains(e.target) && !sheet.contains(e.target)) close();
  });
  window.addEventListener('resize', () => { if (current) place(current); });
  sheet.addEventListener('scroll', () => { if (current) place(current); }, { passive: true });
  sheet.addEventListener('transitionend', e => { if (current && e.target === sheet) place(current); });
  // The sidebar put away (unpinned and closed) takes the panel with it.
  new MutationObserver(() => { if (current && !showing()) close(); })
    .observe(sheet, { attributes: true, attributeFilter: ['aria-hidden'] });
  window.addEventListener('hashchange', () => setTimeout(markGroups, 0));

  // While the search box has words in it the groups open in place, which is
  // what nav.js does with a search; the panel waits until it is empty again.
  const q = sheet.querySelector('#navQ');
  if (q) q.addEventListener('input', () => {
    body.classList.toggle('nav-searching', !!q.value.trim());
    if (q.value.trim()) close();
  });

  // ------------------------------------------------- after a − or a ＋
  // nav.js has drawn the list again (docfind:navchange): the group that was
  // open is a new element now. The panel stays on it, quietly -- no entrance
  // again -- or goes, if the group has gone (its last entry taken out, or
  // left with one and so drawn as a single entry).
  document.addEventListener('docfind:navchange', () => {
    const was = current && nameOf(current);
    const focusX = panel.contains(document.activeElement) && document.activeElement.matches('.nav-x');
    apply();
    if (!was) return;
    const g = groups().find(x => nameOf(x) === was);
    if (!g || !active()) { current = null; panel.classList.add('out'); panel.hidden = true; return; }
    current = g;
    const head = headOf(g);
    head.setAttribute('aria-expanded', 'true');
    head.setAttribute('aria-controls', 'navMega');
    fill(g, true);
    place(g);
    if (focusX) { const x = links.querySelector('.nav-x'); if (x) x.focus(); }
  });

  // ------------------------------------------------------------ the option
  const pin = sheet.querySelector('[data-pin]');
  const btn = document.createElement('button');
  btn.type = 'button';
  btn.className = 'navpin';
  btn.setAttribute('data-mega', '');
  btn.innerHTML = svg('layout-grid');
  if (pin) pin.after(btn);

  function apply() {
    const on = chosen() && DESK.matches;
    body.classList.toggle('nav-mega', on);
    // One name, and the pressed state says whether it is on.
    btn.setAttribute('aria-pressed', chosen() ? 'true' : 'false');
    btn.setAttribute('aria-label', 'منوی گسترده');
    btn.title = chosen() ? 'منوی گسترده: گروه‌ها در پنل کنار فهرست باز می‌شوند' : 'منوی گسترده خاموش است: گروه‌ها در همان فهرست باز می‌شوند';
    markGroups();
    for (const g of groups()) {
      // In the panel's place a group is a single row; back as an accordion,
      // the group holding this page opens, as nav.js opens it, and so does
      // one the reader had open.
      const open = !on && (g.classList.contains('open') || !!g.querySelector('a.dest.on'));
      g.classList.toggle('open', open);
      headOf(g).setAttribute('aria-expanded', String(on ? g === current : open));
    }
    if (!on) close();
  }
  btn.addEventListener('click', () => {
    try { localStorage.setItem(KEY, chosen() ? '0' : '1'); } catch (_) {}
    apply();
  });
  DESK.addEventListener('change', apply);

  apply();
})();
