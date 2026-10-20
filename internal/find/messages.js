// messages.js: the messenger screen (docs/chat/SPEC.md). This module owns the
// chat list, opening a chat, the socket's events and start-up; the open chat
// is chatpane.js, the message bar chatcompose.js, formatting chatfmt.js and
// the overlays chatmenu.js. Every piece of user text reaches the page through
// text nodes, never innerHTML: a message is data, not markup (check 5).
import { Cache, flip, enter, roll, reduced } from '/chatcore.js';
import { $, el, fa, svg, timeFmt, S, hooks, toast } from '/chatstate.js';
import { avatar, header, paintStatus, renderMessages, loadPins, paintPinBar, jumpTo, nextMention, removeMessages, findMsg, repaint, CONN_TEXT } from '/chatpane.js';
import { wirePane } from '/chatwire.js';
import { composer, flushOutbox, clearDraft, paintFoot } from '/chatcompose.js';
import { plain } from '/chatfmt.js';
import { closeOverlay } from '/chatmenu.js';
import '/chatmedia.js';
import '/chatattach.js';
import '/chatviewer.js';
import '/chatgroup.js';
import '/chatsecret.js'; // secret chats (phase 10): hooks.hideChat, decorRow, openSecret, pickPerson
import '/chatlist.js'; // folders, archive, pins, mute, the row menu (phase 5): hooks.inFolder, order, listHead, listRow
import '/chatsearch.js'; // global and in-chat search (phase 5)
import '/chatnotify.js'; // notification settings and in-app alerts (phase 6)
import '/chatcall.js'; // one-to-one calls (phase 7): header buttons, call lines, the call list
import '/chatperson.js'; // a colleague's contact card from the chat header
import '/chatgcall.js'; // group voice and video calls: header buttons, the join bar, call lines
import '/chatextras.js'; // phase 11: polls, locations, stickers and GIFs, scheduled messages, translation

hooks.jumpTo = jumpTo;

