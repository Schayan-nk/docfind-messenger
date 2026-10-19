// chatlist.js: how a person keeps their chat list (docs/chat/SPEC.md 6.5,
// 8.2): folders (tabs on a phone, the rail on a desktop), the archive,
// pinned chats, muting, read and unread, the row menu (right click or long
// press) and swipes on a phone. The folder editor is chatfolders.js, loaded
// on first use. Every change is the person's own; the server keeps it and
// tells their other devices.
import { reduced, SPRING } from '/chatcore.js';
import { $, el, fa, svg, S, hooks, toast, vibrate } from '/chatstate.js';
import { openMenu } from '/chatmenu.js';
import { api, ask, forget } from '/chatgroup.js';

S.folders = [];
const editor = () => import('/chatfolders.js');
const ICON = {
  pin: '<path d="M12 17v5"/><path d="M5 17h14l-2-4V6a2 2 0 0 0-2-2H9a2 2 0 0 0-2 2v7Z"/>',
  mute: '<path d="M11 5L6 9H2v6h4l5 4z"/><path d="M23 9l-6 6M17 9l6 6"/>',
  bell: '<path d="M18 8a6 6 0 0 0-12 0c0 7-3 9-3 9h18s-3-2-3-9"/><path d="M13.7 21a2 2 0 0 1-3.4 0"/>',
  archive: '<rect x="2" y="3" width="20" height="5" rx="1"/><path d="M4 8v11a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8M10 12h4"/>',
  read: '<path d="M2 12l5 5L18 6"/><path d="M12 17l1 1 9-11"/>', unread: '<circle cx="12" cy="12" r="5"/>',
  folder: '<path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/>',
  at: '<circle cx="12" cy="12" r="4"/><path d="M16 8v5a3 3 0 0 0 6 0v-1a10 10 0 1 0-4 8"/>',
  trash: '<path d="M3 6h18"/><path d="M8 6V4h8v2"/><path d="M6 6l1 14h10l1-14"/>',
  leave: '<path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4M16 17l5-5-5-5M21 12H9"/>',
  edit: '<path d="M12 20h9"/><path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4Z"/>',
  back: '<path d="M9 18l6-6-6-6"/>',
};
const GROUPISH = { group: 1, supergroup: 1, channel: 1 };
export const isMuted = c => !!(c.muted_until && new Date(c.muted_until) > new Date());
const unreadish = c => c.unread > 0 || !!c.marked_unread;
const folderOf = id => S.folders.find(f => f.id === id);

// ------------------------------------------------------------ which chats
// inFolder decides one chat for the open folder; order puts pinned chats
// first (in their own order), then the newest.
hooks.inFolder = c => {
  if (S.folder === 'archive') return !!c.archived;
  if (S.folder === 'all') return !c.archived;
  if (S.folder === 'unread') return !c.archived && unreadish(c);
  const f = folderOf(S.folder); if (!f) return !c.archived;
  if (f.exclude_chats.includes(c.id)) return false;
  if (f.include_chats.includes(c.id) || f.pinned.includes(c.id)) return true;
  const inc = f.include || {}, exc = f.exclude || {};
  if ((exc.archived && c.archived) || (exc.muted && isMuted(c)) || (exc.read && !unreadish(c))) return false;
  if (c.type === 'channel') return !!inc.channels;
  if (GROUPISH[c.type]) return !!inc.groups;
  if (c.type === 'private' || c.type === 'secret') return c.contact ? !!inc.contacts : !!inc.non_contacts;
  return false;
};
export function pinOf(c) {
  const f = folderOf(S.folder);
  if (f) { const i = f.pinned.indexOf(c.id); return i < 0 ? 0 : i + 1; }
  return (S.folder === 'archive') === !!c.archived ? (c.pinned || 0) : 0;
}
const at = c => new Date((c.last && c.last.at) || 0).getTime();
hooks.order = (a, b) => {
  const pa = pinOf(a), pb = pinOf(b);
  if (pa || pb) return !pa ? 1 : !pb ? -1 : pa - pb;
  return at(b) - at(a);
};

