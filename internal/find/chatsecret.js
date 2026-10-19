// chatsecret.js: secret chats on this device (SPEC 6.12, docs/chat/SECRET_CHATS.md).
// This module speaks the secret_* frames, keeps the chats in their own store
// (chatsecretcrypto.js) and joins them to the chat list through the page's
// hooks; the open chat is chatsecretpane.js. A secret chat lives on exactly
// one device of each side: other devices never receive it.
import { uuidv7 } from '/chatcore.js';
import { $, el, svg, S, hooks, toast } from '/chatstate.js';
import { SecretStore, supported, offerKeys, answerKey, derive, seal, open } from '/chatsecretcrypto.js';
import { initPane, openPane, paint, paneEnded, LOCK, TTLS, ttlText } from '/chatsecretpane.js';

const X = { store: null, device: '', recs: new Map(), views: new Map(), unread: new Map(), q: Promise.resolve(), ready: null, hello: false };
const KINDS = new Set(['text', 'shot', 'ttl']);
const myDir = rec => rec.role === 'i' ? 1 : 2;
const peerDir = rec => rec.role === 'i' ? 2 : 1;
const OFFLINE = 'اتصال برقرار نیست؛ دوباره تلاش کنید.';
const refresh = () => hooks.renderList && hooks.renderList();

function init() {
  if (!X.ready) X.ready = (async () => {
    if (!supported() || !S.me) return false;
    X.store = await new SecretStore(S.me).open();
    X.device = await X.store.device();
    for (const r of await X.store.chats()) X.recs.set(r.chat, r);
    for (const id of X.recs.keys()) await load(id);
    for (const r of X.recs.values()) listChat(r);
    expiry(); refresh();
    return true;
  })().catch(() => false);
  return X.ready;
}

// ------------------------------------------------------------ messages in memory
async function view(rec, m) {
  try {
    const { obj } = await open(m.out ? rec.send : rec.recv, m.out ? myDir(rec) : peerDir(rec), rec.chat, m.body);
    if (!obj || !KINDS.has(obj.t)) return null;
    return { key: m.key, out: m.out, ctr: m.ctr, t: obj.t, text: typeof obj.text === 'string' ? obj.text.slice(0, 4096) : '', ttl: TTLS.includes(obj.ttl) ? obj.ttl : 0, at: m.at, state: m.state, expires: m.expires || 0, m };
  } catch { return null; }
}

async function load(id) {
  const rec = X.recs.get(id); if (!rec || rec.state !== 'active') return;
  const out = [];
  for (const m of await X.store.msgs(id)) {
    if (m.expires && m.expires <= Date.now()) { await X.store.dropMsgs(id, [m.key]); continue; }
    const v = await view(rec, m); if (v) out.push(v);
  }
  X.views.set(id, out.sort((a, b) => a.at - b.at || a.key.localeCompare(b.key)));
  X.unread.set(id, out.filter(v => !v.out && v.state === 'recv' && v.t === 'text').length);
}

function addView(id, v) {
  const list = X.views.get(id) || [];
  list.push(v); list.sort((a, b) => a.at - b.at || a.key.localeCompare(b.key));
  X.views.set(id, list);
}

// changed redraws what shows a chat: its row, and the pane when it is open.
function changed(id) {
  const c = S.chats.get(id), list = X.views.get(id) || [], last = list[list.length - 1], rec = X.recs.get(id);
  if (c) c.last = { at: new Date(last ? last.at : (rec && rec.created) || Date.now()).toISOString(), from: last && last.out ? S.me : '', text: '' };
  refresh();
  paint();
}

// listChat makes sure the chat list has a row for a secret chat this device holds.
function listChat(rec, peer) {
  if (S.chats.has(rec.chat)) return;
  S.chats.set(rec.chat, { id: rec.chat, type: 'secret', title: rec.peerName, peer: peer || { id: rec.peer, name: rec.peerName }, pts: 0, read_pts: 0, peer_read_pts: 0, unread: 0, role: 'member', last: { at: new Date(rec.created || Date.now()).toISOString(), from: '', text: '' } });
}

async function save(rec) { X.recs.set(rec.chat, rec); await X.store.putChat(rec); }