// ---------------------------------------------------------------- chat list
// A last message that was media reads as its icon and what it was, or the
// icon before its caption (a file shows its name) -- never a blank line.
const MEDIA = { photo: ['📷', 'عکس'], video: ['🎥', 'ویدیو'], round: ['📹', 'پیام ویدیویی'], voice: ['🎤', 'پیام صوتی'], file: ['📎', 'فایل'], doc: ['📄', 'سند'] };
function lastText(last) {
  const words = plain(last.text), m = MEDIA[last.kind];
  if (!m) return words;
  const name = last.kind === 'file' && last.media && last.media.name;
  return m[0] + ' ' + (words || name || m[1]);
}
function preview(chat) {
  const t = S.typing.get(chat.id);
  if (t && t.until > Date.now()) { const d = el('span', 'm-dots'); d.append(el('i'), el('i'), el('i')); return d; }
  if (chat.draft && chat.id !== S.active) { const s = el('span'); s.append(el('span', 'draft', 'پیش‌نویس: '), document.createTextNode(plain(chat.draft))); return s; }
  const last = chat.last;
  if (!last) return document.createTextNode('');
  if (last.kind === 'service' && hooks.serviceText) return document.createTextNode(hooks.serviceText(last, chat));
  const who = last.from === S.me && chat.type !== 'saved' ? 'شما: ' : (hooks.lastPrefix ? hooks.lastPrefix(chat, last) : '');
  return document.createTextNode(who + lastText(last));
}
function sortedChats() {
  return [...S.chats.values()]
    .filter(c => hooks.inFolder ? hooks.inFolder(c) : (S.folder !== 'unread' || c.unread > 0))
    .filter(c => !S.filter || (c.title || '').toLowerCase().includes(S.filter))
    .filter(c => !hooks.hideChat || !hooks.hideChat(c))
    .sort(hooks.order || ((a, b) => new Date((b.last && b.last.at) || 0) - new Date((a.last && a.last.at) || 0)));
}
// One's own last message carries the ticks the bubbles do: one once the
// server has it, two (blue) once the other side has read it, a clock while
// it is still on its way.
function rowTick(chat) {
  const l = chat.last;
  if (!l || l.from !== S.me || l.kind === 'service' || chat.type === 'saved' || chat.type === 'channel') return null;
  const s = el('span', 'm-row-tick'); s.setAttribute('role', 'img');
  if (!l.pts) {
    s.setAttribute('aria-label', 'در صف ارسال');
    s.innerHTML = '<svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="8"/><path d="M12 8v4l2.5 1.5"/></svg>';
    return s;
  }
  const read = !!(chat.peer_read_pts && l.pts <= chat.peer_read_pts);
  s.classList.toggle('read', read); s.setAttribute('aria-label', read ? 'خوانده شد' : 'فرستاده شد');
  s.innerHTML = `<svg viewBox="0 0 18 12" aria-hidden="true"><path d="M1 6.5l3.2 3.2L11 2.5"/>${read ? '<path d="M6.8 9.7L7.4 10.3 15.6 2.5"/>' : ''}</svg>`;
  return s;
}
function rowFor(chat) {
  const r = el('button', 'm-row'); r.type = 'button'; r.dataset.key = String(chat.id); r.setAttribute('role', 'listitem');
  if (chat.id === S.active) r.setAttribute('aria-current', 'true');
  const main = el('div', 'm-row-main');
  const top = el('div', 'm-row-top'), bot = el('div', 'm-row-bot');
  const name = el('span', 'm-row-name', chat.title); name.dir = 'auto';
  const kind = hooks.rowKind && hooks.rowKind(chat); if (kind) name.prepend(kind);
  const time = el('span', 'm-row-time', chat.last ? timeFmt.format(new Date(chat.last.at)) : '');
  const last = el('span', 'm-row-last'); last.dir = 'auto'; last.appendChild(preview(chat));
  const at = el('span', 'm-badge m-badge-at' + (chat.mentions ? ' on' : ''), '@'); at.setAttribute('aria-label', chat.mentions ? 'از شما نام برده شده' : '');
  const badge = el('span', 'm-badge' + (chat.unread ? ' on' : ''), chat.unread ? fa(chat.unread) : '');
  badge.setAttribute('aria-label', chat.unread ? `${fa(chat.unread)} پیام خوانده‌نشده` : '');
  const tick = rowTick(chat);
  top.append(...(tick ? [name, tick, time] : [name, time])); bot.append(last, at, badge); main.append(top, bot);
  r.append(avatar(chat), main);
  r.addEventListener('click', () => openChat(chat.id));
  if (hooks.decorRow) hooks.decorRow(chat, r);
  if (hooks.listRow) hooks.listRow(chat, r);
  return r;
}
function renderList() {
  const box = $('m-rows');
  const list = sortedChats();
  flip(box, () => {
    const old = new Map([...box.querySelectorAll('.m-row')].map(r => [r.dataset.key, r]));
    const note = hooks.notifyRow ? hooks.notifyRow() : null; // notifications: how to get them here (chatnotify.js)
    box.replaceChildren(...(note ? [note] : []), ...(hooks.listHead ? hooks.listHead() : []), ...list.map(c => {
      const next = rowFor(c), prev = old.get(String(c.id));
      if (prev) { // keep the badge element so its count can roll and its scale can spring
        const pb = prev.querySelector('.m-badge:not(.m-badge-at)'), nb = next.querySelector('.m-badge:not(.m-badge-at)');
        if (pb && nb && pb.classList.contains('on') && nb.classList.contains('on') && pb.textContent !== nb.textContent) { nb.replaceWith(pb); roll(pb, fa(c.unread)); }
      }
      return next;
    }));
    if (!list.length) box.appendChild(el('div', 'm-empty', S.filter ? 'گفت‌وگویی با این نام پیدا نشد.' : S.folder === 'all' ? 'هنوز گفت‌وگویی ندارید.' : 'گفت‌وگویی در این پوشه نیست.'));
  });
  // The open chat's way back counts what waits elsewhere (muted chats aside).
  const waiting = $('m-back-n');
  if (waiting) {
    let n = 0;
    for (const c of S.chats.values()) if (c.id !== S.active && !(c.muted_until && new Date(c.muted_until) > new Date())) n += c.unread || 0;
    waiting.textContent = n ? fa(n) : ''; waiting.classList.toggle('on', n > 0);
  }
  if (hooks.listRendered) hooks.listRendered();
}
hooks.renderList = renderList;