// The archive's row at the top of «همه».
hooks.listHead = () => {
  if (S.folder !== 'all' || S.filter) return [];
  const arch = [...S.chats.values()].filter(c => c.archived && (!hooks.hideChat || !hooks.hideChat(c)));
  if (!arch.length) return [];
  const r = el('button', 'm-row m-archive-row'); r.type = 'button'; r.dataset.key = 'archive';
  const av = el('div', 'm-av m-av-archive'); av.innerHTML = svg(ICON.archive);
  const main = el('div', 'm-row-main'), top = el('div', 'm-row-top'), bot = el('div', 'm-row-bot');
  top.append(el('span', 'm-row-name', 'بایگانی'));
  const names = arch.sort((a, b) => at(b) - at(a)).slice(0, 4).map(c => c.title).join('، ');
  const last = el('span', 'm-row-last', names); last.dir = 'auto';
  const n = arch.filter(unreadish).length;
  const badge = el('span', 'm-badge muted' + (n ? ' on' : ''), n ? fa(n) : '');
  bot.append(last, badge); main.append(top, bot); r.append(av, main);
  r.addEventListener('click', () => setFolder('archive'));
  return [r];
};

// listRow adds what the row shows of the person's own settings.
hooks.listRow = (chat, r) => {
  const name = r.querySelector('.m-row-name'), bot = r.querySelector('.m-row-bot'), badge = r.querySelector('.m-badge:not(.m-badge-at)');
  const muted = isMuted(chat);
  if (muted && name) { const i = el('span', 'm-row-muted'); i.innerHTML = svg(ICON.mute); i.setAttribute('aria-label', 'بی‌صدا'); name.appendChild(i); }
  if (badge) {
    badge.classList.toggle('muted', muted);
    if (!chat.unread && chat.marked_unread) { badge.classList.add('on', 'dot'); badge.setAttribute('aria-label', 'خوانده‌نشده'); }
  }
  if (chat.reactions && bot) {
    const h = el('span', 'm-badge m-badge-react on', '❤'); h.setAttribute('aria-label', `${fa(chat.reactions)} واکنش تازه به پیام‌های شما`);
    bot.insertBefore(h, badge || null);
  }
  if (pinOf(chat) && bot && !chat.unread && !chat.marked_unread && !chat.mentions) {
    const p = el('span', 'm-row-pin'); p.innerHTML = svg(ICON.pin); p.setAttribute('aria-label', 'سنجاق‌شده'); bot.appendChild(p);
  }
  r.addEventListener('contextmenu', e => { e.preventDefault(); rowMenu(chat, r, e.clientX, e.clientY); });
};

// ------------------------------------------------------------- settings
async function prefs(chat, req) {
  try {
    const j = await api(`/api/chat/chats/${chat.id}/prefs`, req);
    applyPrefs(j.prefs);
  } catch (e) { toast(e.text || 'انجام نشد؛ دوباره تلاش کنید.'); }
}
function applyPrefs(p) {
  const c = S.chats.get(p.chat); if (!c) return;
  Object.assign(c, { pinned: p.pinned, archived: p.archived, muted_until: p.muted_until, mentions_only: p.mentions_only, marked_unread: p.marked_unread, sound: p.sound });
  S.cache.putChats([c]);
  hooks.renderList();
}
S.net.addEventListener('prefs', e => applyPrefs(e.detail.d));
S.net.addEventListener('pins_order', () => hooks.refreshChats());
S.net.addEventListener('folders', () => loadFolders());
S.net.addEventListener('chat_cleared', e => forget(e.detail.d.chat));
// A new message brings an archived chat back unless it is muted (as the server does).
S.net.addEventListener('update', e => {
  const u = e.detail.d; const c = u && u.kind === 'message' && S.chats.get(u.chat);
  if (c && c.archived && !isMuted(c) && u.d && u.d.from !== S.me) { c.archived = false; c.pinned = 0; hooks.renderList(); }
});

function markRead(chat) {
  chat.unread = 0; chat.mentions = 0; chat.marked_unread = false; chat.reactions = 0; chat.read_pts = chat.pts;
  S.net.send('read', { chat: chat.id, pts: chat.pts });
  S.cache.putChats([chat]); hooks.renderList();
}
async function togglePin(chat) {
  const f = folderOf(S.folder);
  if (!f) return prefs(chat, { pinned: !pinOf(chat) });
  const pinned = pinOf(chat) ? f.pinned.filter(x => x !== chat.id) : [...f.pinned, chat.id];
  if (pinned.length > 20) { toast('بیش از این نمی‌توان در این پوشه سنجاق کرد.'); return; }
  await saveFolder({ ...f, pinned });
}
export async function saveFolder(f) {
  try { const j = await api('/api/chat/folders', f); const i = S.folders.findIndex(x => x.id === j.folder.id); i >= 0 ? (S.folders[i] = j.folder) : S.folders.push(j.folder); renderTabs(); hooks.renderList(); return j.folder; }
  catch (e) { toast(e.text || 'پوشه ذخیره نشد.'); return null; }
}

