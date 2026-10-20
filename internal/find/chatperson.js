// chatperson.js: a colleague's contact card in the messenger. A press on the
// picture or the name at the top of a private chat (or on a colleague in the
// call list) shows what DocFind has from the office-automation directory:
// the personnel code, the e-mail addresses, the mobile; and the ways to
// reach them from here (a message, a call, a secret chat). The pictures
// themselves are drawn by avatar() in chatpane.js from S.photos.
import { el, svg, S, hooks, toast } from '/chatstate.js';
import { api, openPanel } from '/chatgroup.js';
import { statusText } from '/chatpane.js';
import { rowMenu } from '/chatlist.js';
import { listPhotos, smallURL, fullURL, viewPhotos } from '/photoview.js';

const IC = {
  id: '<rect x="3" y="4" width="18" height="16" rx="2"/><circle cx="9" cy="11" r="2.5"/><path d="M5.5 17a3.5 3.5 0 0 1 7 0M15 9h3M15 13h3"/>',
  mail: '<rect x="3" y="5" width="18" height="14" rx="2"/><path d="M3 7l9 6 9-6"/>',
  mobile: '<rect x="7" y="2" width="10" height="20" rx="2"/><path d="M11 18h2"/>',
  copy: '<rect x="9" y="9" width="11" height="11" rx="2"/><path d="M5 15V5a2 2 0 0 1 2-2h10"/>',
  msg: '<path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/>',
  phone: '<path d="M22 16.9v3a2 2 0 0 1-2.2 2 19.8 19.8 0 0 1-8.6-3.1 19.5 19.5 0 0 1-6-6A19.8 19.8 0 0 1 2.1 4.2 2 2 0 0 1 4.1 2h3a2 2 0 0 1 2 1.7c.1.9.4 1.8.7 2.7a2 2 0 0 1-.5 2.1L8 9.8a16 16 0 0 0 6 6l1.3-1.3a2 2 0 0 1 2.1-.4c.9.3 1.8.6 2.7.7a2 2 0 0 1 1.7 2z"/>',
  video: '<path d="M23 7l-7 5 7 5V7z"/><rect x="1" y="5" width="15" height="14" rx="2"/>',
  lock: '<rect x="5" y="11" width="14" height="10" rx="2"/><path d="M8 11V7a4 4 0 0 1 8 0v4"/>',
  search: '<circle cx="11" cy="11" r="7"/><path d="M20 20l-3.5-3.5"/>',
  more: '<circle cx="5" cy="12" r="1.4"/><circle cx="12" cy="12" r="1.4"/><circle cx="19" cy="12" r="1.4"/>',
  at: '<circle cx="12" cy="12" r="4"/><path d="M16 8v5a3 3 0 0 0 6 0v-1a10 10 0 1 0-4 8"/>',
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

// theater opens a profile's picture to the full width (Telegram's profile,
// asked for on 2026-10-19): pulling the profile down, or pressing the round
// picture, and the picture fills the top with the name over it; the dashes
// are the pictures, a press on either side moves between them, in the middle
// it opens the viewer; scrolling on puts the circle back.
function theater(scr, head, c, own) {
  const scroller = scr.querySelector('.g-body');
  const av = head.querySelector('.m-av');
  let photos = [], at = 0, open = false;
  const stage = el('div', 'p-stage'); stage.hidden = true;
  const img = el('img', 'p-stage-img'); img.alt = ''; img.draggable = false;
  const dashes = el('div', 'p-dashes'); dashes.setAttribute('aria-hidden', 'true');
  const meta = el('div', 'p-stage-meta');
  const nm = el('div', 'p-stage-name', c.name); nm.dir = 'auto';
  meta.appendChild(nm);
  if (own) { const s = statusText(own); if (s.text) meta.appendChild(el('div', 'p-stage-seen', s.text)); }
  stage.append(img, dashes, meta);
  stage.setAttribute('role', 'button'); stage.tabIndex = 0; stage.setAttribute('aria-label', 'تصویرهای ' + c.name);
  head.prepend(stage);
  const show = i => {
    at = (i + photos.length) % photos.length;
    const p = photos[at];
    img.src = smallURL(c.id, p.id);
    const sharp = new Image(); sharp.onload = () => { if (open && photos[at] === p) img.src = sharp.src; }; sharp.src = fullURL(c.id, p.id);
    dashes.replaceChildren(...(photos.length > 1 ? photos.map((_, j) => el('i', j === at ? 'on' : '')) : []));
  };
  const expand = () => {
    if (open || !photos.length) return;
    open = true; scr.classList.add('p-theater'); stage.hidden = false; show(0);
    if (!matchMedia('(prefers-reduced-motion: reduce)').matches && av) {
      const a = av.getBoundingClientRect(), s = stage.getBoundingClientRect();
      const r = a.width / 2, cx = a.left - s.left + r, cy = a.top - s.top + r;
      stage.animate([{ clipPath: `circle(${r}px at ${cx}px ${cy}px)` }, { clipPath: `circle(${Math.hypot(s.width, s.height)}px at ${cx}px ${cy}px)` }],
        { duration: 360, easing: 'cubic-bezier(0.2,0,0,1)' });
    }
  };
  const collapse = () => { if (!open) return; open = false; scr.classList.remove('p-theater'); stage.hidden = true; };
  listPhotos(c.id).then(list => {
    photos = list;
    if (!photos.length || !av) return;
    av.setAttribute('role', 'button'); av.tabIndex = 0; av.setAttribute('aria-label', 'نمایش تصویرهای ' + c.name);
    av.classList.add('p-zoom');
    av.addEventListener('click', expand);
    av.addEventListener('keydown', e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); expand(); } });
  });
  stage.addEventListener('click', e => {
    const r = stage.getBoundingClientRect(), x = (e.clientX - r.left) / r.width;
    if (photos.length > 1 && x < 1 / 3) show(at + 1);       // the next one is to the left
    else if (photos.length > 1 && x > 2 / 3) show(at - 1);
    else viewPhotos({ user: c.id, name: c.name, photos, start: at });
  });
  stage.addEventListener('keydown', e => {
    if (e.key === 'Enter') viewPhotos({ user: c.id, name: c.name, photos, start: at });
    else if (e.key === 'ArrowLeft') show(at + 1);
    else if (e.key === 'ArrowRight') show(at - 1);
    else if (e.key === 'Escape') { e.stopPropagation(); collapse(); }
  });
  // Pulling down at the top opens it; scrolling on closes it again.
  let y0 = -1;
  scroller.addEventListener('touchstart', e => { y0 = scroller.scrollTop <= 0 ? e.touches[0].clientY : -1; }, { passive: true });
  scroller.addEventListener('touchmove', e => { if (y0 >= 0 && e.touches[0].clientY - y0 > 64) { y0 = -1; expand(); } }, { passive: true });
  scroller.addEventListener('wheel', e => {
    if (!open && scroller.scrollTop <= 0 && e.deltaY < -30) expand();
    else if (open && e.deltaY > 30) collapse();
  }, { passive: true });
  scroller.addEventListener('scroll', () => { if (open && scroller.scrollTop > 48) collapse(); }, { passive: true });
}

