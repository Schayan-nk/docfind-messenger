// chatgcall.js: group voice and video calls on the messenger page
// (docs/chat/SPEC.md 6.9). Loaded with the page and small: the call buttons
// in a group's header, the «تماس گروهی · پیوستن» bar while a call goes on in
// it, the lines a call leaves in the chat, and the socket's gcall_* events.
// The engine (chatgcallrtc.js) and the screen (chatgcallui.js) load on join.
import { $, el, svg, fa, S, hooks, toast } from '/chatstate.js';
import { PHONE, VIDEO, mmss, keepAlive } from '/chatcall.js';

const engine = () => Promise.all([import('/chatgcallrtc.js'), import('/chatgcallui.js')]);
const TAB = (crypto.randomUUID && crypto.randomUUID()) || String(Math.random()).slice(2);
const GROUP = { group: 1, supergroup: 1 };
S.gcalls = new Map(); // chat -> the call going on in it {id, count, started_at, by}

// The group call on this device, if any.
export const G = { call: null, ui: null, queue: [], joining: false };
hooks.groupCall = () => !!G.call || G.joining;

async function join(chat, video) {
  if (G.call) { G.ui && G.ui.restore(); return; }
  if (G.joining) return;
  if (hooks.oneCall && hooks.oneCall()) { toast('شما در تماس دیگری هستید؛ نخست آن را پایان دهید.'); return; }
  if (!S.net.up) { toast('اتصال برقرار نیست؛ کمی بعد دوباره تلاش کنید.'); return; }
  G.joining = true;
  let GroupCall, UI;
  try { [{ GroupCall }, UI] = await engine(); } finally { G.joining = false; }
  const call = G.call = new GroupCall({ id: 0, chat: chat.id }, [], video);
  call.addEventListener('give-up', () => { if (G.call === call) { toast('ارتباط با تماس گروهی قطع شد.'); leave(); } });
  G.ui = UI.open(call, chat, { leave, end, mute });
  try { await call.media(); }
  catch (e) {
    if (call.closed) return;
    G.ui.fail(e && e.name === 'NotAllowedError' ? 'اجازهٔ دسترسی به میکروفون داده نشد.' : 'میکروفون در دسترس نیست.'); finish(); return;
  }
  if (call.closed) return;
  G.ui.local();
  keepAlive(true, call.cam);
  let ack;
  try { ack = await S.net.request('gcall_join', { chat: chat.id, video: call.cam, tab: TAB }); }
  catch (e) { if (!call.closed) { G.ui.fail((e && e.text) || 'به تماس گروهی وصل نشد.'); finish(); } return; }
  if (call.closed) { S.net.send('gcall_leave', { call: ack.call.id }); return; }
  Object.assign(call, { id: ack.call.id, info: ack.call, ice: ack.ice || [] });
  call.open(); call.ready = true;
  for (const d of G.queue.splice(0)) if (d.call === call.id) call.onSignal(d.d).catch(() => { });
  G.ui.state(ack.call);
}
hooks.joinGroupCall = join;

function leave() {
  const call = G.call; if (!call) return;
  if (call.id) S.net.request('gcall_leave', { call: call.id }).catch(() => { });
  G.ui && G.ui.close();
  finish();
}
function end() { if (G.call && G.call.id) S.net.request('gcall_end', { call: G.call.id }).catch(e => toast((e && e.text) || 'انجام نشد.')); }
function mute(user, on) { if (G.call && G.call.id) S.net.request('gcall_mute', { call: G.call.id, user, on }).catch(e => toast((e && e.text) || 'انجام نشد.')); }
function finish() {
  if (G.call) G.call.close();
  G.call = null; G.queue = [];
  keepAlive(false);
  paintBar(S.active);
}

