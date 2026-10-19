// chatnotify.js: notifications on the messenger page (docs/chat/SPEC.md 6.10).
// The settings (the bell beside the list's search): this device's push, and
// one's switches for private chats, groups, channels, calls, the preview,
// in-app sound, vibration and the badge. While the page is open it alerts
// in-app from the socket's own updates (a sound, a vibration), keeps the app
// icon's number, tells the server which chat is on screen (no push for it),
// and closes a chat's notification once it is read. A chat's own sound is in
// its row menu. The push itself is DocFind's (push.js, sw.js + chatsw.js).
import { $, el, svg, S, hooks, toast } from '/chatstate.js';
import { api, openPanel } from '/chatgroup.js';

const BELL = '<path d="M18 8a6 6 0 0 0-12 0c0 7-3 9-3 9h18s-3-2-3-9"/><path d="M13.7 21a2 2 0 0 1-3.4 0"/>';
S.notify = { private: true, groups: true, channels: true, calls: true, preview: true, sound: true, vibrate: true, badge: true };
const SWITCHES = [
  ['private', 'گفت‌وگوهای خصوصی'], ['groups', 'گروه‌ها'], ['channels', 'کانال‌ها'], ['calls', 'تماس‌ها'],
  ['preview', 'نمایش متن پیام در اعلان', 'خاموش: فقط «پیام تازه» و نام گفت‌وگو'],
  ['sound', 'صدا در برنامه'], ['vibrate', 'لرزش در برنامه'], ['badge', 'شمار پیام‌ها روی آیکون برنامه'],
];
const SOUNDS = [['', 'پیش‌فرض'], ['note', 'نت'], ['chime', 'زنگوله'], ['pop', 'تق'], ['none', 'بی‌صدا']];
const GROUPISH = { group: 1, supergroup: 1, channel: 1 };

async function load() {
  try { Object.assign(S.notify, (await api('/api/chat/notify-settings')).settings); } catch { }
  badge();
}
S.net.addEventListener('notify_settings', e => { Object.assign(S.notify, e.detail.d); badge(); });

// ------------------------------------------------------------ sound
// Made with Web Audio: no sound files to ship or fetch. A browser starts
// audio only after the person has touched the page, so the context waits.
let ac = null;
addEventListener('pointerdown', () => { try { ac = ac || new AudioContext(); } catch { } }, { once: true, capture: true });
function play(kind) {
  if (!ac || kind === 'none') return;
  const notes = { note: [[880, 0], [660, .12]], chime: [[1046, 0], [1318, .1], [1568, .2]], pop: [[520, 0]] }[kind || 'note'] || [[880, 0], [660, .12]];
  const t0 = ac.currentTime + .01;
  for (const [f, at] of notes) {
    const o = ac.createOscillator(), g = ac.createGain();
    o.type = kind === 'pop' ? 'triangle' : 'sine'; o.frequency.value = f;
    g.gain.setValueAtTime(0, t0 + at); g.gain.linearRampToValueAtTime(.18, t0 + at + .015); g.gain.exponentialRampToValueAtTime(.001, t0 + at + .25);
    o.connect(g).connect(ac.destination); o.start(t0 + at); o.stop(t0 + at + .3);
  }
}