const MUTE = [[1, 'یک ساعت'], [8, 'هشت ساعت'], [24, 'یک روز'], [168, 'هفت روز']];
function muteMenu(chat, x, y) {
  const items = MUTE.map(([h, label]) => ({ icon: ICON.mute, label: 'بی‌صدا برای ' + label, run: () => prefs(chat, { muted: true, muted_until: new Date(Date.now() + h * 3600e3).toISOString() }) }));
  items.push({ icon: ICON.mute, label: 'بی‌صدا برای همیشه', run: () => prefs(chat, { muted: true }) });
  items.push({ icon: ICON.mute, label: 'بی‌صدا تا…', run: async () => {
    const v = await ask({ title: 'بی‌صدا تا', fields: [{ id: 'until', label: 'تاریخ و ساعت', type: 'datetime-local', ltr: true }], ok: 'بی‌صدا' });
    const t = v && new Date(v.until);
    if (t && t > new Date()) prefs(chat, { muted: true, muted_until: t.toISOString() });
  } });
  openMenu({ x, y, items });
}

export function rowMenu(chat, row, x, y) {
  const items = [];
  items.push({ icon: ICON.pin, label: pinOf(chat) ? 'برداشتن سنجاق' : 'سنجاق کردن', run: () => togglePin(chat) });
  items.push(unreadish(chat) || chat.reactions
    ? { icon: ICON.read, label: 'خوانده شد', run: () => markRead(chat) }
    : { icon: ICON.unread, label: 'علامت خوانده‌نشده', run: () => prefs(chat, { marked_unread: true }) });
  items.push(isMuted(chat)
    ? { icon: ICON.bell, label: 'صدادار کردن', run: () => prefs(chat, { muted: false }) }
    : { icon: ICON.mute, label: 'بی‌صدا…', run: () => setTimeout(() => muteMenu(chat, x, y), 180) });
  if (GROUPISH[chat.type] && chat.type !== 'channel') items.push({ icon: ICON.at, label: chat.mentions_only ? 'آگاه‌سازی برای همهٔ پیام‌ها' : 'آگاه‌سازی فقط با نام‌بردن از من', run: () => prefs(chat, { mentions_only: !chat.mentions_only }) });
  if (chat.type !== 'saved') items.push({ icon: ICON.archive, label: chat.archived ? 'خروج از بایگانی' : 'بایگانی', run: () => archive(chat, row) });
  items.push({ icon: ICON.folder, label: 'افزودن به پوشه…', run: () => setTimeout(() => folderMenu(chat, x, y), 180) });
  if (chat.type !== 'channel' && chat.type !== 'secret') items.push({ icon: ICON.trash, label: chat.type === 'private' ? 'حذف گفت‌وگو' : 'پاک کردن تاریخچه', danger: true, run: () => clear(chat) });
  if (GROUPISH[chat.type]) items.push({ icon: ICON.leave, label: chat.type === 'channel' ? 'ترک کانال' : 'ترک گروه', danger: true, run: () => leave(chat) });
  if (hooks.rowMenuItems) hooks.rowMenuItems(chat, items, x, y);
  vibrate(12);
  openMenu({ x, y, bubble: row, items });
}
function folderMenu(chat, x, y) {
  const items = S.folders.map(f => {
    const inIt = f.include_chats.includes(chat.id);
    return { icon: ICON.folder, label: (inIt ? '✓ ' : '') + f.title, run: () => saveFolder({ ...f, include_chats: inIt ? f.include_chats.filter(i => i !== chat.id) : [...f.include_chats, chat.id], exclude_chats: f.exclude_chats.filter(i => i !== chat.id) }) };
  });
  items.push({ icon: ICON.edit, label: 'پوشهٔ تازه…', run: () => editor().then(m => m.editFolder({ include_chats: [chat.id] })) });
  openMenu({ x, y, items });
}
function archive(chat, row) {
  const go = () => prefs(chat, { archived: !chat.archived });
  if (reduced() || !row || !row.isConnected) { go(); return; }
  row.animate([{ opacity: 1, transform: 'none' }, { opacity: 0, transform: 'translateX(-30%)' }], { duration: 200, easing: 'cubic-bezier(0.2,0,0,1)', fill: 'forwards' }).finished.then(go);
}
async function clear(chat) {
  const priv = chat.type === 'private';
  const v = await ask({ title: priv ? 'حذف گفت‌وگو' : 'پاک کردن تاریخچه', text: priv ? `همهٔ پیام‌های این گفت‌وگو از فهرست شما پاک می‌شود.` : 'پیام‌های تا امروز برای شما پاک می‌شود؛ دیگران آن‌ها را می‌بینند.',
    fields: priv ? [{ id: 'all', label: 'برای', type: 'select', options: [['0', 'فقط برای من'], ['1', `برای من و ${chat.title}`]], value: '0' }] : [], ok: 'حذف', danger: true });
  if (!v) return;
  try { await api(`/api/chat/chats/${chat.id}/clear`, { for_all: v.all === '1' }); toast(priv ? 'گفت‌وگو حذف شد.' : 'تاریخچه پاک شد.'); }
  catch (e) { toast(e.text || 'انجام نشد.'); }
}
async function leave(chat) {
  if (!await ask({ title: chat.type === 'channel' ? 'ترک کانال' : 'ترک گروه', text: `«${chat.title}» را ترک می‌کنید؟`, ok: 'ترک', danger: true })) return;
  try { await api(`/api/chat/chats/${chat.id}/leave`, {}); forget(chat.id); } catch (e) { toast(e.text || 'انجام نشد.'); }
}

