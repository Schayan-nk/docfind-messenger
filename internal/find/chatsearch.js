// chatsearch.js: search on the messenger page (docs/chat/SPEC.md 6.6, 8.3):
// the list's search box finds chats, colleagues and messages (with files,
// links, media and voice as tabs); the search button in a chat's header finds
// within it, with previous and next, a sender, a kind and a date. Found words
// are highlighted with the same Persian equivalences the server uses
// (normalize.go). A hashtag in a message searches its chat.
import { reduced } from '/chatcore.js';
import { $, el, fa, svg, timeFmt, dayFmt, dayKey, S, hooks, toast } from '/chatstate.js';
import { avatar, statusText } from '/chatpane.js';
import { plain } from '/chatfmt.js';
import { api, openPanel } from '/chatgroup.js';

// ------------------------------------------------------------ normalising
// The same table as normalize.go: Arabic yeh, alef maksura, yeh with hamza and kaf to Persian;
// teh marbuta and heh with yeh to heh; hamza forms of alef to alef; digits to ASCII.
const MAP = { '\u064A': '\u06CC', '\u0649': '\u06CC', '\u0626': '\u06CC', '\u0643': '\u06A9', '\u0629': '\u0647', '\u06C0': '\u0647', '\u0623': '\u0627', '\u0625': '\u0627', '\u0671': '\u0627' };
const GONE = /[\u0640\u200B-\u200F\uFEFF\u064B-\u065F\u0670]/g;
export function norm(s) {
  return String(s || '').replace(GONE, '').replace(/[\u0660-\u0669\u06F0-\u06F9]/g, d => String(d.charCodeAt(0) & 0xF))
    .replace(/[\u064A\u0649\u0626\u0643\u0629\u06C0\u0623\u0625\u0671]/g, c => MAP[c]).toLowerCase();
}
export const words = q => norm(q).split(/[^\p{L}\p{N}]+/u).filter(Boolean).slice(0, 8);
// Each letter matches its variants, with zero-width marks, tatweel and harakat
// allowed between letters; a word matches at the start of a word (a prefix).
const VAR = { '\u06CC': '[\u06CC\u064A\u0649\u0626]', '\u06A9': '[\u06A9\u0643]', '\u0647': '[\u0647\u0629\u06C0]', '\u0627': '[\u0627\u0623\u0625\u0671]' };
const esc = c => c.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
function pattern(ws) {
  if (!ws.length) return null;
  const one = w => [...w].map(c => /\d/.test(c) ? `[${c}${String.fromCharCode(0x06F0 + +c)}${String.fromCharCode(0x0660 + +c)}]` : (VAR[c] || esc(c))).join('[\\u0640\\u200B-\\u200F\\u064B-\\u065F\\u0670]*');
  return new RegExp(`(?<![\\p{L}\\p{N}])(?:${ws.map(one).join('|')})`, 'giu');
}
// marked: text as nodes, the found parts in <mark> (text nodes only).
export function marked(text, ws) {
  const f = document.createDocumentFragment(), re = pattern(ws);
  if (!re) { f.appendChild(document.createTextNode(text)); return f; }
  let i = 0;
  for (const m of text.matchAll(re)) {
    if (m.index > i) f.appendChild(document.createTextNode(text.slice(i, m.index)));
    f.appendChild(el('mark', 'm-hit', m[0])); i = m.index + m[0].length;
  }
  if (i < text.length) f.appendChild(document.createTextNode(text.slice(i)));
  return f;
}
// glow highlights the words inside a message on screen (CSS Custom Highlight).
function glow(node, ws) {
  if (!node || !window.Highlight || !CSS.highlights) return;
  const re = pattern(ws); if (!re) return;
  const ranges = [], walk = document.createTreeWalker(node.querySelector('.m-text') || node, NodeFilter.SHOW_TEXT);
  for (let t = walk.nextNode(); t; t = walk.nextNode()) {
    for (const m of t.data.matchAll(re)) { const r = new Range(); r.setStart(t, m.index); r.setEnd(t, m.index + m[0].length); ranges.push(r); }
  }
  CSS.highlights.set('m-found', new Highlight(...ranges));
}
const snippet = (m, ws) => {
  const text = plain(m.text || '') || (m.media && m.media.name) || ({ photo: 'عکس', video: 'ویدیو', voice: 'پیام صوتی', round: 'پیام ویدیویی', file: 'فایل', doc: 'سند' })[m.kind] || '';
  const re = pattern(ws); if (!re) return text.slice(0, 160);
  const hit = text.search(re); // a window around the first hit
  return hit > 60 ? '…' + text.slice(hit - 40, hit + 120) : text.slice(0, 160);
};
const when = at => { const d = new Date(at); return dayKey(d) === dayKey(Date.now()) ? timeFmt.format(d) : dayFmt.format(d); };

