// chatpane.js: the open chat (docs/chat/SPEC.md 6.3, 8.4): bubbles with their
// replies, forwards, formatting and reactions; the pinned bar; selection;
// jumping to a message; the message menu.
import { uuidv7, flip, enter, reduced, roll, SPRING } from '/chatcore.js';
import { $, el, fa, svg, timeFmt, dayFmt, dayKey, S, hooks, nameOf, toast, vibrate, facePhoto } from '/chatstate.js';
import { renderText, plain } from '/chatfmt.js';
import { confirmDelete, pickChat } from '/chatmenu.js';

export const I = {
  reply: '<path d="M9 17l-5-5 5-5"/><path d="M4 12h11a5 5 0 0 1 5 5v2"/>',
  edit: '<path d="M12 20h9"/><path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4Z"/>',
  copy: '<rect x="9" y="9" width="12" height="12" rx="2"/><path d="M5 15V5a2 2 0 0 1 2-2h10"/>',
  pin: '<path d="M12 17v5"/><path d="M5 17h14l-2-4V6a2 2 0 0 0-2-2H9a2 2 0 0 0-2 2v7Z"/>',
  fwd: '<path d="M15 17l5-5-5-5"/><path d="M20 12H9a5 5 0 0 0-5 5v2"/>',
  check: '<circle cx="12" cy="12" r="9"/><path d="M8 12l3 3 5-6"/>',
  trash: '<path d="M3 6h18"/><path d="M8 6V4h8v2"/><path d="M6 6l1 14h10l1-14"/>',
  quote: '<path d="M7 7h4v4H7zM13 7h4v4h-4z"/><path d="M7 11c0 3-1 5-3 6M13 11c0 3-1 5-3 6"/>'
};

// -------------------------------------------------------------- avatar
function hue(s) { let h = 0; for (const c of s) h = (h * 31 + c.codePointAt(0)) % 360; return h; }
export function avatar(chat, size) {
  const a = el('div', 'm-av');
  if (chat.type === 'saved') {
    a.classList.add('saved');
    a.innerHTML = svg('<path d="M19 21l-7-5-7 5V5a2 2 0 0 1 2-2h10a2 2 0 0 1 2 2z"/>');
  } else {
    const name = chat.title || '؟';
    a.textContent = [...name.trim()][0] || '؟';
    a.style.background = `hsl(${hue(chat.peer ? chat.peer.id : String(chat.id))} 45% 38%)`;
    a.appendChild(el('span', 'dot' + (chat.peer && chat.peer.online ? ' on' : '')));
    if (chat.peer) facePhoto(a, chat.peer.id, chat.peer.photo);
  }
  if (size) Object.assign(a.style, { width: size + 'px', height: size + 'px' });
  return a;
}
hooks.avatar = avatar;

// -------------------------------------------------------------- status
const CONN_TEXT = { offline: 'در انتظار شبکه…', connecting: 'در حال اتصال…', updating: 'در حال به‌روزرسانی…' };
export { CONN_TEXT };
export function statusText(chat) {
  if (S.conn !== 'online' && CONN_TEXT[S.conn]) return { text: CONN_TEXT[S.conn] };
  const g = hooks.groupStatus && hooks.groupStatus(chat); if (g) return g;
  const t = S.typing.get(chat.id);
  if (t && t.until > Date.now()) return { typing: true, text: t.action === 'typing' ? 'در حال نوشتن' : 'در حال ارسال' };
  if (chat.type === 'saved') return { text: 'یادداشت‌های شخصی' };
  const p = chat.peer;
  if (!p) return { text: '' };
  if (p.online) return { online: true, text: 'آنلاین' };
  if (p.last_seen) {
    const d = new Date(p.last_seen);
    return { text: 'آخرین بازدید ' + (dayKey(d) === dayKey(Date.now()) ? 'ساعت ' + timeFmt.format(d) : dayFmt.format(d)) };
  }
  return { text: { recently: 'به‌تازگی آنلاین بوده', week: 'در هفتهٔ گذشته آنلاین بوده', month: 'در ماه گذشته آنلاین بوده', long: 'مدت‌ها پیش آنلاین بوده' }[p.seen] || '' };
}
export function paintStatus() {
  const chat = S.chats.get(S.active), st = $('m-status'); if (!chat || !st) return;
  const s = statusText(chat);
  const span = el('span', s.online ? 'online' : '');
  if (s.typing) { const d = el('span', 'm-dots'); d.append(el('i'), el('i'), el('i')); span.append(document.createTextNode(s.text + ' '), d); }
  else span.textContent = s.text;
  const old = st.firstChild;
  if (old && old.textContent === span.textContent) return;
  st.replaceChildren(span);
  if (old && !reduced()) span.animate([{ transform: 'translateY(8px)', opacity: 0 }, { transform: 'none', opacity: 1 }], { duration: 240, easing: 'cubic-bezier(0.2,0,0,1)' });
}
hooks.paintStatus = paintStatus;