// ---------------------------------------------------------------- open a chat
async function openChat(id) {
  const chat = S.chats.get(id); if (!chat) return;
  if (S.active && S.active !== id) { S.cache.trim(S.active); S.sel.clear(); clearDraft(); }
  if (S.active !== id) { S.topic = null; S.topicInfo = null; }
  closeOverlay();
  S.active = id;
  document.dispatchEvent(new CustomEvent('chat-open', { detail: id })); // for chatsearch, chatnotify
  document.body.classList.add('open');
  if (history.state?.chat !== id) history.pushState({ chat: id }, '', '#c' + id);
  if (chat.type === 'secret' && hooks.openSecret) { renderList(); hooks.openSecret(chat); return; }
  const pane = $('m-chat');
  const msgs = el('div', 'm-msgs'); msgs.id = 'm-msgs'; msgs.setAttribute('role', 'log'); msgs.setAttribute('aria-live', 'polite');
  const jump = el('button', 'm-jump'); jump.type = 'button'; jump.setAttribute('aria-label', 'رفتن به آخرین پیام');
  jump.innerHTML = svg('<path d="M6 9l6 6 6-6"/>');
  jump.appendChild(el('span', 'm-badge'));
  jump.addEventListener('click', () => msgs.scrollTo({ top: msgs.scrollHeight, behavior: reduced() ? 'auto' : 'smooth' }));
  const at = el('button', 'm-jump m-at'); at.id = 'm-at'; at.type = 'button'; at.setAttribute('aria-label', 'رفتن به پیامی که از شما نام برده');
  at.append(el('span', 'm-at-sign', '@'), el('span', 'm-badge'));
  at.addEventListener('click', nextMention);
  const foot = composer(chat); foot.append(at, jump);
  pane.replaceChildren(header(chat), msgs, foot);
  wirePane(msgs);
  if (!reduced() && matchMedia('(min-width: 901px)').matches) msgs.animate([{ opacity: .4 }, { opacity: 1 }], { duration: 120 });
  msgs.addEventListener('scroll', () => {
    S.atBottom = msgs.scrollHeight - msgs.scrollTop - msgs.clientHeight < 80;
    jump.classList.toggle('on', msgs.scrollHeight - msgs.scrollTop - msgs.clientHeight > msgs.clientHeight);
    if (S.atBottom) markRead();
  }, { passive: true });
  // Pinned to the newest message while the reader is there. What arrives
  // after the chat was placed -- a photo, a video's frame, the fonts, a
  // taller message bar, the keyboard -- grows or shrinks the list under the
  // view, and left it stranded above the newest message after a refresh.
  // Somebody who scrolled up to read is never moved.
  const pin = () => { if (S.atBottom && S.active === id) msgs.scrollTop = msgs.scrollHeight; };
  msgs.addEventListener('load', pin, true); // images and posters: load does not bubble, so catch it on the way down
  new ResizeObserver(pin).observe(msgs);
  if (document.fonts && document.fonts.ready) document.fonts.ready.then(pin);
  paintStatus();
  renderList();
  hooks.openGroup && hooks.openGroup(chat);
  const unreadFrom = chat.unread ? chat.read_pts + 1 : 0;
  if (!S.msgs.has(id)) S.msgs.set(id, await S.cache.msgs(id));
  renderMessages({ toUnread: !!unreadFrom, unreadFrom, bottom: !unreadFrom });
  if (S.pins.has(id)) paintPinBar();
  loadPins(id);
  try {
    const r = await fetch(`/api/chat/chats/${id}/messages?limit=50`, { credentials: 'same-origin' });
    if (r.ok && S.active === id) { merge(id, (await r.json()).messages || []); renderMessages({ toUnread: !!unreadFrom, unreadFrom, bottom: !unreadFrom }); }
  } catch { }
  markRead();
}
hooks.openChat = openChat;

function merge(chatID, list) {
  const cur = S.msgs.get(chatID) || [];
  const byKey = new Map(cur.map(m => [m.pts || m.cid, m]));
  for (const m of list) {
    if (m.cid && byKey.has(m.cid)) byKey.delete(m.cid);
    byKey.set(m.pts, m);
  }
  const out = [...byKey.values()].sort((a, b) => (a.pts || 1e15) - (b.pts || 1e15));
  S.msgs.set(chatID, out);
  S.cache.putMsgs(list);
}
hooks.merge = merge;