// open takes a person to one message: the chat, its topic, the message, the words.
async function open(m, ws) {
  const chat = S.chats.get(m.chat); if (!chat) { toast('این گفت‌وگو دیگر در فهرست شما نیست.'); return; }
  if (S.active !== chat.id) await hooks.openChat(chat.id);
  if (chat.forum && hooks.enterTopicById) await hooks.enterTopicById(chat, m.topic || 0);
  await hooks.jumpTo(m.pts);
  glow($('m-msgs')?.querySelector(`[data-pts="${m.pts}"]`), ws);
}

// ------------------------------------------------------------ the list's search
const KINDS = [['', 'همه'], ['media', 'رسانه'], ['files', 'فایل'], ['links', 'پیوند'], ['voice', 'صدا']];
let G = { q: '', kind: '', ctl: null, before: 0, msgs: [] };
function panel() {
  let p = $('m-search');
  if (!p) {
    p = el('div', 'm-search'); p.id = 'm-search'; p.setAttribute('role', 'region'); p.setAttribute('aria-label', 'نتیجه‌های جستجو'); p.hidden = true;
    $('m-rows').after(p);
  }
  return p;
}
function show(on) {
  const p = panel(), rows = $('m-rows'), tabs = $('m-tabs');
  if (p.hidden === !on) return;
  p.hidden = !on; rows.hidden = on; if (tabs) tabs.hidden = on;
  if (on && !reduced()) p.animate([{ opacity: 0 }, { opacity: 1 }], { duration: 160, easing: 'linear' });
}
const section = (title, kids) => { const s = el('div', 'm-sres'); s.appendChild(el('div', 'g-sec', title)); s.append(...kids); return s; };
async function globalSearch(more) {
  const q = G.q, ws = words(q);
  if (!more) { G.before = 0; G.msgs = []; }
  G.ctl && G.ctl.abort(); G.ctl = new AbortController();
  const sig = G.ctl.signal;
  const p = panel();
  const chats = [...S.chats.values()].filter(c => (!hooks.hideChat || !hooks.hideChat(c)) && ws.every(w => norm(c.title).split(/[^\p{L}\p{N}]+/u).some(t => t.startsWith(w)))).slice(0, 6);
  let people = [];
  if (!more && !G.kind) try { people = ((await (await fetch('/api/chat/people?q=' + encodeURIComponent(q), { credentials: 'same-origin', signal: sig })).json()).people || []).slice(0, 6); } catch { if (sig.aborted) return; }
  let msgs = [];
  try {
    const u = `/api/chat/search?q=${encodeURIComponent(q)}&kind=${G.kind}&limit=30${G.before ? '&before=' + G.before : ''}`;
    const r = await fetch(u, { credentials: 'same-origin', signal: sig });
    const j = await r.json().catch(() => ({}));
    if (!r.ok) { if (r.status === 429) toast('کمی آهسته‌تر؛ جستجو چند لحظه بعد دوباره کار می‌کند.'); }
    else msgs = j.messages || [];
  } catch { if (sig.aborted) return; }
  if (sig.aborted || q !== G.q) return;
  G.msgs = G.msgs.concat(msgs); if (msgs.length) G.before = msgs[msgs.length - 1].id;
  const kinds = el('div', 'm-kinds'); kinds.setAttribute('role', 'tablist');
  for (const [k, label] of KINDS) {
    const b = el('button', 'm-kind', label); b.type = 'button'; b.setAttribute('role', 'tab'); b.setAttribute('aria-selected', String(G.kind === k));
    b.addEventListener('click', () => { G.kind = k; globalSearch(); });
    kinds.appendChild(b);
  }
  const kids = [kinds];
  if (!G.kind && chats.length) kids.push(section('گفت‌وگوها', chats.map(c => {
    const r = el('button', 'm-row'); r.type = 'button';
    const n = el('div', 'm-row-name'); n.dir = 'auto'; n.appendChild(marked(c.title, ws));
    r.append(avatar(c), n); r.addEventListener('click', () => { remember(c.id); clearSearch(); hooks.openChat(c.id); });
    return r;
  })));
  if (!G.kind && people.length) kids.push(section('همکاران', people.map(pp => {
    const r = el('button', 'm-row'); r.type = 'button';
    const n = el('div', 'm-row-name'); n.dir = 'auto'; n.appendChild(marked(pp.name, ws));
    r.append(avatar({ title: pp.name, peer: { id: pp.id } }), n); r.addEventListener('click', () => { clearSearch(); hooks.start(pp.id); });
    return r;
  })));
  if (G.msgs.length) {
    const rows = G.msgs.map(m => {
      const c = S.chats.get(m.chat) || { id: m.chat, title: '' };
      const r = el('button', 'm-row m-hit-row'); r.type = 'button';
      const main = el('div', 'm-row-main'), top = el('div', 'm-row-top'), bot = el('div', 'm-row-bot');
      const n = el('span', 'm-row-name', c.title); n.dir = 'auto';
      top.append(n, el('span', 'm-row-time', when(m.at)));
      const t = el('span', 'm-row-last'); t.dir = 'auto';
      if (m.from_name && c.type !== 'private' && c.type !== 'saved') t.appendChild(el('b', '', (m.from === S.me ? 'شما' : m.from_name) + ': '));
      t.appendChild(marked(snippet(m, ws), ws));
      bot.appendChild(t); main.append(top, bot); r.append(avatar(c), main);
      r.addEventListener('click', () => { remember(m.chat); open(m, ws); });
      return r;
    });
    if (msgs.length === 30) {
      const b = el('button', 'btn m-more', 'نتیجه‌های بیشتر'); b.type = 'button';
      b.addEventListener('click', () => globalSearch(true)); rows.push(b);
    }
    kids.push(section(G.kind ? KINDS.find(k => k[0] === G.kind)[1] : 'پیام‌ها', rows));
  }
  if (kids.length === 1) kids.push(el('div', 'm-empty', 'چیزی پیدا نشد.'));
  p.replaceChildren(...kids);
}
function clearSearch() {
  const f = $('m-filter'); if (f) f.value = '';
  S.filter = ''; G.q = ''; G.kind = ''; show(false);
  searching = false; document.body.classList.remove('m-searching');
  hooks.renderList();
}
hooks.clearSearch = clearSearch;

