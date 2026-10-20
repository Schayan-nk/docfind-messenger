// chatextras.js: phase 11 of the messenger page (docs/chat/SPEC.md): polls,
// locations, stickers and GIFs, scheduled messages and translation. Each has
// its own module; this one joins them to the page through its hooks, without
// changing the modules that own the list, the chat and the message bar.
import { reduced } from '/chatcore.js';
import { el, svg, S, hooks, toast } from '/chatstate.js';
import { api } from '/chatgroup.js';
import { EXTRAS_CSS } from '/chatextrascss.js';
import { drawPoll, openPollMaker, pollUpdate, pollMenu } from '/chatpoll.js';
import { drawPlace, openPlacePicker, placeMenu } from '/chatplace.js';
import { drawSticker, togglePanel } from '/chatsticker.js';
import { wireSendMenu, paintScheduled, loadScheduled } from '/chatsched.js';

const style = document.createElement('style'); style.textContent = EXTRAS_CSS; document.head.appendChild(style);

// ------------------------------------------------------------ message kinds
hooks.kinds = hooks.kinds || {};
Object.assign(hooks.kinds, { poll: drawPoll, location: drawPlace, sticker: drawSticker, gif: drawSticker });

// ------------------------------------------------------------ attach sheet
const ATTACH = [
  ['نظرسنجی', '<path d="M4 20V10M10 20V4M16 20v-7M22 20H2"/>', chat => openPollMaker(chat)],
  ['موقعیت', '<path d="M12 22s7-6.2 7-12a7 7 0 1 0-14 0c0 5.8 7 12 7 12z"/><circle cx="12" cy="10" r="2.5"/>', chat => openPlacePicker(chat)],
];
const baseAttach = hooks.openAttach;
hooks.openAttach = chat => {
  if (baseAttach) baseAttach(chat);
  const row = document.querySelector('.m-attach-row'); if (!row) return;
  const n = row.children.length;
  ATTACH.forEach(([label, icon, run], i) => {
    const b = el('button', 'm-attach-opt'); b.type = 'button';
    const ic = el('span', 'm-attach-icon'); ic.innerHTML = svg(icon);
    b.append(ic, el('span', '', label));
    b.addEventListener('click', () => {
      const back = row.closest('.m-sheet-back');
      if (back) back.dispatchEvent(new MouseEvent('click', { bubbles: true })); // closes the sheet
      run(chat);
    });
    row.appendChild(b);
    if (!reduced()) ic.animate([{ transform: 'scale(0)' }, { transform: 'scale(1)' }], { duration: 260, delay: 80 + (n + i) * 30, easing: 'cubic-bezier(0.34, 1.56, 0.64, 1)', fill: 'backwards' });
  });
};

// ------------------------------------------------------------ message menu
let translateOn = false;
api('/api/chat/translate').then(j => { translateOn = !!j.available; }).catch(() => { });
const baseMenu = hooks.menuItems;
hooks.menuItems = (m, chat, items) => {
  if (baseMenu) baseMenu(m, chat, items);
  pollMenu(m, chat, items);
  placeMenu(m, chat, items);
  if (translateOn && m.id && (m.text || '').trim() && chat.type !== 'secret')
    items.push({ icon: '<path d="M4 5h8M8 3v2M5 9c1.5 3 4 5 7 6M11 5c-1 4-3.5 7-7 9"/><path d="M13 21l4-9 4 9M14.5 18h5"/>', label: 'ترجمه', run: () => translate(m) });
};

const LANGS = [['fa', 'فارسی'], ['en', 'انگلیسی'], ['de', 'آلمانی']];
function translate(m) {
  const back = el('div', 'm-ov m-modal-back'), card = el('div', 'm-modal');
  card.setAttribute('role', 'dialog'); card.setAttribute('aria-modal', 'true'); card.setAttribute('aria-label', 'ترجمه');
  const out = el('div', 'x-tr', 'در حال ترجمه…'); out.dir = 'auto'; out.setAttribute('aria-live', 'polite');
  const langs = el('div', 'x-langs');
  const go = async to => {
    langs.querySelectorAll('.x-chip').forEach(c => c.setAttribute('aria-pressed', String(c.dataset.to === to)));
    out.textContent = 'در حال ترجمه…';
    try { const j = await api(`/api/chat/chats/${m.chat}/messages/${m.id}/translate?to=${to}`); out.textContent = j.text; }
    catch (e) { out.textContent = (e && e.text) || 'ترجمه انجام نشد.'; }
  };
  for (const [to, name] of LANGS) { const c = el('button', 'x-chip', name); c.type = 'button'; c.dataset.to = to; c.addEventListener('click', () => go(to)); langs.appendChild(c); }
  const acts = el('div', 'm-modal-actions');
  const close = el('button', 'btn', 'بستن'); close.type = 'button';
  const copy = el('button', 'btn', 'رونوشت'); copy.type = 'button';
  copy.addEventListener('click', () => navigator.clipboard?.writeText(out.textContent).then(() => toast('رونوشت شد.'), () => { }));
  acts.append(copy, close);
  card.append(el('h2', 'm-modal-title', 'ترجمه'), langs, out, acts);
  back.appendChild(card); document.body.appendChild(back);
  const done = () => { back.remove(); removeEventListener('keydown', esc, true); };
  const esc = e => { if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); done(); } };
  addEventListener('keydown', esc, true);
  close.addEventListener('click', done);
  back.addEventListener('click', e => { if (e.target === back) done(); });
  if (!reduced()) card.animate([{ transform: 'scale(.9)', opacity: 0 }, { transform: 'none', opacity: 1 }], { duration: 140, easing: 'cubic-bezier(0.2,0,0,1)' });
  go(/[؀-ۿ]/.test(m.text) ? 'en' : 'fa');
}

