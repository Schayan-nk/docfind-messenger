// chatgroupmake.js: making a group or channel, choosing colleagues, joining
// by an invite link, and a forum's topics (docs/chat/SPEC.md 6.2, 6.7). The
// rest of the group screens are chatgroup.js; the admin ones chatgroupadmin.js.
import { reduced } from '/chatcore.js';
import { $, el, fa, svg, timeFmt, dayFmt, dayKey, S, hooks, toast } from '/chatstate.js';
import { avatar } from '/chatpane.js';
import { api, openPanel, ask, row, kindName, paintGate, hue, ICON } from '/chatgroup.js';

const admin = () => import('/chatgroupadmin.js');
const fail = e => toast((e && e.text) || 'انجام نشد؛ دوباره تلاش کنید.');
const panelOf = () => openPanel();

// pickPeople: colleagues to add, with chips for the chosen ones.
function pickPeople({ title, ok }) {
  return new Promise(resolve => {
    const chosen = new Map();
    const body = el('div', 'g-pick');
    const chips = el('div', 'g-chips');
    const q = el('input', 'input'); q.type = 'search'; q.placeholder = 'نام همکار'; q.setAttribute('aria-label', 'نام همکار'); q.id = 'g-pq';
    const list = el('div', 'g-list');
    const go = el('button', 'btn btn-primary g-go', ok); go.type = 'button'; go.disabled = true;
    const paintChips = () => {
      chips.replaceChildren(...[...chosen.values()].map(p => { const c = el('button', 'g-chip', p.name); c.type = 'button'; c.dir = 'auto'; c.setAttribute('aria-label', `برداشتن ${p.name}`); c.addEventListener('click', () => { chosen.delete(p.id); paintChips(); search(); }); return c; }));
      go.disabled = !chosen.size; go.textContent = chosen.size ? `${ok} (${fa(chosen.size)})` : ok;
    };
    const search = async () => {
      let ppl = [];
      try { ppl = (await api('/api/chat/people?q=' + encodeURIComponent(q.value.trim()))).people || []; } catch { }
      list.replaceChildren(...ppl.filter(p => p.id !== S.me).map(p => {
        const r = row(avatar({ title: p.name, peer: { id: p.id } }, 36), p.name, () => { chosen.has(p.id) ? chosen.delete(p.id) : chosen.set(p.id, p); paintChips(); r.classList.toggle('on', chosen.has(p.id)); r.setAttribute('aria-pressed', String(chosen.has(p.id))); });
        r.classList.toggle('on', chosen.has(p.id)); r.setAttribute('aria-pressed', String(chosen.has(p.id)));
        return r;
      }));
    };
    let t; q.addEventListener('input', () => { clearTimeout(t); t = setTimeout(search, 200); });
    let settled = false;
    body.append(chips, q, list, go);
    const p = panelOf(); const depth = p.stack.length;
    go.addEventListener('click', () => { settled = true; p.pop(); resolve([...chosen.keys()]); });
    p.push(title, body);
    search();
    const watch = setInterval(() => { if (!settled && (!p.box.isConnected || p.stack.length <= depth)) { clearInterval(watch); settled = true; resolve(null); } if (settled) clearInterval(watch); }, 300);
  });
}