// ------------------------------------------------------------ folders
export async function loadFolders() {
  try { S.folders = (await api('/api/chat/folders')).folders || []; } catch { return; }
  if (typeof S.folder === 'number' && !folderOf(S.folder)) S.folder = 'all';
  renderTabs(); hooks.renderList();
}
export function setFolder(id) {
  if (S.folder === id) return;
  const box = $('m-rows'), from = S.folder;
  S.folder = id; renderTabs(); hooks.renderList();
  if (!reduced() && box) { // the list slides the way the tabs moved (a fade on a desktop)
    const ids = tabIds(), dir = ids.indexOf(id) > ids.indexOf(from) ? -1 : 1;
    box.animate(matchMedia('(max-width: 900px)').matches ? [{ transform: `translateX(${dir * 12}%)`, opacity: .4 }, { transform: 'none', opacity: 1 }] : [{ opacity: .4 }, { opacity: 1 }], { duration: 200, easing: 'cubic-bezier(0.2,0,0,1)' });
  }
}
hooks.setFolder = setFolder;
const tabIds = () => ['all', 'unread', ...S.folders.map(f => f.id)];
function unreadIn(fid) {
  const was = S.folder; S.folder = fid;
  const n = [...S.chats.values()].filter(c => hooks.inFolder(c) && unreadish(c) && !isMuted(c) && (!hooks.hideChat || !hooks.hideChat(c))).length;
  S.folder = was; return n;
}
function tab(id, label, icon) {
  const b = el('button', 'm-tab'); b.type = 'button'; b.dataset.fid = String(id); b.setAttribute('role', 'tab');
  b.setAttribute('aria-selected', String(S.folder === id));
  if (icon) { const i = el('span', 'm-tab-ic'); /^</.test(icon) ? (i.innerHTML = svg(icon)) : (i.textContent = icon); b.appendChild(i); }
  const t = el('span', 'm-tab-t', label); t.dir = 'auto'; b.appendChild(t);
  const n = id === 'archive' ? 0 : unreadIn(id); if (n) b.appendChild(el('span', 'm-tab-n', fa(n)));
  b.addEventListener('click', () => setFolder(id));
  return b;
}
// renderTabs draws the folder tabs above the list and the same folders in the
// desktop rail; the archive shows its own heading with a way back.
export function renderTabs() {
  const list = document.querySelector('.m-list'); if (!list) return;
  let bar = $('m-tabs');
  if (!bar) { bar = el('div', 'm-tabs'); bar.id = 'm-tabs'; bar.setAttribute('role', 'tablist'); bar.setAttribute('aria-label', 'پوشه‌ها'); list.querySelector('.m-list-head').after(bar); }
  if (S.folder === 'archive') {
    const back = el('button', 'm-tab m-tab-back'); back.type = 'button'; back.innerHTML = svg(ICON.back);
    back.appendChild(el('span', 'm-tab-t', 'بایگانی')); back.setAttribute('aria-label', 'بازگشت از بایگانی');
    back.addEventListener('click', () => setFolder('all'));
    bar.replaceChildren(back); bar.hidden = false; bar.classList.add('arch');
  } else {
    const tabs = [tab('all', 'همه'), tab('unread', 'خوانده‌نشده'), ...S.folders.map(f => tab(f.id, f.title, f.icon))];
    const edit = el('button', 'm-tab m-tab-edit'); edit.type = 'button'; edit.innerHTML = svg(ICON.edit); edit.setAttribute('aria-label', 'ویرایش پوشه‌ها'); edit.title = 'ویرایش پوشه‌ها';
    edit.addEventListener('click', () => editor().then(m => m.openFolders()));
    const ink = el('span', 'm-tab-ink'); ink.setAttribute('aria-hidden', 'true');
    bar.replaceChildren(...tabs, edit, ink);
    bar.hidden = false; bar.classList.remove('arch');
    const on = bar.querySelector('[aria-selected="true"]');
    if (on) { // the underline slides to the chosen tab
      const prev = bar.inkAt;
      const to = { x: on.offsetLeft, w: on.offsetWidth };
      ink.style.transform = `translateX(${to.x}px)`; ink.style.width = to.w + 'px';
      if (prev && !reduced()) ink.animate([{ transform: `translateX(${prev.x}px)`, width: prev.w + 'px' }, { transform: `translateX(${to.x}px)`, width: to.w + 'px' }], { duration: 240, easing: 'cubic-bezier(0.2,0,0,1)' });
      bar.inkAt = to;
      on.scrollIntoView({ block: 'nearest', inline: 'nearest', behavior: reduced() ? 'auto' : 'smooth' });
    }
  }
  const rail = document.querySelector('.m-folders');
  if (rail) {
    const mk = (id, label, icon) => {
      const b = el('button', 'm-fold'); b.type = 'button'; b.dataset.folder = String(id);
      if (S.folder === id) b.setAttribute('aria-current', 'true');
      const i = el('span', 'm-fold-ic'); /^</.test(icon || '') || !icon ? (i.innerHTML = svg(icon || ICON.folder)) : (i.textContent = icon);
      const n = unreadIn(id); if (n) i.appendChild(el('span', 'm-fold-n', fa(n)));
      b.append(i, Object.assign(el('span', 'm-fold-t', label), { dir: 'auto' }));
      b.addEventListener('click', () => setFolder(id));
      return b;
    };
    rail.replaceChildren(mk('all', 'همه', '<path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/>'), mk('unread', 'خوانده‌نشده', '<circle cx="12" cy="12" r="9"/><circle cx="12" cy="12" r="3"/>'),
      ...S.folders.map(f => mk(f.id, f.title, f.icon)), Object.assign(el('button', 'm-fold m-fold-edit'), { type: 'button', title: 'ویرایش پوشه‌ها', onclick: () => editor().then(m => m.openFolders()) }));
    const last = rail.lastChild; last.innerHTML = svg(ICON.edit); last.setAttribute('aria-label', 'ویرایش پوشه‌ها');
  }
}
hooks.listRendered = () => { const bar = $('m-tabs'); if (bar && S.folder !== 'archive') renderTabsCounts(); };
function renderTabsCounts() {
  for (const b of document.querySelectorAll('#m-tabs .m-tab[data-fid]')) {
    const id = /^\d+$/.test(b.dataset.fid) ? Number(b.dataset.fid) : b.dataset.fid;
    const n = unreadIn(id); let c = b.querySelector('.m-tab-n');
    if (n && !c) { c = el('span', 'm-tab-n'); b.appendChild(c); }
    if (c) n ? (c.textContent = fa(n)) : c.remove();
  }
}