export function header(chat) {
  const h = el('div', 'm-chat-head t-' + chat.type);
  const back = el('button', 'm-icon-btn m-back'); back.type = 'button'; back.setAttribute('aria-label', 'بازگشت');
  back.innerHTML = svg('<path d="M9 18l6-6-6-6"/>');
  // What is waiting in the other chats rides on the way back (renderList).
  const waiting = el('span', 'm-back-n'); waiting.id = 'm-back-n'; back.appendChild(waiting);
  back.addEventListener('click', () => history.back());
  const title = el('div', 'm-title'); const name = el('b', '', chat.title); name.dir = 'auto';
  const st = el('div', 'm-status'); st.id = 'm-status';
  title.append(name, st);
  if (GROUPISH[chat.type]) { // the title opens the chat's info (chatgroup.js)
    title.classList.add('m-title-btn'); title.setAttribute('role', 'button'); title.tabIndex = 0; title.setAttribute('aria-label', 'اطلاعات ' + chat.title);
    const open = () => hooks.openInfo && hooks.openInfo(chat);
    title.addEventListener('click', open);
    title.addEventListener('keydown', e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); open(); } });
  }
  const av = avatar(chat, 40);
  // A private chat: the picture and the name open the colleague's card.
  if (chat.type === 'private' && chat.peer) {
    const open = () => hooks.openPerson && hooks.openPerson(chat.peer.id, chat);
    for (const t of [title, av]) {
      t.classList.add('m-title-btn'); t.setAttribute('role', 'button'); t.tabIndex = 0;
      t.addEventListener('click', open);
      t.addEventListener('keydown', e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); open(); } });
    }
    title.setAttribute('aria-label', 'اطلاعات تماس ' + chat.title);
    av.setAttribute('aria-hidden', 'true'); av.tabIndex = -1;
  }
  h.append(back, av, title);
  if (hooks.headerTools) hooks.headerTools(chat, h);
  return h;
}

// -------------------------------------------------------------- bubbles
function ticks(m, chat) {
  if (m.from !== S.me) return null;
  const s = el('span');
  if (!m.pts) {
    s.innerHTML = '<svg class="m-clock" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" role="img" aria-label="در صف ارسال"><circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/></svg>';
    return s.firstChild;
  }
  const read = chat.type === 'saved' || m.pts <= chat.peer_read_pts;
  s.innerHTML = `<svg class="m-ticks${read ? ' read' : ''}" viewBox="0 0 18 12" role="img" aria-label="${read ? 'خوانده شد' : 'ارسال شد'}"><path d="M1 6.5l3.2 3.2L11 2.5"/><path class="t2" d="M6.8 9.7L7.4 10.3 15.6 2.5"/></svg>`;
  return s.firstChild;
}
export const keyOf = m => m.cid || String(m.pts);

function replyStrip(m, chat) {
  const q = m.quote || null;
  const other = q && q.chat && q.chat !== m.chat;
  const r = m.reply;
  if (!r && !other) return null;
  const b = el('button', 'm-reply'); b.type = 'button';
  const who = el('b', '', other ? nameOf(q.from, null) : nameOf(r.from, chat)); who.dir = 'auto';
  const text = q && q.text ? q.text : (r && r.deleted ? 'پیام حذف شده' : plain(r ? r.text : ''));
  const t = el('span', 'm-reply-text', text || 'پیام'); t.dir = 'auto';
  b.append(who, t);
  if (q && q.text) b.classList.add('quoted');
  if (other) { b.classList.add('elsewhere'); b.disabled = true; }
  else { b.dataset.jump = String(r.pts); b.setAttribute('aria-label', 'رفتن به پیام پاسخ‌داده‌شده'); }
  return b;
}

