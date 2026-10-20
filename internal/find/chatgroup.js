// chatgroup.js: groups, supergroups and channels on the page (docs/chat/
// SPEC.md 6.2, 6.7): the side panel and its screens, the info panel and
// members, service lines, who wrote a message, and the bar that replaces the
// composer where one may not write. Making a chat, joining by link and a
// forum's topics are chatgroupmake.js; the admin screens (settings, links,
// requests, the log, statistics, rights) chatgroupadmin.js, loaded on first
// use. What anyone may do is decided by the server; the page only hides what
// would be refused.
import { reduced, SPRING } from '/chatcore.js';
import { $, el, fa, svg, timeFmt, dayFmt, dayKey, S, hooks, toast } from '/chatstate.js';
import { avatar } from '/chatpane.js';
import '/chatgroupmake.js';

const GROUPISH = { group: 1, supergroup: 1, channel: 1 };
export const isGroup = c => !!(c && GROUPISH[c.type]);
S.ginfo = new Map();   // chat -> {chat, me, may, link}
S.gmembers = new Map(); // chat -> members (first page)
S.names = new Map();   // username -> display name, as groups show them
S.topic = null;        // the open forum topic (null: the topic list)
const admin = () => import('/chatgroupadmin.js');

// api: a JSON call to the messenger's REST side; a refusal throws with the
// server's own Persian sentence.
export async function api(path, body) {
  const init = body === undefined ? { credentials: 'same-origin', cache: 'no-store' }
    : { method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) };
  let r;
  try { r = await fetch(path, init); } catch { throw { text: 'اتصال برقرار نیست؛ دوباره تلاش کنید.' }; }
  const j = await r.json().catch(() => ({}));
  if (!r.ok) throw { text: j.error || 'انجام نشد؛ دوباره تلاش کنید.', code: j.code, status: r.status };
  return j;
}
const fail = e => toast((e && e.text) || 'انجام نشد؛ دوباره تلاش کنید.');
export const kindName = t => ({ group: 'گروه', supergroup: 'ابرگروه', channel: 'کانال' })[t] || 'گفت‌وگو';
export const nameOf = u => u === S.me ? 'شما' : (S.names.get(u) || u);
export function hue(s) { let h = 0; for (const c of s) h = (h * 31 + c.codePointAt(0)) % 360; return h; }
export const joinURL = token => `${location.origin}/messages#join=${token}`;
export const ICON = {
  people: '<path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M22 21v-2a4 4 0 0 0-3-3.9M16 3.1a4 4 0 0 1 0 7.8"/>',
  channel: '<path d="M3 11l18-5v12L3 14v-3z"/><path d="M11.6 16.8a3 3 0 1 1-5.8-1.6"/>',
  link: '<path d="M10 13a5 5 0 0 0 7.5.5l3-3a5 5 0 0 0-7-7l-1.7 1.7"/><path d="M14 11a5 5 0 0 0-7.5-.5l-3 3a5 5 0 0 0 7 7l1.7-1.7"/>',
  eye: '<path d="M2 12s3.6-7 10-7 10 7 10 7-3.6 7-10 7S2 12 2 12z"/><circle cx="12" cy="12" r="3"/>',
  back: '<path d="M9 18l6-6-6-6"/>', x: '<path d="M18 6L6 18M6 6l12 12"/>', plus: '<path d="M12 5v14M5 12h14"/>',
  lock: '<rect x="4" y="11" width="16" height="10" rx="2"/><path d="M8 11V7a4 4 0 0 1 8 0v4"/>',
};

