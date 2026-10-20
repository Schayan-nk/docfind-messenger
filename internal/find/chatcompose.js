// chatcompose.js: the message bar (docs/chat/SPEC.md 6.3, 8.5): the reply
// and edit strip above it, formatting from the selection toolbar or the
// keyboard, @ suggestions, sending and editing, the offline queue, and the
// selection toolbar that takes the bar's place while messages are selected.
import { uuidv7, fly, reduced } from '/chatcore.js';
import { $, el, fa, svg, S, hooks, nameOf, toast } from '/chatstate.js';
import { parseComposer, toComposer, wrapSelection, plain } from '/chatfmt.js';
import { renderMessages, repaint, doForward, doDelete, endSelection, selectionText, findMsg } from '/chatpane.js';

let typingSent = 0, draftTimer;
const LINES = 8;

export function composer(chat) {
  const foot = el('div', 'm-foot'); foot.id = 'm-foot';
  const bar = el('div', 'm-bar'); bar.id = 'm-bar'; bar.hidden = true;
  const sugg = el('div', 'm-suggest'); sugg.id = 'm-suggest'; sugg.hidden = true; sugg.setAttribute('role', 'listbox'); sugg.setAttribute('aria-label', 'پیشنهاد نام');
  const fmt = formatBar(); fmt.hidden = true;
  const f = el('form', 'm-compose'); f.id = 'm-compose';
  const ta = el('textarea'); ta.id = 'm-input'; ta.rows = 1; ta.placeholder = 'پیام'; ta.dir = 'auto';
  ta.setAttribute('aria-label', 'متن پیام'); ta.value = chat.draft || '';
  const btn = el('button', 'm-send'); btn.type = 'submit'; btn.setAttribute('aria-label', 'ارسال');
  btn.innerHTML = svg('<rect x="9" y="2" width="6" height="12" rx="3"/><path d="M5 10a7 7 0 0 0 14 0M12 19v3"/>', 'i-mic') +
    svg('<rect x="3" y="6" width="13" height="12" rx="3"/><path d="M16 10l5-3v10l-5-3"/>', 'i-cam') +
    '<svg class="i-send" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M3 20.5l18-8.5L3 3.5l.01 6.6L15 12 3.01 13.9z" transform="scale(-1,1) translate(-24,0)"/></svg>';
  // Empty bar: hold to record a voice (or round video) message, tap to switch.
  let recMode = 'voice';
  const setMode = mode => { recMode = mode; btn.classList.toggle('cam', mode === 'round'); btn.setAttribute('aria-label', mode === 'round' ? 'نگه دارید تا پیام ویدیویی ضبط شود' : 'نگه دارید تا پیام صوتی ضبط شود'); };
  btn.addEventListener('pointerdown', e => {
    if (ta.value.trim() || S.draft.mode === 'edit' || e.button !== 0) return;
    e.preventDefault();
    import('/chatrec.js').then(r => r.begin(e, btn, chat, recMode, setMode)).catch(() => toast('ضبط در این مرورگر ممکن نیست.'));
  });
  btn.addEventListener('click', e => { if (!ta.value.trim() && S.draft.mode !== 'edit') e.preventDefault(); });
  // iPhone starts its text selection from the touch itself, which the
  // pointerdown above cannot stop; in record mode the touch is ours. With
  // words in the bar it is left alone, so a tap still sends.
  btn.addEventListener('touchstart', e => { if (!ta.value.trim() && S.draft.mode !== 'edit') e.preventDefault(); }, { passive: false });
  const clip = el('button', 'm-icon-btn m-attach'); clip.type = 'button'; clip.setAttribute('aria-label', 'پیوست'); clip.title = 'پیوست';
  clip.innerHTML = svg('<path d="M21 12.5l-8.5 8.5a5 5 0 0 1-7-7l9-9a3.5 3.5 0 0 1 5 5l-9 9a2 2 0 0 1-3-3l8-8"/>');
  clip.addEventListener('click', () => hooks.openAttach && hooks.openAttach(chat));
  const grow = () => {
    ta.style.height = 'auto';
    ta.style.height = Math.min(ta.scrollHeight, 1.6 * 16 * LINES + 20) + 'px';
    btn.classList.toggle('ready', ta.value.trim() !== '' || S.draft.mode === 'edit');
  };
  ta.addEventListener('input', () => {
    grow(); suggest(chat, ta);
    if (Date.now() - typingSent > 3000 && ta.value) { typingSent = Date.now(); S.net.send('typing', { chat: chat.id, action: 'typing' }); }
    if (S.draft.mode === 'edit') return; // an edit is not a draft
    clearTimeout(draftTimer);
    draftTimer = setTimeout(() => { chat.draft = ta.value; S.net.send('draft', { chat: chat.id, text: ta.value }); S.cache.putChats([chat]); }, 800);
  });
  const showFmt = () => { fmt.hidden = ta.selectionStart === ta.selectionEnd || document.activeElement !== ta; };
  ta.addEventListener('select', showFmt); ta.addEventListener('mouseup', showFmt); ta.addEventListener('keyup', showFmt);
  ta.addEventListener('blur', () => setTimeout(() => { if (!fmt.contains(document.activeElement)) fmt.hidden = true; }, 150));
  ta.addEventListener('keydown', e => keys(e, ta, f, chat));
  f.addEventListener('submit', e => { e.preventDefault(); submit(chat, ta); grow(); });
  f.append(clip, ta, btn);
  const selbar = el('div', 'm-selbar'); selbar.id = 'm-selbar'; selbar.hidden = true;
  foot.append(bar, sugg, fmt, f, selbar);
  queueMicrotask(() => { grow(); paintBar(); paintFoot(); hooks.paintGate && hooks.paintGate(); if (matchMedia('(min-width: 901px)').matches) ta.focus(); });
  foot.grow = grow;
  return foot;
}