// -------------------------------------------------------------- creating
hooks.newRows = () => [
  newRow(ICON.people, 'گروه تازه', () => create('group')),
  newRow(ICON.channel, 'کانال تازه', () => create('channel')),
];
function newRow(icon, label, run) {
  const b = el('button', 'm-row'); b.type = 'button';
  const a = el('div', 'm-av g-new-av'); a.innerHTML = svg(icon);
  b.append(a, el('div', 'm-row-name', label));
  b.addEventListener('click', () => { $('m-new').hidden = true; run(); });
  return b;
}
async function create(kind) {
  const body = el('form', 'g-form');
  const f = (label, input) => { const l = el('label', 'g-field'); l.append(el('span', '', label), input); return l; };
  const title = el('input', 'input'); title.id = 'g-c-title'; title.required = true; title.maxLength = 128; title.dir = 'auto';
  const about = el('textarea', 'input'); about.id = 'g-c-about'; about.rows = 2; about.maxLength = 255; about.dir = 'auto';
  body.append(f(kind === 'channel' ? 'نام کانال' : 'نام گروه', title), f('توضیح (اختیاری)', about));
  let type = kind, forum = null;
  if (kind === 'group') {
    const seg = el('div', 'g-seg'); seg.setAttribute('role', 'radiogroup'); seg.setAttribute('aria-label', 'نوع گروه');
    const opt = (v, label, sub) => {
      const b = el('button', 'g-seg-btn'); b.type = 'button'; b.setAttribute('role', 'radio'); b.setAttribute('aria-checked', String(v === type));
      b.append(el('b', '', label), el('small', '', sub));
      b.addEventListener('click', () => { type = v; seg.querySelectorAll('.g-seg-btn').forEach(x => x.setAttribute('aria-checked', String(x === b))); forumLine.hidden = v !== 'supergroup'; });
      return b;
    };
    seg.append(opt('group', 'گروه', 'تا ۲۰۰ عضو'), opt('supergroup', 'ابرگروه', 'تا ۱۰٬۰۰۰ عضو، موضوع‌ها'));
    const forumLine = el('label', 'm-checkline'); forum = el('input'); forum.type = 'checkbox'; forum.id = 'g-c-forum';
    forumLine.append(forum, el('span', '', 'گفت‌وگو در موضوع‌های جدا (انجمن)')); forumLine.hidden = true;
    body.append(seg, forumLine);
  }
  const people = el('p', 'g-note', kind === 'channel' ? 'مشترکان را پس از ساختن کانال، با پیوند دعوت یا از فهرست همکاران بیفزایید.' : 'اعضا را در گام بعد انتخاب می‌کنید.');
  const go = el('button', 'btn btn-primary g-go', kind === 'channel' ? 'ساختن کانال' : 'ادامه'); go.type = 'submit';
  body.append(people, go);
  const p = panelOf();
  p.push(kind === 'channel' ? 'کانال تازه' : 'گروه تازه', body);
  body.addEventListener('submit', async e => {
    e.preventDefault();
    if (!title.value.trim()) { title.focus(); return; }
    let members = [];
    if (kind === 'group') { members = await pickPeople({ title: 'اعضای گروه', ok: 'ساختن گروه' }); if (members === null) return; }
    go.disabled = true;
    try {
      const j = await api('/api/chat/groups', { type, title: title.value.trim(), about: about.value.trim(), members, forum: !!(forum && forum.checked && type === 'supergroup') });
      p.close();
      await hooks.refreshChats();
      hooks.openChat(j.chat);
      if ((j.refused || []).length) toast(`${fa(j.refused.length)} همکار اجازه نمی‌دهد او را بیفزایید؛ پیوند دعوت را برایش بفرستید.`);
    } catch (err) { fail(err); go.disabled = false; }
  });
}