// ------------------------------------------------------------ before a word
// Telegram's search (2026-10-19): the people one writes to most, in a row of
// faces, and the chats opened from search before, with «پاک کردن» -- until
// a word is typed. Kept on this phone only.
const RECENT = 'msgr.recent';
const recentIds = () => { try { return (JSON.parse(localStorage.getItem(RECENT) || '[]') || []).filter(Number.isFinite); } catch { return []; } };
function remember(id) { try { localStorage.setItem(RECENT, JSON.stringify([id, ...recentIds().filter(x => x !== id)].slice(0, 12))); } catch { } }
const at = c => Date.parse((c.last && c.last.at) || 0) || 0;
function recents() {
  const p = panel(), kids = [];
  const go = c => () => { remember(c.id); clearSearch(); hooks.openChat(c.id); };
  const often = [...S.chats.values()].filter(c => c.type === 'private' && c.peer && (!hooks.hideChat || !hooks.hideChat(c)))
    .sort((a, b) => at(b) - at(a)).slice(0, 10);
  if (often.length) {
    const strip = el('div', 'm-top-people'); strip.setAttribute('aria-label', 'پرتماس‌ها');
    for (const c of often) {
      const b = el('button', 'm-top-p'); b.type = 'button';
      const n = el('span', 'm-top-n', (c.title || '').trim().split(/\s+/)[0]); n.dir = 'auto';
      b.append(avatar(c), n); b.addEventListener('click', go(c)); strip.appendChild(b);
    }
    kids.push(strip);
  }
  const rec = recentIds().map(id => S.chats.get(id)).filter(Boolean);
  if (rec.length) {
    const head = el('div', 'm-rec-head'), clr = el('button', 'm-rec-clear', 'پاک کردن'); clr.type = 'button';
    clr.addEventListener('click', () => { try { localStorage.removeItem(RECENT); } catch { } recents(); });
    head.append(el('span', '', 'اخیر'), clr); kids.push(head);
    for (const c of rec) {
      const r = el('button', 'm-row'); r.type = 'button';
      const main = el('div', 'm-row-main'), n = el('div', 'm-row-name', c.title); n.dir = 'auto';
      main.append(n, el('div', 'm-row-last', statusText(c).text || ''));
      r.append(avatar(c), main); r.addEventListener('click', go(c)); kids.push(r);
    }
  }
  if (!kids.length) kids.push(el('div', 'm-empty', 'نام یک گفت‌وگو یا همکار، یا واژه‌ای از پیام‌ها را بنویسید.'));
  p.replaceChildren(...kids);
}
let searching = false;
function enter() {
  if (searching) return;
  searching = true; document.body.classList.add('m-searching');
  if (!$('m-find-x')) {
    const x = el('button', 'm-icon-btn m-find-x'); x.id = 'm-find-x'; x.type = 'button'; x.setAttribute('aria-label', 'بستن جستجو'); x.innerHTML = svg(X);
    x.addEventListener('pointerdown', e => e.preventDefault()); // keeps the keyboard until the press is done
    x.addEventListener('click', () => { $('m-filter').blur(); clearSearch(); });
    $('m-filter').after(x);
  }
  if (!G.q) { show(true); recents(); }
}
// The dock's round search button means this search on this page.
window.DocfindPageSearch = () => { const f = $('m-filter'); if (f) f.focus(); };

