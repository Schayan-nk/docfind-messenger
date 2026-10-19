// chatpoll.js: polls (SPEC 6.4). A poll message draws itself from its extra
// (the question and the options) and from its state, fetched for this reader:
// the results show once the reader has voted or the poll is closed. Every
// piece of text goes in through text nodes.
import { uuidv7, reduced, SPRING } from '/chatcore.js';
import { el, fa, svg, S, hooks, toast, timeFmt } from '/chatstate.js';
import { openPanel, row, api } from '/chatgroup.js';

const states = new Map();   // poll id -> state for this reader
const loading = new Set();
const picked = new Map();   // poll id -> options ticked before "vote" (several-answer polls)
const fail = e => toast((e && e.text) || 'انجام نشد؛ دوباره تلاش کنید.');
const msgOf = (chatID, id) => (S.msgs.get(chatID) || []).find(x => x.id === id);

function load(m) {
  const d = m.extra || {};
  if (!d.id || d.forwarded || loading.has(d.id)) return;
  loading.add(d.id);
  api(`/api/chat/polls/${d.id}?chat=${m.chat}`).then(j => { states.set(d.id, j.poll); hooks.repaint && hooks.repaint(m); })
    .catch(() => { }).finally(() => loading.delete(d.id));
}

// pollUpdate follows a "poll" update: someone voted or the poll closed.
export function pollUpdate(chatID, d) {
  const st = states.get(d.poll);
  const m = msgOf(chatID, d.msg);
  if (st) { st.total = d.total; if (d.closed) st.closed = true; }
  if (m && (!st || st.voted || st.counts || d.closed)) { states.delete(d.poll); load(m); }
  else if (m && hooks.repaint) hooks.repaint(m);
}

export function drawPoll(m, chat) {
  const d = m.extra || {};
  const box = el('div', 'x-poll');
  if (!d.question) { box.appendChild(el('div', 'x-poll-kind', 'نظرسنجی در دسترس نیست.')); return box; }
  const q = el('div', 'x-poll-q', d.question); q.dir = 'auto';
  const st = states.get(d.id);
  if (!st && !m.pending) load(m);
  const closed = st ? st.closed : (d.close_at && new Date(d.close_at) <= new Date());
  const kind = (d.quiz ? 'آزمون' : d.anonymous ? 'نظرسنجی ناشناس' : 'نظرسنجی عمومی') + (closed ? '، بسته' : '') + (d.forwarded ? '، هدایت‌شده' : '');
  box.append(q, el('div', 'x-poll-kind', kind));
  const results = !!(st && st.counts);
  const total = st ? st.total : 0;
  const mine = new Set(st ? st.mine : []);
  const ticked = picked.get(d.id) || new Set();
  const canVote = !!st && !closed && !d.forwarded && !st.voted; // the creator sees results first and may still vote
  d.options.forEach((text, i) => {
    const o = el('button', 'x-opt' + (d.multiple ? ' multi' : '')); o.type = 'button';
    const dot = el('span', 'x-dot'), t = el('span', 'x-opt-text', text); t.dir = 'auto';
    o.append(dot, t);
    if (results) {
      const n = st.counts[i] || 0, p = total ? Math.round(100 * n / total) : 0;
      const bar = el('span', 'x-bar'); bar.style.setProperty('--p', reduced() ? String(p / 100) : '0');
      o.prepend(bar); o.appendChild(el('span', 'x-pct', fa(p) + '٪'));
      o.setAttribute('aria-label', `${text}: ${fa(p)} درصد، ${fa(n)} رأی`);
      if (!reduced()) requestAnimationFrame(() => requestAnimationFrame(() => bar.style.setProperty('--p', String(p / 100))));
      if (mine.has(i)) o.classList.add('mine', 'on');
      if (st.correct != null) { if (i === st.correct) o.classList.add('right'); else if (mine.has(i)) o.classList.add('wrong'); }
    } else if (ticked.has(i)) o.classList.add('on');
    if (canVote) o.addEventListener('click', () => {
      if (!d.multiple) { vote(m, [i]); return; }
      const set = picked.get(d.id) || new Set(); set.has(i) ? set.delete(i) : set.add(i); picked.set(d.id, set);
      hooks.repaint && hooks.repaint(m);
    });
    else o.setAttribute('aria-disabled', 'true');
    box.appendChild(o);
  });
  if (st && st.explanation && st.correct != null) { const ex = el('div', 'x-explain', st.explanation); ex.dir = 'auto'; box.appendChild(ex); }
  const foot = el('div', 'x-poll-foot');
  foot.appendChild(el('span', '', total ? `${fa(total)} رأی` : 'هنوز رأیی نیامده'));
  if (canVote && d.multiple && ticked.size) foot.appendChild(link('ثبت رأی', () => vote(m, [...ticked])));
  if (st && st.voted && !closed && !d.quiz && !d.forwarded) foot.appendChild(link('پس گرفتن رأی', () => vote(m, [])));
  if (results && st.public && total) foot.appendChild(link('رأی‌دهندگان', () => voters(m, d)));
  if (!closed && (st?.close_at || d.close_at)) foot.appendChild(el('span', '', 'تا ' + timeFmt.format(new Date(st?.close_at || d.close_at))));
  box.appendChild(foot);
  return box;
}