// ------------------------------------------------------------------ panel
//
// One panel at the side of the chat (the whole screen on a phone) holding a
// stack of screens: info, then a member, then their rights... Each screen
// slides in from the side; back slides it away (SPEC 8.1).
let panel = null;
export function openPanel() {
  if (panel) return panel;
  const back = el('div', 'g-back'), box = el('aside', 'g-panel');
  box.setAttribute('role', 'dialog'); box.setAttribute('aria-modal', 'true'); box.setAttribute('aria-label', 'اطلاعات گفت‌وگو');
  back.appendChild(box); document.body.appendChild(back);
  const prev = document.activeElement;
  const p = panel = { box, stack: [] };
  p.push = (title, body, opts = {}) => {
    const scr = el('section', 'g-screen'), head = el('div', 'g-head');
    const b = el('button', 'm-icon-btn'); b.type = 'button';
    const first = !p.stack.length;
    b.setAttribute('aria-label', first ? 'بستن' : 'بازگشت'); b.innerHTML = svg(first ? ICON.x : ICON.back);
    b.addEventListener('click', () => first ? p.close() : p.pop());
    const h = el('h2', 'g-title', title); h.dir = 'auto';
    head.append(b, h);
    if (opts.action) head.appendChild(opts.action);
    const scroll = el('div', 'g-body'); scroll.appendChild(body);
    scr.append(head, scroll);
    const under = p.stack[p.stack.length - 1];
    p.stack.push({ scr, refresh: opts.refresh });
    box.appendChild(scr);
    if (!reduced()) {
      scr.animate([{ transform: 'translateX(-100%)' }, { transform: 'none' }], { duration: first ? 280 : 260, easing: SPRING });
      if (under) under.scr.animate([{ transform: 'none', opacity: 1 }, { transform: 'translateX(25%)', opacity: 0 }], { duration: 240, easing: 'cubic-bezier(0.2,0,0,1)', fill: 'forwards' });
      if (first) back.animate([{ opacity: 0 }, { opacity: 1 }], { duration: 180, easing: 'linear' });
    } else if (under) under.scr.style.visibility = 'hidden';
    queueMicrotask(() => { const f = scr.querySelector('input, button:not(.m-icon-btn)') || b; f.focus(); });
    return scr;
  };
  p.pop = () => {
    const top = p.stack.pop(); if (!top) return;
    if (!p.stack.length) { p.stack.push(top); p.close(); return; }
    const under = p.stack[p.stack.length - 1];
    under.scr.getAnimations().forEach(a => a.cancel()); under.scr.style.visibility = '';
    if (under.refresh) under.refresh();
    if (reduced()) { top.scr.remove(); return; }
    under.scr.animate([{ transform: 'translateX(25%)', opacity: 0 }, { transform: 'none', opacity: 1 }], { duration: 240, easing: 'cubic-bezier(0.2,0,0,1)' });
    top.scr.animate([{ transform: 'none' }, { transform: 'translateX(-100%)' }], { duration: 220, easing: 'cubic-bezier(.4,0,1,1)' }).finished.finally(() => top.scr.remove());
  };
  p.close = () => {
    if (panel !== p) return; panel = null;
    if (prev && prev.focus && document.contains(prev)) prev.focus();
    if (reduced()) { back.remove(); return; }
    box.animate([{ transform: 'none' }, { transform: 'translateX(-100%)' }], { duration: 220, easing: 'cubic-bezier(.4,0,1,1)', fill: 'forwards' });
    back.animate([{ opacity: 1 }, { opacity: 0 }], { duration: 220, easing: 'linear', fill: 'forwards' }).finished.finally(() => back.remove());
  };
  p.top = () => p.stack[p.stack.length - 1];
  back.addEventListener('click', e => { if (e.target === back) p.close(); });
  return p;
}
addEventListener('keydown', e => {
  if (e.key !== 'Escape' || !panel || document.querySelector('.m-ov')) return;
  e.preventDefault(); e.stopPropagation(); panel.pop();
}, true);