function wireGlobal() {
  const f = $('m-filter'); if (!f) return;
  f.placeholder = 'جستجو';
  let t;
  f.addEventListener('focus', enter);
  f.addEventListener('input', () => {
    clearTimeout(t);
    const q = f.value.trim();
    if (q.length < 2) { G.q = ''; if (searching) recents(); else show(false); return; }
    t = setTimeout(() => { G.q = q; show(true); globalSearch(); }, 250);
  });
  f.addEventListener('keydown', e => { if (e.key === 'Escape') { e.preventDefault(); f.blur(); clearSearch(); } });
}

// ------------------------------------------------------------ in a chat
const SEARCH = '<circle cx="11" cy="11" r="7"/><path d="M21 21l-4.3-4.3"/>';
const UP = '<path d="M18 15l-6-6-6 6"/>', DOWN = '<path d="M6 9l6 6 6-6"/>', X = '<path d="M18 6L6 18M6 6l12 12"/>';
const LIST = '<path d="M8 6h13M8 12h13M8 18h13M3 6h.01M3 12h.01M3 18h.01"/>';
hooks.headerTools = (chat, h) => {
  if (chat.type === 'secret') return;
  const b = el('button', 'm-icon-btn m-search-btn'); b.type = 'button'; b.setAttribute('aria-label', 'جستجو در این گفت‌وگو'); b.title = 'جستجو در این گفت‌وگو';
  b.innerHTML = svg(SEARCH); b.addEventListener('click', () => inChat(chat));
  h.appendChild(b);
};
let C = null; // the open in-chat search
function inChat(chat, q = '') {
  const head = $('m-chat')?.querySelector('.m-chat-head'); if (!head) return;
  closeInChat();
  const bar = el('form', 'm-insearch'); bar.setAttribute('role', 'search');
  const close = el('button', 'm-icon-btn'); close.type = 'button'; close.setAttribute('aria-label', 'بستن جستجو'); close.innerHTML = svg(X);
  const input = el('input', 'input'); input.type = 'search'; input.id = 'm-insearch-q'; input.placeholder = 'جستجو در این گفت‌وگو'; input.setAttribute('aria-label', 'جستجو در این گفت‌وگو'); input.value = q;
  const count = el('span', 'm-insearch-n'); count.setAttribute('aria-live', 'polite');
  const btn = (icon, label) => { const b = el('button', 'm-icon-btn'); b.type = 'button'; b.innerHTML = svg(icon); b.setAttribute('aria-label', label); b.title = label; return b; };
  const up = btn(UP, 'قدیمی‌تر'), down = btn(DOWN, 'تازه‌تر'), list = btn(LIST, 'همهٔ نتیجه‌ها');
  const filters = el('div', 'm-insearch-f');
  const sel = (id, label, opts) => { const s = el('select', 'input'); s.id = id; s.setAttribute('aria-label', label); for (const [v, t] of opts) { const o = el('option', '', t); o.value = v; s.appendChild(o); } return s; };
  const kind = sel('m-insearch-k', 'نوع', KINDS);
  const members = (S.gmembers && S.gmembers.get(chat.id)) || [];
  const from = sel('m-insearch-from', 'فرستنده', [['', 'همه'], ...members.map(m => [m.id, m.name || m.id])]);
  from.hidden = !members.length;
  // Until a day, picked on DocFind's own Jalali calendar (datepick.js, loaded once).
  const until = el('input', 'input'); until.type = 'text'; until.id = 'm-insearch-until'; until.placeholder = 'تا تاریخ'; until.setAttribute('aria-label', 'تا تاریخ'); until.dir = 'ltr'; until.autocomplete = 'off';
  datePicker().then(dp => dp && dp.attach(until));
  filters.append(kind, from, until);
  bar.append(close, input, count, up, down, list, filters);
  head.appendChild(bar);
  C = { chat, bar, input, count, results: [], at: -1, ws: [] };
  if (!reduced()) bar.animate([{ opacity: 0, transform: 'scaleX(.92)' }, { opacity: 1, transform: 'none' }], { duration: 200, easing: 'cubic-bezier(0.2,0,0,1)' });
  input.focus();
  let t;
  const run = async () => {
    const qs = new URLSearchParams({ q: input.value.trim(), kind: kind.value, from: from.value, limit: '100' });
    const end = untilDate(until.value); if (end) qs.set('until', end.toISOString());
    if (!input.value.trim() && !kind.value && !from.value) { C.results = []; C.at = -1; count.textContent = ''; return; }
    try {
      const j = await api(`/api/chat/chats/${chat.id}/search?${qs}`);
      if (!C || C.chat !== chat) return;
      C.results = j.messages || []; C.ws = words(input.value); C.at = C.results.length ? 0 : -1;
      paintCount(); if (C.at === 0) go();
    } catch (e) { toast(e.text || 'جستجو انجام نشد.'); }
  };
  const later = () => { clearTimeout(t); t = setTimeout(run, 300); };
  input.addEventListener('input', later);
  for (const x of [kind, from, until]) x.addEventListener('change', run);
  bar.addEventListener('submit', e => { e.preventDefault(); step(1); });
  up.addEventListener('click', () => step(1)); down.addEventListener('click', () => step(-1));
  list.addEventListener('click', () => showList());
  close.addEventListener('click', closeInChat);
  bar.addEventListener('keydown', e => { if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); closeInChat(); } });
  if (q) run();
}
// datePicker loads DocFind's Jalali date picker once (a classic script).
let dpLoad;
function datePicker() {
  if (window.docfindDatePicker) return Promise.resolve(window.docfindDatePicker);
  dpLoad = dpLoad || new Promise(res => { const sc = el('script'); sc.src = '/datepick.js'; sc.onload = () => res(window.docfindDatePicker); sc.onerror = () => res(null); document.head.appendChild(sc); });
  return dpLoad;
}
// untilDate: the end of a Jalali day written 1404/07/21 (Persian digits too).
function untilDate(v) {
  const m = norm(v).match(/^(\d{4})[\/-](\d{1,2})[\/-](\d{1,2})$/); const dp = window.docfindDatePicker;
  if (!m || !dp) return null;
  const [gy, gm, gd] = dp.jalaliToGregorian(+m[1], +m[2], +m[3]);
  return new Date(gy, gm - 1, gd, 23, 59, 59);
}
hooks.searchChat = (q) => { const chat = S.chats.get(S.active); if (chat) inChat(chat, q); };
function paintCount() { C.count.textContent = C.results.length ? `${fa(C.at + 1)} از ${fa(C.results.length)}` : 'نتیجه‌ای نیست'; }
function step(d) {
  if (!C || !C.results.length) return;
  C.at = Math.min(C.results.length - 1, Math.max(0, C.at + d)); paintCount(); go();
}
async function go() {
  const m = C && C.results[C.at]; if (!m) return;
  const chat = C.chat;
  if (chat.forum && hooks.enterTopicById) await hooks.enterTopicById(chat, m.topic || 0);
  await hooks.jumpTo(m.pts);
  glow($('m-msgs')?.querySelector(`[data-pts="${m.pts}"]`), C.ws);
}
function showList() {
  if (!C || !C.results.length) return;
  const box = el('div', 'g-list'), ws = C.ws;
  C.results.forEach((m, i) => {
    const r = el('button', 'g-row'); r.type = 'button';
    const t = el('span', 'g-row-text');
    const l = el('span', 'g-row-label'); l.dir = 'auto'; l.appendChild(marked(snippet(m, ws), ws));
    t.append(l, el('span', 'g-row-sub', `${m.from === S.me ? 'شما' : (m.from_name || '')} · ${when(m.at)}`));
    r.appendChild(t);
    r.addEventListener('click', () => { C.at = i; paintCount(); go(); });
    box.appendChild(r);
  });
  openPanel().push(`${fa(C.results.length)} نتیجه`, box);
}
function closeInChat() {
  if (!C) return;
  C.bar.remove(); C = null;
  if (CSS.highlights) CSS.highlights.delete('m-found');
}
// A new chat closes the search; a hashtag opens it.
document.addEventListener('chat-open', () => closeInChat());
addEventListener('click', e => {
  const tag = e.target.closest && e.target.closest('#m-msgs .m-tag');
  if (tag && tag.dataset.tag) { e.preventDefault(); hooks.searchChat(tag.dataset.tag); }
}, true);
queueMicrotask(wireGlobal);
