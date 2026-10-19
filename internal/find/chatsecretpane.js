// chatsecretpane.js: the open secret chat (SPEC 6.12): the lock in the
// header, the messages drawn as text nodes only, the self-destruct rings,
// the timer menu, the key fingerprint and the question before the chat ends.
// The protocol (chatsecret.js) lends this module its actions via initPane.
import { reduced, enter, fly, flip } from '/chatcore.js';
import { $, el, fa, svg, S, timeFmt, toast } from '/chatstate.js';
import { header, paintStatus } from '/chatpane.js';
import { openMenu, closeOverlay } from '/chatmenu.js';
import { fingerprintHex, fingerprintImage } from '/chatsecretcrypto.js';
import { SECRET_CSS } from '/chatsecretcss.js';

let api = null;
export const initPane = a => { api = a; };
export const LOCK = '<rect x="5" y="11" width="14" height="10" rx="2"/><path d="M8 11V7a4 4 0 0 1 8 0v4"/>';
const TIMER = '<circle cx="12" cy="13" r="8"/><path d="M12 9v4l2 2M9 2h6"/>';
const SEND = '<svg class="i-send" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M3 20.5l18-8.5L3 3.5l.01 6.6L15 12 3.01 13.9z" transform="scale(-1,1) translate(-24,0)"/></svg>';
export const TTLS = [0, 1, 2, 5, 10, 30, 60, 3600, 86400, 604800];
export const ttlText = s => !s ? 'خاموش' : s < 60 ? `${fa(s)} ثانیه` : s < 3600 ? `${fa(s / 60)} دقیقه` : s < 86400 ? `${fa(s / 3600)} ساعت` : s < 604800 ? `${fa(s / 86400)} روز` : `${fa(s / 604800)} هفته`;
const ttlShort = s => !s ? '' : s < 60 ? fa(s) : s < 3600 ? fa(s / 60) + 'د' : s < 86400 ? fa(s / 3600) + 'س' : s < 604800 ? fa(s / 86400) + 'ر' : fa(s / 604800) + 'ه';

const style = document.createElement('style'); style.textContent = SECRET_CSS; document.head.appendChild(style);

let cur = 0, flyFrom = null;
const peerName = () => { const c = S.chats.get(cur); return (c && (c.title || (c.peer && c.peer.name))) || ''; };
const showing = () => S.active === cur && !!document.querySelector('#m-msgs.m-secret-msgs');

// ------------------------------------------------------------ open
export function openPane(chat) {
  cur = chat.id;
  const pane = $('m-chat');
  const head = header(chat);
  const lock = el('span', 'm-lock'); lock.innerHTML = svg(LOCK); lock.setAttribute('aria-label', 'محرمانه');
  head.querySelector('.m-title b').prepend(lock);
  const more = el('button', 'm-icon-btn'); more.type = 'button'; more.setAttribute('aria-label', 'گزینه‌های گفت‌وگوی محرمانه');
  more.innerHTML = svg('<circle cx="12" cy="5" r="1"/><circle cx="12" cy="12" r="1"/><circle cx="12" cy="19" r="1"/>');
  more.addEventListener('click', () => chatMenu(more));
  head.appendChild(more);
  const msgs = el('div', 'm-msgs m-secret-msgs'); msgs.id = 'm-msgs'; msgs.setAttribute('role', 'log'); msgs.setAttribute('aria-live', 'polite');
  pane.replaceChildren(head, msgs, composer());
  wireList(msgs);
  if (!reduced() && matchMedia('(min-width: 901px)').matches) msgs.animate([{ opacity: .4 }, { opacity: 1 }], { duration: 120 });
  paint({ bottom: true });
  paintStatus();
}