function reactionRow(m) {
  if (!m.reactions || !m.reactions.length) return null;
  const row = el('div', 'm-reacts');
  for (const r of m.reactions) {
    const mine = (m.mine || []).includes(r.emoji);
    const c = el('button', 'm-chip' + (mine ? ' on' : '')); c.type = 'button';
    c.dataset.emoji = r.emoji; c.setAttribute('aria-pressed', mine ? 'true' : 'false');
    c.setAttribute('aria-label', `${r.emoji} ${fa(r.n)}`);
    c.append(el('span', 'm-chip-e', r.emoji), el('span', 'm-chip-n', fa(r.n)));
    row.appendChild(c);
  }
  return row;
}

const VISUAL = { photo: 1, video: 1 };
export function bubble(m, chat, group) {
  const b = el('div', 'm-bubble' + (m.from === S.me ? ' me' : ''));
  if (m.kind && m.kind !== 'text') b.classList.add('k-' + m.kind);
  if ((VISUAL[m.kind] || group) && !(m.text || '').trim() && !m.reply && !m.fwd) b.classList.add('m-bare');
  b.dataset.key = keyOf(m);
  if (m.id) b.dataset.id = String(m.id);
  if (m.pts) b.dataset.pts = String(m.pts);
  b.dir = 'auto';
  if (S.sel.size) {
    const on = S.sel.has(m.id);
    const c = el('span', 'm-check' + (on ? ' on' : '')); c.setAttribute('aria-hidden', 'true');
    b.classList.toggle('picked', on); b.setAttribute('aria-selected', on ? 'true' : 'false');
    b.appendChild(c);
  }
  if (m.fwd) {
    const f = el('div', 'm-fwd'); f.append(document.createTextNode('هدایت‌شده از '), Object.assign(el('b', '', m.fwd.name || nameOf(m.fwd.from, chat)), { dir: 'auto' }));
    b.appendChild(f);
  }
  const rs = replyStrip(m, chat); if (rs) b.appendChild(rs);
  // Kinds other than text (polls, stickers, media...) draw themselves through
  // hooks.kinds[kind](m, chat), registered by their own module.
  const draw = group ? null : hooks.kinds && hooks.kinds[m.kind];
  if (group && hooks.albumGrid) b.appendChild(hooks.albumGrid(group));
  else if (draw) { const k = draw(m, chat); if (k) b.appendChild(k); }
  const body = el('div', 'm-text'); body.dir = 'auto';
  body.appendChild(renderText(m.text || '', m.entities, keyOf(m), S));
  const card = m.preview && hooks.previewCard ? hooks.previewCard(m) : null;
  if (card && m.preview.above) b.appendChild(card);
  b.appendChild(body);
  if (card && !m.preview.above) b.appendChild(card);
  const meta = el('span', 'm-meta');
  if (m.edited) meta.appendChild(el('span', 'm-edited', 'ویرایش‌شده'));
  meta.appendChild(el('time', '', timeFmt.format(new Date(m.at))));
  const t = ticks(m, chat); if (t) meta.appendChild(t);
  if (m.failed) { meta.appendChild(el('span', 'm-failed', 'ارسال نشد؛ برای تلاش دوباره بزنید')); b.dataset.failed = '1'; }
  body.prepend(meta);
  const rr = reactionRow(m); if (rr) b.appendChild(rr);
  return b;
}