// ------------------------------------------------------------ in-app alerts
const isMuted = c => !!(c.muted_until && new Date(c.muted_until) > new Date());
function namesMe(m) {
  if (m.reply && m.reply.from === S.me) return true;
  let ents = []; try { ents = Array.isArray(m.entities) ? m.entities : JSON.parse(m.entities || '[]'); } catch { }
  return ents.some(e => e.user === S.me);
}
S.net.addEventListener('update', e => {
  const u = e.detail.d; if (!u || u.kind !== 'message' || !u.d) return;
  const m = u.d, chat = S.chats.get(u.chat);
  if (!chat || m.from === S.me || m.kind === 'service' || chat.type === 'saved') return;
  if (chat.id === S.active && !document.hidden) return; // it is on screen
  const kindOn = chat.type === 'channel' ? S.notify.channels : GROUPISH[chat.type] ? S.notify.groups : S.notify.private;
  const named = namesMe(m);
  if (!kindOn || (isMuted(chat) && !named) || (chat.mentions_only && !named)) return;
  if (S.notify.sound && !m.silent && !document.hidden) play(chat.sound || 'note');
  // docfind's Android app, in the background: the phone's own notification,
  // straight from this socket (no push service; works with only the
  // domestic network).
  const app = window.DocfindAndroid;
  if (document.hidden && app && typeof app.notify === 'function' && (chat.type !== 'channel' || S.notify.channels)) {
    const who = m.from_name || (chat.peer && chat.peer.name) || '';
    const words = S.notify.preview ? (m.text || ({ photo: 'عکس', video: 'ویدیو', round: 'پیام ویدیویی', voice: 'پیام صوتی', file: 'فایل' })[m.kind] || 'پیام تازه') : 'پیام تازه';
    try { app.notify(chat.title || who, GROUPISH[chat.type] && who ? who + ': ' + words : words, 'chat-' + chat.id, '/messages#c' + chat.id, false); } catch { }
  }
  if (S.notify.vibrate && !m.silent && navigator.vibrate && matchMedia('(pointer: coarse)').matches) navigator.vibrate([25, 40, 25]);
});

// ------------------------------------------------------------ the badge
function badge() {
  if (!navigator.setAppBadge) return;
  if (!S.notify.badge) { navigator.clearAppBadge().catch(() => { }); return; }
  let n = 0;
  for (const c of S.chats.values()) if (!isMuted(c) && !c.archived) n += c.unread || 0;
  (n ? navigator.setAppBadge(n) : navigator.clearAppBadge()).catch(() => { });
}
const listWas = hooks.listRendered;
hooks.listRendered = () => { if (listWas) listWas(); badge(); };

// ------------------------------------------------------------ what is on screen
// The server sends no push to a device for the chat it shows (SPEC 6.10:
// never notify the device the person is reading on).
let shown = -1;
function focus() {
  const chat = !document.hidden && document.body.classList.contains('open') || matchMedia('(min-width: 901px)').matches ? (S.active || 0) : 0;
  const now = document.hidden ? 0 : chat;
  if (now === shown) return;
  shown = now; S.net.send('focus', { chat: now });
}
document.addEventListener('visibilitychange', focus);
addEventListener('popstate', () => setTimeout(focus, 0));
S.net.addEventListener('state', e => { if (e.detail === 'online') { shown = -1; focus(); } });
// Reading a chat closes its notification on this device (the others hear
// of the read through the socket and do the same).
function closeFor(chatID) {
  try { window.DocfindAndroid && window.DocfindAndroid.unnotify && window.DocfindAndroid.unnotify('chat-' + chatID); } catch { }
  if (!navigator.serviceWorker) return;
  navigator.serviceWorker.getRegistration().then(r => r && r.active && r.active.postMessage({ type: 'chat-read', chat: chatID })).catch(() => { });
}
const send = S.net.send.bind(S.net);
S.net.send = (t, d, id) => { if (t === 'read' && d && d.chat) closeFor(d.chat); return send(t, d, id); };
S.net.addEventListener('update', e => { const u = e.detail.d; if (u && u.kind === 'read' && u.d && u.d.user === S.me) closeFor(u.chat); });

// ------------------------------------------------------------ a chat's sound
const rowMenuWas = hooks.rowMenuItems; // others add to the same menu (secret chats)
hooks.rowMenuItems = (chat, items, x, y) => {
  if (rowMenuWas) rowMenuWas(chat, items, x, y);
  if (chat.type === 'saved') return;
  items.splice(3, 0, { icon: BELL, label: 'صدای اعلان…', run: () => setTimeout(() => soundMenu(chat, x, y), 180) });
};
function soundMenu(chat, x, y) {
  import('/chatmenu.js').then(({ openMenu }) => openMenu({ x, y, items: SOUNDS.map(([v, label]) => ({
    icon: BELL, label: ((chat.sound || '') === v ? '✓ ' : '') + label,
    run: async () => {
      try { await api(`/api/chat/chats/${chat.id}/prefs`, { sound: v }); chat.sound = v; play(v || 'note'); }
      catch (e) { toast(e.text || 'انجام نشد.'); }
    },
  })) }));
}

