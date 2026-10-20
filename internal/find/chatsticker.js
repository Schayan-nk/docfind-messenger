// chatsticker.js: stickers and GIFs (SPEC 6.4, 8.8). The panel above the
// message bar has two tabs: the sticker packs (with a row of recent ones) and
// the GIFs (searched only in DocFind's own packs). Animated stickers are
// Lottie, played by the vendored lottie-web light build, which has no
// expression engine; the server strips expressions on upload as well
// (security check 16). Every file comes from DocFind (check 17).
//
// Motion: a sticker plays once when it comes into view and again on a tap; at
// most 3 (phone) or 6 (desktop) play at once, the rest show their first
// frame; nothing plays while the tab is hidden; with reduced motion nothing
// plays by itself. A sent sticker flies from the panel to its place.
import { uuidv7, reduced, fly, flip } from '/chatcore.js';
import { $, el, svg, S, hooks, toast } from '/chatstate.js';
import { api } from '/chatgroup.js';

const media = id => `/api/chat/media/${id}`;
const fail = e => toast((e && e.text) || 'انجام نشد؛ دوباره تلاش کنید.');
const MAX_PLAY = () => matchMedia('(max-width: 900px)').matches ? 3 : 6;

// ------------------------------------------------------------ Lottie
let lottieP;
function lottieLib() {
  return lottieP ||= new Promise((ok, no) => {
    if (window.lottie) { ok(window.lottie); return; }
    const s = Object.assign(document.createElement('script'), { src: '/static/vendor/lottie/lottie_light.min.js' });
    s.onload = () => ok(window.lottie); s.onerror = () => { lottieP = null; no(); };
    document.head.appendChild(s);
  });
}
const jsonCache = new Map(); // media id -> Promise<animation data>
const animData = id => { if (!jsonCache.has(id)) jsonCache.set(id, fetch(media(id), { credentials: 'same-origin' }).then(r => { if (!r.ok) throw 0; return r.json(); })); return jsonCache.get(id); };

const live = new Set(); // {node, anim, playing, video}
let playing = 0;
const io = new IntersectionObserver(ents => {
  for (const e of ents) { const it = e.target._x; if (it) { it.visible = e.isIntersecting; if (e.isIntersecting && !it.played) play(it); else if (!e.isIntersecting) pause(it); } }
  sweep();
}, { threshold: 0.5 });
function sweep() { for (const it of live) if (!it.node.isConnected) { it.anim && it.anim.destroy(); live.delete(it); if (it.playing) playing--; } }

document.addEventListener('visibilitychange', () => { if (document.hidden) for (const it of live) pause(it); });

function play(it, force) {
  if (document.hidden || (!force && reduced()) || it.playing) return;
  if (playing >= MAX_PLAY()) return; // stays on its first frame
  it.played = true; it.playing = true; playing++;
  if (it.anim) { it.anim.goToAndPlay(0, true); }
  else if (it.video) { it.video.currentTime = 0; it.video.play().catch(() => done(it)); }
}
function pause(it) {
  if (!it.playing) return;
  if (it.anim) it.anim.pause(); else if (it.video && !it.loop) it.video.pause();
  if (!it.loop) done(it);
}
function done(it) { if (it.playing) { it.playing = false; playing = Math.max(0, playing - 1); } }

// mount shows one pack file in node: a picture, a Lottie animation or a video.
function mount(node, item, opts = {}) {
  const fmt = item.format, id = item.media;
  const it = { node, loop: !!opts.loop };
  if (fmt === 'webp' || fmt === 'gif') {
    const img = el('img'); img.alt = item.emoji || ''; img.loading = 'lazy'; img.decoding = 'async'; img.src = media(id);
    node.appendChild(img); return;
  }
  if (fmt === 'webm' || fmt === 'mp4') {
    const v = el('video'); v.muted = true; v.playsInline = true; v.loop = it.loop; v.preload = 'metadata'; v.src = media(id);
    v.setAttribute('aria-label', item.emoji || 'گیف');
    v.addEventListener('ended', () => done(it));
    node.appendChild(v); it.video = v;
  } else if (fmt === 'tgs') {
    Promise.all([lottieLib(), animData(id)]).then(([lottie, data]) => {
      if (!node.isConnected && !opts.detached) return;
      it.anim = lottie.loadAnimation({ container: node, renderer: 'svg', loop: false, autoplay: false, animationData: structuredClone(data), rendererSettings: { progressiveLoad: true } });
      it.anim.setSubframe(false);
      it.anim.addEventListener('complete', () => done(it));
      it.anim.goToAndStop(0, true);
      if (it.visible && !it.played) play(it);
    }).catch(() => { node.appendChild(el('span', '', item.emoji || '🙂')); });
  } else return;
  sweep(); node._x = it; live.add(it); io.observe(node);
  node.addEventListener('click', e => { if (opts.tap !== false) { e.stopPropagation(); it.played = false; play(it, true); } });
}