async function ended(rec, byPeer) {
  await X.store.wipe(rec.chat);
  X.recs.delete(rec.chat); X.views.delete(rec.chat); X.unread.delete(rec.chat);
  if (byPeer) toast(`گفت‌وگوی محرمانه با ${rec.peerName} پایان یافت.`);
  if (S.active === rec.chat) paneEnded(rec.chat); else S.chats.delete(rec.chat);
  refresh();
}

// ------------------------------------------------------------ what arrives
const ack = id => { if (id) S.net.send('secret_ack', { ids: [id] }); };

async function onItem(it) {
  if (!(await init()) || !it) return;
  const rec = X.recs.get(it.chat);
  switch (it.kind) {
    case 'request': { // any of my devices may take it, so it is not confirmed here
      if (rec) return;
      let keys = []; try { keys = JSON.parse(it.body).keys || []; } catch { }
      const name = (it.peer && it.peer.name) || it.from;
      const r = { chat: it.chat, role: 'r', state: 'invited', peer: it.from, peerName: name, offer: keys, created: Date.parse(it.at) || Date.now(), sendCtr: 0, recvCtr: 0, ttl: 0 };
      await save(r); listChat(r, it.peer); X.unread.set(r.chat, 1); changed(r.chat);
      return;
    }
    case 'accept': {
      if (rec && rec.role === 'i' && rec.state === 'requested') {
        let k = {}; try { k = JSON.parse(it.body); } catch { }
        const priv = rec.priv && rec.priv[k.alg];
        if (priv) {
          const keys = await derive(k.alg, priv, k.key, rec.pub[k.alg], k.key);
          // The private keys are dropped once the message keys exist.
          Object.assign(rec, { state: 'active', alg: k.alg, priv: null, pub: null, send: keys.i2r, recv: keys.r2i, fp: keys.fp });
          await save(rec); X.views.set(rec.chat, []); changed(rec.chat); flush();
        }
      }
      return ack(it.id);
    }
    case 'msg': await onMsg(rec, it); return ack(it.id);
    case 'close': if (rec) await ended(rec, true); return ack(it.id);
    case 'gone': if (rec) await ended(rec, false); return;
  }
}

async function onMsg(rec, it) {
  if (!rec || rec.state !== 'active') return;
  let o; try { o = await open(rec.recv, peerDir(rec), rec.chat, it.body); } catch { return; }
  const obj = o.obj || {}, at = Date.parse(it.at) || Date.now();
  if (KINDS.has(obj.t)) {
    const m = { chat: rec.chat, key: 'i' + o.ctr, out: false, ctr: o.ctr, body: it.body, at, state: 'recv', expires: 0 };
    if (!(await X.store.takeIncoming(rec.chat, o.ctr, m))) return; // seen before: a replay or a resend
    const v = await view(rec, m); if (!v) return;
    if (obj.t === 'ttl') { rec.ttl = v.ttl; await save(rec); }
    if (obj.t !== 'text') { v.state = m.state = 'read'; await X.store.putMsgs([m]); }
    addView(rec.chat, v);
    if (obj.t === 'text') { X.unread.set(rec.chat, (X.unread.get(rec.chat) || 0) + 1); S.typing.delete(rec.chat); }
    changed(rec.chat);
    return;
  }
  if (!(await X.store.takeIncoming(rec.chat, o.ctr, null))) return;
  if (obj.t === 'read') await readUpTo(rec, Number(obj.upto) || 0);
  if (obj.t === 'del') await drop(rec, [...num(obj.mine).map(c => 'i' + c), ...num(obj.yours).map(c => 'o' + c)]);
}
const num = a => (Array.isArray(a) ? a : []).slice(0, 500).map(Number).filter(n => Number.isSafeInteger(n) && n > 0);

// readUpTo is the other side's receipt: my messages up to upto are read, and
// their self-destruct timers start now, the same moment as on that side.
async function readUpTo(rec, upto) {
  const touched = [];
  for (const v of X.views.get(rec.chat) || []) {
    if (!v.out || v.ctr > upto || v.state === 'read') continue;
    v.state = v.m.state = 'read';
    if (v.ttl) v.expires = v.m.expires = Date.now() + v.ttl * 1000;
    touched.push(v.m);
  }
  if (touched.length) { await X.store.putMsgs(touched); changed(rec.chat); expiry(); }
}

