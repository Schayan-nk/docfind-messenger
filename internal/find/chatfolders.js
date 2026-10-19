// chatfolders.js: making and changing chat folders (docs/chat/SPEC.md 6.5):
// a name and a sign, which kinds of chat are in (contacts, others, groups,
// channels), which are kept out (muted, read, archived), chats chosen one by
// one, the folder's order, and its share link. Loaded on first use by
// chatlist.js.
import { el, fa, svg, S, toast } from '/chatstate.js';
import { avatar } from '/chatpane.js';
import { api, openPanel, ask, row } from '/chatgroup.js';
import { saveFolder, loadFolders, renderTabs } from '/chatlist.js';

const ICONS = ['📁', '💼', '👥', '📢', '⭐', '🏠', '🔔', '💬', '📦', '💰', '🚚', '📄'];
const INCLUDE = [['contacts', 'مخاطبان'], ['non_contacts', 'دیگر همکاران'], ['groups', 'گروه‌ها'], ['channels', 'کانال‌ها']];
const EXCLUDE = [['muted', 'بی‌صداها'], ['read', 'خوانده‌شده‌ها'], ['archived', 'بایگانی‌شده‌ها']];
const SUGGEST = [
  { title: 'گروه‌ها', icon: '👥', include: { groups: true } },
  { title: 'کانال‌ها', icon: '📢', include: { channels: true } },
  { title: 'خصوصی', icon: '💬', include: { contacts: true, non_contacts: true } },
];
const FOLDER = '<path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/>';
const PLUS = '<path d="M12 5v14M5 12h14"/>';
const UP = '<path d="M18 15l-6-6-6 6"/>';

function summary(f) {
  const kinds = [...INCLUDE.filter(([k]) => f.include && f.include[k]).map(([, t]) => t)];
  const n = (f.include_chats || []).length;
  if (n) kinds.push(`${fa(n)} گفت‌وگو`);
  return kinds.join('، ') || 'خالی';
}

// openFolders: the person's folders, in order, with suggestions.
export function openFolders() {
  const body = el('div', 'g-info');
  const p = openPanel();
  const paint = () => {
    const list = el('div', 'g-list');
    S.folders.forEach((f, i) => {
      const r = row(f.icon ? Object.assign(el('span', 'g-ic g-emo-ic'), { textContent: f.icon }) : FOLDER, f.title, () => editFolder(f), { sub: summary(f) });
      if (i > 0) {
        const up = el('button', 'm-icon-btn g-up'); up.type = 'button'; up.innerHTML = svg(UP); up.setAttribute('aria-label', `بالا بردن ${f.title}`);
        up.addEventListener('click', async e => {
          e.stopPropagation();
          const ids = S.folders.map(x => x.id); [ids[i - 1], ids[i]] = [ids[i], ids[i - 1]];
          try { await api('/api/chat/folders/order', { ids }); await loadFolders(); paint(); } catch (err) { toast(err.text || 'انجام نشد.'); }
        });
        r.appendChild(up);
      }
      list.appendChild(r);
    });
    const kids = [el('p', 'g-note', 'پوشه‌ها گفت‌وگوها را جدا می‌کنند؛ هر گفت‌وگو در «همه» هم می‌ماند.'), list];
    kids.push(row(PLUS, 'پوشهٔ تازه', () => editFolder({})));
    const have = new Set(S.folders.map(f => f.title));
    const sug = SUGGEST.filter(s => !have.has(s.title));
    if (sug.length) {
      const box = el('div', 'g-list');
      box.appendChild(el('div', 'g-sec', 'پیشنهاد'));
      for (const s of sug) box.appendChild(row(Object.assign(el('span', 'g-ic g-emo-ic'), { textContent: s.icon }), s.title, async () => { await saveFolder({ ...s, include_chats: [], exclude_chats: [], pinned: [], exclude: {} }); paint(); }, { sub: 'افزودن' }));
      kids.push(box);
    }
    body.replaceChildren(...kids);
  };
  paint();
  p.push('پوشه‌ها', body, { refresh: paint });
}

// pickChats: choose chats from the list; resolves with their ids, or null.
function pickChats(title, chosen) {
  return new Promise(resolve => {
    const set = new Set(chosen);
    const body = el('div', 'g-pick');
    const q = el('input', 'input'); q.type = 'search'; q.placeholder = 'جستجوی گفت‌وگو'; q.setAttribute('aria-label', 'جستجوی گفت‌وگو'); q.id = 'g-fq';
    const list = el('div', 'g-list');
    const go = el('button', 'btn btn-primary g-go', 'تأیید'); go.type = 'button';
    const chats = [...S.chats.values()].filter(c => c.type !== 'secret').sort((a, b) => new Date((b.last && b.last.at) || 0) - new Date((a.last && a.last.at) || 0));
    const paint = () => {
      const f = q.value.trim().toLowerCase();
      list.replaceChildren(...chats.filter(c => !f || (c.title || '').toLowerCase().includes(f)).map(c => {
        const r = row(avatar(c, 36), c.title, () => { set.has(c.id) ? set.delete(c.id) : set.add(c.id); r.classList.toggle('on', set.has(c.id)); r.setAttribute('aria-pressed', String(set.has(c.id))); });
        r.classList.toggle('on', set.has(c.id)); r.setAttribute('aria-pressed', String(set.has(c.id)));
        return r;
      }));
    };
    q.addEventListener('input', paint);
    body.append(q, list, go);
    const p = openPanel(), depth = p.stack.length;
    let done = false;
    go.addEventListener('click', () => { done = true; p.pop(); resolve([...set]); });
    p.push(title, body);
    paint();
    const watch = setInterval(() => { if (done) { clearInterval(watch); return; } if (!p.box.isConnected || p.stack.length <= depth) { clearInterval(watch); resolve(null); } }, 300);
  });
}