// ---------------------------------------------------- reply / edit strip
export function setDraft(mode, m, quote) {
  const ta = $('m-input'); if (!ta) return;
  if (S.draft.mode === 'edit' && mode !== 'edit') ta.value = S.draft.saved || '';
  if (mode === 'edit') {
    const saved = S.draft.mode === 'edit' ? S.draft.saved : ta.value;
    S.mentions = new Map();
    ta.value = toComposer(m.text || '', m.entities, S.mentions);
    S.draft = { mode, msg: m, quote: null, saved };
  } else S.draft = { mode, msg: m, quote: quote || null, saved: '' };
  paintBar(true);
  $('m-foot')?.grow();
  ta.focus(); ta.selectionStart = ta.selectionEnd = ta.value.length;
}
hooks.setDraft = setDraft;
export function clearDraft() {
  const ta = $('m-input');
  if (S.draft.mode === 'edit' && ta) ta.value = S.draft.saved || '';
  S.draft = { mode: '', msg: null, quote: null, saved: '' };
  paintBar(); $('m-foot')?.grow();
}
function paintBar(animate) {
  const bar = $('m-bar'); if (!bar) return;
  const d = S.draft;
  if (!d.mode) { bar.hidden = true; bar.replaceChildren(); return; }
  const chat = S.chats.get(S.active);
  const icon = el('span', 'm-bar-icon'); icon.innerHTML = svg(d.mode === 'edit' ? '<path d="M12 20h9"/><path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4Z"/>' : '<path d="M9 17l-5-5 5-5"/><path d="M4 12h11a5 5 0 0 1 5 5v2"/>');
  const body = el('button', 'm-bar-body'); body.type = 'button';
  const title = el('b', '', d.mode === 'edit' ? 'ویرایش پیام' : 'پاسخ به ' + nameOf(d.msg.from, chat)); title.dir = 'auto';
  const text = el('span', '', d.quote ? '«' + d.quote.text + '»' : plain(d.msg.text) || 'پیام'); text.dir = 'auto';
  body.append(title, text);
  body.addEventListener('click', () => hooks.jumpTo && d.msg.pts && hooks.jumpTo(d.msg.pts));
  const x = el('button', 'm-icon-btn m-bar-x'); x.type = 'button'; x.setAttribute('aria-label', d.mode === 'edit' ? 'لغو ویرایش' : 'لغو پاسخ');
  x.innerHTML = svg('<path d="M18 6L6 18M6 6l12 12"/>');
  x.addEventListener('click', () => { clearDraft(); $('m-input')?.focus(); });
  const was = !bar.hidden;
  bar.replaceChildren(icon, body, x); bar.hidden = false;
  if (animate && !was && !reduced()) bar.animate([{ transform: 'translateY(8px)', opacity: 0 }, { transform: 'none', opacity: 1 }], { duration: 240, easing: 'cubic-bezier(0.2,0,0,1)' });
}