// ask: a small question with optional fields; resolves with their values, or
// null. fields: [{id, label, type, value, options, ltr}].
export function ask({ title, text, fields = [], ok = 'تأیید', danger = false }) {
  return new Promise(resolve => {
    const back = el('div', 'm-ov m-modal-back'), card = el('form', 'm-modal');
    card.setAttribute('role', 'dialog'); card.setAttribute('aria-modal', 'true');
    const h = el('h2', 'm-modal-title', title); h.id = 'g-ask-head'; card.setAttribute('aria-labelledby', h.id);
    card.appendChild(h);
    if (text) { const p = el('p', 'm-modal-text', text); p.dir = 'auto'; card.appendChild(p); }
    const inputs = {};
    for (const f of fields) {
      const lab = el('label', 'g-field'); lab.appendChild(el('span', '', f.label));
      let i;
      if (f.type === 'select') { i = el('select', 'input'); for (const [v, t] of f.options) { const o = el('option', '', t); o.value = v; i.appendChild(o); } i.value = f.value ?? f.options[0][0]; }
      else if (f.type === 'textarea') { i = el('textarea', 'input'); i.rows = 3; i.value = f.value || ''; }
      else { i = el('input', 'input'); i.type = f.type || 'text'; i.value = f.value || ''; if (f.max) i.maxLength = f.max; }
      i.id = 'g-ask-' + f.id; i.dir = f.ltr ? 'ltr' : 'auto'; if (f.autocomplete) i.autocomplete = f.autocomplete;
      lab.appendChild(i); card.appendChild(lab); inputs[f.id] = i;
    }
    const row = el('div', 'm-modal-actions');
    const no = el('button', 'btn', 'انصراف'); no.type = 'button';
    const yes = el('button', 'btn ' + (danger ? 'm-danger' : 'btn-primary'), ok); yes.type = 'submit';
    row.append(no, yes); card.appendChild(row);
    back.appendChild(card); document.body.appendChild(back);
    const prev = document.activeElement;
    queueMicrotask(() => (card.querySelector('input, textarea, select') || yes).focus());
    if (!reduced()) card.animate([{ transform: 'scale(.9)', opacity: 0 }, { transform: 'none', opacity: 1 }], { duration: 140, easing: 'cubic-bezier(0.2,0,0,1)' });
    let done = false;
    const finish = v => {
      if (done) return; done = true; removeEventListener('keydown', esc, true);
      back.remove(); if (prev && prev.focus && document.contains(prev)) prev.focus(); resolve(v);
    };
    const esc = e => { if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); finish(null); } };
    addEventListener('keydown', esc, true);
    no.addEventListener('click', () => finish(null));
    back.addEventListener('click', e => { if (e.target === back) finish(null); });
    card.addEventListener('submit', e => { e.preventDefault(); finish(Object.fromEntries(Object.entries(inputs).map(([k, i]) => [k, i.value]))); });
  });
}

// row: one line of a panel list: an icon or avatar, words, and a click.
export function row(icon, label, run, opts = {}) {
  const b = el('button', 'g-row' + (opts.danger ? ' danger' : '')); b.type = 'button';
  if (icon instanceof Node) b.appendChild(icon);
  else { const i = el('span', 'g-ic'); i.innerHTML = svg(icon); b.appendChild(i); }
  const t = el('span', 'g-row-text'); const l = el('span', 'g-row-label', label); l.dir = 'auto'; t.appendChild(l);
  if (opts.sub) { const s = el('span', 'g-row-sub', opts.sub); s.dir = 'auto'; t.appendChild(s); }
  b.appendChild(t);
  if (opts.badge) b.appendChild(el('span', 'm-badge on', fa(opts.badge)));
  b.addEventListener('click', run);
  return b;
}