function link(label, run) { const b = el('button', 'x-link', label); b.type = 'button'; b.addEventListener('click', e => { e.stopPropagation(); run(); }); return b; }

async function vote(m, options) {
  const d = m.extra;
  try {
    const r = await S.net.request('vote', { chat: m.chat, poll: d.id, options });
    states.set(d.id, r.poll); picked.delete(d.id);
    if (navigator.vibrate) try { navigator.vibrate(10); } catch { }
  } catch (e) { fail(e); }
  hooks.repaint && hooks.repaint(m);
}

async function voters(m, d) {
  let j; try { j = await api(`/api/chat/polls/${d.id}/voters?chat=${m.chat}`); } catch (e) { fail(e); return; }
  const p = openPanel(), list = el('div');
  d.options.forEach((text, i) => {
    const who = (j.voters || {})[String(i)] || [];
    if (!who.length) return;
    const h = el('div', 'x-pack-title', text); h.dir = 'auto'; list.appendChild(h);
    for (const v of who) list.appendChild(row(hooks.avatar({ title: v.name, peer: { id: v.id } }, 34), v.name, () => { p.close(); hooks.start && hooks.start(v.id); }));
  });
  p.push('رأی‌دهندگان', list);
}

// pollMenu adds "close the poll" for those who may.
export function pollMenu(m, chat, items) {
  if (m.kind !== 'poll' || !m.extra) return;
  const st = states.get(m.extra.id);
  if (st && st.may_close && !st.closed) items.push({ icon: '<rect x="5" y="11" width="14" height="10" rx="2"/><path d="M8 11V7a4 4 0 0 1 8 0v4"/>', label: 'بستن نظرسنجی', run: async () => {
    try { await S.net.request('close_poll', { chat: m.chat, poll: m.extra.id }); states.delete(m.extra.id); load(m); } catch (e) { fail(e); }
  } });
}

// ------------------------------------------------------------ the maker
const CLOSE = [[0, 'بدون زمان'], [300, '۵ دقیقه'], [3600, '۱ ساعت'], [86400, '۱ روز'], [604800, '۱ هفته']];