async function drop(rec, keys) {
  if (!rec || !keys.length) return;
  await X.store.dropMsgs(rec.chat, keys);
  const gone = new Set(keys);
  X.views.set(rec.chat, (X.views.get(rec.chat) || []).filter(v => !gone.has(v.key)));
  changed(rec.chat);
}

// ------------------------------------------------------------ what leaves
// sendObj seals one object and queues it. Sends run one at a time, so the
// outbox holds them in the order of their numbers.
let sendQ = Promise.resolve();
function sendObj(rec, obj, keep) {
  const p = sendQ.then(() => sealOne(rec, obj, keep));
  sendQ = p.catch(() => { });
  return p;
}
async function sealOne(rec, obj, keep) {
  const ctr = await X.store.nextCounter(rec.chat); // stored before use: a number is never sealed twice
  const body = await seal(rec.send, myDir(rec), rec.chat, ctr, obj);
  const cid = uuidv7();
  if (keep) {
    const m = { chat: rec.chat, key: 'o' + ctr, out: true, ctr, body, at: Date.now(), state: 'queued', expires: 0 };
    await X.store.putMsgs([m]);
    const v = await view(rec, m); if (v) addView(rec.chat, v);
  }
  await X.store.putOut({ cid, chat: rec.chat, n: ctr, body, key: keep ? 'o' + ctr : '' });
  if (keep) changed(rec.chat);
  flush();
}

let flushing = false, retry;
async function flush() {
  if (flushing || !S.net.up || !X.hello) return;
  flushing = true;
  try {
    for (const o of (await X.store.outbox()).sort((a, b) => a.chat - b.chat || a.n - b.n)) { // in the order of their numbers
      try { await S.net.request('secret_send', { chat: o.chat, cid: o.cid, body: o.body }); }
      catch (e) {
        if (!e || ['offline', 'timeout', 'rate', 'server'].includes(e.code)) { clearTimeout(retry); retry = setTimeout(flush, 3000); break; }
        await X.store.dropOut(o.cid); toast(e.text || 'پیام رمزشده ارسال نشد.'); continue;
      }
      await X.store.dropOut(o.cid);
      const v = o.key && (X.views.get(o.chat) || []).find(x => x.key === o.key);
      if (v && v.state === 'queued') { v.state = v.m.state = 'sent'; await X.store.putMsgs([v.m]); changed(o.chat); }
    }
  } finally { flushing = false; }
}

const active = id => { const r = X.recs.get(id); if (!r || r.state !== 'active') throw new Error('not active'); return r; };

initPane({
  rec: id => X.recs.get(id),
  views: id => X.views.get(id) || [],
  typing: id => S.net.send('typing', { chat: id, action: 'typing' }),
  send: async (id, text) => { const r = active(id); await sendObj(r, { t: 'text', text, ttl: r.ttl || 0, at: Date.now() }, true); },
  setTtl: async (id, ttl) => { const r = active(id); if ((r.ttl || 0) === ttl) return; r.ttl = ttl; await save(r); await sendObj(r, { t: 'ttl', ttl }, true); },
  shot: id => { const r = X.recs.get(id); if (r && r.state === 'active') sendObj(r, { t: 'shot' }, true); },
  del: async (id, keys) => {
    const r = active(id);
    const mine = keys.filter(k => k[0] === 'o').map(k => Number(k.slice(1))), yours = keys.filter(k => k[0] === 'i').map(k => Number(k.slice(1)));
    await sendObj(r, { t: 'del', mine, yours }, false);
    await drop(r, keys);
  },
  // seen marks what arrived as read while the chat is on screen and tells the
  // other side, whose self-destruct timers start with it.
  seen: id => {
    const r = X.recs.get(id); if (!r || r.state !== 'active' || document.hidden) return;
    const fresh = (X.views.get(id) || []).filter(v => !v.out && v.state === 'recv');
    if (!fresh.length) return;
    const upto = Math.max(...fresh.map(v => v.ctr));
    for (const v of fresh) { v.state = v.m.state = 'read'; if (v.ttl) v.expires = v.m.expires = Date.now() + v.ttl * 1000; }
    X.store.putMsgs(fresh.map(v => v.m)).then(() => { X.unread.set(id, 0); changed(id); expiry(); });
    sendObj(r, { t: 'read', upto }, false);
  },
  close: async id => {
    const r = X.recs.get(id); if (!r) return;
    try { await S.net.request('secret_close', { chat: id }); }
    catch (e) { if (!e || !['secret_state', 'not_member', 'forbidden'].includes(e.code)) { toast((e && e.text) || OFFLINE); return; } }
    await ended(r, false);
  },
});