// ----------------------------------------------------------- formatting
const FMT = [
  ['**', '**', 'پررنگ', '<path d="M7 5h6a3.5 3.5 0 0 1 0 7H7zM7 12h7a3.5 3.5 0 0 1 0 7H7z"/>', 'b'],
  ['__', '__', 'کج', '<path d="M19 4h-9M14 20H5M15 4L9 20"/>', 'i'],
  ['++', '++', 'زیرخط', '<path d="M6 4v6a6 6 0 0 0 12 0V4M4 20h16"/>', 'u'],
  ['~~', '~~', 'خط‌خورده', '<path d="M16 5H9.5a3.5 3.5 0 0 0 0 7h5a3.5 3.5 0 0 1 0 7H7M4 12h16"/>', 'X'],
  ['`', '`', 'کد', '<path d="M8 6l-6 6 6 6M16 6l6 6-6 6"/>', 'M'],
  ['||', '||', 'پنهان', '<path d="M3 3l18 18M10.6 10.6a2 2 0 0 0 2.8 2.8M9.9 4.2A10 10 0 0 1 22 12a14 14 0 0 1-3 3.6M6.6 6.6A14 14 0 0 0 2 12s3.6 7 10 7a9.7 9.7 0 0 0 4.4-1"/>', 'P']
];
function formatBar() {
  const box = el('div', 'm-fmt'); box.id = 'm-fmt'; box.setAttribute('role', 'toolbar'); box.setAttribute('aria-label', 'قالب‌بندی');
  for (const [open, close, label, icon, key] of FMT) {
    const b = el('button', 'm-fmt-btn'); b.type = 'button'; b.title = label + ' (Ctrl+' + (key === key.toUpperCase() ? 'Shift+' : '') + key.toUpperCase() + ')';
    b.setAttribute('aria-label', label); b.innerHTML = svg(icon);
    b.addEventListener('mousedown', e => e.preventDefault());
    b.addEventListener('click', () => wrapSelection($('m-input'), open, close));
    box.appendChild(b);
  }
  const q = el('button', 'm-fmt-btn'); q.type = 'button'; q.title = 'نقل قول'; q.setAttribute('aria-label', 'نقل قول');
  q.innerHTML = svg('<path d="M7 7h4v4H7zM13 7h4v4h-4z"/><path d="M7 11c0 3-1 5-3 6M13 11c0 3-1 5-3 6"/>');
  q.addEventListener('mousedown', e => e.preventDefault());
  q.addEventListener('click', () => quoteLines($('m-input')));
  const link = el('button', 'm-fmt-btn'); link.type = 'button'; link.title = 'پیوند (Ctrl+K)'; link.setAttribute('aria-label', 'پیوند');
  link.innerHTML = svg('<path d="M10 13a5 5 0 0 0 7.5.5l3-3a5 5 0 0 0-7-7l-1.7 1.7"/><path d="M14 11a5 5 0 0 0-7.5-.5l-3 3a5 5 0 0 0 7 7l1.7-1.7"/>');
  link.addEventListener('mousedown', e => e.preventDefault());
  link.addEventListener('click', () => askLink(box));
  box.append(q, link);
  return box;
}
function quoteLines(ta) {
  const v = ta.value; let a = ta.selectionStart, b = ta.selectionEnd;
  a = v.lastIndexOf('\n', a - 1) + 1;
  const lines = v.slice(a, b).split('\n').map(l => l.startsWith('> ') ? l : '> ' + l).join('\n');
  ta.setRangeText(lines, a, b, 'select');
  ta.dispatchEvent(new Event('input', { bubbles: true })); ta.focus();
}
// askLink turns the toolbar into an address field for [words](address).
function askLink(box) {
  const ta = $('m-input'); const a = ta.selectionStart, b = ta.selectionEnd;
  const keep = [...box.children];
  const inp = el('input', 'input m-fmt-url'); inp.type = 'url'; inp.dir = 'ltr'; inp.placeholder = 'https://'; inp.setAttribute('aria-label', 'نشانی پیوند'); inp.id = 'm-fmt-url';
  const ok = el('button', 'btn btn-primary', 'افزودن'); ok.type = 'button';
  const done = (add) => {
    box.replaceChildren(...keep);
    ta.focus(); ta.setSelectionRange(a, b);
    if (add && inp.value.trim()) wrapSelection(ta, '[', '](' + inp.value.trim().replace(/[()\s]/g, encodeURIComponent) + ')');
  };
  ok.addEventListener('click', () => done(true));
  inp.addEventListener('keydown', e => { if (e.key === 'Enter') { e.preventDefault(); done(true); } if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); done(false); } });
  box.replaceChildren(inp, ok); box.hidden = false; inp.focus();
}