// ------------------------------------------------------------- the chat
export async function loadInfo(id) {
  try {
    const g = await api(`/api/chat/chats/${id}/info`);
    S.ginfo.set(id, g);
    const c = S.chats.get(id);
    if (c) { c.title = g.chat.title; c.forum = !!g.chat.forum; }
    if (S.active === id) { hooks.paintStatus(); paintGate(); const t = $('m-chat')?.querySelector('.m-title b'); if (t) t.textContent = (c && c.forum && S.topic != null && S.topicInfo ? S.topicInfo.title : '') || g.chat.title; }
    return g;
  } catch { return null; }
}
export async function loadMembers(id) {
  try {
    const j = await api(`/api/chat/chats/${id}/members`);
    S.gmembers.set(id, j.members || []);
    for (const m of j.members || []) if (m.photo) S.photos.add(m.id);
    for (const m of j.members || []) S.names.set(m.id, m.name || m.id);
    return j.members || [];
  } catch { return []; }
}
hooks.openGroup = async chat => {
  if (!isGroup(chat)) { S.topic = null; return; }
  if (!chat.forum) S.topic = null;
  const g = await loadInfo(chat.id);
  if (g && (chat.type !== 'channel' || g.may.ban || g.may.info)) loadMembers(chat.id);
};
hooks.members = chat => {
  if (!isGroup(chat)) return chat.type === 'private' && chat.peer ? [{ id: chat.peer.id, name: chat.peer.name || chat.title }] : [];
  return (S.gmembers.get(chat.id) || []).filter(m => m.id !== S.me && m.role !== 'banned').map(m => ({ id: m.id, name: m.name || m.id }));
};
hooks.groupStatus = chat => {
  if (!isGroup(chat)) return null;
  const t = S.typing.get(chat.id);
  if (t && t.until > Date.now()) return { typing: true, text: nameOf(t.user) + (t.action === 'typing' ? ' در حال نوشتن' : ' در حال ارسال') };
  if (chat.forum && S.topic != null) return { text: kindName(chat.type) };
  const g = S.ginfo.get(chat.id);
  return { text: g ? `${fa(g.chat.members)} ${chat.type === 'channel' ? 'مشترک' : 'عضو'}` : kindName(chat.type) };
};
hooks.rowKind = chat => {
  if (!isGroup(chat)) return null;
  const i = el('span', 'm-row-kind'); i.innerHTML = svg(chat.type === 'channel' ? ICON.channel : ICON.people);
  i.setAttribute('aria-label', kindName(chat.type)); return i;
};
hooks.lastPrefix = (chat, last) => {
  if (!isGroup(chat) || chat.type === 'channel' || last.kind === 'service' || last.from === S.me) return '';
  return (last.from_name || nameOf(last.from)) + ': ';
};

// The bar that takes the composer's place where one may not write.
export function paintGate() {
  const chat = S.chats.get(S.active), foot = $('m-foot'), form = $('m-compose'); if (!chat || !foot || !form) return;
  const g = S.ginfo.get(chat.id);
  let gate = $('m-gate'), why = '';
  if (chat.forum && S.topic == null) why = ' ';
  else if (isGroup(chat) && g) {
    const admin = g.me.role === 'owner' || g.me.role === 'admin';
    if (!g.may.send) why = chat.type === 'channel' ? 'فقط مدیران کانال پیام می‌فرستند.' : (g.me.until ? `تا ${dayFmt.format(new Date(g.me.until))} اجازهٔ فرستادن پیام ندارید.` : 'اجازهٔ فرستادن پیام در این گروه را ندارید.');
    else if (chat.forum && S.topicInfo && S.topicInfo.closed && !admin) why = 'این موضوع بسته است.';
    const ta = $('m-input');
    if (ta) ta.placeholder = g.chat.slow_mode && !admin ? `پیام (حالت آهسته: هر ${slowText(g.chat.slow_mode)} یک پیام)` : (chat.type === 'channel' ? 'پست' : 'پیام');
    const clip = form.querySelector('.m-attach'); if (clip) clip.hidden = !g.may.media;
  }
  if (!why) { if (gate) gate.remove(); if (!S.sel.size) form.hidden = false; return; }
  if (!gate) { gate = el('div', 'm-gate'); gate.id = 'm-gate'; gate.setAttribute('role', 'status'); foot.appendChild(gate); }
  gate.textContent = why.trim(); gate.hidden = !why.trim();
  form.hidden = true;
}
hooks.paintGate = paintGate;
export const slowText = s => s >= 3600 ? 'یک ساعت' : s >= 60 ? `${fa(s / 60)} دقیقه` : `${fa(s)} ثانیه`;