const GROUPISH = { group: 1, supergroup: 1, channel: 1 };
// A run is one person's messages in a row on one day (the group name shows on
// its first, the picture on its last).
const sameRun = (a, b) => !!(a && b && a.kind !== 'service' && b.kind !== 'service' && a.from === b.from && dayKey(a.at) === dayKey(b.at));
const shown = chat => { const all = S.msgs.get(chat.id) || []; return chat.forum && hooks.topicFilter ? all.filter(hooks.topicFilter) : all; };
function mark(b, list, i, j, chat) {
  if (GROUPISH[chat.type] && hooks.groupMark) hooks.groupMark(b, list[i], chat, !sameRun(list[i - 1], list[i]), !sameRun(list[j], list[j + 1]));
  return b;
}

export function renderMessages(opts = {}) {
  const chat = S.chats.get(S.active); if (!chat) return;
  const box = $('m-msgs'); if (!box) return;
  if (chat.forum && S.topic == null && hooks.topicList) { hooks.topicList(box, chat); return; } // a forum opens on its topics
  const list = shown(chat);
  const keepFromBottom = box.scrollHeight - box.scrollTop;
  const nodes = [el('div', 'm-top-sentinel')];
  let lastDay = '', unreadPlaced = false;
  for (let i = 0; i < list.length; i++) {
    const m = list[i];
    const d = dayKey(m.at);
    if (d !== lastDay) { nodes.push(el('div', 'm-date', dayFmt.format(new Date(m.at)))); lastDay = d; }
    if (!unreadPlaced && opts.unreadFrom && m.pts >= opts.unreadFrom && m.from !== S.me) { nodes.push(el('div', 'm-unread-bar', 'پیام‌های خوانده‌نشده')); unreadPlaced = true; }
    // Photos and videos sent together are one album bubble (SPEC 6.4).
    if (m.album && VISUAL[m.kind]) {
      let j = i; while (j + 1 < list.length && list[j + 1].album === m.album && VISUAL[list[j + 1].kind]) j++;
      if (j > i) { const group = list.slice(i, j + 1); nodes.push(mark(bubble(group.find(x => (x.text || '').trim()) || m, chat, group), list, i, j, chat)); i = j; continue; }
    }
    // One message that cannot be drawn must not take the whole chat with it.
    try {
      if (m.kind === 'service' && hooks.serviceLine) { nodes.push(hooks.serviceLine(m, chat)); continue; }
      nodes.push(mark(bubble(m, chat), list, i, i, chat));
    } catch (err) {
      const f = el('div', 'm-service m-broken'); f.dataset.key = keyOf(m);
      f.appendChild(el('span', '', 'این پیام نمایش داده نشد'));
      nodes.push(f);
      console.error('messenger: a message could not be drawn', m.id, err);
    }
  }
  const paint = () => box.replaceChildren(...nodes);
  opts.flip ? flip(box, paint) : paint();
  box.classList.toggle('selecting', S.sel.size > 0);
  if (opts.keepScroll) box.scrollTop = box.scrollHeight - keepFromBottom;
  else if (opts.toUnread) { const u = box.querySelector('.m-unread-bar'); u ? centre(box, u, false) : (box.scrollTop = box.scrollHeight); }
  else if (opts.bottom || S.atBottom) box.scrollTop = box.scrollHeight;
  hooks.watchTop && hooks.watchTop();
  paintMentionButton();
}
hooks.renderMessages = renderMessages;

// centre scrolls the message list (and only it) so node sits in the middle.
// scrollIntoView would also scroll <main>, which is overflow:hidden but still
// a scroll container, and push the chat header under the app bar.
function centre(box, node, smooth) {
  const top = node.offsetTop - box.clientHeight / 2 + node.offsetHeight / 2;
  box.scrollTo({ top: Math.max(0, top), behavior: smooth && !reduced() ? 'smooth' : 'auto' });
}

export const findMsg = (chatID, id) => (S.msgs.get(chatID) || []).find(x => x.id === id);
const nodeFor = m => $('m-msgs')?.querySelector(`[data-key="${CSS.escape(keyOf(m))}"]`);

// flash is the highlight a jumped-to message gets (SPEC 8.4).
function flash(node) {
  if (!node) return;
  node.classList.remove('flash'); void node.offsetWidth; node.classList.add('flash');
  setTimeout(() => node.classList.remove('flash'), 1300);
}

