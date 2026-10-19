// chatgroupadmin.js: what the owner and admins of a group or channel do
// (docs/chat/SPEC.md 6.7): the chat's settings and members' permissions,
// invite links, join requests, the 48-hour log, statistics, an admin's rights,
// restricting, removing and banning, handing the chat over, deleting it, and
// a forum's topics. Loaded on first use by chatgroup.js. The server decides
// every one of these again; a refusal comes back as its own sentence.
import { reduced } from '/chatcore.js';
import { el, fa, timeFmt, dayFmt, S, toast } from '/chatstate.js';
import { avatar } from '/chatpane.js';
import { api, openPanel, ask, row, kindName, nameOf, joinURL, loadInfo, forget, ICON } from '/chatgroup.js';

const fail = e => toast((e && e.text) || 'انجام نشد؛ دوباره تلاش کنید.');
// call: GET without a body, POST with one.
const call = (chat, path, body) => api(`/api/chat/chats/${chat.id}${path}`, body);

// A switch line: a checkbox drawn as a switch, with its words.
function toggle(id, label, on, sub) {
  const l = el('label', 'g-switch');
  const i = el('input'); i.type = 'checkbox'; i.id = id; i.checked = !!on; i.setAttribute('role', 'switch');
  const t = el('span', 'g-row-text'); t.appendChild(el('span', 'g-row-label', label));
  if (sub) t.appendChild(el('span', 'g-row-sub', sub));
  l.append(t, i);
  return l;
}
const section = (title, ...kids) => { const s = el('div', 'g-block'); s.appendChild(el('div', 'g-sec', title)); s.append(...kids); return s; };
function select(id, options, value) {
  const s = el('select', 'input'); s.id = id;
  for (const [v, t] of options) { const o = el('option', '', t); o.value = String(v); s.appendChild(o); }
  s.value = String(value); return s;
}
const field = (label, input) => { const l = el('label', 'g-field'); l.append(el('span', '', label), input); return l; };

// ------------------------------------------------------------- the chat
export async function editInfo(chat, g, refresh) {
  const v = await ask({ title: 'ویرایش ' + kindName(g.chat.type), ok: 'ذخیره', fields: [
    { id: 'title', label: 'نام', value: g.chat.title, max: 128 },
    { id: 'about', label: 'توضیح', type: 'textarea', value: g.chat.about || '' }] });
  if (!v) return;
  try { await call(chat, '/info', { title: v.title, about: v.about }); toast('ذخیره شد.'); await loadInfo(chat.id); refresh && refresh(); } catch (e) { fail(e); }
}

export async function addMembers(chat, users, refresh) {
  try {
    const j = await call(chat, '/members', { users });
    const n = (j.added || []).length, r = (j.refused || []).length;
    toast(n ? `${fa(n)} همکار افزوده شد.` + (r ? ` ${fa(r)} نفر اجازه نمی‌دهند؛ پیوند دعوت را برایشان بفرستید.` : '') : (r ? 'تنظیمات حریم خصوصی این همکاران اجازه نمی‌دهد؛ پیوند دعوت را برایشان بفرستید.' : 'همه از پیش عضو بودند.'));
    await loadInfo(chat.id); refresh && refresh();
  } catch (e) { fail(e); }
}

const PERMS = [
  ['send_messages', 'فرستادن پیام'], ['send_media', 'فرستادن عکس، فیلم و فایل'], ['send_polls', 'نظرسنجی'],
  ['add_previews', 'پیش‌نمایش پیوند'], ['invite_users', 'دعوت همکاران'], ['pin_messages', 'سنجاق کردن پیام'],
  ['change_info', 'تغییر نام و توضیح'], ['manage_topics', 'ساختن موضوع'],
];
const SLOW = [[0, 'خاموش'], [10, '۱۰ ثانیه'], [30, '۳۰ ثانیه'], [60, '۱ دقیقه'], [300, '۵ دقیقه'], [900, '۱۵ دقیقه'], [3600, '۱ ساعت']];
const SPAM = [[0, 'خاموش'], [1, '۱ ساعت'], [6, '۶ ساعت'], [24, '۱ روز'], [72, '۳ روز'], [168, '۱ هفته']];
const defaultPerms = { send_messages: true, send_media: true, send_polls: true, add_previews: true, invite_users: true };