export function openPollMaker(chat) {
  const p = openPanel(), f = el('form', 'x-form');
  const q = Object.assign(el('input', 'input'), { maxLength: 300, placeholder: 'پرسش', dir: 'auto', required: true });
  q.setAttribute('aria-label', 'پرسش');
  const opts = el('div', 'x-form');
  const addOpt = (focus) => {
    if (opts.children.length >= 10) return;
    const r = el('div', 'x-optrow');
    const i = Object.assign(el('input', 'input'), { maxLength: 100, placeholder: 'گزینهٔ ' + fa(opts.children.length + 1), dir: 'auto' });
    i.setAttribute('aria-label', i.placeholder);
    const right = Object.assign(el('input'), { type: 'radio', name: 'x-right', hidden: true }); right.setAttribute('aria-label', 'پاسخ درست');
    const x = el('button', 'm-icon-btn'); x.type = 'button'; x.setAttribute('aria-label', 'حذف گزینه'); x.innerHTML = svg('<path d="M18 6L6 18M6 6l12 12"/>');
    x.addEventListener('click', () => { if (opts.children.length > 2) { r.remove(); renumber(); } });
    i.addEventListener('input', () => { if (i.value && r === opts.lastElementChild) addOpt(false); });
    r.append(right, i, x); opts.appendChild(r);
    if (!reduced()) r.animate([{ opacity: 0, transform: 'translateY(-6px)' }, { opacity: 1, transform: 'none' }], { duration: 200, easing: SPRING });
    if (focus) i.focus();
    syncQuiz();
  };
  const renumber = () => [...opts.querySelectorAll('input.input')].forEach((i, n) => { i.placeholder = 'گزینهٔ ' + fa(n + 1); });
  const check = (label, on) => { const l = el('label', 'x-check'), c = Object.assign(el('input'), { type: 'checkbox', checked: on }); l.append(c, el('span', '', label)); return [l, c]; };
  const [anonL, anon] = check('ناشناس', true), [multiL, multi] = check('چند پاسخ', false), [quizL, quiz] = check('آزمون (یک پاسخ درست)', false);
  const explain = Object.assign(el('textarea', 'input'), { rows: 2, maxLength: 200, placeholder: 'توضیح پاسخ درست (اختیاری)', hidden: true, dir: 'auto' });
  const closeSel = el('select', 'input'); closeSel.setAttribute('aria-label', 'زمان بسته‌شدن');
  for (const [v, t] of CLOSE) { const o = el('option', '', t === 'بدون زمان' ? 'بسته‌شدن خودکار: ندارد' : 'بسته‌شدن پس از ' + t); o.value = v; closeSel.appendChild(o); }
  function syncQuiz() {
    for (const r of opts.querySelectorAll('input[type=radio]')) r.hidden = !quiz.checked;
    explain.hidden = !quiz.checked; multi.disabled = quiz.checked; if (quiz.checked) multi.checked = false;
  }
  quiz.addEventListener('change', syncQuiz);
  const send = el('button', 'btn btn-primary', 'ارسال نظرسنجی'); send.type = 'submit';
  f.append(q, el('div', 'x-pack-title', 'گزینه‌ها'), opts, anonL, multiL, quizL, explain, closeSel, send);
  addOpt(false); addOpt(false);
  f.addEventListener('submit', async e => {
    e.preventDefault();
    const rows = [...opts.children].map(r => ({ text: r.querySelector('input.input').value.trim(), right: r.querySelector('input[type=radio]').checked })).filter(o => o.text);
    if (!q.value.trim() || rows.length < 2) { toast('یک پرسش و دست‌کم دو گزینه بنویسید.'); return; }
    const correct = rows.findIndex(o => o.right);
    if (quiz.checked && correct < 0) { toast('پاسخ درست را انتخاب کنید.'); return; }
    send.disabled = true;
    try {
      const r = await S.net.request('send_poll', {
        chat: chat.id, cid: uuidv7(), topic: chat.forum ? (S.topic || 0) : 0, question: q.value.trim(), options: rows.map(o => o.text),
        anonymous: anon.checked, multiple: multi.checked, quiz: quiz.checked, correct: Math.max(0, correct), explanation: quiz.checked ? explain.value.trim() : '',
        close_in: Number(closeSel.value) });
      p.close();
      for (const m of r.messages || []) hooks.accept(m);
    } catch (err) { send.disabled = false; fail(err); }
  });
  p.push('نظرسنجی تازه', f);
  queueMicrotask(() => q.focus());
}