// paint brings the list in line with the chat's messages: new ones are added
// in place (an arriving one scales in, a sent one flies from the input), gone
// ones shrink away and the rest close the gap with FLIP.
export function paint(opts = {}) {
  const box = $('m-msgs');
  if (!api || !box || !box.classList.contains('m-secret-msgs') || S.active !== cur) return;
  const rec = api.rec(cur);
  let top = box.querySelector('.m-secret-intro');
  const fresh = intro(rec);
  if (top) top.replaceWith(fresh); else box.prepend(fresh);
  top = fresh;
  const foot = document.querySelector('.m-secret-foot');
  if (foot) foot.hidden = !rec || rec.state !== 'active';
  paintTimer();
  const list = rec ? api.views(cur) : [];
  const want = new Set(list.map(v => v.key));
  const old = new Map([...box.querySelectorAll('[data-key]')].map(n => [n.dataset.key, n]));
  for (const [k, n] of old) if (!want.has(k)) leave(box, n);
  const near = box.scrollHeight - box.scrollTop - box.clientHeight < 80;
  let prev = top; const added = [];
  for (const v of list) {
    let n = old.get(v.key);
    if (n) update(n, v); else { n = node(v); added.push([n, v]); }
    if (prev.nextElementSibling !== n) prev.after(n);
    prev = n;
  }
  if (near || opts.bottom || added.some(([, v]) => v.out)) box.scrollTop = box.scrollHeight;
  for (const [n, v] of added) {
    if (opts.bottom) continue;
    if (v.out && flyFrom) { fly(flyFrom, n); flyFrom = null; } else enter(n);
  }
  if (!document.hidden && rec && rec.state === 'active') api.seen(cur);
}

function leave(box, n) {
  delete n.dataset.key; n.classList.add('m-leaving');
  const done = () => flip(box, () => n.remove());
  if (document.hidden) { n.remove(); return; }
  const frames = reduced() ? [{ opacity: 1 }, { opacity: 0 }] : [{ transform: 'none', opacity: 1 }, { transform: 'scale(.8)', opacity: 0 }];
  n.animate(frames, { duration: reduced() ? 100 : 240, easing: 'cubic-bezier(0.2, 0, 0, 1)', fill: 'forwards' }).finished.then(done, done);
}

// ended shows that a chat this pane holds is over.
export function paneEnded(id) { if (S.active === id && cur === id) paint(); }

// ------------------------------------------------------------ pieces
function intro(rec) {
  const c = el('div', 'm-secret-intro');
  const t = el('b'); const ic = el('span', 'm-lock'); ic.innerHTML = svg(LOCK);
  t.append(ic, document.createTextNode('گفت‌وگوی محرمانه'));
  c.appendChild(t);
  const name = peerName();
  let lines;
  if (!rec) lines = ['این گفت‌وگوی محرمانه پایان یافته است و پیام‌هایش از این دستگاه پاک شده‌اند.'];
  else if (rec.state === 'requested') lines = [`در انتظار ${name}: وقتی یکی از دستگاه‌هایش آنلاین شود و گفت‌وگو را باز کند، کلیدها ساخته می‌شوند.`];
  else lines = [
    'پیام‌ها روی همین دستگاه رمز و باز می‌شوند؛ سرور فقط متن رمزشده را جابه‌جا می‌کند.',
    'فقط روی همین دستگاه؛ دستگاه‌های دیگر شما آن را نمی‌بینند.',
    'با تایمر خودنابودی، پیام پس از خوانده‌شدن از هر دو دستگاه پاک می‌شود.',
    'پیام‌ها به گفت‌وگوی دیگری هدایت نمی‌شوند.',
  ];
  const ul = el('ul'); for (const l of lines) ul.appendChild(el('li', '', l));
  c.appendChild(ul);
  return c;
}

function node(v) {
  if (v.t !== 'text') {
    const n = el('div', 'm-secret-note', noteText(v)); n.dataset.key = v.key; return n;
  }
  const b = el('div', 'm-bubble' + (v.out ? ' me' : '')); b.dataset.key = v.key; b.tabIndex = -1;
  const t = el('span', 'm-text', v.text); t.dir = 'auto';
  b.append(t, meta(v));
  return b;
}