// jumpTo scrolls to a message by pts, loading older pages until it is there.
export async function jumpTo(pts) {
  const chatID = S.active; if (!chatID || !pts) return;
  for (let i = 0; i < 20; i++) {
    const n = $('m-msgs')?.querySelector(`[data-pts="${pts}"]`);
    if (n) { centre($('m-msgs'), n, true); flash(n); return; }
    const list = S.msgs.get(chatID) || []; const oldest = list.find(m => m.pts);
    if (!oldest || oldest.pts <= pts) break;
    const r = await fetch(`/api/chat/chats/${chatID}/messages?before=${oldest.pts}&limit=100`, { credentials: 'same-origin' }).catch(() => null);
    const older = r && r.ok ? (await r.json()).messages || [] : [];
    if (!older.length || S.active !== chatID) break;
    hooks.merge(chatID, older); renderMessages({ keepScroll: true });
  }
  toast('این پیام دیگر در دسترس نیست.');
}

// -------------------------------------------------------------- pins
export async function loadPins(chatID) {
  try {
    const r = await fetch(`/api/chat/chats/${chatID}/pins`, { credentials: 'same-origin', cache: 'no-store' });
    if (!r.ok) return;
    S.pins.set(chatID, (await r.json()).messages || []);
    S.pinAt.set(chatID, 0);
    if (S.active === chatID) paintPinBar(true);
  } catch { }
}
export function paintPinBar(animate) {
  const chat = S.chats.get(S.active); if (!chat) return;
  const pins = S.pins.get(chat.id) || [];
  let bar = $('m-pinbar');
  if (!pins.length) {
    if (bar) { if (reduced()) bar.remove(); else bar.animate([{ transform: 'none', opacity: 1 }, { transform: 'translateY(-100%)', opacity: 0 }], { duration: 200, easing: 'cubic-bezier(0.2,0,0,1)' }).finished.finally(() => bar.remove()); }
    return;
  }
  const at = Math.min(S.pinAt.get(chat.id) || 0, pins.length - 1);
  const p = pins[at];
  const fresh = !bar;
  if (fresh) {
    bar = el('div', 'm-pinbar'); bar.id = 'm-pinbar';
    $('m-chat').querySelector('.m-chat-head')?.after(bar);
  }
  const seg = el('div', 'm-pin-seg'); seg.setAttribute('aria-hidden', 'true');
  const shown = Math.min(pins.length, 4);
  for (let i = 0; i < shown; i++) seg.appendChild(el('i', i === Math.min(at, shown - 1) ? 'on' : ''));
  const go = el('button', 'm-pin-go'); go.type = 'button';
  const label = el('b', '', pins.length > 1 ? `پیام سنجاق‌شدهٔ ${fa(at + 1)} از ${fa(pins.length)}` : 'پیام سنجاق‌شده');
  const text = el('span', 'm-pin-text', plain(p.text) || 'پیام'); text.dir = 'auto';
  go.append(label, text);
  go.addEventListener('click', () => { jumpTo(p.pts); S.pinAt.set(chat.id, (at + 1) % pins.length); paintPinBar(); });
  const x = el('button', 'm-icon-btn m-pin-x'); x.type = 'button'; x.setAttribute('aria-label', 'برداشتن سنجاق'); x.title = 'برداشتن سنجاق';
  x.innerHTML = svg('<path d="M18 6L6 18M6 6l12 12"/>');
  x.addEventListener('click', () => doPin(p, false));
  bar.replaceChildren(seg, go, x);
  if (fresh && animate && !reduced()) bar.animate([{ transform: 'translateY(-100%)', opacity: 0 }, { transform: 'none', opacity: 1 }], { duration: 240, easing: 'cubic-bezier(0.2,0,0,1)' });
}

// -------------------------------------------------------------- mentions
function paintMentionButton() {
  const chat = S.chats.get(S.active), b = $('m-at'); if (!chat || !b) return;
  const n = chat.mentions || 0;
  b.classList.toggle('on', n > 0);
  const badge = b.querySelector('.m-badge');
  badge.classList.toggle('on', n > 0);
  roll(badge, n ? fa(n) : '');
}
export async function nextMention() {
  const chatID = S.active;
  const r = await fetch(`/api/chat/chats/${chatID}/mentions`, { credentials: 'same-origin', cache: 'no-store' }).catch(() => null);
  const pts = r && r.ok ? (await r.json()).pts || [] : [];
  if (pts.length) jumpTo(pts[0]);
}