// ------------------------------------------------------------ bubbles
// A redraw of the chat (a new message, a read receipt, a send) builds every
// bubble anew, and a GIF built anew loads again and starts from black: the
// GIFs "refreshing" (2026-10-19). The same message keeps the same box, already
// loaded and playing, moved into the new bubble.
const drawn = new Map();
export function drawSticker(m) {
  const x = m.extra || {}, md = m.media || (x.media ? { id: x.media, mime: x.mime } : {});
  const fmt = x.format || ({ 'image/webp': 'webp', 'application/json': 'tgs', 'video/webm': 'webm', 'image/gif': 'gif', 'video/mp4': 'mp4' })[md.mime];
  const gif = m.kind === 'gif';
  const key = (m.chat || S.active) + ':' + (m.id || m.cid) + ':' + (md.id || '');
  let box = drawn.get(key);
  if (!box) {
    box = el('div', gif ? 'x-gif' : 'x-sticker');
    box.setAttribute('role', 'img'); box.setAttribute('aria-label', gif ? 'گیف' : 'برچسب ' + (x.emoji || ''));
    if (gif && x.w && x.h) box.style.aspectRatio = `${x.w} / ${x.h}`;
    if (md.id && fmt) mount(box, { media: md.id, format: fmt, emoji: x.emoji }, { loop: gif });
    if (md.id) { drawn.set(key, box); if (drawn.size > 300) drawn.delete(drawn.keys().next().value); }
  }
  queueMicrotask(() => { const b = box.closest('.m-bubble'); if (b && !gif) b.classList.add('x-bare'); });
  return box;
}

// ------------------------------------------------------------ the panel
const packs = { sticker: null, gif: null };
let manage = false;
const recentKey = () => 'docfind-stickers-recent:' + S.me;
function recent() { try { return JSON.parse(localStorage.getItem(recentKey()) || '[]'); } catch { return []; } }
function remember(item) {
  const list = [item, ...recent().filter(r => r.id !== item.id)].slice(0, 16);
  try { localStorage.setItem(recentKey(), JSON.stringify(list)); } catch { }
}
async function loadPacks(kind, fresh) {
  if (packs[kind] && !fresh) return packs[kind];
  const j = await api('/api/chat/packs?kind=' + kind);
  manage = !!j.manage; packs[kind] = j.packs || [];
  return packs[kind];
}
export function forgetPacks() { packs.sticker = packs.gif = null; }

function tile(item, chat, panel) {
  const b = el('button', 'x-tile'); b.type = 'button'; b.setAttribute('aria-label', item.kind === 'gif' ? 'گیف ' + (item.keywords || '') : 'برچسب ' + (item.emoji || ''));
  mount(b, item, { loop: item.kind === 'gif', tap: false });
  let t, long = false; // a long press previews; its release must not also send
  b.addEventListener('click', () => { if (long) { long = false; return; } send(item, chat, b, panel); });
  b.addEventListener('pointerdown', () => { long = false; t = setTimeout(() => { long = true; preview(item); }, 450); });
  for (const ev of ['pointerup', 'pointerleave', 'pointercancel']) b.addEventListener(ev, () => clearTimeout(t));
  return b;
}

function preview(item) {
  const back = el('div', 'x-preview'), inner = el('div'); back.appendChild(inner);
  mount(inner, item, { loop: true, detached: true, tap: false });
  document.body.appendChild(back);
  if (!reduced()) inner.animate([{ transform: 'scale(.6)', opacity: 0 }, { transform: 'none', opacity: 1 }], { duration: 220, easing: 'cubic-bezier(0.34, 1.56, 0.64, 1)' });
  const close = () => { back.remove(); removeEventListener('pointerup', close, true); };
  addEventListener('pointerup', close, true);
  back.addEventListener('click', close);
}

async function send(item, chat, from, panel) {
  const rect = from.getBoundingClientRect();
  try {
    const r = await S.net.request('send_sticker', { chat: chat.id, cid: uuidv7(), item: item.id, topic: chat.forum ? (S.topic || 0) : 0 });
    if (item.kind === 'sticker') { remember(item); if (panel && !panel.hidden) paintStickers(panel, chat); }
    for (const m of r.messages || []) hooks.accept(m);
    const bubbles = $('m-msgs')?.querySelectorAll('.m-bubble'); const last = bubbles && bubbles[bubbles.length - 1];
    if (last) fly(rect, last);
  } catch (e) { fail(e); }
}