// ---------------------------------------------------- bubbles and service
const SV = { // [the third person, «شما»]
  create: (p, t) => [`${kindName(t)} «${p.title}» را ساخت`, `${kindName(t)} «${p.title}» را ساختید`],
  add: p => [`${names(p)} را افزود`, `${names(p)} را افزودید`],
  join: () => ['با پیوند دعوت پیوست', 'با پیوند دعوت پیوستید'],
  signup: () => ['عضو پیام‌رسان شد', 'عضو پیام‌رسان شدید'], // a new account, told to the invite's maker
  leave: () => ['بیرون رفت', 'بیرون رفتید'],
  remove: p => [`${names(p)} را بیرون کرد`, `${names(p)} را بیرون کردید`],
  owner: p => [`مالکیت را به ${names(p)} سپرد`, `مالکیت را به ${names(p)} سپردید`],
  title: p => [`نام را به «${p.title}» تغییر داد`, `نام را به «${p.title}» تغییر دادید`],
  topic: p => [`موضوع «${p.title}» را ساخت`, `موضوع «${p.title}» را ساختید`],
};
const names = p => (p.names || p.users || []).map((n, i) => (p.users && p.users[i] === S.me) ? 'شما' : n).join('، ');
export function serviceText(m, chat) {
  const p = (typeof m.service === 'string' ? JSON.parse(m.service || '{}') : m.service) || {};
  const f = SV[p.action]; if (!f) return '';
  const mine = m.from === S.me;
  // A sign-up line carries the name the account was made with: in a private
  // chat there is no sender name and no member list to look the person up in.
  const who = m.from_name || (p.action === 'signup' && p.names && p.names[0]) || nameOf(m.from);
  return (mine ? 'شما ' : who + ' ') + f(p, chat && chat.type)[mine ? 1 : 0];
}
hooks.serviceText = serviceText;
hooks.serviceLine = (m, chat) => {
  const d = el('div', 'm-service'); d.dataset.key = m.cid || String(m.pts); if (m.pts) d.dataset.pts = String(m.pts);
  const s = el('span', '', serviceText(m, chat)); s.dir = 'auto'; d.appendChild(s);
  return d;
};

// groupMark adds a group bubble's sender: the name on the first of a run, the
// picture on the last. Channels show views and, when signed, the author.
hooks.groupMark = (b, m, chat, first, last) => {
  if (chat.type === 'channel') {
    const meta = b.querySelector('.m-meta'); if (!meta) return;
    const g = S.ginfo.get(chat.id);
    if (m.views) { const v = el('span', 'm-views'); v.innerHTML = svg(ICON.eye); v.appendChild(document.createTextNode(fa(m.views))); v.setAttribute('aria-label', `${fa(m.views)} بازدید`); meta.prepend(v); }
    if (g && g.chat.settings && g.chat.settings.signatures && m.from_name) { const s = el('span', 'm-sign', m.from_name); s.dir = 'auto'; meta.prepend(s); }
    return;
  }
  if (m.from === S.me) return;
  b.classList.add('g-them');
  const who = m.from_name || nameOf(m.from);
  if (first) {
    const n = el('button', 'm-from', who); n.type = 'button'; n.dir = 'auto'; n.dataset.user = m.from;
    n.style.color = `hsl(${hue(m.from)} 70% 72%)`;
    const after = b.querySelector('.m-check'); after ? after.after(n) : b.prepend(n);
  }
  if (last) {
    const av = avatar({ title: who, peer: { id: m.from } }, 32); av.classList.add('m-gav'); av.dataset.user = m.from;
    av.dir = 'auto'; // its letter must not decide the bubble's direction (dir=auto skips it then)
    av.setAttribute('role', 'button'); av.setAttribute('aria-label', who); av.tabIndex = -1;
    b.classList.add('g-last'); b.appendChild(av);
  }
};
addEventListener('click', e => {
  const t = e.target.closest && e.target.closest('.m-from, .m-gav'); if (!t || !t.dataset.user || t.dataset.user === S.me) return;
  e.stopPropagation(); hooks.start(t.dataset.user);
}, true);