// editFolder: one folder's screen (a new one when f has no id).
export function editFolder(f) {
  const cur = { title: '', icon: '', include: {}, exclude: {}, include_chats: [], exclude_chats: [], pinned: [], ...f };
  cur.include = { ...cur.include }; cur.exclude = { ...cur.exclude };
  const form = el('form', 'g-form');
  const title = el('input', 'input'); title.id = 'g-f-title'; title.maxLength = 32; title.value = cur.title; title.dir = 'auto'; title.required = true;
  const tl = el('label', 'g-field'); tl.append(el('span', '', 'نام پوشه'), title);
  const icons = el('div', 'g-emoji'); icons.setAttribute('role', 'radiogroup'); icons.setAttribute('aria-label', 'نشانه');
  for (const i of ['', ...ICONS]) {
    const b = el('button', 'g-emo' + (cur.icon === i ? ' on' : ''), i || '—'); b.type = 'button'; b.setAttribute('role', 'radio'); b.setAttribute('aria-checked', String(cur.icon === i));
    b.addEventListener('click', () => { cur.icon = i; icons.querySelectorAll('.g-emo').forEach(x => { x.classList.toggle('on', x === b); x.setAttribute('aria-checked', String(x === b)); }); });
    icons.appendChild(b);
  }
  const sw = (id, label, on, set) => {
    const l = el('label', 'g-switch'); const i = el('input'); i.type = 'checkbox'; i.id = id; i.checked = !!on; i.setAttribute('role', 'switch');
    i.addEventListener('change', () => set(i.checked));
    l.append(Object.assign(el('span', 'g-row-text'), { textContent: label }), i); return l;
  };
  const chatList = (key, label) => {
    const box = el('div', 'g-list');
    const paint = () => {
      box.replaceChildren(row(PLUS, label, async () => { const ids = await pickChats(label, cur[key]); if (ids) { cur[key] = ids; paint(); } }, { sub: cur[key].length ? `${fa(cur[key].length)} گفت‌وگو` : '' }),
        ...cur[key].map(id => S.chats.get(id)).filter(Boolean).map(c => row(avatar(c, 32), c.title, () => { cur[key] = cur[key].filter(x => x !== c.id); paint(); }, { sub: 'برای برداشتن بزنید' })));
    };
    paint(); return box;
  };
  const inc = el('div', 'g-block'); inc.appendChild(el('div', 'g-sec', 'گفت‌وگوهایی که در این پوشه هستند'));
  for (const [k, t] of INCLUDE) inc.appendChild(sw('g-fi-' + k, t, cur.include[k], v => { cur.include[k] = v; }));
  inc.appendChild(chatList('include_chats', 'افزودن گفت‌وگو'));
  const exc = el('div', 'g-block'); exc.appendChild(el('div', 'g-sec', 'جز این‌ها'));
  for (const [k, t] of EXCLUDE) exc.appendChild(sw('g-fe-' + k, t, cur.exclude[k], v => { cur.exclude[k] = v; }));
  exc.appendChild(chatList('exclude_chats', 'کنار گذاشتن گفت‌وگو'));
  const go = el('button', 'btn btn-primary g-go', 'ذخیره'); go.type = 'submit';
  form.append(tl, icons, inc, exc, go);
  if (cur.id) {
    const more = el('div', 'g-list g-danger');
    more.appendChild(row('<path d="M10 13a5 5 0 0 0 7.5.5l3-3a5 5 0 0 0-7-7l-1.7 1.7"/><path d="M14 11a5 5 0 0 0-7.5-.5l-3 3a5 5 0 0 0 7 7l1.7-1.7"/>', 'پیوند هم‌رسانی پوشه', () => share(cur), { sub: 'گروه‌ها و کانال‌های این پوشه با پیوندشان' }));
    more.appendChild(row('<path d="M3 6h18"/><path d="M8 6V4h8v2"/><path d="M6 6l1 14h10l1-14"/>', 'حذف پوشه', () => remove(cur), { danger: true }));
    form.appendChild(more);
  }
  form.addEventListener('submit', async e => {
    e.preventDefault();
    if (!title.value.trim()) { title.focus(); return; }
    cur.title = title.value.trim(); go.disabled = true;
    const saved = await saveFolder(cur);
    go.disabled = false;
    if (saved) { openPanel().pop(); toast('پوشه ذخیره شد.'); }
  });
  openPanel().push(cur.id ? cur.title : 'پوشهٔ تازه', form);
}

async function share(f) {
  try {
    const j = await api(`/api/chat/folders/${f.id}/share`, {});
    const url = `${location.origin}/messages#folder=${j.token}`;
    try { await navigator.clipboard.writeText(url); toast('پیوند پوشه رونوشت شد.'); }
    catch { await ask({ title: 'پیوند پوشه', text: url, ok: 'بستن' }); }
  } catch (e) { toast(e.text || 'پیوند ساخته نشد.'); }
}
async function remove(f) {
  if (!await ask({ title: 'حذف پوشه', text: `پوشهٔ «${f.title}» حذف شود؟ گفت‌وگوهایش در «همه» می‌مانند.`, ok: 'حذف', danger: true })) return;
  try { await api(`/api/chat/folders/${f.id}/delete`, {}); if (S.folder === f.id) S.folder = 'all'; await loadFolders(); renderTabs(); openPanel().pop(); }
  catch (e) { toast(e.text || 'حذف نشد.'); }
}