// ------------------------------------------------------------ the socket
const on = (t, f) => S.net.addEventListener(t, e => f((e.detail && e.detail.d) || {}));
const mine = id => G.call && G.call.id && G.call.id === id;
on('gcall_update', d => {
  if (d.call) S.gcalls.set(d.chat, d.call); else S.gcalls.delete(d.chat);
  paintBar(d.chat);
});
on('gcall_signal', d => {
  if (!G.call) return;
  if (!G.call.ready) { G.queue.push(d); return; } // before the join's answer
  if (mine(d.call)) G.call.onSignal(d.d).catch(() => { });
});
on('gcall_state', d => { if (mine(d.id)) G.ui && G.ui.state(d); });
on('gcall_ended', d => { if (mine(d.call)) { G.ui && G.ui.ended(); finish(); } });
on('gcall_held', d => {
  if (!mine(d.call)) return;
  G.call.setHeld(!!d.on);
  toast(d.on ? 'مدیر تماس میکروفون شما را بست.' : 'مدیر تماس میکروفون شما را باز کرد.');
});
on('gcall_moved', d => { if (mine(d.call)) { toast('تماس گروهی روی دستگاه دیگرتان ادامه یافت.'); G.ui && G.ui.close(); finish(); } });
function resume() {
  const call = G.call; if (!call || !call.id) return;
  S.net.request('gcall_resume', { call: call.id, tab: TAB }).catch(e => {
    if (G.call === call && e && e.code === 'no_call') { G.ui && G.ui.ended(); finish(); }
  });
}
on('gcall_active', d => { if (mine(d.id)) resume(); });
S.net.addEventListener('state', e => { if (e.detail === 'online') resume(); });

// ------------------------------------------------------------ the chat
// Call buttons in a group's header, and the bar under it while a call goes on.
const headWas = hooks.headerTools;
hooks.headerTools = (chat, h) => {
  if (headWas) headWas(chat, h);
  if (!GROUP[chat.type]) return;
  for (const [icon, video, label] of [[VIDEO, true, 'تماس تصویری گروهی'], [PHONE, false, 'تماس صوتی گروهی']]) {
    const b = el('button', 'm-icon-btn m-call-btn'); b.type = 'button'; b.setAttribute('aria-label', label); b.title = label;
    b.innerHTML = svg(icon); b.addEventListener('click', () => join(chat, video));
    h.appendChild(b);
  }
  queueMicrotask(() => paintBar(chat.id));
};
function paintBar(chatID) {
  if (!chatID || chatID !== S.active) return;
  const head = $('m-chat') && $('m-chat').querySelector('.m-chat-head'); if (!head) return;
  let bar = $('gc-bar');
  const live = S.gcalls.get(chatID), chat = S.chats.get(chatID);
  if (!live || !chat) { if (bar) bar.remove(); return; }
  const inIt = G.call && G.call.chat === chatID;
  if (!bar) {
    bar = el('button', 'gc-bar'); bar.type = 'button'; bar.id = 'gc-bar';
    head.after(bar);
  }
  const ic = el('span', 'gc-bar-ic'); ic.innerHTML = svg(PHONE);
  const t = el('span', 'gc-bar-t', `تماس گروهی در جریان است · ${fa(live.count || 1)} نفر`);
  const go = el('span', 'gc-bar-go', inIt ? 'بازگشت' : 'پیوستن');
  bar.replaceChildren(ic, t, go);
  bar.onclick = () => (inIt ? G.ui && G.ui.restore() : join(chat, false));
}

// The lines a group call leaves: «… تماس گروهی را شروع کرد», «… پایان یافت · ۱۲:۰۵».
const payload = m => { try { return typeof m.service === 'string' ? JSON.parse(m.service || '{}') : (m.service || {}); } catch { return {}; } };
function gtext(m) {
  const p = payload(m);
  if (p.reason === 'started') return (m.from === S.me ? 'شما تماس گروهی را شروع کردید' : `${m.from_name || m.from} تماس گروهی را شروع کرد`);
  return 'تماس گروهی پایان یافت' + (p.duration > 0 ? ' · ' + mmss(p.duration) : '');
}
const textWas = hooks.serviceText;
hooks.serviceText = (m, chat) => payload(m).action === 'gcall' ? gtext(m) : (textWas ? textWas(m, chat) : '');
const lineWas = hooks.serviceLine;
hooks.serviceLine = (m, chat) => {
  if (payload(m).action !== 'gcall') return lineWas ? lineWas(m, chat) : el('div');
  const d = el('div', 'm-service gc-line'); d.dataset.key = m.cid || String(m.pts); if (m.pts) d.dataset.pts = String(m.pts);
  const s = el('span'); s.dir = 'auto';
  const ic = el('span', 'gc-line-ic'); ic.innerHTML = svg(PHONE);
  s.append(ic, document.createTextNode(gtext(m)));
  d.appendChild(s);
  return d;
};