// ------------------------------------------------------------ swipes
// On a phone a row follows the finger: towards the end it archives, towards
// the start it marks read or unread. Past half its width, or a fast flick,
// does it; otherwise it springs back (SPEC 8.2). A long press opens the menu.
function wireSwipes() {
  const box = $('m-rows'); if (!box) return;
  let row = null, chat = null, x0 = 0, y0 = 0, t0 = 0, dx = 0, mode = '', act = null, press = 0;
  const reset = () => { clearTimeout(press); row = null; mode = ''; act && act.remove(); act = null; };
  box.addEventListener('pointerdown', e => {
    if (e.pointerType === 'mouse') return;
    const r = e.target.closest('.m-row'); if (!r || !r.dataset.key || r.dataset.key === 'archive') return;
    row = r; chat = S.chats.get(Number(r.dataset.key)); if (!chat) { row = null; return; }
    x0 = e.clientX; y0 = e.clientY; t0 = performance.now(); dx = 0; mode = '';
    press = setTimeout(() => { if (!mode && row) { mode = 'menu'; rowMenu(chat, row, x0, y0); } }, 500);
  });
  box.addEventListener('pointermove', e => {
    if (!row || mode === 'menu' || mode === 'scroll') return;
    const mx = e.clientX - x0, my = e.clientY - y0;
    if (!mode) {
      if (Math.abs(my) > 10 && Math.abs(my) > Math.abs(mx)) { mode = 'scroll'; clearTimeout(press); return; }
      if (Math.abs(mx) < 10) return;
      mode = 'swipe'; clearTimeout(press); try { row.setPointerCapture(e.pointerId); } catch { }
      act = el('div', 'm-swipe'); act.setAttribute('aria-hidden', 'true');
      Object.assign(act.style, { top: row.offsetTop + 'px', height: row.offsetHeight + 'px' });
      box.insertBefore(act, row);
    }
    dx = mx;
    const left = dx < 0; // towards the end of an RTL row: archive
    act.className = 'm-swipe ' + (left ? 'arch' : 'read');
    const w = row.offsetWidth, past = Math.abs(dx) > w * 0.3;
    act.replaceChildren(); if (past) { act.innerHTML = svg(left ? ICON.archive : (unreadish(chat) ? ICON.read : ICON.unread)); act.appendChild(el('span', '', left ? (chat.archived ? 'خروج از بایگانی' : 'بایگانی') : (unreadish(chat) ? 'خوانده شد' : 'خوانده‌نشده'))); }
    row.style.transform = `translateX(${dx}px)`;
  });
  const end = () => {
    if (!row) return;
    const r = row, c = chat, w = r.offsetWidth, v = Math.abs(dx) / Math.max(1, performance.now() - t0);
    if (mode === 'swipe') {
      r.addEventListener('click', ev => { ev.stopPropagation(); ev.preventDefault(); }, { capture: true, once: true });
      const doIt = Math.abs(dx) > w * 0.5 || v > 0.6;
      const back = () => { r.style.transform = ''; if (!reduced()) r.animate([{ transform: `translateX(${dx}px)` }, { transform: 'none' }], { duration: 320, easing: SPRING }); };
      if (doIt && dx < 0 && c.type !== 'saved') { prefs(c, { archived: !c.archived }); r.style.transform = ''; }
      else if (doIt && dx > 0) { back(); unreadish(c) ? markRead(c) : prefs(c, { marked_unread: true }); }
      else back();
    }
    reset();
  };
  box.addEventListener('pointerup', end);
  box.addEventListener('pointercancel', () => { if (row) row.style.transform = ''; reset(); });
}