function keys(e, ta, f, chat) {
  const sg = $('m-suggest');
  if (sg && !sg.hidden && ['ArrowDown', 'ArrowUp', 'Enter', 'Tab', 'Escape'].includes(e.key)) { suggestKeys(e, ta); return; }
  const mod = e.ctrlKey || e.metaKey;
  if (mod) {
    const k = e.key.toLowerCase();
    const hit = FMT.find(([, , , , key]) => key.toLowerCase() === k && (key === key.toUpperCase()) === e.shiftKey);
    if (hit) { e.preventDefault(); wrapSelection(ta, hit[0], hit[1]); return; }
    if (k === 'k') { e.preventDefault(); const fb = $('m-fmt'); if (fb) askLink(fb); return; }
    if (e.shiftKey && (e.key === '>' || e.key === '.')) { e.preventDefault(); quoteLines(ta); return; }
  }
  if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) { e.preventDefault(); f.requestSubmit(); return; }
  if (e.key === 'Escape') {
    if (S.draft.mode) { e.preventDefault(); clearDraft(); }
    return;
  }
  // Up in an empty bar edits your last message, as in Telegram Desktop.
  if (e.key === 'ArrowUp' && !ta.value && !S.draft.mode) {
    const last = [...(S.msgs.get(chat.id) || [])].reverse().find(m => m.from === S.me && m.id && !m.fwd);
    if (last) { e.preventDefault(); setDraft('edit', last); }
  }
}