export function openSettings(chat, g, refresh) {
  const set = g.chat.settings || {}, type = g.chat.type;
  const form = el('form', 'g-form');
  const blocks = [];
  let perms = null;
  if (type !== 'channel' && g.may.ban) {
    const cur = set.perms || defaultPerms;
    perms = PERMS.filter(([k]) => k !== 'manage_topics' || g.chat.forum).map(([k, label]) => toggle('g-p-' + k, label, cur[k]));
    blocks.push(section('اعضا می‌توانند', ...perms));
    blocks.push(section('حالت آهسته', field('هر عضو هر چند وقت یک پیام بفرستد', select('g-slow', SLOW, g.chat.slow_mode || 0))));
    blocks.push(section('پیوند از اعضای تازه', field('اعضای تازه تا این مدت پیوند نفرستند', select('g-spam', SPAM, set.antispam_hours || 0))));
  }
  if (g.may.info) {
    const mode = set.no_reactions ? 'none' : (set.reactions && set.reactions.length ? 'some' : 'all');
    const pick = el('div', 'g-emoji'); pick.hidden = mode !== 'some';
    for (const e of S.reactions) {
      const b = el('button', 'g-emo' + (!set.reactions || !set.reactions.length || set.reactions.includes(e) ? ' on' : ''), e); b.type = 'button';
      b.setAttribute('aria-pressed', String(b.classList.contains('on')));
      b.addEventListener('click', () => { b.classList.toggle('on'); b.setAttribute('aria-pressed', String(b.classList.contains('on'))); });
      pick.appendChild(b);
    }
    const rs = select('g-react', [['all', 'همهٔ واکنش‌ها'], ['some', 'فقط این‌ها'], ['none', 'بدون واکنش']], mode);
    rs.addEventListener('change', () => { pick.hidden = rs.value !== 'some'; });
    blocks.push(section('واکنش‌ها', field('واکنش به پیام‌ها', rs), pick));
    const more = [];
    if (type !== 'channel') more.push(toggle('g-forum', 'موضوع‌ها', g.chat.forum, 'گفت‌وگو در موضوع‌های جدا؛ گروه به ابرگروه تبدیل می‌شود.'));
    if (type === 'supergroup') more.push(toggle('g-hidden', 'تاریخچه برای اعضای تازه پنهان بماند', set.hidden_history, 'اعضای تازه پیام‌ها را از زمان پیوستن می‌بینند.'));
    if (type === 'channel') more.push(toggle('g-sign', 'امضای نویسنده زیر پست‌ها', set.signatures));
    if (more.length) blocks.push(section('گفت‌وگو', ...more));
  }
  const go = el('button', 'btn btn-primary g-go', 'ذخیره'); go.type = 'submit';
  form.append(...blocks, go);
  form.addEventListener('submit', async e => {
    e.preventDefault();
    const req = {};
    const $f = id => form.querySelector('#' + id);
    if (perms) {
      req.perms = Object.fromEntries(PERMS.map(([k]) => [k, $f('g-p-' + k) ? $f('g-p-' + k).checked : !!(set.perms || defaultPerms)[k]]));
      req.slow_mode = Number($f('g-slow').value); req.antispam_hours = Number($f('g-spam').value);
    }
    if (g.may.info) {
      const m = $f('g-react').value;
      req.no_reactions = m === 'none';
      req.reactions = m === 'some' ? [...form.querySelectorAll('.g-emo.on')].map(b => b.textContent) : [];
      if ($f('g-forum') && $f('g-forum').checked !== !!g.chat.forum) req.forum = $f('g-forum').checked;
      if ($f('g-hidden')) req.hidden_history = $f('g-hidden').checked;
      if ($f('g-sign')) req.signatures = $f('g-sign').checked;
    }
    go.disabled = true;
    try {
      await call(chat, '/info', req); toast('ذخیره شد.');
      const c = S.chats.get(chat.id); if (c && 'forum' in req) { c.forum = req.forum; S.topic = null; }
      await loadInfo(chat.id); openPanel().pop(); refresh && refresh();
    } catch (err) { fail(err); go.disabled = false; }
  });
  openPanel().push('تنظیمات و دسترسی‌ها', form);
}