// ------------------------------------------------------------ self-destruct
let expTimer;
function expiry() {
  clearTimeout(expTimer);
  let next = Infinity;
  for (const list of X.views.values()) for (const v of list) if (v.expires) next = Math.min(next, v.expires);
  if (next !== Infinity) expTimer = setTimeout(expire, Math.min(Math.max(0, next - Date.now()) + 30, 2 ** 31 - 1));
}
async function expire() {
  const now = Date.now();
  for (const [id, list] of X.views) {
    const gone = list.filter(v => v.expires && v.expires <= now).map(v => v.key);
    if (gone.length) await drop(X.recs.get(id), gone);
  }
  expiry();
}

// ------------------------------------------------------------ starting one
hooks.startSecret = user => startSecret(user);
async function startSecret(user) {
  $('m-new').hidden = true;
  if (!(await init())) { toast('این مرورگر از رمزنگاری گفت‌وگوی محرمانه پشتیبانی نمی‌کند.'); return; }
  if (!X.hello) { toast(OFFLINE); return; }
  try {
    const k = await offerKeys();
    const { chat } = await S.net.request('secret_open', { peer: user, keys: k.list });
    const rec = { chat: chat.id, role: 'i', state: 'requested', peer: user, peerName: chat.title, priv: k.priv, pub: k.pub, created: Date.now(), sendCtr: 0, recvCtr: 0, ttl: 0 };
    await save(rec);
    S.chats.set(chat.id, Object.assign({}, chat, { pts: 0, read_pts: 0, peer_read_pts: 0, unread: 0, last: { at: new Date().toISOString(), from: '', text: '' } }));
    refresh(); hooks.openChat(chat.id);
  } catch (e) { toast((e && e.text) || 'گفت‌وگوی محرمانه ساخته نشد.'); }
}

// acceptInvite answers an invitation on this device, the one it was opened on.
async function acceptInvite(rec) {
  try {
    const k = await answerKey(rec.offer);
    const theirs = rec.offer.find(o => o.alg === k.alg).key;
    const keys = await derive(k.alg, k.priv, theirs, theirs, k.pub);
    await S.net.request('secret_accept', { chat: rec.chat, key: { alg: k.alg, key: k.pub } });
    Object.assign(rec, { state: 'active', alg: k.alg, offer: null, send: keys.r2i, recv: keys.i2r, fp: keys.fp });
    await save(rec); X.views.set(rec.chat, []); X.unread.set(rec.chat, 0);
  } catch (e) {
    if (e && ['secret_state', 'not_member', 'forbidden'].includes(e.code)) { await ended(rec, false); toast('این دعوت روی دستگاه دیگری پذیرفته شده یا پایان یافته است.'); }
    else toast((e && e.text) || OFFLINE);
  }
}

hooks.openSecret = async chat => {
  await init();
  const rec = X.recs.get(chat.id);
  if (rec && rec.state === 'invited') await acceptInvite(rec);
  if (S.active !== chat.id) return;
  openPane(chat);
  X.unread.set(chat.id, (X.views.get(chat.id) || []).filter(v => !v.out && v.state === 'recv' && v.t === 'text').length);
  refresh();
};

