// chatperson.js: a colleague's contact card in the messenger. A press on the
// picture or the name at the top of a private chat (or on a colleague in the
// call list) shows what DocFind has from the office-automation directory:
// the personnel code, the e-mail addresses, the mobile; and the ways to
// reach them from here (a message, a call, a secret chat). The pictures
// themselves are drawn by avatar() in chatpane.js from S.photos.
import { el, svg, S, hooks, toast } from '/chatstate.js';
import { api, openPanel } from '/chatgroup.js';

const IC = {
  id: '<rect x="3" y="4" width="18" height="16" rx="2"/><circle cx="9" cy="11" r="2.5"/><path d="M5.5 17a3.5 3.5 0 0 1 7 0M15 9h3M15 13h3"/>',
  mail: '<rect x="3" y="5" width="18" height="14" rx="2"/><path d="M3 7l9 6 9-6"/>',
  mobile: '<rect x="7" y="2" width="10" height="20" rx="2"/><path d="M11 18h2"/>',
  copy: '<rect x="9" y="9" width="11" height="11" rx="2"/><path d="M5 15V5a2 2 0 0 1 2-2h10"/>',
  msg: '<path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/>',
  phone: '<path d="M22 16.9v3a2 2 0 0 1-2.2 2 19.8 19.8 0 0 1-8.6-3.1 19.5 19.5 0 0 1-6-6A19.8 19.8 0 0 1 2.1 4.2 2 2 0 0 1 4.1 2h3a2 2 0 0 1 2 1.7c.1.9.4 1.8.7 2.7a2 2 0 0 1-.5 2.1L8 9.8a16 16 0 0 0 6 6l1.3-1.3a2 2 0 0 1 2.1-.4c.9.3 1.8.6 2.7.7a2 2 0 0 1 1.7 2z"/>',
  video: '<path d="M23 7l-7 5 7 5V7z"/><rect x="1" y="5" width="15" height="14" rx="2"/>',
  lock: '<rect x="5" y="11" width="14" height="10" rx="2"/><path d="M8 11V7a4 4 0 0 1 8 0v4"/>',
};

async function copy(text) {
  try { await navigator.clipboard.writeText(text); toast('رونوشت شد.'); }
  catch { toast('رونوشت گرفته نشد؛ متن را نگه دارید و انتخاب کنید.'); }
}

function row(icon, label, value, href) {
  const r = el('div', 'g-row p-row');
  const ic = el('span', 'g-ic'); ic.innerHTML = svg(icon);
  const t = el('span', 'g-row-text');
  const v = el(href ? 'a' : 'span', 'g-row-label p-val', value); v.dir = 'ltr';
  if (href) v.href = href;
  t.append(v, el('span', 'g-row-sub', label));
  const c = el('button', 'm-icon-btn'); c.type = 'button'; c.setAttribute('aria-label', 'رونوشت ' + label); c.title = 'رونوشت';
  c.innerHTML = svg(IC.copy); c.addEventListener('click', () => copy(value));
  r.append(ic, t, c);
  return r;
}

function action(icon, label, run) {
  const b = el('button', 'p-act'); b.type = 'button';
  const i = el('span', 'p-act-ic'); i.innerHTML = svg(icon);
  b.append(i, el('span', '', label));
  b.addEventListener('click', run);
  return b;
}

hooks.openPerson = async (user, chat) => {
  if (!user || user === S.me) return;
  const body = el('div', 'p-card');
  const panel = openPanel();
  panel.push('اطلاعات تماس', body);
  body.appendChild(el('div', 'm-skel'));
  let c;
  try { c = (await api(`/api/chat/people/${encodeURIComponent(user)}/card`)).card; }
  catch (e) { body.replaceChildren(el('p', 'g-note', (e && e.text) || 'اطلاعات این همکار خوانده نشد.')); return; }
  if (c.photo) S.photos.add(c.id);
  const head = el('div', 'p-head');
  const name = el('h3', 'p-name', c.name); name.dir = 'auto';
  head.append(hooks.avatar({ title: c.name, peer: { id: c.id, photo: c.photo } }, 96), name);
  if (c.english_name) { const en = el('div', 'p-en', c.english_name); en.dir = 'ltr'; head.appendChild(en); }
  if (c.disabled) head.appendChild(el('div', 'p-off', 'این حساب غیرفعال است'));

  const acts = el('div', 'p-acts');
  const close = () => panel.close();
  if (!chat || chat.type !== 'private') acts.appendChild(action(IC.msg, 'پیام', () => { close(); hooks.start && hooks.start(c.id); }));
  if (!c.disabled && hooks.startCall) {
    acts.appendChild(action(IC.phone, 'تماس صوتی', () => { close(); hooks.startCall(c.id, false); }));
    acts.appendChild(action(IC.video, 'تماس تصویری', () => { close(); hooks.startCall(c.id, true); }));
  }
  if (!c.disabled && hooks.startSecret) acts.appendChild(action(IC.lock, 'محرمانه', () => { close(); hooks.startSecret(c.id); }));

  const rows = el('div', 'g-block p-rows');
  if (c.personnel_code) rows.appendChild(row(IC.id, 'کد پرسنلی', c.personnel_code));
  for (const e of c.emails || []) rows.appendChild(row(IC.mail, 'ایمیل', e, 'mailto:' + e));
  if (c.phone) rows.appendChild(row(IC.mobile, 'موبایل', c.phone, 'tel:' + c.phone));
  if (!rows.children.length) rows.appendChild(el('p', 'g-note', 'اطلاعات تماسی برای این همکار در DocFind ثبت نشده است.'));
  body.replaceChildren(head, acts, rows);
};