// ------------------------------------------------------------ folder links
export async function folderFromHash() {
  const m = location.hash.match(/^#folder=([A-Za-z0-9_-]{8,64})$/); if (!m) return;
  history.replaceState(history.state, '', location.pathname);
  let p; try { p = await api('/api/chat/folder-link/' + m[1]); } catch (e) { toast(e.text || 'این پیوند پوشه دیگر معتبر نیست.'); return; }
  const names = (p.chats || []).map(c => '• ' + c.title).join('\n');
  if (!await ask({ title: 'پوشهٔ «' + p.title + '»', text: (p.chats || []).length ? `این گفت‌وگوها به پوشه می‌آیند و به آن‌ها می‌پیوندید:\n${names}` : 'این پوشه گفت‌وگوی مشترکی ندارد.', ok: 'افزودن پوشه' })) return;
  try {
    const j = await api(`/api/chat/folder-link/${m[1]}/join`, {});
    toast(j.joined ? `پوشه افزوده شد؛ به ${fa(j.joined)} گفت‌وگو پیوستید.` : 'پوشه افزوده شد.');
    await hooks.refreshChats(); await loadFolders(); setFolder(j.folder.id);
  } catch (e) { toast(e.text || 'پوشه افزوده نشد.'); }
}
addEventListener('hashchange', folderFromHash);
queueMicrotask(() => { wireSwipes(); loadFolders(); folderFromHash(); });
