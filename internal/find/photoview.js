// photoview.js: somebody's profile pictures, full screen (asked for on
// 2026-10-19 with Telegram's viewer as the model): the picture, whose and
// when in a glass capsule, a strip of all of them to move between, a way to
// keep one -- and on one's own, making an older one current and letting one
// go. The profile (chatperson.js) and the settings page open it; the pictures
// themselves are photos.go's gallery.
//
// Its look travels with it (one <style>, added once), so a page that opens it
// needs nothing else.
const enc = encodeURIComponent;
export const smallURL = (user, id) => `/api/account/photo?u=${enc(user)}&id=${id}`;
export const fullURL = (user, id) => `/api/account/photo?u=${enc(user)}&id=${id}&size=full`;

// listPhotos is somebody's pictures, newest (current) first; [] when none or
// unreachable.
export async function listPhotos(user) {
  try {
    const r = await fetch('/api/account/photos?u=' + enc(user), { credentials: 'same-origin' });
    return r.ok ? ((await r.json()).photos || []) : [];
  } catch { return []; }
}

const day = new Intl.DateTimeFormat('fa-IR-u-ca-persian', { year: 'numeric', month: 'long', day: 'numeric' });
const reduced = () => matchMedia('(prefers-reduced-motion: reduce)').matches;
const svg = d => `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${d}</svg>`;
const IC = {
  back: '<path d="M9 6l6 6-6 6"/>',
  save: '<path d="M12 3v12"/><path d="M7 10l5 5 5-5"/><path d="M5 21h14"/>',
  star: '<path d="M12 3l2.7 5.6 6.1.9-4.4 4.3 1 6.1L12 17l-5.4 2.9 1-6.1-4.4-4.3 6.1-.9z"/>',
  trash: '<path d="M3 6h18"/><path d="M8 6V4h8v2"/><path d="M6 6l1 14h10l1-14"/>',
};

const CSS = `
.pv { position: fixed; inset: 0; z-index: 90; background: #000; color: #fff; display: grid; grid-template-rows: auto minmax(0,1fr) auto; touch-action: none; }
.pv-top { display: flex; align-items: center; gap: 10px; padding: calc(10px + env(safe-area-inset-top, 0px)) 14px 10px; }
.pv-btn { all: unset; box-sizing: border-box; cursor: pointer; width: 46px; height: 46px; border-radius: 50%; display: grid; place-items: center; flex: none;
  background: rgb(44 44 48 / .72); box-shadow: inset 0 1px 0 rgb(255 255 255 / .14), 0 6px 20px rgb(0 0 0 / .4); color: #fff; }
@supports ((-webkit-backdrop-filter: blur(1px)) or (backdrop-filter: blur(1px))) {
  .pv-btn, .pv-cap { background: rgb(44 44 48 / .5); -webkit-backdrop-filter: blur(20px) saturate(170%); backdrop-filter: blur(20px) saturate(170%); }
}
.pv-btn svg { width: 22px; height: 22px; }
.pv-btn:focus-visible { outline: 2px solid #5b9cff; outline-offset: 2px; }
.pv-btn.arm { background: #d93a3a; }
.pv-cap { flex: 1; min-width: 0; max-width: 340px; margin-inline: auto; display: grid; justify-items: center; padding: 7px 18px; border-radius: 999px;
  background: rgb(44 44 48 / .72); box-shadow: inset 0 1px 0 rgb(255 255 255 / .14); text-align: center; }
.pv-cap b { font-size: 1.02rem; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; max-width: 100%; }
.pv-cap span { font-size: .8rem; color: #b8b8bf; }
.pv-sp { width: 46px; flex: none; }
.pv-stage { display: grid; place-items: center; overflow: hidden; }
.pv-img { max-width: 100%; max-height: 100%; width: 100%; aspect-ratio: 1; object-fit: contain; user-select: none; -webkit-user-drag: none; transition: transform .22s cubic-bezier(.2,0,0,1), opacity .22s; }
.pv-foot { display: grid; gap: 12px; padding: 10px 14px calc(14px + env(safe-area-inset-bottom, 0px)); }
.pv-strip { display: flex; gap: 6px; justify-content: center; overflow-x: auto; scrollbar-width: none; padding: 2px; }
.pv-strip::-webkit-scrollbar { display: none; }
.pv-th { all: unset; cursor: pointer; flex: none; width: 34px; height: 46px; border-radius: 6px; overflow: hidden; opacity: .55; transition: opacity .18s, transform .18s; }
.pv-th img { width: 100%; height: 100%; object-fit: cover; display: block; }
.pv-th.on { opacity: 1; transform: scale(1.12); box-shadow: 0 0 0 2px #fff; }
.pv-th:focus-visible { outline: 2px solid #5b9cff; outline-offset: 2px; }
.pv-acts { display: flex; gap: 12px; justify-content: space-between; align-items: center; }
.pv-acts .pv-grow { flex: 1; }
.pv-note { font-size: .82rem; color: #d8d8de; text-align: center; min-height: 1.2em; }
@media (prefers-reduced-motion: reduce) { .pv-img, .pv-th { transition: none; } }
`;
function style() {
  if (document.getElementById('pv-css')) return;
  const s = document.createElement('style'); s.id = 'pv-css'; s.textContent = CSS; document.head.appendChild(s);
}
const make = (tag, cls, text) => { const e = document.createElement(tag); if (cls) e.className = cls; if (text != null) e.textContent = text; return e; };
function btn(icon, label) {
  const b = make('button', 'pv-btn'); b.type = 'button'; b.innerHTML = svg(icon); b.setAttribute('aria-label', label); b.title = label; return b;
}