let topObs;
function watchTop() {
  const box = $('m-msgs'); if (!box) return;
  topObs && topObs.disconnect();
  // At most two older pages without the person scrolling. A layout that left
  // the top of the chat always in view once fetched the whole history, page
  // after page, over mobile data. Their own scrolling re-arms it.
  if (!box.dataset.olderGuard) {
    box.dataset.olderGuard = '1'; S.olderRun = 0;
    const arm = () => { S.olderRun = 0; };
    for (const t of ['wheel', 'touchmove', 'pointerdown', 'keydown']) box.addEventListener(t, arm, { passive: true });
  }
  topObs = new IntersectionObserver(async ents => {
    if (!ents.some(e => e.isIntersecting) || S.loadingOlder || S.olderRun >= 2) return;
    const chat = S.chats.get(S.active);
    const inTopic = chat && chat.forum && S.topic != null;
    const list = (S.msgs.get(S.active) || []).filter(m => !inTopic || hooks.topicFilter(m)); const oldest = list.find(m => m.pts);
    if (!oldest || oldest.pts <= 1) return;
    S.loadingOlder = true; S.olderRun++;
    try {
      const r = await fetch(`/api/chat/chats/${S.active}/messages?before=${oldest.pts}&limit=50${inTopic ? hooks.topicQuery() : ''}`, { credentials: 'same-origin' });
      const older = r.ok ? (await r.json()).messages || [] : [];
      if (older.length) { merge(S.active, older); renderMessages({ keepScroll: true }); }
    } finally { S.loadingOlder = false; }
  }, { root: box, rootMargin: '300px 0px 0px 0px' });
  const s = box.querySelector('.m-top-sentinel'); if (s) topObs.observe(s);
}
hooks.watchTop = watchTop;

let readTimer;
function markRead() {
  const chat = S.chats.get(S.active);
  if (chat && chat.forum) { if (!document.hidden && S.atBottom && hooks.markTopicRead) hooks.markTopicRead(chat); return; }
  if (!chat || document.hidden || !S.atBottom || (chat.read_pts >= chat.pts && !chat.marked_unread && !chat.reactions)) return;
  clearTimeout(readTimer);
  readTimer = setTimeout(() => {
    chat.read_pts = chat.pts; chat.unread = 0; chat.mentions = 0; chat.marked_unread = false; chat.reactions = 0; renderList();
    S.net.send('read', { chat: chat.id, pts: chat.pts });
    S.cache.putChats([chat]);
    renderMessages();
  }, 250);
}

hooks.markRead = markRead;

// ---------------------------------------------------------------- updates
function accept(m) {
  const chat = S.chats.get(m.chat); if (!chat) { refreshChats(); return; }
  const list = S.msgs.get(m.chat);
  const wasNew = !list || !list.some(x => x.pts === m.pts || (m.cid && x.cid === m.cid && x.pts));
  merge(m.chat, [m]);
  chat.pts = Math.max(chat.pts, m.pts);
  if (!chat.last || new Date(m.at) >= new Date(chat.last.at)) chat.last = { id: m.id, pts: m.pts, text: m.text, from: m.from, at: m.at, kind: m.kind, media: m.media, service: m.service, from_name: m.from_name };
  if (m.from !== S.me && wasNew) {
    if (S.active === m.chat && S.atBottom && !document.hidden) markRead();
    else chat.unread = (chat.unread || 0) + 1;
    S.typing.delete(m.chat);
  }
  S.cache.putChats([chat]);
  renderList();
  if (S.active === m.chat) {
    renderMessages();
    if (wasNew && m.from !== S.me) { const n = $('m-msgs')?.querySelector(`[data-pts="${m.pts}"]`); if (n) enter(n); }
    paintStatus();
  }
}
hooks.accept = accept;