async function paintStickers(panel, chat) {
  const scroll = panel.querySelector('.x-scroll');
  let list; try { list = await loadPacks('sticker'); } catch (e) { scroll.replaceChildren(el('div', 'x-empty', 'برچسب‌ها بارگذاری نشد.')); return; }
  const nodes = [];
  const rec = recent();
  if (rec.length) {
    nodes.push(el('div', 'x-pack-title', 'اخیر'));
    const g = el('div', 'x-grid'); g.dataset.key = 'recent';
    const old = scroll.querySelector('.x-grid[data-key="recent"]');
    for (const it of rec) { const t = tile(it, chat, panel); t.dataset.key = 'r' + it.id; g.appendChild(t); }
    if (old) { flip(old, () => old.replaceChildren(...g.children)); nodes.push(old); } else nodes.push(g);
  }
  for (const p of list) {
    if (!p.items.length) continue;
    const h = el('div', 'x-pack-title', p.title); h.dir = 'auto'; nodes.push(h);
    const g = el('div', 'x-grid'); p.items.forEach((it, i) => {
      const t = tile(it, chat, panel); g.appendChild(t);
      if (!reduced() && !scroll.dataset.shown) t.animate([{ transform: 'scale(.6)', opacity: 0 }, { transform: 'none', opacity: 1 }], { duration: 200, delay: Math.min(i, 24) * 20, easing: 'cubic-bezier(0.34, 1.56, 0.64, 1)', fill: 'backwards' });
    });
    nodes.push(g);
  }
  if (!nodes.length) nodes.push(el('div', 'x-empty', manage ? 'هنوز بسته‌ای نیست؛ از «مدیریت بسته‌ها» بسازید.' : 'هنوز برچسبی اضافه نشده است.'));
  scroll.replaceChildren(...nodes); scroll.dataset.shown = '1';
}

async function paintGifs(panel, chat, q) {
  const scroll = panel.querySelector('.x-scroll');
  let items = [];
  try {
    if (q) items = (await api('/api/chat/packs/search?kind=gif&q=' + encodeURIComponent(q))).items || [];
    else for (const p of await loadPacks('gif')) items.push(...p.items);
  } catch { scroll.replaceChildren(el('div', 'x-empty', 'گیف‌ها بارگذاری نشد.')); return; }
  const search = panel._q;
  const g = el('div', 'x-grid gifs'); for (const it of items.slice(0, 80)) g.appendChild(tile(it, chat, panel));
  scroll.replaceChildren(search, items.length ? g : el('div', 'x-empty', q ? 'گیفی با این واژه پیدا نشد.' : 'هنوز گیفی اضافه نشده است.'));
}

// togglePanel opens or closes the sticker and GIF panel of the open chat.
export function togglePanel(foot, chat) {
  let panel = foot.querySelector('.x-panel');
  if (panel) { panel.hidden = !panel.hidden; if (!panel.hidden) slide(panel); return; }
  panel = el('div', 'x-panel'); panel.setAttribute('role', 'dialog'); panel.setAttribute('aria-label', 'برچسب و گیف');
  const tabs = el('div', 'x-tabs'); tabs.setAttribute('role', 'tablist');
  const scroll = el('div', 'x-scroll');
  const q = Object.assign(el('input', 'input x-gif-q'), { type: 'search', placeholder: 'جستجوی گیف', dir: 'auto' }); q.setAttribute('aria-label', 'جستجوی گیف');
  let qt; q.addEventListener('input', () => { clearTimeout(qt); qt = setTimeout(() => paintGifs(panel, chat, q.value.trim()), 250); });
  const tab = (label, kind) => {
    const b = el('button', 'x-tab', label); b.type = 'button'; b.setAttribute('role', 'tab'); b.dataset.kind = kind;
    b.addEventListener('click', () => select(kind)); return b;
  };
  const select = kind => {
    for (const b of tabs.querySelectorAll('.x-tab')) b.setAttribute('aria-selected', String(b.dataset.kind === kind));
    if (kind === 'gif') paintGifs(panel, chat, q.value.trim()); else paintStickers(panel, chat);
  };
  const adm = el('button', 'x-link', 'مدیریت بسته‌ها'); adm.type = 'button'; adm.hidden = true;
  adm.addEventListener('click', () => import('/chatpackadmin.js').then(m => m.openPackAdmin(tabs.querySelector('[aria-selected="true"]')?.dataset.kind || 'sticker', () => { forgetPacks(); select('sticker'); })));
  tabs.append(tab('برچسب', 'sticker'), tab('گیف', 'gif'), el('span', 'x-sp'), adm);
  panel.append(tabs, scroll); panel._q = q;
  foot.prepend(panel);
  slide(panel);
  loadPacks('sticker').then(() => { adm.hidden = !manage; }).catch(() => { });
  select('sticker');
}
function slide(panel) {
  if (reduced()) { panel.animate([{ opacity: 0 }, { opacity: 1 }], { duration: 100 }); return; }
  panel.animate([{ transform: 'translateY(16px)', opacity: 0 }, { transform: 'none', opacity: 1 }], { duration: 240, easing: 'cubic-bezier(0.2, 0, 0, 1)' });
}