// ----------------------------------------------------------- join by link
export async function joinFromHash() {
  const m = location.hash.match(/^#join=([A-Za-z0-9_-]{8,64})$/); if (!m) return;
  history.replaceState(history.state, '', location.pathname);
  let p; try { p = await api('/api/chat/invite/' + m[1]); } catch (e) { fail(e); return; }
  const ok = await ask({ title: p.title, text: `${kindName(p.type)} · ${fa(p.members)} ${p.type === 'channel' ? 'مشترک' : 'عضو'}${p.about ? '\n' + p.about : ''}`, ok: p.approval ? 'درخواست عضویت' : 'پیوستن' });
  if (!ok) return;
  try {
    const r = await fetch(`/api/chat/invite/${m[1]}/join`, { method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json' }, body: '{}' });
    const j = await r.json().catch(() => ({}));
    if (r.status === 202) { toast(j.text); return; }
    if (!r.ok) { toast(j.error || 'پیوستن انجام نشد.'); return; }
    await hooks.refreshChats(); hooks.openChat(j.chat);
  } catch { toast('اتصال برقرار نیست؛ دوباره تلاش کنید.'); }
}
hooks.joinFromHash = joinFromHash;
addEventListener('hashchange', joinFromHash);

// ------------------------------------------------------------------ topics
export const topicTitle = chat => chat && chat.forum && S.topic != null && S.topicInfo ? S.topicInfo.title : '';
hooks.topicFilter = m => (m.topic || 0) === S.topic;
hooks.topicQuery = () => S.topic != null ? '&topic=' + S.topic : '';
hooks.topicList = async (box, chat) => {
  const list = el('div', 'g-topics'); list.setAttribute('role', 'list');
  box.replaceChildren(list);
  let j; try { j = await api(`/api/chat/chats/${chat.id}/topics`); } catch (e) { list.appendChild(el('div', 'm-empty', e.text)); return; }
  if (S.active !== chat.id || S.topic != null) return;
  const g = S.ginfo.get(chat.id);
  const rows = (j.topics || []).map(t => {
    const b = el('button', 'g-topic'); b.type = 'button'; b.setAttribute('role', 'listitem');
    const ic = el('span', 'g-topic-ic', t.icon || [...t.title][0] || '#'); ic.style.background = `hsl(${t.color || hue(t.title)} 45% 34%)`;
    const main = el('span', 'g-row-text'); const n = el('span', 'g-row-label', t.title); n.dir = 'auto';
    main.append(n, el('span', 'g-row-sub', t.last_at ? (dayKey(t.last_at) === dayKey(Date.now()) ? timeFmt.format(new Date(t.last_at)) : dayFmt.format(new Date(t.last_at))) : 'هنوز پیامی نیست'));
    b.append(ic, main);
    if (t.closed) { const l = el('span', 'g-ic'); l.innerHTML = svg(ICON.lock); l.setAttribute('aria-label', 'بسته'); b.appendChild(l); }
    if (t.unread) b.appendChild(el('span', 'm-badge on', fa(t.unread)));
    b.addEventListener('click', () => enterTopic(chat, t));
    if (g && g.may.topics && t.id) b.addEventListener('contextmenu', e => { e.preventDefault(); admin().then(a => a.topicMenu(chat, t, () => hooks.topicList(box, chat))); });
    return b;
  });
  if (g && g.may.topics) {
    const add = el('button', 'g-topic g-topic-new'); add.type = 'button';
    const ic = el('span', 'g-topic-ic'); ic.innerHTML = svg(ICON.plus); add.append(ic, el('span', 'g-row-label', 'موضوع تازه'));
    add.addEventListener('click', () => admin().then(a => a.newTopic(chat, () => hooks.topicList(box, chat))));
    rows.unshift(add);
  }
  list.replaceChildren(...rows);
  if (!reduced()) rows.forEach((r, i) => r.animate([{ opacity: 0, transform: 'translateY(6px)' }, { opacity: 1, transform: 'none' }], { duration: 200, delay: Math.min(i, 8) * 25, easing: 'cubic-bezier(0.2,0,0,1)', fill: 'backwards' }));
};
async function enterTopic(chat, t) {
  S.topic = t.id; S.topicInfo = t;
  history.pushState({ chat: chat.id, topic: t.id }, '', `#c${chat.id}`);
  const name = $('m-chat')?.querySelector('.m-title b'); if (name) name.textContent = t.title;
  hooks.paintStatus(); paintGate();
  hooks.renderMessages({ bottom: true });
  try {
    const r = await api(`/api/chat/chats/${chat.id}/messages?limit=50&topic=${t.id}`);
    if (S.active === chat.id && S.topic === t.id) { hooks.merge(chat.id, r.messages || []); hooks.renderMessages({ bottom: true }); }
  } catch { }
  hooks.markRead && hooks.markRead();
}
// enterTopicById opens one topic of a forum by its id (a search result in it).
hooks.enterTopicById = async (chat, id) => {
  if (S.topic === id) return;
  let t = { id, title: id ? '' : 'عمومی' };
  try { t = ((await api(`/api/chat/chats/${chat.id}/topics`)).topics || []).find(x => x.id === id) || t; } catch { }
  await enterTopic(chat, t);
};
export function leaveTopic() {
  const chat = S.chats.get(S.active); if (!chat || S.topic == null) return false;
  S.topic = null; S.topicInfo = null;
  const name = $('m-chat')?.querySelector('.m-title b'); if (name) name.textContent = chat.title;
  hooks.paintStatus(); paintGate(); hooks.renderMessages();
  return true;
}
hooks.leaveTopic = leaveTopic;
// markTopicRead: the open topic, up to its newest message.
hooks.markTopicRead = chat => {
  if (S.topic == null) return;
  const list = (S.msgs.get(chat.id) || []).filter(hooks.topicFilter);
  const pts = Math.max(0, ...list.map(m => m.pts || 0)); if (!pts) return;
  if (S.topicInfo && S.topicInfo.readTo >= pts) return;
  if (S.topicInfo) S.topicInfo.readTo = pts;
  S.net.send('read', { chat: chat.id, pts, topic: S.topic });
  clearTimeout(hooks.markTopicRead.t); hooks.markTopicRead.t = setTimeout(() => hooks.refreshChats(), 800);
};
hooks.pickPeople = pickPeople;