// --------------------------------------------------------- @ suggestions
// Only the people who are in this chat: a mention of anyone else would
// notify nobody (the server resolves mentions to members only).
function members(chat) {
  if (hooks.members) return hooks.members(chat);
  return chat.type === 'private' && chat.peer ? [{ id: chat.peer.id, name: chat.peer.name || chat.title }] : [];
}
function suggest(chat, ta) {
  const sg = $('m-suggest'); if (!sg) return;
  const before = ta.value.slice(0, ta.selectionStart);
  const m = before.match(/(^|\s)@([^\s@]{0,32})$/);
  const list = m ? members(chat).filter(p => !m[2] || (p.name || '').includes(m[2]) || p.id.toLowerCase().includes(m[2].toLowerCase())).slice(0, 6) : [];
  if (!list.length) { sg.hidden = true; return; }
  sg.replaceChildren(...list.map((p, i) => {
    const o = el('button', 'm-suggest-row' + (i === 0 ? ' on' : '')); o.type = 'button'; o.setAttribute('role', 'option');
    o.append(hooks.avatar({ title: p.name, peer: { id: p.id } }, 30), Object.assign(el('span', '', p.name), { dir: 'auto' }));
    o.addEventListener('mousedown', e => e.preventDefault());
    o.addEventListener('click', () => pick(ta, p, m[2].length));
    o.person = p;
    return o;
  }));
  const was = !sg.hidden; sg.hidden = false; sg.tokenLen = m[2].length;
  if (!was && !reduced()) sg.animate([{ transform: 'translateY(8px)', opacity: 0 }, { transform: 'none', opacity: 1 }], { duration: 200, easing: 'cubic-bezier(0.2,0,0,1)' });
}
function suggestKeys(e, ta) {
  const sg = $('m-suggest'); const rows = [...sg.children]; let i = rows.findIndex(r => r.classList.contains('on'));
  if (e.key === 'Escape') { e.preventDefault(); sg.hidden = true; return; }
  if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
    e.preventDefault(); rows[i]?.classList.remove('on');
    i = (i + (e.key === 'ArrowDown' ? 1 : -1) + rows.length) % rows.length; rows[i].classList.add('on'); return;
  }
  e.preventDefault(); pick(ta, rows[Math.max(0, i)].person, sg.tokenLen);
}
function pick(ta, p, tokenLen) {
  const at = ta.selectionStart - tokenLen - 1;
  ta.setRangeText('@' + p.name + ' ', at, ta.selectionStart, 'end');
  S.mentions.set(p.name, p.id);
  $('m-suggest').hidden = true;
  ta.dispatchEvent(new Event('input', { bubbles: true })); ta.focus();
}

// ------------------------------------------------------------ send / edit
const request = (t, d) => S.net.request(t, d);
function submit(chat, ta) {
  const { text, entities } = parseComposer(ta.value, S.mentions);
  const d = S.draft;
  if (d.mode === 'edit') {
    const m = d.msg;
    if (!text) { toast('پیام خالی است.'); return; }
    const before = { text: m.text, entities: m.entities, edited: m.edited };
    Object.assign(m, { text, entities, edited: new Date().toISOString() });
    repaint(m);
    ta.value = ''; S.mentions = new Map(); clearDraft();
    request('edit', { chat: chat.id, id: m.id, text, entities }).catch(e => { Object.assign(m, before); repaint(m); toast((e && e.text) || 'ویرایش انجام نشد.'); });
    return;
  }
  if (!text) return;
  const from = ta.getBoundingClientRect();
  ta.value = ''; S.mentions = new Map(); chat.draft = ''; S.net.send('draft', { chat: chat.id, text: '' });
  const reply = d.mode === 'reply' ? d : null;
  clearDraft();
  sendText(chat, text, entities, from, reply);
}
async function sendText(chat, text, entities, fromRect, reply) {
  const m = { cid: uuidv7(), chat: chat.id, from: S.me, text, entities, at: new Date().toISOString(), pts: 0, topic: chat.forum ? (S.topic || 0) : 0 };
  if (reply && reply.msg) {
    m.reply_to = reply.msg.id;
    m.reply = { id: reply.msg.id, pts: reply.msg.pts, from: reply.msg.from, text: plain(reply.msg.text).slice(0, 200) };
    if (reply.quote) m.quote = { text: reply.quote.text, offset: reply.quote.offset };
  }
  const list = S.msgs.get(chat.id) || []; list.push(m); S.msgs.set(chat.id, list);
  S.atBottom = true;
  renderMessages({ bottom: true });
  const node = $('m-msgs')?.querySelector(`[data-key="${m.cid}"]`); if (node) fly(fromRect, node);
  chat.last = { text, from: S.me, at: m.at }; hooks.renderList();
  await S.cache.put('outbox', outboxRow(m));
  deliver(m);
}
const outboxRow = m => ({ cid: m.cid, chat: m.chat, text: m.text, entities: m.entities || [], reply_to: m.reply_to || 0, reply: m.reply || null, quote: m.quote || null, at: m.at, topic: m.topic || 0 });
async function deliver(m) {
  try {
    const r = await request('send', { chat: m.chat, cid: m.cid, text: m.text, entities: m.entities || [], reply_to: m.reply_to || 0, quote: m.quote || null, topic: m.topic || 0 });
    S.cache.del('outbox', m.cid);
    for (const real of r.messages || []) hooks.accept(real);
  } catch (e) {
    if (e && e.code && e.code !== 'offline' && e.code !== 'timeout') { m.failed = true; S.cache.del('outbox', m.cid); renderMessages(); toast(e.text || 'پیام ارسال نشد.'); }
  }
}
export function resend(m) { m.failed = false; S.cache.put('outbox', outboxRow(m)); renderMessages(); deliver(m); }
hooks.resend = resend;
export async function flushOutbox() {
  for (const o of await S.cache.all('outbox')) {
    if (o.file) { hooks.resumeMedia && hooks.resumeMedia(o); continue; }
    const list = S.msgs.get(o.chat) || [];
    let m = list.find(x => x.cid === o.cid);
    if (!m) { m = { ...o, from: S.me, pts: 0 }; list.push(m); S.msgs.set(o.chat, list); }
    deliver(m);
  }
}

