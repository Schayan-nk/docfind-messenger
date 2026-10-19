// chatsched.js: the send button's menu (SPEC 6.3, 8.5): a long press or a
// right click offers "send silently", "schedule" and, in a private chat,
// "send when online"; and the list of the chat's scheduled messages, behind
// a clock button in the message bar that pulses once a minute.
import { uuidv7, reduced } from '/chatcore.js';
import { el, fa, svg, S, hooks, toast } from '/chatstate.js';
import { parseComposer } from '/chatfmt.js';
import { openMenu } from '/chatmenu.js';
import { openPanel, api } from '/chatgroup.js';

const CLOCK = '<circle cx="12" cy="13" r="8"/><path d="M12 9v4l3 2M9 2h6"/>';
const fail = e => toast((e && e.text) || 'انجام نشد؛ دوباره تلاش کنید.');
const whenFmt = new Intl.DateTimeFormat('fa-IR-u-ca-persian', { weekday: 'long', day: 'numeric', month: 'long', hour: '2-digit', minute: '2-digit' });
const counts = new Map(); // chat -> waiting messages of mine

// take reads the message bar and empties it, as a send does.
function take(chat, ta) {
  const { text, entities } = parseComposer(ta.value, S.mentions);
  if (!text) return null;
  ta.value = ''; S.mentions = new Map(); chat.draft = '';
  ta.dispatchEvent(new Event('input')); // the bar shrinks and the empty draft is saved
  return { text, entities, topic: chat.forum ? (S.topic || 0) : 0 };
}

// wireSendMenu adds the menu to a freshly built message bar.
export function wireSendMenu(form, chat) {
  const btn = form.querySelector('.m-send'), ta = form.querySelector('textarea');
  if (!btn || !ta || btn.dataset.xmenu) return;
  btn.dataset.xmenu = '1';
  let timer, swallow = false;
  const show = () => {
    if (!ta.value.trim() || S.draft.mode === 'edit') return;
    swallow = true;
    const r = btn.getBoundingClientRect();
    const items = [
      { icon: '<path d="M11 5L6 9H2v6h4l5 4V5z"/><path d="M23 9l-6 6M17 9l6 6"/>', label: 'ارسال بی‌صدا', run: () => silent(chat, ta) },
      { icon: CLOCK, label: 'زمان‌بندی پیام', run: () => schedule(chat, ta) },
    ];
    if (chat.type === 'private') items.push({ icon: '<circle cx="12" cy="8" r="4"/><path d="M4 21a8 8 0 0 1 16 0"/>', label: 'ارسال وقتی آنلاین شد', run: () => whenOnline(chat, ta) });
    openMenu({ x: r.left, y: r.top, items });
  };
  btn.addEventListener('contextmenu', e => { if (ta.value.trim()) { e.preventDefault(); show(); } });
  btn.addEventListener('pointerdown', e => { if (e.button === 0 && ta.value.trim()) { clearTimeout(timer); timer = setTimeout(show, 450); } });
  for (const ev of ['pointerup', 'pointerleave', 'pointercancel']) btn.addEventListener(ev, () => clearTimeout(timer));
  // The release that ends a long press is not also a send.
  btn.addEventListener('click', e => { if (swallow) { swallow = false; e.preventDefault(); e.stopImmediatePropagation(); } }, true);
}

async function silent(chat, ta) {
  const t = take(chat, ta); if (!t) return;
  try {
    const r = await S.net.request('send', { chat: chat.id, cid: uuidv7(), text: t.text, entities: t.entities || [], topic: t.topic, silent: true });
    for (const m of r.messages || []) hooks.accept(m);
  } catch (e) { ta.value = t.text; fail(e); }
}

async function whenOnline(chat, ta) {
  const t = take(chat, ta); if (!t) return;
  try {
    await S.net.request('schedule', { chat: chat.id, cid: uuidv7(), text: t.text, entities: t.entities || [], topic: t.topic, when_online: true });
    toast('پیام وقتی ' + (chat.title || 'او') + ' آنلاین شود فرستاده می‌شود.');
  } catch (e) { ta.value = t.text; fail(e); }
}

const pad = n => String(n).padStart(2, '0');
const localInput = d => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;