// ---------------------------------------------------------- invite links
const EXPIRES = [['', 'بدون انقضا'], ['1', '۱ ساعت'], ['24', '۱ روز'], ['168', '۷ روز'], ['720', '۳۰ روز']];
export async function openInvites(chat, g) {
  const body = el('div', 'g-info');
  const paint = async () => {
    let j; try { j = await call(chat, '/invites'); } catch (e) { body.replaceChildren(el('p', 'g-note', e.text)); return; }
    const list = el('div', 'g-list');
    for (const inv of j.invites || []) {
      const bits = [inv.primary ? 'پیوند اصلی' : (inv.name || 'پیوند'), `${fa(inv.used)}${inv.limit ? ' از ' + fa(inv.limit) : ''} نفر`];
      if (inv.approval) bits.push('با تأیید مدیر');
      if (inv.expires) bits.push((new Date(inv.expires) < new Date() ? 'منقضی ' : 'تا ') + dayFmt.format(new Date(inv.expires)));
      if (inv.revoked) bits.push('باطل‌شده');
      const r = row(ICON.link, bits.join(' · '), () => linkScreen(chat, inv, paint), { sub: joinURL(inv.token) });
      if (inv.revoked) r.classList.add('off');
      list.appendChild(r);
    }
    const make = el('button', 'btn btn-primary g-go', 'پیوند تازه'); make.type = 'button';
    make.addEventListener('click', async () => {
      const v = await ask({ title: 'پیوند تازه', ok: 'ساختن', fields: [
        { id: 'name', label: 'نام (فقط برای مدیران)', max: 32 },
        { id: 'exp', label: 'اعتبار', type: 'select', options: EXPIRES, value: '' },
        { id: 'limit', label: 'حداکثر نفر (۰: بی‌حد)', type: 'number', value: '0' },
        { id: 'appr', label: 'پیوستن با تأیید مدیر', type: 'select', options: [['0', 'خیر'], ['1', 'بله']], value: '0' }] });
      if (!v) return;
      const body = { name: v.name.trim(), limit: Math.max(0, parseInt(v.limit, 10) || 0), approval: v.appr === '1' };
      if (v.exp) body.expires = new Date(Date.now() + Number(v.exp) * 3600e3).toISOString();
      try { const r = await call(chat, '/invites', body); copyText(joinURL(r.invite.token), 'پیوند ساخته و رونوشت شد.'); paint(); } catch (e) { fail(e); }
    });
    body.replaceChildren(list, make);
  };
  await paint();
  openPanel().push('پیوندهای دعوت', body, { refresh: paint });
}
async function linkScreen(chat, inv, refresh) {
  const body = el('div', 'g-info');
  const url = el('p', 'g-about g-ltr', joinURL(inv.token)); url.dir = 'ltr';
  const acts = el('div', 'g-list');
  acts.appendChild(row(ICON.link, 'رونوشت پیوند', () => copyText(joinURL(inv.token), 'پیوند دعوت رونوشت شد.')));
  if (!inv.revoked) acts.appendChild(row(ICON.x, 'باطل کردن پیوند', async () => {
    if (!await ask({ title: 'باطل کردن پیوند', text: 'کسی دیگر با این پیوند نمی‌تواند بپیوندد.' + (inv.primary ? ' یک پیوند اصلی تازه ساخته می‌شود.' : ''), ok: 'باطل کن', danger: true })) return;
    try { await call(chat, `/invites/${inv.id}/revoke`, {}); toast('پیوند باطل شد.'); openPanel().pop(); refresh(); } catch (e) { fail(e); }
  }, { danger: true }));
  const who = el('div', 'g-list');
  try {
    const j = await call(chat, `/invites/${inv.id}/joined`);
    who.appendChild(el('div', 'g-sec', `با این پیوند پیوستند (${fa((j.users || []).length)})`));
    for (const u of j.users || []) who.appendChild(row(avatar({ title: nameOf(u), peer: { id: u } }, 36), nameOf(u), () => { }));
  } catch { }
  body.append(url, acts, who);
  openPanel().push(inv.primary ? 'پیوند اصلی' : (inv.name || 'پیوند دعوت'), body);
}
async function copyText(t, ok) { try { await navigator.clipboard.writeText(t); toast(ok); } catch { toast('رونوشت انجام نشد.'); } }