// The message menu in groups: who read it; pin and delete only where allowed.
hooks.menuItems = (m, chat, items) => {
  if (!isGroup(chat)) return;
  const g = S.ginfo.get(chat.id); if (!g) return;
  const drop = label => { const i = items.findIndex(x => x.label === label); if (i >= 0) items.splice(i, 1); };
  if (!g.may.pin) { drop('سنجاق کردن'); drop('برداشتن سنجاق'); }
  if (m.from !== S.me && !g.may.delete) drop('حذف');
  if (m.from === S.me && m.id && chat.type !== 'channel' && g.chat.members <= 100) {
    items.splice(1, 0, { icon: ICON.eye, label: 'خوانده‌شده توسط', run: () => readers(m, chat) });
  }
};
async function readers(m, chat) {
  let j; try { j = await api(`/api/chat/chats/${chat.id}/messages/${m.id}/readers`); } catch (e) { fail(e); return; }
  const list = el('div', 'g-list');
  if (j.hidden) list.appendChild(el('p', 'g-note', 'فهرست خوانندگان فقط تا یک هفته و در گروه‌های تا ۱۰۰ عضو نگه داشته می‌شود.'));
  else if (!(j.readers || []).length) list.appendChild(el('p', 'g-note', 'هنوز کسی نخوانده است.'));
  for (const r of j.readers || []) list.appendChild(row(avatar({ title: r.name, peer: { id: r.id } }, 36), r.name, () => hooks.start(r.id)));
  openPanel().push('خوانده‌شده توسط', list);
}

// ------------------------------------------------------------ info panel
hooks.openInfo = chat => { if (isGroup(chat)) showInfo(chat); };
export async function showInfo(chat) {
  const g = await loadInfo(chat.id); if (!g) { toast('اطلاعات این گفت‌وگو بارگذاری نشد.'); return; }
  const body = el('div', 'g-info');
  const p = openPanel();
  const paint = async () => {
    const g = S.ginfo.get(chat.id) || (await loadInfo(chat.id)); if (!g) return;
    const isAdmin = g.me.role === 'owner' || g.me.role === 'admin';
    const top = el('div', 'g-top');
    const name = el('h3', 'g-name', g.chat.title); name.dir = 'auto';
    const sub = el('div', 'g-sub', `${kindName(g.chat.type)} · ${fa(g.chat.members)} ${g.chat.type === 'channel' ? 'مشترک' : 'عضو'}`);
    top.append(avatar(chat, 84), name, sub);
    if (g.chat.about) { const a = el('p', 'g-about', g.chat.about); a.dir = 'auto'; top.appendChild(a); }
    const acts = el('div', 'g-list');
    if (g.link) acts.appendChild(row(ICON.link, joinURL(g.link), () => copyLink(g.link), { sub: 'پیوند دعوت؛ برای رونوشت بزنید' }));
    if (g.may.info) acts.appendChild(row('<path d="M12 20h9"/><path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4Z"/>', 'ویرایش نام و توضیح', () => admin().then(a => a.editInfo(chat, g, paint))));
    if (g.may.invite) acts.appendChild(row(ICON.plus, chat.type === 'channel' ? 'افزودن مشترک' : 'افزودن عضو', () => hooks.pickPeople({ title: 'افزودن عضو', ok: 'افزودن' }).then(users => users && users.length && admin().then(a => a.addMembers(chat, users, paint)))));
    if (isAdmin) {
      const adm = el('div', 'g-list g-admin');
      if (g.may.info || g.may.ban) adm.appendChild(row('<circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z"/>', 'تنظیمات و دسترسی‌ها', () => admin().then(a => a.openSettings(chat, g, paint))));
      if (g.may.invite) {
        adm.appendChild(row(ICON.link, 'پیوندهای دعوت', () => admin().then(a => a.openInvites(chat, g))));
        adm.appendChild(row('<path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M19 8v6M22 11h-6"/>', 'درخواست‌های عضویت', () => admin().then(a => a.openRequests(chat, g, paint))));
      }
      adm.appendChild(row('<path d="M12 8v4l3 3"/><circle cx="12" cy="12" r="9"/>', 'رویدادهای ۴۸ ساعت اخیر', () => admin().then(a => a.openLog(chat, g))));
      adm.appendChild(row('<path d="M4 20V10M10 20V4M16 20v-7M22 20H2"/>', 'آمار', () => admin().then(a => a.openStats(chat, g))));
      acts.appendChild(adm);
    }
    const kids = [top, acts];
    if (chat.type !== 'channel' || isAdmin) kids.push(await membersBlock(chat, g, paint));
    const out = el('div', 'g-list g-danger');
    out.appendChild(row('<path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4M16 17l5-5-5-5M21 12H9"/>', chat.type === 'channel' ? 'ترک کانال' : 'ترک گروه', () => leave(chat, g), { danger: true }));
    if (g.me.role === 'owner') out.appendChild(row('<path d="M3 6h18"/><path d="M8 6V4h8v2"/><path d="M6 6l1 14h10l1-14"/>', `حذف ${kindName(g.chat.type)} برای همه`, () => admin().then(a => a.removeChat(chat, g)), { danger: true }));
    kids.push(out);
    body.replaceChildren(...kids);
  };
  await paint();
  if (!p.stack.length) p.push(kindName(chat.type), body, { refresh: paint });
}
async function copyLink(token) {
  try { await navigator.clipboard.writeText(joinURL(token)); toast('پیوند دعوت رونوشت شد.'); } catch { toast('رونوشت انجام نشد.'); }
}