// schedule asks when, with a few ready answers and a free one.
function schedule(chat, ta) {
  const back = el('div', 'm-ov m-modal-back'), card = el('form', 'm-modal');
  card.setAttribute('role', 'dialog'); card.setAttribute('aria-modal', 'true'); card.setAttribute('aria-label', 'زمان‌بندی پیام');
  const at = (h, m, addDays = 0) => { const d = new Date(); d.setDate(d.getDate() + addDays); d.setHours(h, m, 0, 0); return d; };
  const soon = new Date(Date.now() + 3600e3);
  const tonight = at(20, 0) > new Date(Date.now() + 120e3) ? at(20, 0) : at(20, 0, 1);
  const presets = [['یک ساعت دیگر', soon], ['امشب ساعت ۲۰', tonight], ['فردا ساعت ۹', at(9, 0, 1)]];
  const input = Object.assign(el('input', 'input'), { type: 'datetime-local', value: localInput(soon), min: localInput(new Date(Date.now() + 60e3)) });
  input.setAttribute('aria-label', 'تاریخ و ساعت ارسال');
  const shown = el('div', 'x-when'); shown.setAttribute('aria-live', 'polite');
  const paint = () => { const d = new Date(input.value); shown.textContent = isNaN(d) ? '' : 'ارسال در ' + whenFmt.format(d); };
  const row = el('div', 'x-presets');
  for (const [label, d] of presets) {
    const c = el('button', 'x-chip', label); c.type = 'button';
    c.addEventListener('click', () => { input.value = localInput(d); paint(); row.querySelectorAll('.x-chip').forEach(x => x.setAttribute('aria-pressed', String(x === c))); });
    row.appendChild(c);
  }
  input.addEventListener('input', () => { paint(); row.querySelectorAll('.x-chip').forEach(x => x.setAttribute('aria-pressed', 'false')); });
  const acts = el('div', 'm-modal-actions');
  const no = el('button', 'btn', 'انصراف'); no.type = 'button';
  const yes = el('button', 'btn btn-primary', 'زمان‌بندی'); yes.type = 'submit';
  acts.append(no, yes);
  card.append(el('h2', 'm-modal-title', 'زمان‌بندی پیام'), row, input, shown, acts);
  back.appendChild(card); document.body.appendChild(back); paint();
  if (!reduced()) card.animate([{ transform: 'scale(.9)', opacity: 0 }, { transform: 'none', opacity: 1 }], { duration: 140, easing: 'cubic-bezier(0.2,0,0,1)' });
  const close = () => { back.remove(); removeEventListener('keydown', esc, true); ta.focus(); };
  const esc = e => { if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); close(); } };
  addEventListener('keydown', esc, true);
  no.addEventListener('click', close);
  back.addEventListener('click', e => { if (e.target === back) close(); });
  queueMicrotask(() => yes.focus());
  card.addEventListener('submit', async e => {
    e.preventDefault();
    const d = new Date(input.value);
    if (isNaN(d) || d < new Date(Date.now() + 55e3)) { toast('زمانی دست‌کم یک دقیقه بعد انتخاب کنید.'); return; }
    const t = take(chat, ta); if (!t) { close(); return; }
    try {
      await S.net.request('schedule', { chat: chat.id, cid: uuidv7(), text: t.text, entities: t.entities || [], topic: t.topic, at: d.toISOString() });
      close(); toast('پیام برای ' + whenFmt.format(d) + ' زمان‌بندی شد.');
    } catch (err) { ta.value = t.text; fail(err); }
  });
}

// ------------------------------------------------------------ the list
// paintScheduled shows (or hides) the clock button of the open chat.
export function paintScheduled(chatID, count) {
  if (count != null) counts.set(chatID, count);
  if (S.active !== chatID) return;
  const form = document.getElementById('m-compose'); if (!form) return;
  let b = form.querySelector('.x-sched-btn');
  const n = counts.get(chatID) || 0;
  if (!n) { if (b) b.remove(); return; }
  if (!b) {
    b = el('button', 'm-icon-btn x-sched-btn'); b.type = 'button';
    b.innerHTML = svg(CLOCK); b.appendChild(el('span', 'm-badge on'));
    b.addEventListener('click', () => openList(S.chats.get(chatID)));
    form.querySelector('textarea')?.before(b);
  }
  b.querySelector('.m-badge').textContent = fa(n);
  b.setAttribute('aria-label', `${fa(n)} پیام زمان‌بندی‌شده`);
}

export async function loadScheduled(chat) {
  try { const j = await api(`/api/chat/chats/${chat.id}/scheduled`); paintScheduled(chat.id, (j.scheduled || []).length); } catch { }
}

async function openList(chat) {
  if (!chat) return;
  const p = openPanel(), box = el('div');
  const paint = async () => {
    let j; try { j = await api(`/api/chat/chats/${chat.id}/scheduled`); } catch (e) { fail(e); return; }
    const list = j.scheduled || [];
    paintScheduled(chat.id, list.length);
    if (!list.length) { box.replaceChildren(el('div', 'x-empty', 'پیام زمان‌بندی‌شده‌ای نمانده است.')); return; }
    box.replaceChildren(...list.map(it => {
      const r = el('div', 'x-sched-item');
      r.appendChild(el('div', 'x-sched-at', it.when_online ? 'وقتی آنلاین شود' : whenFmt.format(new Date(it.at))));
      const t = el('div', 'x-sched-text', it.text); t.dir = 'auto'; r.appendChild(t);
      const acts = el('div', 'x-sched-acts');
      const act = (label, now) => { const b = el('button', 'x-link', label); b.type = 'button'; b.addEventListener('click', async () => {
        try { await S.net.request(now ? 'schedule_now' : 'schedule_cancel', { chat: chat.id, id: it.id }); paint(); } catch (e) { fail(e); }
      }); return b; };
      acts.append(act('ارسال اکنون', true), act('حذف', false));
      r.appendChild(acts);
      return r;
    }));
  };
  p.push('پیام‌های زمان‌بندی‌شده', box, { refresh: paint });
  paint();
}