// ---------------------------------------------------------- join requests
export async function openRequests(chat, g, refresh) {
  const body = el('div', 'g-info');
  const decide = async (user, approve) => {
    try { const r = await call(chat, '/requests', { user, approve }); toast(approve ? `${fa(r.done)} درخواست پذیرفته شد.` : `${fa(r.done)} درخواست رد شد.`); await loadInfo(chat.id); paint(); refresh && refresh(); } catch (e) { fail(e); }
  };
  const paint = async () => {
    let j; try { j = await call(chat, '/requests'); } catch (e) { body.replaceChildren(el('p', 'g-note', e.text)); return; }
    const reqs = j.requests || [];
    if (!reqs.length) { body.replaceChildren(el('p', 'g-note', 'درخواستی در انتظار نیست.')); return; }
    const list = el('div', 'g-list');
    for (const r of reqs) {
      const line = el('div', 'g-req');
      line.append(avatar({ title: r.name || r.user, peer: { id: r.user } }, 36));
      const t = el('span', 'g-row-text'); t.append(el('span', 'g-row-label', r.name || r.user), el('span', 'g-row-sub', dayFmt.format(new Date(r.at)) + ' ' + timeFmt.format(new Date(r.at))));
      const yes = el('button', 'btn btn-primary', 'پذیرش'); yes.type = 'button'; yes.addEventListener('click', () => decide(r.user, true));
      const no = el('button', 'btn', 'رد'); no.type = 'button'; no.addEventListener('click', () => decide(r.user, false));
      line.append(t, no, yes); list.appendChild(line);
    }
    const all = el('div', 'm-modal-actions');
    const ya = el('button', 'btn btn-primary', 'پذیرش همه'); ya.type = 'button'; ya.addEventListener('click', () => decide('', true));
    const na = el('button', 'btn', 'رد همه'); na.type = 'button'; na.addEventListener('click', () => decide('', false));
    all.append(na, ya);
    body.replaceChildren(list, all);
  };
  await paint();
  openPanel().push('درخواست‌های عضویت', body, { refresh: paint });
}

// --------------------------------------------------------------- the log
const LOG = {
  create: 'ساخت', add: 'افزودن عضو', approve: 'پذیرش درخواست', edit: 'تغییر تنظیمات', invite: 'ساخت پیوند', revoke: 'باطل کردن پیوند',
  topic: 'ساخت موضوع', topic_edit: 'تغییر موضوع', transfer: 'سپردن مالکیت', ban: 'کنار گذاشتن', kick: 'بیرون کردن', unban: 'برداشتن کنارگذاری',
  restrict: 'محدود کردن', promote: 'مدیر کردن', demote: 'برکناری از مدیریت',
};
export async function openLog(chat) {
  const body = el('div', 'g-info');
  const filter = select('g-log-a', [['', 'همه'], ...Object.entries(LOG)], '');
  const list = el('div', 'g-list g-log');
  const paint = async () => {
    let j; try { j = await call(chat, '/log?action=' + encodeURIComponent(filter.value)); } catch (e) { list.replaceChildren(el('p', 'g-note', e.text)); return; }
    const rows = (j.log || []).map(x => {
      const d = x.d || {};
      const who = d.user ? ' ← ' + nameOf(d.user) : (d.users ? ' ← ' + d.users.map(nameOf).join('، ') : (d.title ? ' «' + d.title + '»' : ''));
      const r = el('div', 'g-log-row');
      const t = el('span', 'g-row-text');
      const l = el('span', 'g-row-label', `${nameOf(x.actor)}: ${LOG[x.action] || x.action}${who}`); l.dir = 'auto';
      t.append(l, el('span', 'g-row-sub', dayFmt.format(new Date(x.at)) + ' ' + timeFmt.format(new Date(x.at))));
      r.appendChild(t); return r;
    });
    list.replaceChildren(...(rows.length ? rows : [el('p', 'g-note', 'در ۴۸ ساعت گذشته رویدادی نبوده است.')]));
  };
  filter.addEventListener('change', paint);
  body.append(field('نوع رویداد', filter), list);
  await paint();
  openPanel().push('رویدادهای ۴۸ ساعت اخیر', body);
}