const ROLE = { owner: 'مالک', admin: 'مدیر', restricted: 'محدود', banned: 'کنار گذاشته' };
async function membersBlock(chat, g, refresh) {
  const box = el('div', 'g-members');
  const head = el('div', 'g-sec', `${chat.type === 'channel' ? 'مشترکان' : 'اعضا'} (${fa(g.chat.members)})`);
  const q = el('input', 'input'); q.type = 'search'; q.placeholder = 'جستجوی عضو'; q.setAttribute('aria-label', 'جستجوی عضو'); q.id = 'g-mq';
  const list = el('div', 'g-list');
  const paint = members => list.replaceChildren(...members.map(m => row(avatar({ title: m.name, peer: { id: m.id } }, 40), m.name || m.id,
    () => memberScreen(chat, g, m, refresh), { sub: m.title || ROLE[m.role] || '' })));
  paint(await loadMembers(chat.id));
  let t; q.addEventListener('input', () => { clearTimeout(t); t = setTimeout(async () => {
    try { const j = await api(`/api/chat/chats/${chat.id}/members?q=${encodeURIComponent(q.value.trim())}`); paint(j.members || []); } catch { }
  }, 200); });
  box.append(head, q, list);
  return box;
}

function memberScreen(chat, g, m, refresh) {
  const body = el('div', 'g-info');
  const top = el('div', 'g-top'); const n = el('h3', 'g-name', m.name || m.id); n.dir = 'auto';
  top.append(avatar({ title: m.name, peer: { id: m.id } }, 72), n, el('div', 'g-sub', m.title || ROLE[m.role] || 'عضو'));
  const acts = el('div', 'g-list');
  const done = () => { panel && panel.pop(); refresh && refresh(); };
  if (m.id !== S.me) acts.appendChild(row('<path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/>', 'پیام خصوصی', () => { panel && panel.close(); hooks.start(m.id); }));
  if (m.id !== S.me && m.role !== 'owner') {
    if (g.may.admins && m.role !== 'banned') acts.appendChild(row('<path d="M12 2l3 7h7l-5.5 4.5 2 7.5L12 17l-6.5 4 2-7.5L2 9h7z"/>', m.role === 'admin' ? 'اختیارات مدیر' : 'مدیر کردن', () => admin().then(a => a.openRights(chat, g, m, done))));
    if (g.may.admins && m.role === 'admin') acts.appendChild(row(ICON.x, 'برکناری از مدیریت', () => admin().then(a => a.change(chat, m, { action: 'demote' }, done))));
    if (g.may.ban && m.role !== 'admin' && m.role !== 'banned' && chat.type !== 'channel') acts.appendChild(row(ICON.lock, m.role === 'restricted' ? 'تغییر محدودیت' : 'محدود کردن', () => admin().then(a => a.openRestrict(chat, g, m, done))));
    if (g.may.ban && m.role !== 'banned') {
      acts.appendChild(row('<path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4M16 17l5-5-5-5M21 12H9"/>', 'بیرون کردن', () => admin().then(a => a.kick(chat, m, done)), { danger: true }));
      acts.appendChild(row('<circle cx="12" cy="12" r="9"/><path d="M5.6 5.6l12.8 12.8"/>', 'کنار گذاشتن', () => admin().then(a => a.ban(chat, m, done)), { danger: true }));
    }
    if (g.may.ban && m.role === 'banned') acts.appendChild(row(ICON.plus, 'برداشتن کنارگذاری', () => admin().then(a => a.change(chat, m, { action: 'unban' }, done))));
    if (g.me.role === 'owner' && m.role !== 'banned') acts.appendChild(row('<path d="M15 3h6v6M10 14L21 3M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"/>', 'سپردن مالکیت به این همکار', () => admin().then(a => a.transfer(chat, m, done)), { danger: true }));
  }
  body.append(top, acts);
  openPanel().push(m.name || m.id, body);
}