// viewPhotos opens the viewer. own: the signed-in person's own pictures, with
// «تصویر اصلی» and «حذف»; onChange runs after either, so the page can redraw.
export async function viewPhotos({ user, name, photos, start = 0, own = false, onChange } = {}) {
  photos = photos && photos.length ? photos.slice() : await listPhotos(user);
  if (!photos.length) return;
  style();
  const prev = document.activeElement;
  const box = make('div', 'pv'); box.setAttribute('role', 'dialog'); box.setAttribute('aria-modal', 'true');
  box.setAttribute('aria-label', 'تصویرهای ' + (name || user)); box.dir = 'rtl';
  const top = make('div', 'pv-top');
  const close = btn(IC.back, 'بستن');
  const cap = make('div', 'pv-cap'); const who = make('b', '', name || user); who.dir = 'auto'; const when = make('span');
  cap.append(who, when);
  top.append(close, cap, make('span', 'pv-sp'));
  const stage = make('div', 'pv-stage'); const img = make('img', 'pv-img'); img.alt = ''; img.draggable = false; stage.appendChild(img);
  const foot = make('div', 'pv-foot'); const strip = make('div', 'pv-strip'); const note = make('div', 'pv-note'); note.setAttribute('role', 'status');
  const acts = make('div', 'pv-acts');
  const save = make('a', 'pv-btn'); save.innerHTML = svg(IC.save); save.setAttribute('aria-label', 'ذخیرهٔ تصویر'); save.title = 'ذخیرهٔ تصویر';
  const main = own ? btn(IC.star, 'تصویر اصلی شود') : null;
  const del = own ? btn(IC.trash, 'حذف این تصویر') : null;
  acts.append(save, make('span', 'pv-grow'), ...(main ? [main] : []), ...(del ? [del] : []));
  foot.append(strip, note, acts);
  box.append(top, stage, foot);
  document.body.appendChild(box);
  if (!reduced()) box.animate([{ opacity: 0 }, { opacity: 1 }], { duration: 180, easing: 'linear' });

  let at = Math.min(Math.max(0, start), photos.length - 1), armed = 0;
  const say = t => { note.textContent = t || ''; };
  function paintStrip() {
    strip.replaceChildren(...photos.map((p, i) => {
      const b = make('button', 'pv-th' + (i === at ? ' on' : '')); b.type = 'button';
      b.setAttribute('aria-label', 'تصویر ' + (i + 1).toLocaleString('fa-IR')); if (i === at) b.setAttribute('aria-current', 'true');
      const t = make('img'); t.alt = ''; t.loading = 'lazy'; t.src = smallURL(user, p.id); b.appendChild(t);
      b.addEventListener('click', () => show(i));
      return b;
    }));
    strip.hidden = photos.length < 2;
  }
  function show(i) {
    at = (i + photos.length) % photos.length;
    const p = photos[at];
    img.src = smallURL(user, p.id); // already drawn elsewhere, so at once; then sharp
    const sharp = new Image(); sharp.onload = () => { if (photos[at] === p) img.src = sharp.src; }; sharp.src = fullURL(user, p.id);
    when.textContent = p.at ? day.format(new Date(p.at)) : '';
    save.href = fullURL(user, p.id); save.setAttribute('download', (name || user) + '-' + (at + 1) + '.jpg');
    if (main) main.hidden = at === 0;
    disarm(); say('');
    paintStrip();
    const on = strip.querySelector('.on'); if (on) on.scrollIntoView({ inline: 'center', block: 'nearest' });
  }
  function disarm() { if (del) { del.classList.remove('arm'); del.setAttribute('aria-label', 'حذف این تصویر'); } armed = 0; }
  async function refresh(go) {
    photos = await listPhotos(user);
    if (onChange) try { onChange(photos); } catch { }
    if (!photos.length) { shut(); return; }
    show(Math.min(go, photos.length - 1));
  }
  if (main) main.addEventListener('click', async () => {
    const p = photos[at];
    const r = await fetch('/api/account/photo/current?id=' + p.id, { method: 'POST', credentials: 'same-origin' }).catch(() => null);
    if (!r || !r.ok) { say('انجام نشد؛ دوباره امتحان کنید.'); return; }
    await refresh(0); say('تصویر اصلی شد.');
  });
  if (del) del.addEventListener('click', async () => {
    // Twice, not a dialog: the second press within a few seconds is the yes.
    if (!armed) {
      del.classList.add('arm'); del.setAttribute('aria-label', 'برای حذف دوباره بزنید');
      say('برای حذف این تصویر دوباره بزنید.');
      armed = setTimeout(() => { disarm(); say(''); }, 3500);
      return;
    }
    clearTimeout(armed); disarm();
    const r = await fetch('/api/account/photo?id=' + photos[at].id, { method: 'DELETE', credentials: 'same-origin' }).catch(() => null);
    if (!r || !r.ok) { say('حذف نشد؛ دوباره امتحان کنید.'); return; }
    await refresh(at); say('تصویر حذف شد.');
  });

  // A swipe moves between them (in Persian the next one is to the left, so a
  // swipe to the right brings it); down puts the viewer away.
  let x0 = 0, y0 = 0, dragging = false;
  stage.addEventListener('pointerdown', e => { dragging = true; x0 = e.clientX; y0 = e.clientY; stage.setPointerCapture(e.pointerId); });
  stage.addEventListener('pointermove', e => {
    if (!dragging) return;
    const dx = e.clientX - x0, dy = e.clientY - y0;
    img.style.transition = 'none';
    img.style.transform = Math.abs(dy) > Math.abs(dx) && dy > 0 ? `translateY(${dy}px) scale(${Math.max(.8, 1 - dy / 900)})` : `translateX(${dx}px)`;
    img.style.opacity = dy > 0 && Math.abs(dy) > Math.abs(dx) ? String(Math.max(.4, 1 - dy / 500)) : '1';
  });
  const end = e => {
    if (!dragging) return; dragging = false;
    const dx = e.clientX - x0, dy = e.clientY - y0;
    img.style.transition = ''; img.style.transform = ''; img.style.opacity = '';
    if (dy > 110 && Math.abs(dy) > Math.abs(dx)) { shut(); return; }
    if (Math.abs(dx) > 60 && photos.length > 1) show(at + (dx > 0 ? 1 : -1));
  };
  stage.addEventListener('pointerup', end);
  stage.addEventListener('pointercancel', end);
  const keys = e => {
    if (e.key === 'Escape') { e.preventDefault(); shut(); }
    else if (e.key === 'ArrowLeft') show(at + 1);
    else if (e.key === 'ArrowRight') show(at - 1);
  };
  document.addEventListener('keydown', keys, true);
  close.addEventListener('click', shut);
  function shut() {
    document.removeEventListener('keydown', keys, true);
    clearTimeout(armed);
    const done = () => { box.remove(); if (prev && prev.focus) prev.focus(); };
    if (reduced()) done(); else box.animate([{ opacity: 1 }, { opacity: 0 }], { duration: 160, easing: 'linear' }).onfinish = done;
  }
  show(at);
  close.focus();
}