// ------------------------------------------------------------ statistics
// Drawn as plain SVG bars, not Chart.js: no hit-testing (which the app's
// font-size zoom breaks in Chart.js) and nothing more to load.
function bars(points, label) {
  const W = 320, H = 120, P = 18, n = Math.max(points.length, 1);
  const max = Math.max(1, ...points.map(p => p.n));
  const s = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  s.setAttribute('viewBox', `0 0 ${W} ${H + P}`); s.setAttribute('class', 'g-chart'); s.setAttribute('role', 'img'); s.setAttribute('aria-label', label);
  const bw = (W - 8) / n;
  points.forEach((p, i) => {
    const h = Math.max(1, (p.n / max) * (H - 8));
    const r = document.createElementNS(s.namespaceURI, 'rect');
    r.setAttribute('x', String(W - 4 - (i + 1) * bw + 1)); r.setAttribute('y', String(H - h)); // right to left, as the page reads
    r.setAttribute('width', String(Math.max(1, bw - 2))); r.setAttribute('height', String(h)); r.setAttribute('rx', '2');
    const t = document.createElementNS(s.namespaceURI, 'title'); t.textContent = `${p.label}: ${fa(p.n)}`; r.appendChild(t);
    s.appendChild(r);
  });
  const top = document.createElementNS(s.namespaceURI, 'text');
  top.setAttribute('x', String(W - 4)); top.setAttribute('y', String(H + P - 4)); top.setAttribute('text-anchor', 'start'); top.textContent = 'بیشینه ' + fa(max);
  s.appendChild(top);
  return s;
}
export async function openStats(chat, g) {
  let j; try { j = await call(chat, '/stats'); } catch (e) { fail(e); return; }
  // The last 30 days, one slot each, the quiet ones too.
  const days = list => {
    const n = new Map((list || []).map(d => [new Date(d.day).toDateString(), d.n]));
    return Array.from({ length: 30 }, (_, i) => { const t = new Date(Date.now() - (29 - i) * 864e5); return { label: dayFmt.format(t), n: n.get(t.toDateString()) || 0 }; });
  };
  const body = el('div', 'g-info');
  const sum = (list) => (list || []).reduce((a, x) => a + x.n, 0);
  body.appendChild(section('در ۳۰ روز گذشته',
    el('div', 'g-kpis', ''),
  ));
  const k = body.querySelector('.g-kpis');
  for (const [n, t] of [[g.chat.members, g.chat.type === 'channel' ? 'مشترک' : 'عضو'], [sum(j.joins), 'پیوسته'], [sum(j.messages), 'پیام']]) {
    const c = el('div', 'g-kpi'); c.append(el('b', '', fa(n)), el('span', '', t)); k.appendChild(c);
  }
  body.appendChild(section('پیوستن در هر روز', bars(days(j.joins), 'پیوستن در هر روز')));
  body.appendChild(section('پیام در هر روز', bars(days(j.messages), 'پیام در هر روز')));
  if ((j.top || []).length && g.chat.type !== 'channel') {
    const list = el('div', 'g-list');
    for (const p of j.top) list.appendChild(row(avatar({ title: p.name, peer: { id: p.user } }, 32), p.name, () => { }, { sub: `${fa(p.n)} پیام` }));
    body.appendChild(section('پرکارترین اعضا', list));
  }
  if ((j.views || []).length) {
    const list = el('div', 'g-list');
    for (const v of j.views) { const r = row(ICON.eye, v.text || 'پست', () => { }, { sub: `${fa(v.views)} بازدید` }); list.appendChild(r); }
    body.appendChild(section('بازدید پست‌های اخیر', list));
  }
  if (!reduced()) body.querySelectorAll('.g-chart rect').forEach((r, i) => r.animate([{ transform: 'scaleY(0)' }, { transform: 'none' }], { duration: 360, delay: Math.min(i, 30) * 12, easing: 'cubic-bezier(0.2,0,0,1)', fill: 'backwards' }));
  openPanel().push('آمار', body);
}