// -------------------------------------------------------- selection bar
export function paintFoot() {
  const form = $('m-compose'), sb = $('m-selbar'); if (!form || !sb) return;
  const chat = S.chats.get(S.active);
  const on = S.sel.size > 0;
  if (!on) { if (!sb.hidden) { sb.hidden = true; form.hidden = false; hooks.paintGate && hooks.paintGate(); } return; }
  const cancel = el('button', 'm-icon-btn'); cancel.type = 'button'; cancel.setAttribute('aria-label', 'لغو انتخاب');
  cancel.innerHTML = svg('<path d="M18 6L6 18M6 6l12 12"/>');
  cancel.addEventListener('click', endSelection);
  const count = el('span', 'm-sel-count', `${fa(S.sel.size)} پیام`);
  const act = (label, icon, run, cls = '') => { const b = el('button', 'btn m-sel-btn ' + cls); b.type = 'button'; b.innerHTML = svg(icon); b.appendChild(el('span', '', label)); b.addEventListener('click', run); return b; };
  const copyB = act('رونوشت', '<rect x="9" y="9" width="12" height="12" rx="2"/><path d="M5 15V5a2 2 0 0 1 2-2h10"/>', async () => { try { await navigator.clipboard.writeText(selectionText(chat)); toast('رونوشت شد.'); endSelection(); } catch { toast('رونوشت انجام نشد.'); } });
  const fwdB = act('هدایت', '<path d="M15 17l5-5-5-5"/><path d="M20 12H9a5 5 0 0 0-5 5v2"/>', () => doForward([...S.sel], chat));
  const delB = act('حذف', '<path d="M3 6h18"/><path d="M8 6V4h8v2"/><path d="M6 6l1 14h10l1-14"/>', () => doDelete([...S.sel], chat), 'm-danger');
  sb.replaceChildren(cancel, count, copyB, fwdB, delB);
  if (sb.hidden) {
    sb.hidden = false; form.hidden = true;
    if (!reduced()) sb.animate([{ transform: 'translateY(100%)', opacity: 0 }, { transform: 'none', opacity: 1 }], { duration: 240, easing: 'cubic-bezier(0.2,0,0,1)' });
  }
}
hooks.paintFoot = paintFoot;

// A tap above the message bar puts the keyboard and the sticker panel away,
// as every messenger does: on the messages, on the header, anywhere in the
// open chat but the bar itself and what it opened. A menu or dialog of its
// own (a message's menu, the photo viewer) is left to manage its taps.
document.addEventListener('pointerdown', e => {
  const t = e.target, foot = document.querySelector('#m-chat .m-foot');
  if (!foot || !(t instanceof Element) || foot.contains(t) || !t.closest('#m-chat')) return;
  if (t.closest('.m-ctx, .m-pop, .m-sheet, .m-viewer, .pe, [role="dialog"]')) return;
  const panel = foot.querySelector('.x-panel:not([hidden])'); if (panel) panel.hidden = true;
  const ta = document.getElementById('m-input');
  if (ta && document.activeElement === ta) ta.blur();
}, true);
export { findMsg };