function update(n, v) {
  if (!n.classList.contains('m-bubble')) return;
  const m = n.querySelector('.m-meta'), fresh = meta(v);
  if (m.dataset.sig !== fresh.dataset.sig) {
    const ring = m.querySelector('.m-ttl[data-armed]');
    if (ring && v.expires) fresh.querySelector('.m-ttl')?.replaceWith(ring);
    const wasSent = m.dataset.sig.startsWith('sent|');
    m.replaceWith(fresh);
    const t = fresh.querySelector('.m-ticks.read');
    if (wasSent && t && !reduced()) { // the second tick draws from the first
      t.classList.remove('read');
      requestAnimationFrame(() => requestAnimationFrame(() => t.classList.add('read')));
    }
  }
}

function meta(v) {
  const m = el('span', 'm-meta');
  m.dataset.sig = [v.state, v.expires].join('|');
  if (v.ttl) m.appendChild(ring(v));
  m.appendChild(el('span', '', timeFmt.format(new Date(v.at))));
  if (v.out) m.appendChild(ticks(v.state));
  return m;
}

function ticks(state) {
  const s = el('span');
  if (state === 'queued') s.innerHTML = '<svg class="m-clock" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" role="img" aria-label="در صف ارسال"><circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/></svg>';
  else s.innerHTML = `<svg class="m-ticks${state === 'read' ? ' read' : ''}" viewBox="0 0 18 12" role="img" aria-label="${state === 'read' ? 'خوانده شد' : 'ارسال شد'}"><path d="M1 6.5l3.2 3.2L11 2.5"/><path class="t2" d="M6.8 9.7L7.4 10.3 15.6 2.5"/></svg>`;
  return s.firstChild;
}

// ring is the self-destruct timer: a circle that drains until the message goes.
function ring(v) {
  const s = el('span', 'm-ttl'); s.title = 'تایمر خودنابودی: ' + ttlText(v.ttl);
  s.innerHTML = '<svg viewBox="0 0 20 20" aria-hidden="true"><circle class="bg" cx="10" cy="10" r="7"/><circle class="fg" cx="10" cy="10" r="7" pathLength="100"/></svg>';
  if (!v.expires) return s;
  const fg = s.querySelector('.fg'), total = v.ttl * 1000, left = Math.max(0, v.expires - Date.now());
  const from = Math.min(100, 100 * (1 - left / total));
  s.dataset.armed = '1';
  if (reduced()) { fg.style.strokeDashoffset = String(from); return s; }
  fg.animate([{ strokeDashoffset: from }, { strokeDashoffset: 100 }], { duration: left, easing: 'linear', fill: 'forwards' });
  return s;
}

function noteText(v) {
  const who = v.out ? 'شما' : peerName();
  if (v.t === 'shot') return v.out ? 'شما از صفحه عکس گرفتید.' : `${who} از صفحه عکس گرفت.`;
  if (v.t === 'ttl') {
    if (!v.ttl) return v.out ? 'شما تایمر خودنابودی را خاموش کردید.' : `${who} تایمر خودنابودی را خاموش کرد.`;
    return v.out ? `شما تایمر خودنابودی را روی ${ttlText(v.ttl)} گذاشتید.` : `${who} تایمر خودنابودی را روی ${ttlText(v.ttl)} گذاشت.`;
  }
  return '';
}

// ------------------------------------------------------------ composer
// paintTimer shows the chat's timer on its button; either side may change it.
function paintTimer() {
  const timer = document.querySelector('.m-secret-foot .m-ttl-btn'); if (!timer) return;
  const rec = api.rec(cur), t = rec ? rec.ttl || 0 : 0;
  if (timer.dataset.ttl === String(t)) return;
  timer.dataset.ttl = String(t);
  timer.innerHTML = svg(TIMER); timer.appendChild(el('span', 'm-ttl-n', ttlShort(t)));
  timer.setAttribute('aria-label', 'تایمر خودنابودی: ' + ttlText(t)); timer.classList.toggle('on', !!t);
}