// ---------------------------------------------------------------- members
export async function change(chat, m, req, done) {
  try { await call(chat, `/members/${encodeURIComponent(m.id)}`, req); toast('انجام شد.'); await loadInfo(chat.id); done && done(); } catch (e) { fail(e); }
}
const RIGHTS = [
  ['change_info', 'تغییر نام و توضیح'], ['delete_messages', 'حذف پیام دیگران'], ['ban_users', 'کنار گذاشتن و محدود کردن اعضا'],
  ['invite_users', 'دعوت و پیوندهای دعوت'], ['pin_messages', 'سنجاق کردن'], ['manage_topics', 'مدیریت موضوع‌ها'], ['manage_calls', 'مدیریت تماس‌های گروهی'],
  ['post_messages', 'فرستادن پست'], ['edit_messages', 'ویرایش پست دیگران'], ['add_admins', 'افزودن مدیر تازه'],
];
export function openRights(chat, g, m, done) {
  const mine = g.me.role === 'owner' ? null : (g.me.admin || {});
  const form = el('form', 'g-form');
  // An admin's own rights; for someone being made one, the usual moderator set.
  const cur = m.role === 'admin' ? (m.admin || {}) : { delete_messages: true, ban_users: true, invite_users: true, pin_messages: true, post_messages: true };
  const lines = RIGHTS.filter(([k]) => (k !== 'post_messages' && k !== 'edit_messages') || chat.type === 'channel')
    .filter(([k]) => k !== 'manage_topics' || (S.ginfo.get(chat.id) || g).chat.forum)
    .map(([k, label]) => { const t = toggle('g-r-' + k, label, cur[k]); if (mine && !mine[k]) { t.querySelector('input').disabled = true; t.classList.add('off'); } return t; });
  const title = el('input', 'input'); title.id = 'g-r-title'; title.maxLength = 16; title.value = m.title || ''; title.dir = 'auto';
  const go = el('button', 'btn btn-primary g-go', 'ذخیره'); go.type = 'submit';
  form.append(section('این مدیر می‌تواند', ...lines), field('عنوان (به‌جای «مدیر»)', title), go);
  form.addEventListener('submit', e => {
    e.preventDefault();
    const rights = Object.fromEntries(RIGHTS.map(([k]) => [k, !!(form.querySelector('#g-r-' + k) && form.querySelector('#g-r-' + k).checked)]));
    change(chat, m, { action: 'promote', rights, title: title.value.trim() }, () => { openPanel().pop(); done && done(); });
  });
  openPanel().push(m.role === 'admin' ? 'اختیارات مدیر' : 'مدیر کردن', form);
}
const UNTIL = [['1', '۱ ساعت'], ['24', '۱ روز'], ['168', '۱ هفته'], ['720', '۳۰ روز'], ['', 'همیشه']];
const until = h => h ? new Date(Date.now() + Number(h) * 3600e3).toISOString() : null;
export function openRestrict(chat, g, m, done) {
  const form = el('form', 'g-form');
  const base = (g.chat.settings && g.chat.settings.perms) || defaultPerms;
  const cur = m.restrict || {};
  const lines = PERMS.filter(([k]) => k !== 'manage_topics' || g.chat.forum).map(([k, label]) => toggle('g-x-' + k, label, m.restrict ? cur[k] : base[k]));
  const len = select('g-x-until', UNTIL, '24');
  const go = el('button', 'btn btn-primary g-go', 'محدود کن'); go.type = 'submit';
  form.append(section(`${m.name || m.id} می‌تواند`, ...lines), field('تا', len), go);
  form.addEventListener('submit', e => {
    e.preventDefault();
    const perms = Object.fromEntries(PERMS.map(([k]) => [k, !!(form.querySelector('#g-x-' + k) && form.querySelector('#g-x-' + k).checked)]));
    change(chat, m, { action: 'restrict', perms, until: until(len.value) }, () => { openPanel().pop(); done && done(); });
  });
  openPanel().push('محدود کردن', form);
}
export async function kick(chat, m, done) {
  if (!await ask({ title: 'بیرون کردن', text: `${m.name || m.id} از ${kindName(chat.type)} بیرون شود؟ می‌تواند با پیوند دعوت برگردد.`, ok: 'بیرون کن', danger: true })) return;
  change(chat, m, { action: 'kick' }, done);
}
export async function ban(chat, m, done) {
  const v = await ask({ title: 'کنار گذاشتن', text: `${m.name || m.id} بیرون می‌شود و تا پایان این مدت نمی‌تواند برگردد.`, ok: 'کنار بگذار', danger: true,
    fields: [{ id: 'until', label: 'مدت', type: 'select', options: UNTIL, value: '' }] });
  if (!v) return;
  change(chat, m, { action: 'ban', until: until(v.until) }, done);
}
export async function transfer(chat, m, done) {
  const v = await ask({ title: 'سپردن مالکیت', text: `مالکیت به ${m.name || m.id} سپرده می‌شود و شما مدیر می‌مانید. این کار برگشت‌پذیر نیست مگر او برگرداند.`, ok: 'سپردن', danger: true,
    fields: [{ id: 'pw', label: 'گذرواژهٔ حساب شما', type: 'password', autocomplete: 'current-password' }] });
  if (!v) return;
  try { await call(chat, '/transfer', { user: m.id, password: v.pw }); toast('مالکیت سپرده شد.'); await loadInfo(chat.id); done && done(); } catch (e) { fail(e); }
}
export async function removeChat(chat, g) {
  const v = await ask({ title: `حذف ${kindName(g.chat.type)}`, text: `همهٔ پیام‌ها برای همهٔ اعضا پاک می‌شود و برگشت ندارد. برای تأیید، نام را بنویسید: ${g.chat.title}`, ok: 'حذف برای همه', danger: true,
    fields: [{ id: 'title', label: 'نام' }] });
  if (!v) return;
  if (v.title.trim() !== g.chat.title) { toast('نام نوشته‌شده با نام گفت‌وگو یکی نیست.'); return; }
  try { await call(chat, '/delete', { title: g.chat.title }); openPanel().close(); forget(chat.id); toast('حذف شد.'); } catch (e) { fail(e); }
}