function applyUpdate(u) {
  const chat = S.chats.get(u.chat); if (!chat) { refreshChats(); return; }
  const d = u.d || {};
  switch (u.kind) {
    case 'message': accept(d); break;
    case 'read':
      if (d.user === S.me) { chat.read_pts = Math.max(chat.read_pts, d.pts); chat.unread = 0; chat.mentions = 0; renderList(); }
      else { chat.peer_read_pts = Math.max(chat.peer_read_pts, d.pts); renderList(); if (S.active === chat.id) renderMessages(); } // the list's ticks turn blue too
      break;
    case 'edit': {
      const m = findMsg(chat.id, d.id);
      if (m) {
        Object.assign(m, { text: d.text, entities: d.entities, edited: d.edited });
        S.cache.putMsgs([m]);
        if (S.active === chat.id) {
          repaint(m);
          const n = $('m-msgs')?.querySelector(`[data-id="${d.id}"] .m-text`);
          if (n && !reduced()) n.animate([{ opacity: .3 }, { opacity: 1 }], { duration: 240, easing: 'linear' });
        }
      }
      if (chat.last && chat.last.id === d.id) { chat.last.text = d.text; renderList(); }
      const pins = S.pins.get(chat.id); const p = pins && pins.find(x => x.id === d.id);
      if (p) { p.text = d.text; if (S.active === chat.id) paintPinBar(); }
      break;
    }
    case 'delete':
      removeMessages(chat.id, d.ids || []);
      if (chat.last && (d.ids || []).includes(chat.last.id)) refreshChats();
      break;
    case 'pin': case 'unpin': {
      const m = findMsg(chat.id, d.id); if (m) m.pinned = u.kind === 'pin';
      if (S.active === chat.id) loadPins(chat.id); else S.pins.delete(chat.id);
      break;
    }
    case 'preview': {
      const m = findMsg(chat.id, d.id);
      if (m) { m.preview = d.preview || null; m.fresh = true; S.cache.putMsgs([m]); if (S.active === chat.id) repaint(m); m.fresh = false; }
      break;
    }
    case 'react': {
      const m = findMsg(chat.id, d.id);
      if (m) {
        m.reactions = d.counts || [];
        if (d.user === S.me) m.mine = d.on ? [d.emoji] : [];
        S.cache.putMsgs([m]);
        if (S.active === chat.id) repaint(m, d.on && d.user !== S.me ? d.emoji : null);
      }
      break;
    }
  }
  chat.pts = Math.max(chat.pts, u.pts);
  S.cache.putChats([chat]);
}
async function refreshChats() {
  try {
    const r = await fetch('/api/chat/chats', { credentials: 'same-origin', cache: 'no-store' });
    if (!r.ok) return;
    for (const c of (await r.json()).chats || []) S.chats.set(c.id, Object.assign(S.chats.get(c.id) || {}, c, { mentions: c.mentions || 0 }));
    S.cache.putChats([...S.chats.values()]);
    renderList();
    if (S.active) paintStatus();
  } catch { }
}
hooks.refreshChats = refreshChats;

// ---------------------------------------------------------------- people
async function people(q) {
  const box = $('m-people');
  try {
    const r = await fetch('/api/chat/people?q=' + encodeURIComponent(q), { credentials: 'same-origin' });
    const list = r.ok ? (await r.json()).people || [] : [];
    const saved = el('button', 'm-row'); saved.type = 'button';
    saved.append(avatar({ type: 'saved' }), el('div', 'm-row-name', 'پیام‌های ذخیره‌شده'));
    saved.addEventListener('click', () => start(S.me));
    box.replaceChildren(...(!q && hooks.newRows ? hooks.newRows() : []), saved, ...list.map(p => {
      const b = el('button', 'm-row'); b.type = 'button';
      const n = el('div', 'm-row-name', p.name); n.dir = 'auto';
      b.append(avatar({ title: p.name, peer: { id: p.id, photo: p.photo } }), n);
      b.addEventListener('click', () => (hooks.pickPerson && hooks.pickPerson(p.id)) || start(p.id));
      return b;
    }));
  } catch { box.replaceChildren(el('div', 'm-empty', 'فهرست همکاران بارگذاری نشد.')); }
}
async function start(user) {
  $('m-new').hidden = true;
  const r = await fetch('/api/chat/open', { method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ user }) });
  const j = await r.json().catch(() => ({}));
  if (!r.ok) { toast(j.error || 'گفت‌وگو باز نشد.'); return; }
  S.chats.set(j.chat.id, Object.assign(S.chats.get(j.chat.id) || {}, j.chat));
  renderList(); openChat(j.chat.id);
}
hooks.start = start;