function composer() {
  const foot = el('div', 'm-foot m-secret-foot');
  const row = el('div', 'm-compose');
  const timer = el('button', 'm-icon-btn m-ttl-btn'); timer.type = 'button';
  timer.innerHTML = svg(TIMER); timer.setAttribute('aria-label', 'تایمر خودنابودی');
  timer.addEventListener('click', () => {
    const r = timer.getBoundingClientRect(), rec = api.rec(cur);
    openMenu({ x: r.left, y: r.top, items: TTLS.map(s => ({ icon: s === (rec && rec.ttl || 0) ? '<path d="M5 12l5 5L20 7"/>' : TIMER, label: ttlText(s), run: () => api.setTtl(cur, s).then(paintTimer) })) });
  });
  const ta = el('textarea'); ta.rows = 1; ta.placeholder = 'پیام محرمانه'; ta.setAttribute('aria-label', 'پیام محرمانه'); ta.maxLength = 4096; ta.dir = 'auto';
  const send = el('button', 'm-send'); send.type = 'button'; send.setAttribute('aria-label', 'ارسال');
  send.innerHTML = svg(LOCK, 'i-mic') + SEND;
  let typed = 0;
  const fit = () => { ta.style.height = 'auto'; ta.style.height = Math.min(ta.scrollHeight + 2, 220) + 'px'; send.classList.toggle('ready', ta.value.trim() !== ''); };
  ta.addEventListener('input', () => { fit(); if (Date.now() - typed > 4000 && ta.value) { typed = Date.now(); api.typing(cur); } });
  const go = () => {
    const text = ta.value.trim(); if (!text) return;
    flyFrom = ta.getBoundingClientRect();
    ta.value = ''; fit();
    api.send(cur, text).catch(() => toast('پیام رمزشده ساخته نشد.'));
  };
  ta.addEventListener('keydown', e => { if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) { e.preventDefault(); go(); } });
  send.addEventListener('click', go);
  row.append(timer, ta, send); foot.appendChild(row);
  return foot;
}

// ------------------------------------------------------------ menus
function chatMenu(btn) {
  const rec = api.rec(cur); if (!rec) return;
  const r = btn.getBoundingClientRect();
  const items = [];
  if (rec.state === 'active') items.push({ icon: '<path d="M2 12C2 6.5 6.5 2 12 2a10 10 0 0 1 8 4M5 19.5C5.5 18 6 15 6 12a6 6 0 0 1 3.3-5.4M17.3 21.5c.2-1 .7-3.6.7-5.5M12 12c0 3-.4 6-1.5 8.5M12 7a5 5 0 0 1 5 5"/>', label: 'اثر انگشت کلید', run: () => fingerprint(rec) });
  items.push({ icon: '<path d="M3 6h18M8 6V4h8v2M19 6l-1 14H6L5 6"/>', label: 'پایان گفت‌وگوی محرمانه', danger: true, run: () => askEnd() });
  openMenu({ x: r.left, y: r.bottom, items });
}

function bubbleMenu(b, x, y) {
  const list = api.views(cur), v = list.find(m => m.key === b.dataset.key); if (!v) return;
  openMenu({
    x, y, bubble: b, items: [
      { icon: '<rect x="9" y="9" width="12" height="12" rx="2"/><path d="M5 15V5a2 2 0 0 1 2-2h10"/>', label: 'رونوشت', run: () => navigator.clipboard?.writeText(v.text).then(() => toast('رونوشت شد.'), () => { }) },
      { icon: '<path d="M3 6h18M8 6V4h8v2M19 6l-1 14H6L5 6"/>', label: 'حذف برای هر دو', danger: true, run: () => api.del(cur, [v.key]) },
    ]
  });
}