// ------------------------------------------------------------------ topics
const TOPIC_ICONS = [['', 'حرف اول نام'], ['📌', '📌'], ['💬', '💬'], ['📦', '📦'], ['💰', '💰'], ['🚚', '🚚'], ['📄', '📄'], ['⚠️', '⚠️'], ['✅', '✅']];
export async function newTopic(chat, refresh) {
  const v = await ask({ title: 'موضوع تازه', ok: 'ساختن', fields: [{ id: 'title', label: 'نام موضوع', max: 128 }, { id: 'icon', label: 'نشانه', type: 'select', options: TOPIC_ICONS, value: '' }] });
  if (!v || !v.title.trim()) return;
  try { await call(chat, '/topics', { title: v.title.trim(), icon: v.icon }); refresh && refresh(); } catch (e) { fail(e); }
}
export async function topicMenu(chat, t, refresh) {
  const v = await ask({ title: t.title, ok: 'ذخیره', fields: [
    { id: 'title', label: 'نام', value: t.title, max: 128 },
    { id: 'closed', label: 'وضعیت', type: 'select', options: [['0', 'باز'], ['1', 'بسته (فقط مدیران می‌نویسند)']], value: t.closed ? '1' : '0' },
    { id: 'pinned', label: 'جای موضوع', type: 'select', options: [['0', 'به ترتیب پیام‌ها'], ['1', 'بالای فهرست']], value: t.pinned ? '1' : '0' }] });
  if (!v) return;
  try { await call(chat, `/topics/${t.id}`, { title: v.title.trim(), closed: v.closed === '1', pinned: v.pinned === '1' }); refresh && refresh(); } catch (e) { fail(e); }
}