// ------------------------------------------------------------ the chat list
hooks.hideChat = c => c.type === 'secret' && !X.recs.has(c.id) && S.active !== c.id;
hooks.decorRow = (chat, row) => {
  if (chat.type !== 'secret') return;
  row.classList.add('m-secret');
  const lock = el('span', 'm-lock'); lock.innerHTML = svg(LOCK); row.querySelector('.m-row-name').prepend(lock);
  const rec = X.recs.get(chat.id), list = X.views.get(chat.id) || [], last = list[list.length - 1];
  const line = row.querySelector('.m-row-last');
  let text = '';
  if (!rec) text = 'پایان یافته';
  else if (rec.state === 'requested') text = `در انتظار پاسخ ${rec.peerName}`;
  else if (rec.state === 'invited') { text = 'دعوت به گفت‌وگوی محرمانه'; line.classList.add('m-invite'); }
  else if (last) text = (last.out ? 'شما: ' : '') + (last.t === 'text' ? last.text.replace(/\s+/g, ' ') : last.t === 'shot' ? 'عکس از صفحه' : 'تایمر خودنابودی: ' + ttlText(last.ttl));
  if (!(S.typing.get(chat.id)?.until > Date.now())) line.replaceChildren(document.createTextNode(text));
  const n = X.unread.get(chat.id) || 0, badge = row.querySelector('.m-badge:not(.m-badge-at)');
  chat.unread = n; // the list rolls the badge from this count
  badge.classList.toggle('on', n > 0); badge.textContent = n ? Number(n).toLocaleString('fa-IR') : '';
};

// The new-chat sheet gets a switch: with it on, picking a colleague starts a
// secret chat instead of the ordinary one.
function wireSheet() {
  const sheet = $('m-new'), card = sheet && sheet.querySelector('.m-new-card');
  if (!card || !supported()) return;
  const b = el('button', 'm-secret-toggle'); b.type = 'button'; b.setAttribute('aria-pressed', 'false');
  b.innerHTML = svg(LOCK); b.appendChild(el('span', '', 'گفت‌وگوی محرمانه (فقط روی همین دستگاه)'));
  const title = $('m-new-title'), plain = title.textContent;
  const set = on => { b.setAttribute('aria-pressed', String(on)); sheet.classList.toggle('m-secret-mode', on); title.textContent = on ? 'گفت‌وگوی محرمانهٔ تازه' : plain; };
  b.addEventListener('click', () => set(b.getAttribute('aria-pressed') !== 'true'));
  $('m-new-btn').addEventListener('click', () => set(false));
  title.after(b);
  hooks.pickPerson = user => { if (!sheet.classList.contains('m-secret-mode')) return false; startSecret(user); return true; };
  // Findable where people look for it: a row beside «گروه تازه» and
  // «کانال تازه» (the switch above does the same), and a private chat's own
  // menu in the list.
  const rowsWas = hooks.newRows;
  hooks.newRows = () => {
    const r = el('button', 'm-row m-secret-row'); r.type = 'button';
    const a = el('div', 'm-av g-new-av'); a.innerHTML = svg(LOCK);
    r.append(a, el('div', 'm-row-name', 'گفت‌وگوی محرمانهٔ تازه'));
    r.addEventListener('click', () => { set(true); $('m-people-q').focus(); });
    return [...(rowsWas ? rowsWas() : []), r];
  };
  const menuWas = hooks.rowMenuItems;
  hooks.rowMenuItems = (chat, items, x, y) => {
    if (menuWas) menuWas(chat, items, x, y);
    if (chat.type === 'private' && chat.peer && chat.peer.id !== S.me) items.splice(1, 0, { icon: LOCK, label: 'شروع گفت‌وگوی محرمانه', run: () => startSecret(chat.peer.id) });
  };
}
wireSheet();

// ------------------------------------------------------------ the socket
S.net.addEventListener('state', async e => {
  if (e.detail !== 'online') { X.hello = false; return; }
  if (!(await init())) return;
  try {
    const r = await S.net.request('secret_hello', { device: X.device });
    X.hello = true;
    const live = new Set(r.live || []);
    // What waited arrived before this answer; once it is handled, chats the
    // server no longer has for this device are forgotten here too.
    X.q = X.q.then(async () => { for (const rec of [...X.recs.values()]) if (!live.has(rec.chat)) await ended(rec, false); }).then(flush).catch(() => { });
  } catch { }
});
S.net.addEventListener('secret', e => { const it = e.detail.d; X.q = X.q.then(() => onItem(it)).catch(() => { }); });