// -------------------------------------------------------------- actions
const request = (t, d) => S.net.request(t, d).catch(e => { toast((e && e.text) || (e && e.code === 'offline' ? 'اتصال برقرار نیست؛ دوباره تلاش کنید.' : 'انجام نشد؛ دوباره تلاش کنید.')); throw e; });

export function doReact(m, emoji, on) {
  if (!m.id) return;
  vibrate(10);
  const before = { reactions: m.reactions, mine: m.mine };
  // Shown at once; the socket's update settles the real counts.
  let list = (m.reactions || []).map(r => ({ ...r }));
  for (const old of m.mine || []) { const r = list.find(x => x.emoji === old); if (r) r.n--; }
  list = list.filter(r => r.n > 0);
  if (on) { const r = list.find(x => x.emoji === emoji); r ? r.n++ : list.push({ emoji, n: 1 }); }
  m.reactions = list; m.mine = on ? [emoji] : [];
  repaint(m, on ? emoji : null);
  request('react', { chat: m.chat, id: m.id, emoji, on }).catch(() => { m.reactions = before.reactions; m.mine = before.mine; repaint(m); });
}
// repaint redraws one bubble in place; popEmoji plays the chip's spring.
hooks.repaint = (m) => repaint(m);
export function repaint(m, popEmoji) {
  if (m.album) { renderMessages(); return; }
  const old = nodeFor(m); const chat = S.chats.get(m.chat); if (!old || !chat) return;
  const list = shown(chat), i = list.indexOf(m);
  const next = i >= 0 ? mark(bubble(m, chat), list, i, i, chat) : bubble(m, chat);
  old.replaceWith(next);
  if (popEmoji && !reduced()) {
    const chip = next.querySelector(`.m-chip[data-emoji="${CSS.escape(popEmoji)}"]`);
    if (chip) {
      chip.querySelector('.m-chip-e').animate([{ transform: 'scale(0)' }, { transform: 'scale(1.2)', offset: .6 }, { transform: 'scale(1)' }], { duration: 360, easing: SPRING });
      burst(chip);
    }
  }
}
// burst: a few small particles leave a reaction chip.
function burst(chip) {
  const r = chip.getBoundingClientRect();
  for (let i = 0; i < 8; i++) {
    const p = el('i', 'm-spark');
    Object.assign(p.style, { left: r.left + r.width / 2 + 'px', top: r.top + r.height / 2 + 'px' });
    document.body.appendChild(p);
    const a = (i / 8) * Math.PI * 2, d = 18 + (i % 3) * 6;
    p.animate([{ transform: 'translate(-50%,-50%) scale(1)', opacity: 1 }, { transform: `translate(calc(-50% + ${Math.cos(a) * d}px), calc(-50% + ${Math.sin(a) * d}px)) scale(.3)`, opacity: 0 }], { duration: 420, easing: 'cubic-bezier(0.2,0,0,1)' }).finished.finally(() => p.remove());
  }
}
// heart: double press on a bubble floats a large heart up from the press point.
export function heart(x, y) {
  if (reduced()) return;
  const h = el('span', 'm-heart', '❤️'); Object.assign(h.style, { left: x + 'px', top: y + 'px' });
  document.body.appendChild(h);
  h.animate([{ transform: 'translate(-50%,-50%) scale(.4)', opacity: 0 }, { transform: 'translate(-50%,-60%) scale(1.3)', opacity: 1, offset: .35 }, { transform: 'translate(-50%,-160%) scale(1)', opacity: 0 }], { duration: 800, easing: 'cubic-bezier(0.2,0,0,1)' }).finished.finally(() => h.remove());
}