// ------------------------------------------------------------ chat list
const LABEL = { poll: '📊 نظرسنجی', location: '📍 موقعیت', sticker: 'برچسب', gif: '🎞️ گیف' }; // a sticker leads with its own emoji
const baseRow = hooks.decorRow;
hooks.decorRow = (chat, row) => {
  if (baseRow) baseRow(chat, row);
  const last = chat.last;
  if (chat.type === 'secret' || !last || (last.text || '').trim()) return;
  const m = (S.msgs.get(chat.id) || []).find(x => x.id === last.id);
  const kind = last.kind || (m && m.kind);
  if (!LABEL[kind]) return;
  let text = LABEL[kind];
  if (kind === 'poll' && m && m.extra && m.extra.question) text += ': ' + m.extra.question;
  if (kind === 'sticker' && m && m.extra && m.extra.emoji) text = m.extra.emoji + ' ' + text;
  const line = row.querySelector('.m-row-last');
  if (line && !line.querySelector('.m-dots')) line.replaceChildren(document.createTextNode((last.from === S.me && chat.type !== 'saved' ? 'شما: ' : '') + text));
};

// ------------------------------------------------------------ updates
function applyExtra(u) {
  if (!u || !u.d) return;
  if (u.kind === 'poll') { pollUpdate(u.chat, u.d); return; }
  if (u.kind !== 'extra') return;
  const m = (S.msgs.get(u.chat) || []).find(x => x.id === u.d.id);
  if (!m) return;
  m.extra = u.d.extra;
  S.cache && S.cache.putMsgs && S.cache.putMsgs([m]);
  hooks.repaint && hooks.repaint(m);
}
S.net.addEventListener('update', e => applyExtra(e.detail.d));
S.net.addEventListener('diff', e => { for (const u of (e.detail.d && e.detail.d.updates) || []) applyExtra(u); });
S.net.addEventListener('scheduled', e => { const d = e.detail.d || {}; paintScheduled(d.chat, d.count); });
S.net.addEventListener('scheduled_failed', e => { const d = e.detail.d || {}; toast('یک پیام زمان‌بندی‌شده فرستاده نشد: ' + (d.text || '')); });

// ------------------------------------------------------------ the message bar
// Each chat builds its own bar; when a new one appears, it gets the send
// menu, the sticker button and its scheduled messages.
const SMILE = '<circle cx="12" cy="12" r="9"/><path d="M8.5 14.5s1.3 1.8 3.5 1.8 3.5-1.8 3.5-1.8M9 9.5h.01M15 9.5h.01"/>';
let queued = false;
function wireBar() {
  queued = false;
  const form = document.getElementById('m-compose'), chat = S.chats.get(S.active);
  if (!form || !chat || form.dataset.xbar) return;
  form.dataset.xbar = '1';
  wireSendMenu(form, chat);
  const ta = form.querySelector('textarea'), foot = form.closest('.m-foot');
  if (ta && foot) {
    const b = el('button', 'm-icon-btn x-smile'); b.type = 'button'; b.setAttribute('aria-label', 'برچسب و گیف'); b.title = 'برچسب و گیف';
    b.innerHTML = svg(SMILE);
    b.addEventListener('click', () => togglePanel(foot, chat));
    ta.after(b);
  }
  loadScheduled(chat);
}
const pane = document.getElementById('m-chat');
if (pane) new MutationObserver(() => { if (!queued) { queued = true; queueMicrotask(wireBar); } }).observe(pane, { childList: true, subtree: true });