async function leave(chat, g) {
  if (g.me.role === 'owner' && g.chat.members > 1) { toast('پیش از ترک، مالکیت را از فهرست اعضا به همکار دیگری بسپارید.'); return; }
  const ok = await ask({ title: chat.type === 'channel' ? 'ترک کانال' : 'ترک گروه', text: `«${g.chat.title}» را ترک می‌کنید؟`, ok: 'ترک', danger: true });
  if (!ok) return;
  try { await api(`/api/chat/chats/${chat.id}/leave`, {}); } catch (e) { fail(e); return; }
  panel && panel.close(); forget(chat.id);
}
// forget takes a chat off this device (left, removed or deleted).
export function forget(id) {
  S.chats.delete(id); S.msgs.delete(id); S.ginfo.delete(id); S.gmembers.delete(id);
  S.cache.del('chats', id);
  if (S.active === id) { S.active = 0; document.body.classList.remove('open'); if (history.state?.chat) history.replaceState(null, '', location.pathname); }
  hooks.renderList();
}

// ------------------------------------------------------------- live events
S.net.addEventListener('chat', e => {
  const d = (e.detail && e.detail.d) || {};
  if (d.removed) { if (S.active === d.chat) toast('دیگر عضو این گفت‌وگو نیستید.'); forget(d.chat); return; }
  hooks.refreshChats();
});
S.net.addEventListener('member', e => {
  const d = (e.detail && e.detail.d) || {};
  if (!S.ginfo.has(d.chat)) return;
  loadInfo(d.chat).then(() => { const top = panel && panel.top(); if (top && top.refresh) top.refresh(); });
});
S.net.addEventListener('info', e => { // a group's title or settings changed
  const id = e.detail?.d?.chat; if (!S.chats.has(id)) return;
  hooks.refreshChats(); if (S.ginfo.has(id)) loadInfo(id);
});
S.net.addEventListener('join_request', e => {
  const c = S.chats.get(e.detail?.d?.chat); if (c) toast(`درخواست عضویت تازه در «${c.title}»`);
});