function wireList(box) {
  box.addEventListener('contextmenu', e => { const b = e.target.closest('.m-bubble[data-key]'); if (!b) return; e.preventDefault(); bubbleMenu(b, e.clientX, e.clientY); });
  let press;
  box.addEventListener('pointerdown', e => {
    const b = e.target.closest('.m-bubble[data-key]'); if (!b || e.pointerType === 'mouse') return;
    press = setTimeout(() => bubbleMenu(b, e.clientX, e.clientY), 450);
  });
  for (const t of ['pointerup', 'pointercancel', 'pointermove']) box.addEventListener(t, () => clearTimeout(press));
  box.addEventListener('scroll', () => { if (box.scrollHeight - box.scrollTop - box.clientHeight < 80) api.seen(cur); }, { passive: true });
}

// modal is a small dialog in the page's style; Escape or a press outside closes it.
function modal(title, body, actions) {
  closeOverlay();
  const back = el('div', 'm-ov m-modal-back');
  const card = el('div', 'm-modal'); card.setAttribute('role', 'dialog'); card.setAttribute('aria-modal', 'true');
  const h = el('h2', 'm-modal-title', title); h.id = 'm-secret-title'; card.setAttribute('aria-labelledby', h.id);
  const row = el('div', 'm-modal-actions');
  card.append(h, ...body, row); back.appendChild(card); document.body.appendChild(back);
  const prev = document.activeElement;
  const close = () => {
    removeEventListener('keydown', esc, true);
    const fin = () => { back.remove(); prev && prev.focus && prev.focus(); };
    if (reduced()) fin(); else back.animate([{ opacity: 1 }, { opacity: 0 }], { duration: 120 }).finished.then(fin, fin);
  };
  const esc = e => { if (e.key === 'Escape') { e.preventDefault(); close(); } };
  addEventListener('keydown', esc, true);
  back.addEventListener('click', e => { if (e.target === back) close(); });
  for (const [label, cls, run] of actions) {
    const b = el('button', 'btn ' + cls, label); b.type = 'button';
    b.addEventListener('click', () => { close(); run && run(); });
    row.appendChild(b);
  }
  if (!reduced()) card.animate([{ transform: 'scale(.9)', opacity: 0 }, { transform: 'none', opacity: 1 }], { duration: 140, easing: 'cubic-bezier(0.2, 0, 0, 1)' });
  queueMicrotask(() => row.lastChild.focus());
}

function fingerprint(rec) {
  const img = fingerprintImage(rec.fp, 176); img.setAttribute('aria-label', 'تصویر اثر انگشت کلید');
  const code = el('pre', 'm-fp-hex', fingerprintHex(rec.fp)); code.dir = 'ltr';
  const p = el('p', 'm-modal-text', `این تصویر و این رشته را با دستگاه ${peerName()} مقایسه کنید. اگر هر دو یکی است، کسی میان شما نیست و پیام‌ها فقط روی همین دو دستگاه باز می‌شوند.`);
  modal('اثر انگشت کلید', [img, code, p], [['بستن', '', null]]);
}

function askEnd() {
  const p = el('p', 'm-modal-text', `گفت‌وگوی محرمانه با ${peerName()} پایان یابد؟ کلیدها و همهٔ پیام‌ها از هر دو دستگاه پاک می‌شوند.`);
  modal('پایان گفت‌وگوی محرمانه', [p], [['انصراف', '', null], ['پایان', 'm-danger', () => api.close(cur)]]);
}

// ------------------------------------------------------------ screen guard
// A browser cannot stop a screenshot. Where the platform tells the page (the
// Print Screen key on a desktop), the other side is told; while the page is
// hidden (the app switcher takes a picture) the messages are blurred.
addEventListener('keyup', e => { if (e.key === 'PrintScreen' && showing()) api.shot(cur); });
document.addEventListener('visibilitychange', () => {
  document.body.classList.toggle('m-secret-hide', document.hidden);
  if (!document.hidden && showing()) api.seen(cur);
});