// ---------------------------------------------------------------- the keyboard
// A phone's keyboard: browsers that do not shrink the page for it (iPhone
// Safari, a WebView that pans) slid the whole chat up, header and messages
// out of sight. The open chat follows the visible part of the screen
// instead, the page itself never scrolls under it, and the newest message
// stays right above the message bar.
//
// It acts only when the visible part really changed, once a frame. iPhone
// fires viewport events all through an ordinary scroll; answering each one by
// re-measuring the chat and pinning it to the newest message pulled the list
// back down under the finger. Only a change of height -- the keyboard coming
// or going -- keeps the newest message in view.
//
// While the keyboard comes or goes it is followed every frame. iPhone moves
// the visible part first and says so only afterwards, and the chat used to
// wait for the word: for a moment, as the keyboard closed, the whole chat sat
// where the keyboard had pushed it, a quarter of the screen down, with the
// list showing above it (the recording of 2026-10-19).
(() => {
  const vv = window.visualViewport; if (!vv) return;
  const root = document.documentElement;
  let h0 = -1, top0 = -1, raf = 0, until = 0, hold = 0;
  // The keyboard's height, remembered: before it comes back the chat is
  // already that much shorter, so iPhone finds the message box uncovered and
  // does not push the page up at all -- and with nothing pushed up there is
  // nothing to snap back down when the keyboard goes, which is the jump that
  // following the keyboard frame by frame could not catch (still there on
  // 2026-10-19 after that try).
  let kb = 0; try { kb = Number(localStorage.getItem('msgr.kb')) || 0; } catch { }
  const box = () => $('m-input');
  const glide = () => { const c = $('m-chat'); if (!c) return; c.classList.add('kb-glide'); clearTimeout(glide.t); glide.t = setTimeout(() => c.classList.remove('kb-glide'), 420); };
  const fit = () => {
    raf = 0;
    const h = Math.round(vv.height), top = Math.round(vv.offsetTop);
    const k = window.innerHeight - h;
    root.classList.toggle('kb-open', k > 150);
    if (k > 150 && document.activeElement === box() && Math.abs(k - kb) > 4) { kb = k; try { localStorage.setItem('msgr.kb', String(k)); } catch { } }
    if (performance.now() < hold) { if (performance.now() < until) raf = requestAnimationFrame(fit); return; }
    if (h !== h0 || top !== top0) {
      const resized = h0 >= 0 && h !== h0;
      h0 = h; top0 = top;
      root.style.setProperty('--vv-h', h + 'px');
      root.style.setProperty('--vv-top', top + 'px');
      if (document.body.classList.contains('open')) {
        if (window.scrollY) window.scrollTo(0, 0);
        const box = $('m-msgs');
        if (resized && box && S.atBottom) box.scrollTop = box.scrollHeight;
      }
    }
    if (performance.now() < until) raf = requestAnimationFrame(fit);
  };
  const follow = ms => { until = Math.max(until, performance.now() + ms); if (!raf) raf = requestAnimationFrame(fit); };
  vv.addEventListener('resize', () => follow(400));
  vv.addEventListener('scroll', () => follow(400));
  // A text box gaining or losing focus is the keyboard about to move.
  const typing = e => e.target && e.target.matches && e.target.matches('input, textarea, [contenteditable="true"]');
  document.addEventListener('focusin', e => { if (typing(e)) follow(1000); });
  // Pressing the message box: the chat takes the height it will have before
  // the keyboard arrives; a press that ends elsewhere puts it back.
  document.addEventListener('touchstart', e => {
    const i = box();
    if (!kb || !i || e.target !== i || document.activeElement === i || !document.body.classList.contains('open')) return;
    glide(); h0 = top0 = -1;
    root.style.setProperty('--vv-top', '0px'); root.style.setProperty('--vv-h', (window.innerHeight - kb) + 'px');
    hold = performance.now() + 450;
    setTimeout(() => { if (document.activeElement !== i) { hold = 0; glide(); fit(); } }, 800);
  }, { capture: true, passive: true });
  // The keyboard going: straight to the whole height, gliding down with it,
  // rather than waiting for iPhone to say so.
  document.addEventListener('focusout', e => {
    if (!typing(e)) return;
    if (e.target === box() && document.body.classList.contains('open')) {
      glide(); h0 = top0 = -1;
      root.style.setProperty('--vv-top', '0px'); root.style.setProperty('--vv-h', window.innerHeight + 'px');
      hold = performance.now() + 450;
    }
    follow(1000);
  });
  fit();
})();

