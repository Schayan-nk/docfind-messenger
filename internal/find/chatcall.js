// chatcall.js: calls on the messenger page (docs/chat/SPEC.md 6.8). Loaded
// with the page and small: the call buttons in a private chat's header, the
// lines calls leave in a chat, the «تماس‌ها» button, and the socket's call_*
// events. The engine (chatcallrtc.js), the screens (chatcallui.js) and the
// call list (chatcalls.js) load when first needed.
import { $, el, svg, timeFmt, S, hooks, toast } from '/chatstate.js';

export const PHONE = '<path d="M22 16.9v3a2 2 0 0 1-2.2 2 19.8 19.8 0 0 1-8.6-3.1 19.5 19.5 0 0 1-6-6A19.8 19.8 0 0 1 2.1 4.2 2 2 0 0 1 4.1 2h3a2 2 0 0 1 2 1.7c.1.9.4 1.8.7 2.7a2 2 0 0 1-.5 2.1L8 9.8a16 16 0 0 0 6 6l1.3-1.3a2 2 0 0 1 2.1-.4c.9.3 1.8.6 2.7.7a2 2 0 0 1 1.7 2z"/>';
export const VIDEO = '<path d="M23 7l-7 5 7 5V7z"/><rect x="1" y="5" width="15" height="14" rx="2"/>';
const engine = () => Promise.all([import('/chatcallrtc.js'), import('/chatcallui.js')]);
let css = null;
const style = () => css || (css = new Promise(ok => { const l = document.createElement('link'); l.rel = 'stylesheet'; l.href = '/chatcall.css'; l.onload = l.onerror = ok; document.head.appendChild(l); }));

// The call on this device, if any: { call (Call), ui (the screen) }.
export const C = { call: null, ui: null, queue: [], starting: false };
// This page instance: a reconnect with it takes the call back (callws.go).
const TAB = (crypto.randomUUID && crypto.randomUUID()) || String(Math.random()).slice(2);
const busy = () => !!C.call || C.starting;
hooks.oneCall = busy; // the group calls ask (chatgcall.js); one call at a time
const faDigits = t => t.replace(/\d/g, d => '۰۱۲۳۴۵۶۷۸۹'[d]);
const mmss = s => { s = Math.max(0, Math.round(s)); const h = Math.floor(s / 3600), m = Math.floor(s / 60) % 60, x = String(s % 60).padStart(2, '0'); return faDigits(h ? `${h}:${String(m).padStart(2, '0')}:${x}` : `${m}:${x}`); };
export { mmss };

// ------------------------------------------------------------ starting a call
async function startCall(peer, video) {
  if (busy()) { C.ui && C.ui.restore(); return; }
  if (hooks.groupCall && hooks.groupCall()) { toast('شما در تماس گروهی هستید؛ نخست از آن بیرون بیایید.'); return; }
  if (!S.net.up) { toast('اتصال برقرار نیست؛ کمی بعد دوباره تلاش کنید.'); return; }
  const chat = [...S.chats.values()].find(c => c.type === 'private' && c.peer && c.peer.id === peer);
  const info = { id: 0, outgoing: true, video, state: 'requesting', peer: { id: peer, name: (chat && (chat.peer.name || chat.title)) || peer } };
  C.starting = true; // a second press while the modules load starts nothing
  let Call, UI;
  try { [{ Call }, UI] = await engine(); await style(); } finally { C.starting = false; }
  const call = C.call = wire(new Call(info, []));
  C.ui = UI.open(call, { end });
  try { await call.media(); }
  catch (e) { if (call.closed) return; C.ui.fail(e && e.name === 'NotAllowedError' ? 'اجازهٔ دسترسی به میکروفون' + (video ? ' و دوربین' : '') + ' داده نشد.' : 'میکروفون' + (video ? ' یا دوربین' : '') + ' در دسترس نیست.'); end(); return; }
  if (call.closed) return;
  C.ui.local();
  keepAlive(true, call.cam);
  let ack;
  try { ack = await S.net.request('call_start', { peer, video, tab: TAB }); }
  catch (e) { if (!call.closed) { C.ui.fail((e && e.text) || 'تماس برقرار نشد.'); end(); } return; }
  // Put down while the server was still ringing it up.
  if (call.closed) { if (ack.call.state !== 'ended') S.net.send('call_hangup', { call: ack.call.id }); return; }
  Object.assign(call, { id: ack.call.id, info: ack.call, ice: ack.ice || [] });
  if (ack.call.state === 'ended') { C.ui.fail('همکار در تماس دیگری است.', 'busy'); end(); return; }
  call.setState('ringing');
}
hooks.startCall = startCall;

// wire: a call that has had no media for 30 s is put down.
function wire(call) {
  call.addEventListener('give-up', () => { if (C.call === call) hangup('failed'); });
  return call;
}