// ------------------------------------------------------------ the settings
async function settings() {
  const body = el('div', 'g-form');
  const P = window.DocfindPush;
  const device = el('div', 'g-block');
  device.appendChild(el('div', 'g-sec', 'این دستگاه'));
  const status = el('p', 'g-note', '…'); device.appendChild(status);
  const paintDevice = async () => {
    device.querySelectorAll('.btn').forEach(b => b.remove());
    if (!P || !P.supported()) { status.textContent = 'این مرورگر اعلان پیام را پشتیبانی نمی‌کند.'; return; }
    if (P.needsInstall && P.needsInstall()) { status.textContent = 'در آیفون، اعلان فقط وقتی کار می‌کند که برنامه با «افزودن به صفحهٔ اصلی» نصب شده باشد (iOS 16.4 به بعد).'; return; }
    let on = false; try { on = await P.active(); } catch { }
    status.textContent = on ? 'اعلان‌ها روی این دستگاه روشن است.' : 'اعلان‌ها روی این دستگاه خاموش است.';
    const b = el('button', 'btn ' + (on ? '' : 'btn-primary'), on ? 'فرستادن اعلان آزمایشی' : 'روشن کردن اعلان روی این دستگاه'); b.type = 'button';
    b.addEventListener('click', async () => {
      b.disabled = true;
      try { on ? await P.test() : await P.enable(); toast(on ? 'اعلان آزمایشی فرستاده شد.' : 'اعلان‌ها روشن شد.'); }
      catch (e) { toast((e && e.message) || 'انجام نشد؛ اجازهٔ اعلان را در مرورگر بررسی کنید.'); }
      b.disabled = false; paintDevice();
    });
    device.appendChild(b);
  };
  const rows = el('div', 'g-block');
  rows.appendChild(el('div', 'g-sec', 'اعلان پیام‌ها'));
  for (const [k, label, sub] of SWITCHES) {
    const l = el('label', 'g-switch'), i = el('input'); i.type = 'checkbox'; i.id = 'm-n-' + k; i.checked = S.notify[k] !== false; i.setAttribute('role', 'switch');
    const t = el('span', 'g-row-text'); t.appendChild(el('span', 'g-row-label', label)); if (sub) t.appendChild(el('span', 'g-row-sub', sub));
    i.addEventListener('change', async () => {
      const next = { ...S.notify, [k]: i.checked };
      try { Object.assign(S.notify, (await api('/api/chat/notify-settings', next)).settings); badge(); }
      catch (e) { i.checked = !i.checked; toast(e.text || 'ذخیره نشد.'); }
    });
    l.append(t, i); rows.appendChild(l);
  }
  body.append(device, rows, el('p', 'g-note', 'بی‌صدا کردن یک گفت‌وگو، «فقط با نام‌بردن از من» و صدای هر گفت‌وگو در منوی همان گفت‌وگو در فهرست است (کلیک راست یا نگه‌داشتن). ساعت‌های آرام و خاموش کردن همهٔ اعلان‌های پیام‌رسان در تنظیمات اعلان docfind است.'));
  openPanel().push('اعلان‌ها', body);
  paintDevice();
}

queueMicrotask(() => {
  const nb = $('m-new-btn');
  if (nb) {
    const b = el('button', 'm-icon-btn'); b.type = 'button'; b.id = 'm-notify-btn'; b.setAttribute('aria-label', 'تنظیمات اعلان'); b.title = 'تنظیمات اعلان';
    b.innerHTML = svg(BELL); b.addEventListener('click', settings);
    nb.before(b);
  }
  document.addEventListener('chat-open', () => queueMicrotask(focus));
  load(); focus();
});