// ---------------------------------------------------------------- connection
// setConn shows the socket's state: in the open chat's status line and as the
// pill at the foot of the list. A reconnect that takes under a second shows
// nothing, so a normal page load never flashes "connecting".
let connTimer;
function setConn(s) {
  S.conn = s;
  clearTimeout(connTimer);
  const pill = $('m-conn');
  if (s === 'online') { pill?.classList.remove('on'); paintStatus(); return; }
  connTimer = setTimeout(() => {
    if (S.conn !== s) return;
    if ($('m-conn-text')) $('m-conn-text').textContent = CONN_TEXT[s] || CONN_TEXT.connecting;
    pill?.classList.add('on');
    paintStatus();
  }, s === 'offline' ? 0 : 1000);
}

// ---------------------------------------------------------------- wiring
function wire() {
  // <main> clips the three columns; it must never scroll itself (a focus() or
  // a browser's scroll-to-element would slide the chat header under the bar).
  const main = document.querySelector('main');
  main.addEventListener('scroll', () => { if (main.scrollTop || main.scrollLeft) main.scrollTo(0, 0); });
  $('m-filter').addEventListener('input', e => { S.filter = e.target.value.trim().toLowerCase(); renderList(); });
  for (const b of document.querySelectorAll('.m-fold')) b.addEventListener('click', () => {
    document.querySelectorAll('.m-fold').forEach(x => x.removeAttribute('aria-current'));
    b.setAttribute('aria-current', 'true'); S.folder = b.dataset.folder; renderList();
  });
  $('m-new-btn').addEventListener('click', () => { $('m-new').hidden = false; $('m-people-q').value = ''; people(''); $('m-people-q').focus(); });
  let pt; $('m-people-q').addEventListener('input', e => { clearTimeout(pt); pt = setTimeout(() => people(e.target.value), 200); });
  $('m-new').addEventListener('click', e => { if (e.target.id === 'm-new') $('m-new').hidden = true; });
  addEventListener('keydown', e => {
    if (e.key !== 'Escape') return;
    if (!$('m-new').hidden) { $('m-new').hidden = true; return; }
    if (S.sel.size) { S.sel.clear(); renderMessages(); paintFoot(); }
  });
  addEventListener('popstate', () => {
    if (!history.state?.chat) document.body.classList.remove('open');
    else if (history.state.topic == null && S.topic != null && hooks.leaveTopic) hooks.leaveTopic(); // back from a forum topic to its list
  });
  document.addEventListener('visibilitychange', () => { if (!document.hidden) markRead(); });
  // Edge swipe back on phones: the chat follows the finger; the release speed decides.
  const pane = $('m-chat'); let sx = 0, st = 0, dx = 0, tracking = false;
  hooks.wireDrop && hooks.wireDrop(pane);
  pane.addEventListener('pointerdown', e => { if (matchMedia('(max-width: 900px)').matches && e.clientX < 28) { tracking = true; sx = e.clientX; st = performance.now(); dx = 0; pane.style.transition = 'none'; pane.setPointerCapture(e.pointerId); } });
  pane.addEventListener('pointermove', e => { if (!tracking) return; dx = Math.max(0, e.clientX - sx); pane.style.transform = `translateX(${-dx}px)`; });
  pane.addEventListener('pointerup', () => {
    if (!tracking) return; tracking = false; pane.style.transition = ''; pane.style.transform = '';
    const v = dx / Math.max(1, performance.now() - st);
    if (dx > innerWidth * 0.4 || v > 0.5) history.back();
  });
  const net = S.net;
  net.addEventListener('state', e => {
    const s = e.detail;
    if (s === 'signedout') { location.href = '/login?next=/messages'; return; }
    if (s === 'online') {
      setConn('updating');
      net.send('sync', { state: Object.fromEntries([...S.chats.values()].map(c => [String(c.id), c.pts])) });
      setTimeout(() => { if (S.conn === 'updating' && net.up) setConn('online'); }, 8000);
      flushOutbox(); refreshChats();
    } else setConn(s);
  });
  net.addEventListener('synced', () => setConn('online'));
  // Back from the background on a socket that lived: who is online and when
  // others were last seen may have changed while the page was frozen.
  net.addEventListener('resumed', () => refreshChats());
  net.addEventListener('ready', e => { const d = e.detail.d || {}; S.me = d.user || S.me; if (Array.isArray(d.reactions) && d.reactions.length) S.reactions = d.reactions; });
  net.addEventListener('update', e => applyUpdate(e.detail.d));
  net.addEventListener('diff', e => { for (const u of e.detail.d.updates || []) applyUpdate(u); });
  net.addEventListener('reset', e => { S.msgs.delete(e.detail.d.chat); refreshChats(); if (S.active === e.detail.d.chat) openChat(S.active); });
  net.addEventListener('hidden', e => removeMessages(e.detail.d.chat, e.detail.d.ids || []));
  net.addEventListener('typing', e => {
    const d = e.detail.d; S.typing.set(d.chat, { user: d.user, action: d.action, until: d.action === 'cancel' ? 0 : Date.now() + 5000 });
    renderList(); if (S.active === d.chat) paintStatus();
    setTimeout(() => { renderList(); if (S.active === d.chat) paintStatus(); }, 5200);
  });
  net.addEventListener('presence', e => {
    const p = e.detail.d;
    for (const c of S.chats.values()) if (c.peer && c.peer.id === p.id) { Object.assign(c.peer, p); }
    renderList(); paintStatus();
  });
  net.addEventListener('draft', e => { const c = S.chats.get(e.detail.d.chat); if (c) { c.draft = e.detail.d.text; renderList(); } });
}