// keepAlive: in docfind's Android app, a call keeps its microphone and
// camera while the person is in another app (CallService, app 1.4.0 on).
export function keepAlive(on, video) {
  const app = window.DocfindAndroid;
  try {
    if (on && app && typeof app.callStart === 'function') app.callStart(!!video);
    if (!on && app && typeof app.callStop === 'function') app.callStop();
  } catch { }
}

// end lets go of this device's call (the screen closes itself).
function end() {
  if (C.call) { appRing(C.call, false); C.call.close(); }
  C.call = null; C.queue = [];
  keepAlive(false);
}

// hangup puts the call down from this device.
export async function hangup(reason) {
  const call = C.call; if (!call) return;
  if (call.id) S.net.request(call.state === 'incoming' ? 'call_decline' : 'call_hangup', { call: call.id, reason }).catch(() => { });
  const talked = call.since ? (Date.now() - call.since) / 1000 : 0;
  C.ui && C.ui.ended(call.state === 'incoming' ? 'declined' : 'ended', talked);
  end();
}

// ------------------------------------------------------------ an incoming call
async function incoming(info) {
  if (busy()) return; // this device is in a call; the server rang the others
  const [{ Call }, UI] = await engine(); await style();
  if (busy()) return;
  const call = C.call = wire(new Call(info, []));
  C.ui = UI.open(call, { end, accept, decline });
  appRing(call, true);
}
// appRing: docfind's Android app in the background shows the ringing call as
// the phone's own notification, from this socket (no push service).
function appRing(call, on) {
  const app = window.DocfindAndroid;
  if (!app || typeof app.notify !== 'function' || !call || !call.id) return;
  try {
    if (on && document.hidden) app.notify(call.info.peer.name || '', call.info.video ? 'تماس تصویری…' : 'تماس صوتی…', 'call-' + call.id, '/messages#call' + call.id, true);
    if (!on) app.unnotify('call-' + call.id);
  } catch { }
}
async function accept() {
  const call = C.call; if (!call) return;
  call.setState('connecting');
  try { await call.media(); }
  catch (e) { if (call.closed) return; C.ui.fail(e && e.name === 'NotAllowedError' ? 'اجازهٔ دسترسی به میکروفون داده نشد.' : 'میکروفون در دسترس نیست.'); hangup(); return; }
  if (call.closed) return;
  C.ui.local();
  keepAlive(true, call.cam);
  let ack;
  try { ack = await S.net.request('call_accept', { call: call.id, tab: TAB }); }
  catch (e) { C.ui.fail((e && e.text) || 'تماس دیگر برقرار نیست.'); end(); return; }
  call.ice = ack.ice || [];
  call.open();
  call.ready = true;
  for (const d of C.queue.splice(0)) call.onSignal(d);
}
async function decline(text) {
  const call = C.call; if (!call) return;
  S.net.request('call_decline', { call: call.id, text: text || '' }).catch(() => { });
  C.ui && C.ui.close();
  end();
}

// ------------------------------------------------------------ the socket
const on = (t, f) => S.net.addEventListener(t, e => f((e.detail && e.detail.d) || {}));
const mine = d => C.call && C.call.id && (d.call === C.call.id || d.id === C.call.id);
on('call_incoming', d => incoming(d));
on('call_accepted', d => {
  const call = C.call; if (!mine(d) || call.pc) return; // heard once (again after a resume)
  call.setState('connecting'); call.ready = true;
  call.open(); call.offer().catch(() => hangup('failed'));
});
on('call_signal', d => {
  if (!mine(d)) return;
  if (!C.call.ready) { C.queue.push(d.d); return; }
  C.call.onSignal(d.d).catch(() => { });
});
on('call_media', d => { if (mine(d)) C.call.onPeerMedia(d.d); });
on('call_taken', d => { if (mine(d) && C.call.state === 'incoming') { C.ui && C.ui.close(); end(); } });
on('call_back', d => { if (mine(d) && C.call.state === 'reconnecting') C.call.restart(); });
on('call_ended', d => {
  if (!mine(d)) return;
  const call = C.call;
  if (call.state === 'incoming') { C.ui && C.ui.close(); end(); return; }
  C.ui && C.ui.ended(d.reason, d.duration || 0);
  end();
});
// A socket that came back while this page was in a call takes it back
// (call_active, or on its own when the server has not noticed the drop yet).
// Another page of the same person leaves the call alone: it ends by itself if
// its page never comes back. A call that ended meanwhile ends here too.
function resume() {
  const call = C.call; if (!call || !call.id || call.state === 'incoming') return;
  S.net.request('call_resume', { call: call.id, tab: TAB }).catch(e => {
    if (C.call !== call || !e || e.code !== 'no_call') return;
    C.ui && C.ui.ended('failed', call.since ? (Date.now() - call.since) / 1000 : 0);
    end();
  });
}
on('call_active', d => { if (C.call && C.call.id === d.id) resume(); });
S.net.addEventListener('state', e => { if (e.detail === 'online') resume(); });