export function doPin(m, on) {
  if (!m.id) return;
  request('pin', { chat: m.chat, id: m.id, on }).then(() => {
    if (on && !reduced()) { // a small copy of the bubble rises to the bar
      const n = nodeFor(m), bar = $('m-pinbar') || $('m-chat').querySelector('.m-chat-head');
      if (n && bar) {
        const a = n.getBoundingClientRect(), b = bar.getBoundingClientRect();
        const g = n.cloneNode(true); g.classList.add('m-flight'); Object.assign(g.style, { left: a.left + 'px', top: a.top + 'px', width: a.width + 'px' });
        document.body.appendChild(g);
        g.animate([{ transform: 'none', opacity: 1 }, { transform: `translate(${b.left + 40 - a.left}px, ${b.top - a.top}px) scale(.25)`, opacity: 0 }], { duration: 420, easing: 'cubic-bezier(0.2,0,0,1)' }).finished.finally(() => g.remove());
      }
    }
  }).catch(() => { });
}

export async function doDelete(ids, chat) {
  const msgs = ids.map(id => findMsg(chat.id, id)).filter(Boolean);
  if (!msgs.length) return;
  const ans = await confirmDelete({ count: msgs.length, peer: chat.type === 'private' && chat.peer ? (chat.peer.name || chat.title) : null, forAllOnly: chat.type === 'saved' });
  if (!ans) return;
  try { await request('delete', { chat: chat.id, ids: msgs.map(m => m.id), for_all: ans.forAll }); } catch { return; }
  removeMessages(chat.id, msgs.map(m => m.id));
  endSelection();
}

// removeMessages plays the delete (scale to .8 and fade) and closes the gap.
export function removeMessages(chatID, ids) {
  const set = new Set(ids);
  const list = S.msgs.get(chatID) || [];
  const gone = list.filter(m => set.has(m.id));
  if (!gone.length) return;
  S.msgs.set(chatID, list.filter(m => !set.has(m.id)));
  S.cache.delMsgs(chatID, gone.map(m => m.pts));
  const pins = S.pins.get(chatID); if (pins) S.pins.set(chatID, pins.filter(p => !set.has(p.id)));
  if (S.active !== chatID) return;
  const nodes = gone.map(nodeFor).filter(Boolean);
  const after = () => { renderMessages({ flip: true }); paintPinBar(); };
  if (reduced() || !nodes.length) { after(); return; }
  Promise.all(nodes.map(n => n.animate([{ transform: 'none', opacity: 1 }, { transform: 'scale(.8)', opacity: 0 }], { duration: 200, easing: 'cubic-bezier(0.2,0,0,1)', fill: 'forwards' }).finished)).finally(after);
}

export async function doForward(ids, chat) {
  const msgs = ids.map(id => findMsg(chat.id, id)).filter(Boolean).sort((a, b) => a.pts - b.pts);
  if (!msgs.length) return;
  const to = await pickChat({ chats: [...S.chats.values()].filter(c => c.type !== 'secret').sort((a, b) => new Date((b.last && b.last.at) || 0) - new Date((a.last && a.last.at) || 0)), avatar });
  if (!to) return;
  try {
    const r = await request('forward', { from: chat.id, ids: msgs.map(m => m.id), to: to.chat, cid: uuidv7(), hide_sender: to.hideSender });
    for (const m of (r && r.messages) || []) hooks.accept(m);
    endSelection();
    toast(msgs.length > 1 ? `${fa(msgs.length)} پیام هدایت شد.` : 'پیام هدایت شد.');
  } catch { }
}

export async function copy(text) {
  try { await navigator.clipboard.writeText(text); toast('رونوشت شد.'); } catch { toast('رونوشت انجام نشد.'); }
}

// -------------------------------------------------------------- selection
export function toggleSelect(m) {
  if (!m.id) return;
  S.sel.has(m.id) ? S.sel.delete(m.id) : S.sel.add(m.id);
  renderMessages({ flip: S.sel.size <= 1 });
  hooks.paintFoot();
}
export function endSelection() {
  if (!S.sel.size) return;
  S.sel.clear(); renderMessages({ flip: true }); hooks.paintFoot();
}
export function selectionText(chat) {
  return (S.msgs.get(chat.id) || []).filter(m => S.sel.has(m.id)).map(m => m.text).join('\n\n');
}