async function boot() {
  // Who is signed in -- but a stalled network must not keep the chats this
  // phone already has from the screen (2026-10-19: six minutes of a blank app
  // while the connection hung). Past a moment without an answer, it is
  // whoever was signed in here last, their chats from this phone's store;
  // the answer, when it comes, corrects that if it has to. undefined is no
  // answer at all; an answer of nobody is the server's to give.
  const nameOf = m => (m && (m.username || (m.user && m.user.username))) || '';
  const keep = u => { try { if (u) localStorage.setItem('msgr.me', u); } catch { } };
  const ask = fetch('/api/me', { credentials: 'same-origin' }).then(r => r.ok ? r.json() : null, () => undefined);
  const me = await Promise.race([ask, new Promise(done => setTimeout(done, 2500))]);
  if (me === undefined) {
    try { S.me = localStorage.getItem('msgr.me') || ''; } catch { S.me = ''; }
    ask.then(late => { if (late && nameOf(late) !== S.me) location.reload(); else if (late) keep(nameOf(late)); });
  } else { S.me = nameOf(me); keep(S.me); }
  S.cache = await new Cache(S.me || 'anon').open();
  for (const c of await S.cache.chats()) S.chats.set(c.id, c);
  wire();
  if (S.chats.size) renderList();
  // The list from the server, but not waited on for ever: the socket brings
  // what is new once the network is back.
  await Promise.race([refreshChats(), new Promise(done => setTimeout(done, 4000))]);
  S.net.connect();
  const m = location.hash.match(/^#c(\d+)$/); if (m && S.chats.has(Number(m[1]))) openChat(Number(m[1]));
  // A link to #c<id> while this page is open (a message found with Ctrl+K,
  // nav.js) changes only the hash, so it is read here as well as at load.
  addEventListener('hashchange', () => {
    const h = location.hash.match(/^#c(\d+)$/), id = h ? Number(h[1]) : 0;
    if (id && S.chats.has(id) && S.active !== id) openChat(id);
  });
  hooks.joinFromHash && hooks.joinFromHash();
  // Opened from the settings page's rows: saved messages, the calls, the folders.
  const go = { '#saved': () => start(S.me), '#calls': () => import('/chatcalls.js').then(m => m.openCalls()),
    '#folders': () => import('/chatfolders.js').then(m => m.openFolders()) }[location.hash];
  if (go) { history.replaceState(null, '', location.pathname); go(); }
}
boot();