hooks.openPerson = async (user, chat) => {
  if (!user || user === S.me) return;
  const body = el('div', 'p-card');
  const panel = openPanel();
  const scr = panel.push('اطلاعات', body);
  body.appendChild(el('div', 'm-skel'));
  let c;
  try { c = (await api(`/api/chat/people/${encodeURIComponent(user)}/card`)).card; }
  catch (e) { body.replaceChildren(el('p', 'g-note', (e && e.text) || 'اطلاعات این کاربر خوانده نشد.')); return; }
  if (c.photo) S.photos.add(c.id);
  // Telegram's profile: the face, the name, when they were last seen; then
  // the ways to reach them as tiles; then what is known, in one card.
  const own = chat && chat.type === 'private' && chat.peer && chat.peer.id === c.id ? chat : null;
  const head = el('div', 'p-head');
  const name = el('h3', 'p-name', c.name); name.dir = 'auto';
  head.append(hooks.avatar({ title: c.name, peer: { id: c.id, photo: c.photo } }, 112), name);
  if (own) { const s = statusText(own); if (s.text) head.appendChild(el('div', 'p-seen' + (s.online ? ' online' : ''), s.text)); }
  if (c.english_name) { const en = el('div', 'p-en', c.english_name); en.dir = 'ltr'; head.appendChild(en); }
  if (c.disabled) head.appendChild(el('div', 'p-off', 'این حساب غیرفعال است'));

  const acts = el('div', 'p-acts');
  const close = () => panel.close();
  if (!own) acts.appendChild(action(IC.msg, 'پیام', () => { close(); hooks.start && hooks.start(c.id); }));
  if (!c.disabled && hooks.startCall) {
    acts.appendChild(action(IC.phone, 'تماس', () => { close(); hooks.startCall(c.id, false); }));
    acts.appendChild(action(IC.video, 'تصویری', () => { close(); hooks.startCall(c.id, true); }));
  }
  if (own && hooks.searchChat) acts.appendChild(action(IC.search, 'جستجو', () => { close(); hooks.searchChat(''); }));
  if (!c.disabled && hooks.startSecret) acts.appendChild(action(IC.lock, 'محرمانه', () => { close(); hooks.startSecret(c.id); }));
  if (own) {
    const more = action(IC.more, 'بیشتر', () => { const r = more.getBoundingClientRect(); rowMenu(own, more, r.left + r.width / 2, r.bottom); });
    acts.appendChild(more);
  }

  const rows = el('div', 'g-block p-rows');
  rows.appendChild(row(IC.at, 'نام کاربری', '@' + c.id));
  if (c.phone) rows.appendChild(row(IC.mobile, 'موبایل', c.phone, 'tel:' + c.phone));
  for (const e of c.emails || []) rows.appendChild(row(IC.mail, 'ایمیل', e, 'mailto:' + e));
  if (c.personnel_code) rows.appendChild(row(IC.id, 'کد پرسنلی', c.personnel_code));
  body.replaceChildren(head, acts, rows);
  if (c.photo && scr) theater(scr, head, c, own);
};