// ------------------------------------------------------------ the chat
// Call buttons in a private chat's header (not Saved Messages, not secret).
const headWas = hooks.headerTools;
hooks.headerTools = (chat, h) => {
  if (headWas) headWas(chat, h);
  if (chat.type !== 'private' || !chat.peer) return;
  for (const [icon, video, label] of [[VIDEO, true, 'تماس تصویری'], [PHONE, false, 'تماس صوتی']]) {
    const b = el('button', 'm-icon-btn m-call-btn'); b.type = 'button'; b.setAttribute('aria-label', label); b.title = label;
    b.innerHTML = svg(icon); b.addEventListener('click', () => startCall(chat.peer.id, video));
    h.appendChild(b);
  }
};

// The line a call leaves: «تماس صوتی خروجی · ۲:۳۱», on its caller's side.
const payload = m => { try { return typeof m.service === 'string' ? JSON.parse(m.service || '{}') : (m.service || {}); } catch { return {}; } };
export function callText(p, outgoing) {
  const kind = p.video ? 'تماس تصویری' : 'تماس صوتی';
  const missed = !outgoing && (p.reason === 'missed' || p.reason === 'cancelled' || p.reason === 'busy');
  const text = outgoing
    ? ({ missed: kind + ' بی‌پاسخ', cancelled: kind + ' لغو شد', declined: kind + ' رد شد', busy: 'همکار در تماس دیگری بود', failed: kind + ' برقرار نشد' })[p.reason] || kind + ' خروجی'
    : missed ? kind + ' از دست رفته' : ({ declined: kind + ' رد شد', failed: kind + ' برقرار نشد' })[p.reason] || kind + ' ورودی';
  return { text, missed, dur: p.duration > 0 ? mmss(p.duration) : '' };
}
const textWas = hooks.serviceText;
hooks.serviceText = (m, chat) => {
  const p = payload(m);
  if (p.action !== 'call') return textWas ? textWas(m, chat) : '';
  const t = callText(p, m.from === S.me);
  return t.text + (t.dur ? ' · ' + t.dur : '');
};
const lineWas = hooks.serviceLine;
hooks.serviceLine = (m, chat) => {
  const p = payload(m);
  if (p.action !== 'call') return lineWas ? lineWas(m, chat) : el('div');
  const out = m.from === S.me, t = callText(p, out);
  const b = el('div', 'm-bubble k-call' + (out ? ' me' : '') + (t.missed ? ' missed' : ''));
  b.dataset.key = m.cid || String(m.pts); if (m.pts) b.dataset.pts = String(m.pts);
  const ic = el('span', 'm-call-ic'); ic.innerHTML = svg(p.video ? VIDEO : PHONE);
  const arrow = el('span', 'm-call-dir' + (out ? ' out' : ' in')); arrow.innerHTML = svg(out ? '<path d="M7 17L17 7M9 7h8v8"/>' : '<path d="M17 7L7 17M15 17H7V9"/>');
  const txt = el('div', 'm-call-txt'); txt.append(el('b', '', t.text));
  const sub = el('span', 'm-call-sub'); sub.append(arrow, el('time', '', timeFmt.format(new Date(m.at))));
  if (t.dur) sub.append(document.createTextNode(' · ' + t.dur));
  txt.append(sub);
  const again = el('button', 'm-icon-btn m-call-again'); again.type = 'button';
  again.setAttribute('aria-label', 'تماس دوباره'); again.title = 'تماس دوباره'; again.innerHTML = svg(p.video ? VIDEO : PHONE);
  again.addEventListener('click', e => { e.stopPropagation(); if (chat.peer) startCall(chat.peer.id, !!p.video); });
  b.append(ic, txt, again);
  return b;
};

// ------------------------------------------------------------ the call list
// The call lines in a chat need the styles from the start.
style();
queueMicrotask(() => {
  const nb = $('m-new-btn');
  if (nb) {
    const b = el('button', 'm-icon-btn'); b.type = 'button'; b.id = 'm-calls-btn'; b.setAttribute('aria-label', 'تماس‌ها'); b.title = 'تماس‌ها';
    b.innerHTML = svg(PHONE); b.addEventListener('click', () => style().then(() => import('/chatcalls.js')).then(m => m.openCalls()));
    nb.before(b);
  }
});
// A notification for a ringing call opens /messages#call<id>: the socket
// rings this page itself once it connects; nothing more to do but tidy the
// address.
if (/^#call\d+$/.test(location.hash)) history.replaceState(null, '', location.pathname + location.search);
