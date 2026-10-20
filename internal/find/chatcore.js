// chatcore.js -- the messenger's plumbing: ids, the local cache, the socket and
// the motion helpers. messages.js is the screen built on it.
// Brief: docs/chat/SPEC.md (5.1 transport, 7 frontend, 8.1 motion).

// ---------------------------------------------------------------- ids
// UUIDv7: 48 bits of milliseconds, then randomness. Time-ordered, so the
// server's dedupe index stays append-friendly; unique enough that a resend
// after a lost answer is the only way two sends share one.
export function uuidv7() {
  const b = new Uint8Array(16);
  crypto.getRandomValues(b);
  const ms = Date.now();
  for (let i = 0; i < 6; i++) b[i] = Math.floor(ms / 2 ** (8 * (5 - i))) & 0xff;
  b[6] = (b[6] & 0x0f) | 0x70;
  b[8] = (b[8] & 0x3f) | 0x80;
  const h = [...b].map(x => x.toString(16).padStart(2, '0')).join('');
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`;
}

// ---------------------------------------------------------------- cache
// IndexedDB: chats, the last 200 messages of each, drafts, and the outbox of
// messages written while offline. Everything here is a convenience: a failure
// to open (private window, blocked storage) leaves the page working from the
// server alone.
const DB_NAME = 'docfind-chat', DB_VER = 1, KEEP = 200;

export class Cache {
  constructor(user) { this.user = user; this.db = null; }
  async open() {
    if (!('indexedDB' in window)) return this;
    try {
      this.db = await new Promise((ok, fail) => {
        const rq = indexedDB.open(`${DB_NAME}:${this.user}`, DB_VER);
        rq.onupgradeneeded = () => {
          const db = rq.result;
          db.createObjectStore('chats', { keyPath: 'id' });
          const m = db.createObjectStore('msgs', { keyPath: ['chat', 'pts'] });
          m.createIndex('chat', 'chat');
          db.createObjectStore('drafts', { keyPath: 'chat' });
          db.createObjectStore('outbox', { keyPath: 'cid' });
        };
        rq.onsuccess = () => ok(rq.result);
        rq.onerror = () => fail(rq.error);
      });
    } catch { this.db = null; }
    return this;
  }
  tx(store, mode = 'readonly') { return this.db.transaction(store, mode).objectStore(store); }
  req(r) { return new Promise((ok, fail) => { r.onsuccess = () => ok(r.result); r.onerror = () => fail(r.error); }); }
  async all(store) { if (!this.db) return []; try { return await this.req(this.tx(store).getAll()); } catch { return []; } }
  async put(store, v) { if (!this.db) return; try { await this.req(this.tx(store, 'readwrite').put(v)); } catch { } }
  async del(store, k) { if (!this.db) return; try { await this.req(this.tx(store, 'readwrite').delete(k)); } catch { } }
  async chats() { return this.all('chats'); }
  async putChats(list) {
    if (!this.db) return;
    try { const s = this.tx('chats', 'readwrite'); for (const c of list) s.put(c); } catch { }
  }
  async msgs(chat) {
    if (!this.db) return [];
    try {
      const all = await this.req(this.tx('msgs').index('chat').getAll(chat));
      return all.sort((a, b) => a.pts - b.pts).slice(-KEEP);
    } catch { return []; }
  }
  async putMsgs(list) {
    if (!this.db || !list.length) return;
    try { const s = this.tx('msgs', 'readwrite'); for (const m of list) if (m.pts) s.put(m); } catch { }
  }
  async delMsgs(chat, ptsList) {
    if (!this.db || !ptsList.length) return;
    try { const s = this.tx('msgs', 'readwrite'); for (const p of ptsList) if (p) s.delete([chat, p]); } catch { }
  }
  // trim keeps the newest KEEP messages of a chat; run when a chat is closed.
  async trim(chat) {
    const all = await this.msgs(chat);
    if (!this.db) return;
    try {
      const keys = await this.req(this.tx('msgs').index('chat').getAllKeys(chat));
      const keep = new Set(all.map(m => m.pts));
      const s = this.tx('msgs', 'readwrite');
      for (const k of keys) if (!keep.has(k[1])) s.delete(k);
    } catch { }
  }
}

// ---------------------------------------------------------------- socket
// One socket per device. GET /api/chat/ticket with the session cookie, then
// the ticket goes in the first frame (never the URL). Requests carry an id and
// resolve on the matching "ack" or "err".
export class Net extends EventTarget {
  constructor() {
    super();
    this.ws = null; this.wait = new Map(); this.backoff = 1000; this.up = false; this.closed = false; this.hiddenAt = 0;
    addEventListener('online', () => this.connect(true));
    addEventListener('offline', () => this.ws && this.ws.close());
    // A phone freezes a page in the background and leaves its socket looking
    // open long after the server dropped it: messages and "last seen" then
    // stopped arriving with nothing on screen to say so. Back after a while,
    // and every half minute while in view, the socket must answer a ping or
    // it is replaced at once.
    document.addEventListener('visibilitychange', () => {
      if (document.hidden) { this.hiddenAt = Date.now(); return; }
      if (!this.up) { this.connect(true); return; }
      if (Date.now() - this.hiddenAt > 10000) this.check(true);
    });
    setInterval(() => { if (this.up && !document.hidden) this.check(false); }, 30000);
  }
  // check pings the socket; one that does not answer is dead.
  check(resumed) {
    const ws = this.ws;
    this.request('ping', {}, 5000).then(() => { if (resumed) this.emit('resumed'); }, () => { if (this.ws === ws) this.dead(); });
  }
  // dead lets a socket that stopped answering go and opens a new one now,
  // without waiting out a back-off.
  dead() {
    const ws = this.ws; if (!ws) return;
    ws.onclose = null; ws.onmessage = null;
    try { ws.close(); } catch { }
    this.up = false; this.ws = null;
    for (const [, w] of this.wait) { clearTimeout(w.t); w.fail({ code: 'offline' }); }
    this.wait.clear();
    this.emit('state', 'connecting');
    this.backoff = 1000;
    this.connect(true);
  }
  emit(type, detail) { this.dispatchEvent(new CustomEvent(type, { detail })); }
  async connect(now = false) {
    if (this.closed || (this.ws && this.ws.readyState <= 1)) return;
    if (!navigator.onLine) { this.emit('state', 'offline'); return; }
    clearTimeout(this.timer);
    let ticket;
    try {
      const r = await fetch('/api/chat/ticket', { credentials: 'same-origin', cache: 'no-store' });
      if (r.status === 401) { this.emit('state', 'signedout'); return; }
      if (!r.ok) throw new Error(String(r.status));
      ticket = (await r.json()).ticket;
    } catch { this.retry(); return; }
    const ws = new WebSocket(`${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}/api/chat/ws`);
    this.ws = ws;
    this.emit('state', 'connecting');
    ws.onopen = () => ws.send(JSON.stringify({ t: 'auth', d: { ticket } }));
    ws.onmessage = ev => {
      let f; try { f = JSON.parse(ev.data); } catch { return; }
      if (f.t === 'ready') { this.up = true; this.backoff = 1000; this.emit('state', 'online'); }
      if ((f.t === 'ack' || f.t === 'err' || f.t === 'pong') && f.id && this.wait.has(f.id)) {
        const w = this.wait.get(f.id); this.wait.delete(f.id); clearTimeout(w.t);
        f.t === 'err' ? w.fail(f.d) : w.ok(f.d);
      }
      this.emit(f.t, f);
    };
    ws.onclose = ev => {
      this.up = false; this.ws = null;
      for (const [, w] of this.wait) { clearTimeout(w.t); w.fail({ code: 'offline' }); }
      this.wait.clear();
      if (ev.code === 4001) { this.emit('state', 'signedout'); return; }
      this.emit('state', navigator.onLine ? 'connecting' : 'offline');
      this.retry();
    };
  }
  retry() {
    clearTimeout(this.timer);
    const d = Math.min(this.backoff, 30000) * (0.75 + Math.random() * 0.5);
    this.backoff = Math.min(this.backoff * 2, 30000);
    this.timer = setTimeout(() => this.connect(), d);
  }
  send(t, d, id) {
    if (!this.ws || this.ws.readyState !== 1) return false;
    this.ws.send(JSON.stringify(id ? { t, id, d } : { t, d }));
    return true;
  }
  request(t, d, timeout = 15000) {
    const id = uuidv7();
    return new Promise((ok, fail) => {
      if (!this.send(t, d, id)) { fail({ code: 'offline' }); return; }
      this.wait.set(id, { ok, fail, t: setTimeout(() => { this.wait.delete(id); fail({ code: 'timeout' }); }, timeout) });
    });
  }
}

// ---------------------------------------------------------------- motion
export const reduced = () => matchMedia('(prefers-reduced-motion: reduce)').matches;
const DUR = { fast: 120, base: 240, slow: 360 };
const EASE_OUT = 'cubic-bezier(0.2, 0, 0, 1)';
export const SPRING = (() => {
  try { if (CSS.supports('transition-timing-function', 'linear(0, 1)')) return getComputedStyle(document.documentElement).getPropertyValue('--ease-spring').trim(); } catch { }
  return 'cubic-bezier(0.34, 1.56, 0.64, 1)';
})();

// flip records where the keyed children of box are, runs mutate, then plays
// each child from its old place to its new one with transform only.
export function flip(box, mutate, key = 'key') {
  if (reduced() || document.hidden) { mutate(); return; }
  const before = new Map();
  for (const el of box.children) if (el.dataset[key]) before.set(el.dataset[key], el.getBoundingClientRect());
  mutate();
  for (const el of box.children) {
    const b = before.get(el.dataset[key]);
    if (!b) continue;
    const a = el.getBoundingClientRect();
    const dx = b.left - a.left, dy = b.top - a.top;
    if (!dx && !dy) continue;
    el.style.willChange = 'transform';
    el.animate([{ transform: `translate(${dx}px, ${dy}px)` }, { transform: 'none' }], { duration: DUR.base, easing: EASE_OUT })
      .finished.finally(() => { el.style.willChange = ''; });
  }
}

// enter plays a new element in: scale from .9 and fade (a fade only when reduced).
export function enter(el, from = 0.9) {
  if (document.hidden) return;
  const frames = reduced() ? [{ opacity: 0 }, { opacity: 1 }] : [{ opacity: 0, transform: `scale(${from})` }, { opacity: 1, transform: 'none' }];
  el.animate(frames, { duration: reduced() ? 100 : DUR.base, easing: reduced() ? 'linear' : SPRING });
}

// fly moves a copy of the composer text from its rectangle to the new bubble's
// (Telegram's send flight). The bubble is hidden until the copy lands.
export function fly(fromRect, bubble) {
  if (reduced() || document.hidden || !fromRect) { enter(bubble); return; }
  const to = bubble.getBoundingClientRect();
  if (!to.width) return;
  const ghost = bubble.cloneNode(true);
  ghost.classList.add('m-flight');
  Object.assign(ghost.style, { left: `${to.left}px`, top: `${to.top}px`, width: `${to.width}px` });
  document.body.appendChild(ghost);
  bubble.style.opacity = '0';
  const sx = Math.min(fromRect.width / to.width, 1.2);
  ghost.animate([
    { transform: `translate(${fromRect.right - to.right}px, ${fromRect.top - to.top}px) scale(${sx}, 1)`, opacity: 0.6, borderRadius: '22px' },
    { transform: 'none', opacity: 1 }
  ], { duration: DUR.slow, easing: EASE_OUT }).finished.finally(() => { ghost.remove(); bubble.style.opacity = ''; });
}

// roll swaps a number in place with a vertical roll (badges, counters).
export function roll(el, text) {
  if (el.textContent === text) return;
  if (reduced() || document.hidden || !el.textContent) { el.textContent = text; return; }
  const up = Number(text) > Number(el.textContent);
  el.animate([{ transform: 'translateY(0)', opacity: 1 }, { transform: `translateY(${up ? -60 : 60}%)`, opacity: 0 }], { duration: 90, easing: EASE_OUT })
    .finished.then(() => {
      el.textContent = text;
      el.animate([{ transform: `translateY(${up ? 60 : -60}%)`, opacity: 0 }, { transform: 'none', opacity: 1 }], { duration: 130, easing: EASE_OUT });
    });
}
