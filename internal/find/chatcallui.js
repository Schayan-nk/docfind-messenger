// chatcallui.js: the call screens (docs/chat/SPEC.md 6.8, motion 8.9): the
// incoming screen (pulse rings, the accept button's bounce, the ring and the
// vibration, «رد با پیام»), the call itself (the moving four-colour ground
// that follows the call's state, voice blobs, the four emoji, signal bars,
// the controls, the draggable own picture), the shared screen (phase 8: the
// other person's grows into the main place, one's own shows the «sharing»
// bar), the small floating window when the person goes back to their chats,
// and the rating card at the end.
import { el, svg, S, toast, facePhoto } from '/chatstate.js';
import { reduced, SPRING } from '/chatcore.js';
import { hangup, mmss, PHONE, VIDEO } from '/chatcall.js';
import { api } from '/chatgroup.js';
import { paintGround, Ring } from '/chatcalls.js';

const IC = {
  mic: '<path d="M12 1a3 3 0 0 0-3 3v8a3 3 0 0 0 6 0V4a3 3 0 0 0-3-3z"/><path d="M19 10v2a7 7 0 0 1-14 0v-2M12 19v4M8 23h8"/>',
  micOff: '<path d="M1 1l22 22M9 9v3a3 3 0 0 0 5.1 2.1M15 9.3V4a3 3 0 0 0-5.9-.6"/><path d="M17 16.9A7 7 0 0 1 5 12v-2m14 0v2a7 7 0 0 1-.1 1.2M12 19v4M8 23h8"/>',
  camOff: '<path d="M1 1l22 22M16 16v1a2 2 0 0 1-2 2H3a2 2 0 0 1-2-2V7a2 2 0 0 1 2-2h2m5.7 0H14a2 2 0 0 1 2 2v3.3l1 1L23 7v10"/>',
  screen: '<rect x="2" y="3" width="20" height="14" rx="2"/><path d="M8 21h8M12 17v4"/>',
  flip: '<path d="M20 7h-3.2L15 4H9L7.2 7H4a2 2 0 0 0-2 2v9a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2V9a2 2 0 0 0-2-2z"/><path d="M9 13.5a3 3 0 0 0 5.6 1.4M15 12.5a3 3 0 0 0-5.6-1.4M14.5 15h1v-1M9.5 11h-1v1"/>',
  speaker: '<path d="M11 5L6 9H2v6h4l5 4V5z"/><path d="M15.5 8.5a5 5 0 0 1 0 7M19 5a10 10 0 0 1 0 14"/>',
  end: '<path d="M10.7 13.3a16 16 0 0 0 3.4 2.6l1.3-1.3a2 2 0 0 1 2.1-.4c.9.3 1.8.6 2.7.7a2 2 0 0 1 1.7 2v3a2 2 0 0 1-2.2 2A19.8 19.8 0 0 1 2.1 4.2 2 2 0 0 1 4.1 2h3a2 2 0 0 1 2 1.7c.1.9.4 1.8.7 2.7a2 2 0 0 1-.5 2.1L8 9.8" transform="rotate(135 12 12)"/>',
  msg: '<path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/>',
  down: '<path d="M6 9l6 6 6-6"/>',
  star: '<path d="M12 2l3.1 6.3 6.9 1-5 4.9 1.2 6.8L12 17.8 5.8 21l1.2-6.8-5-4.9 6.9-1z"/>',
};
const REPLIES = ['الان نمی‌توانم صحبت کنم.', 'در جلسه هستم؛ بعد تماس می‌گیرم.', 'پیام بدهید.', 'چند دقیقهٔ دیگر تماس می‌گیرم.'];
const STATE = { requesting: 'در حال درخواست', ringing: 'در حال زنگ خوردن', connecting: 'در حال اتصال', reconnecting: 'در حال اتصال دوباره' };
const ENDED = { ended: 'تماس پایان یافت', missed: 'همکار پاسخ نداد', cancelled: 'تماس لغو شد', declined: 'تماس رد شد', busy: 'همکار در تماس دیگری است', failed: 'تماس برقرار نشد' };
const hueOf = s => { let h = 0; for (const c of s) h = (h * 31 + c.codePointAt(0)) % 360; return h; };

function button(icon, label, cls = '') {
  const w = el('div', 'cl-btn ' + cls);
  const b = el('button', 'cl-round'); b.type = 'button'; b.setAttribute('aria-label', label); b.innerHTML = svg(icon);
  w.append(b, el('span', 'cl-label', label));
  return { w, b, set(icon2, label2, on) { b.innerHTML = svg(icon2); b.setAttribute('aria-label', label2); w.lastChild.textContent = label2; w.classList.toggle('on', !!on); } };
}

export function open(call, act) {
  // A call that ended with its rating card still open makes way for this one.
  for (const old of document.querySelectorAll('.cl-root.over')) old.remove();
  const info = call.info, peer = info.peer || {};
  const root = el('div', 'cl-root' + (call.state === 'incoming' ? ' incoming' : ''));
  root.setAttribute('role', 'dialog'); root.setAttribute('aria-modal', 'true'); root.setAttribute('aria-label', 'تماس با ' + (peer.name || ''));
  const bg = el('canvas', 'cl-bg'); bg.width = 48; bg.height = 48;
  const remoteV = el('video', 'cl-remote'); Object.assign(remoteV, { autoplay: true, playsInline: true, muted: true });
  const screenV = el('video', 'cl-shared'); Object.assign(screenV, { autoplay: true, playsInline: true, muted: true });
  const audio = el('audio'); audio.autoplay = true;
  const localV = el('video', 'cl-local'); Object.assign(localV, { autoplay: true, playsInline: true, muted: true }); localV.hidden = true;

  const top = el('div', 'cl-top');
  const mini = el('button', 'cl-mini-btn'); mini.type = 'button'; mini.setAttribute('aria-label', 'بازگشت به گفت‌وگوها'); mini.innerHTML = svg(IC.down);
  const emo = el('button', 'cl-emoji'); emo.type = 'button'; emo.hidden = true; emo.setAttribute('aria-label', 'شکلک‌های رمزگذاری');
  const bars = el('div', 'cl-bars'); bars.setAttribute('aria-hidden', 'true'); for (let i = 0; i < 4; i++) bars.appendChild(el('i'));
  top.append(mini, emo, bars);
  const weak = el('div', 'cl-weak', 'شبکه ضعیف است'); weak.setAttribute('role', 'status');
  const sharing = el('div', 'cl-sharebar'); sharing.setAttribute('role', 'status');
  const stopShare = el('button', '', 'توقف'); stopShare.type = 'button';
  sharing.append(el('span', '', 'صفحهٔ شما برای همکار نمایش داده می‌شود'), stopShare);

  const mid = el('div', 'cl-mid');
  const face = el('div', 'cl-face');
  const blobR = el('div', 'cl-blob r'), blobL = el('div', 'cl-blob l');
  const av = el('div', 'cl-av', [...(peer.name || '؟').trim()][0] || '؟'); av.style.background = `hsl(${hueOf(peer.id || '')} 45% 38%)`;
  facePhoto(av, peer.id, peer.photo);
  const rings = el('div', 'cl-rings'); rings.append(el('i'), el('i'));
  face.append(rings, blobR, blobL, av);
  const name = el('div', 'cl-name', peer.name || ''); name.dir = 'auto';
  const status = el('div', 'cl-status'); status.setAttribute('aria-live', 'polite');
  mid.append(face, name, status);

  // ------------------------------------------------------------ controls
  const ctl = el('div', 'cl-controls');
  const speaker = button(IC.speaker, 'بلندگو'); speaker.w.hidden = true;
  const cam = button(VIDEO, 'دوربین');
  const flipB = button(IC.flip, 'چرخاندن'); flipB.w.hidden = true;
  const mic = button(IC.mic, 'بی‌صدا');
  // Sharing the screen (phase 8): where the browser can (not iPhone or Android).
  const share = button(IC.screen, 'اشتراک صفحه'); share.w.hidden = !(navigator.mediaDevices && navigator.mediaDevices.getDisplayMedia);
  const endB = button(IC.end, 'پایان', 'end');
  ctl.append(speaker.w, cam.w, flipB.w, share.w, mic.w, endB.w);
  const inc = el('div', 'cl-controls cl-inc');
  const no = button(IC.end, 'رد', 'end'), say = button(IC.msg, 'پیام'), yes = button(info.video ? VIDEO : PHONE, 'پاسخ', 'accept');
  inc.append(no.w, say.w, yes.w);
  root.append(bg, remoteV, screenV, audio, top, weak, sharing, mid, localV, ctl, inc);
  document.body.appendChild(root);
  if (!reduced()) root.animate([{ opacity: 0, transform: 'scale(1.04)' }, { opacity: 1, transform: 'none' }], { duration: 260, easing: 'cubic-bezier(0.2,0,0,1)' });

  const ground = paintGround(bg);
  const ring = new Ring();
  let since = 0, tick = 0, raf = 0, levels = null, gone = false;

  // ------------------------------------------------------------ state
  function paintState(s) {
    root.dataset.state = s;
    ground.set(s === 'connected' ? 'green' : s === 'reconnecting' ? 'orange' : 'neutral');
    if (s === 'incoming') { status.textContent = info.video ? 'تماس تصویری ورودی' : 'تماس صوتی ورودی'; ring.incoming(); return; }
    if (s === 'ringing') ring.back(); else ring.stop();
    if (s === 'connected') {
      if (!since) {
        since = Date.now();
        const t = el('span', 'cl-timer', mmss(0)); status.replaceChildren(t);
        if (!reduced()) t.animate([{ opacity: 0 }, { opacity: 1 }], { duration: 300 });
        tick = setInterval(() => {
          const now = status.querySelector('.cl-timer'); if (now) now.textContent = mmss((Date.now() - since) / 1000);
          if (root.classList.contains('mini')) miniTime();
        }, 1000);
      } else status.replaceChildren(el('span', 'cl-timer', mmss((Date.now() - since) / 1000)));
      return;
    }
    const d = el('span', 'm-dots'); d.append(el('i'), el('i'), el('i'));
    status.replaceChildren(document.createTextNode((STATE[s] || '') + ' '), d);
  }
  call.addEventListener('state', e => paintState(e.detail));
  paintState(call.state);

  // ------------------------------------------------------------ media
  call.addEventListener('remote', e => {
    if (e.detail === 'screen') { if (screenV.srcObject !== call.remoteScreen) screenV.srcObject = call.remoteScreen; screenV.play().catch(() => { }); return; }
    if (audio.srcObject !== call.remote) { audio.srcObject = call.remote; remoteV.srcObject = call.remote; }
    audio.play().catch(() => { }); remoteV.play().catch(() => { });
    if (!levels && call.remote.getAudioTracks().length) startLevels();
  });
  remoteV.addEventListener('resize', paintVideo);
  call.addEventListener('peer-media', paintVideo);
  call.addEventListener('local', e => { paintLocal(e.detail === 'flip'); paintButtons(); sharing.classList.toggle('on', call.screen); });
  stopShare.addEventListener('click', () => call.stopScreen());
  function paintVideo() {
    const cam = call.peerMedia.cam, scr = call.peerMedia.screen, on = cam || scr;
    const was = root.classList.contains('video'), wasScr = root.classList.contains('screen');
    root.classList.toggle('video', on);
    root.classList.toggle('screen', scr);
    root.classList.toggle('peercam', cam);
    // Their shared screen grows from the corner into the main place (8.9).
    if (scr && !wasScr) {
      if (screenV.srcObject !== call.remoteScreen) screenV.srcObject = call.remoteScreen;
      screenV.play().catch(() => { });
      if (!reduced()) screenV.animate([{ transform: 'translate(-34%, -34%) scale(.3)', opacity: 0 }, { transform: 'none', opacity: 1 }], { duration: 420, easing: SPRING });
    }
    if (on && !was && !reduced()) remoteV.animate([{ opacity: 0 }, { opacity: 1 }], { duration: 300 });
    root.classList.toggle('peer-muted', !call.peerMedia.mic);
  }
  function paintLocal(flipped) {
    const on = call.cam;
    const was = !localV.hidden;
    localV.hidden = !on;
    if (on) {
      localV.srcObject = call.local; localV.play().catch(() => { });
      localV.classList.toggle('mirror', call.facing === 'user');
      if (!was && !reduced()) grow(cam.b, localV);
      if (flipped && !reduced()) localV.animate([{ transform: 'rotateY(0)' }, { transform: 'rotateY(180deg)' }], { duration: 360, easing: 'cubic-bezier(0.2,0,0,1)' });
    }
  }
  function paintButtons() {
    mic.set(call.mic ? IC.mic : IC.micOff, call.mic ? 'بی‌صدا' : 'صدا', !call.mic);
    cam.set(call.cam ? VIDEO : IC.camOff, 'دوربین', call.cam);
    share.set(IC.screen, call.screen ? 'پایان اشتراک' : 'اشتراک صفحه', call.screen);
    navigator.mediaDevices.enumerateDevices().then(ds => { flipB.w.hidden = !call.cam || ds.filter(d => d.kind === 'videoinput').length < 2; }).catch(() => { });
  }
  // The own picture grows from the camera button into its corner (8.9).
  function grow(from, to) {
    const a = from.getBoundingClientRect(), b = to.getBoundingClientRect();
    if (!b.width) return;
    to.animate([{ transform: `translate(${a.left - b.left}px, ${a.top - b.top}px) scale(${a.width / b.width})`, borderRadius: '50%' }, { transform: 'none' }], { duration: 380, easing: SPRING });
  }
  function startLevels() {
    const r = call.analyser(call.remote), l = call.local ? call.analyser(call.local) : () => 0;
    levels = () => {
      const lr = r(), ll = call.mic ? l() : 0;
      blobR.style.transform = `scale(${1 + lr * 0.45})`; blobL.style.transform = `scale(${1 + ll * 0.3})`;
      raf = requestAnimationFrame(levels);
    };
    if (!reduced()) raf = requestAnimationFrame(levels);
  }

  // ------------------------------------------------------------ emoji and quality
  call.addEventListener('emoji', e => {
    emo.replaceChildren(...e.detail.map(x => el('span', '', x))); emo.hidden = false;
    if (!reduced()) [...emo.children].forEach((s, i) => s.animate([{ transform: 'scale(0)' }, { transform: 'none' }], { duration: 300, delay: i * 80, easing: SPRING, fill: 'backwards' }));
  });
  emo.addEventListener('click', () => {
    const card = el('div', 'cl-card');
    card.append(el('div', 'cl-card-emoji', call.emoji.join(' ')), el('h3', '', 'این تماس رمزگذاری‌شده است'),
      el('p', '', 'صدا و تصویر میان دو مرورگر رمزگذاری می‌شود و سرور آن را نمی‌بیند. اگر همکارتان همین چهار شکلک را روی صفحهٔ خود می‌بیند، کسی میان شما نیست.'));
    const ok = el('button', 'btn btn-primary', 'باشه'); ok.type = 'button'; card.appendChild(ok);
    root.appendChild(card);
    if (!reduced()) card.animate([{ opacity: 0, transform: 'translate(-50%, -50%) scale(.85)' }, { opacity: 1, transform: 'translate(-50%, -50%)' }], { duration: 240, easing: SPRING });
    ok.addEventListener('click', () => card.remove()); ok.focus();
  });
  let weakShown = false;
  call.addEventListener('quality', e => {
    const q = e.detail;
    [...bars.children].forEach((b, i) => b.classList.toggle('on', i < q.bars));
    bars.title = `تأخیر ${Math.round(q.rtt * 1000)} میلی‌ثانیه${q.relay ? '، از راه سرور واسط' : ''}`;
    const w = q.bars <= 1;
    if (w !== weakShown) { weakShown = w; weak.classList.toggle('on', w); }
  });

  // ------------------------------------------------------------ the controls
  mic.b.addEventListener('click', () => call.setMic(!call.mic));
  cam.b.addEventListener('click', () => call.setCam(!call.cam).catch(() => toast('دوربین در دسترس نیست.')));
  flipB.b.addEventListener('click', () => call.flipCam().catch(() => { }));
  share.b.addEventListener('click', () => (call.screen ? call.stopScreen() : call.shareScreen()).catch(() => { }));
  endB.b.addEventListener('click', () => hangup());
  call.outputs().then(list => {
    if (list.length < 2) return;
    speaker.w.hidden = false;
    let at = 0;
    speaker.b.addEventListener('click', () => {
      at = (at + 1) % list.length;
      audio.setSinkId(list[at].deviceId).then(() => toast('صدا از: ' + (list[at].label || 'خروجی ' + (at + 1)))).catch(() => { });
      speaker.w.classList.toggle('on', at !== 0);
    });
  }).catch(() => { });
  yes.b.addEventListener('click', () => { root.classList.remove('incoming'); ring.stop(); act.accept(); });
  no.b.addEventListener('click', () => { ring.stop(); act.decline(''); });
  // «رد با پیام»: the replies open inside the call screen (a menu would sit
  // under it).
  const replies = el('div', 'cl-replies'); replies.setAttribute('role', 'menu'); replies.hidden = true;
  for (const t of REPLIES) {
    const b = el('button', 'cl-reply', t); b.type = 'button'; b.setAttribute('role', 'menuitem');
    b.addEventListener('click', () => { ring.stop(); act.decline(t); });
    replies.appendChild(b);
  }
  root.appendChild(replies);
  say.b.addEventListener('click', () => {
    replies.hidden = !replies.hidden;
    if (!replies.hidden) {
      if (!reduced()) replies.animate([{ opacity: 0, transform: 'translateY(12px)' }, { opacity: 1, transform: 'none' }], { duration: 220, easing: SPRING });
      replies.firstChild.focus();
    }
  });
  paintButtons();
  (call.state === 'incoming' ? yes.b : endB.b).focus();

  // ------------------------------------------------------------ the own picture: drag, snap to a corner
  let drag = null;
  localV.addEventListener('pointerdown', e => {
    const r = localV.getBoundingClientRect();
    drag = { x: e.clientX, y: e.clientY, r }; localV.setPointerCapture(e.pointerId); localV.classList.add('dragging');
  });
  localV.addEventListener('pointermove', e => { if (drag) localV.style.transform = `translate(${e.clientX - drag.x}px, ${e.clientY - drag.y}px)`; });
  localV.addEventListener('pointerup', e => {
    if (!drag) return;
    const cx = drag.r.left + drag.r.width / 2 + e.clientX - drag.x, cy = drag.r.top + drag.r.height / 2 + e.clientY - drag.y;
    const box = root.getBoundingClientRect();
    const before = localV.getBoundingClientRect();
    localV.classList.remove('dragging');
    localV.style.transform = '';
    localV.dataset.corner = (cy < box.top + box.height / 2 ? 't' : 'b') + (cx < box.left + box.width / 2 ? 'l' : 'r');
    const after = localV.getBoundingClientRect();
    if (!reduced()) localV.animate([{ transform: `translate(${before.left - after.left}px, ${before.top - after.top}px)` }, { transform: 'none' }], { duration: 420, easing: SPRING });
    drag = null;
  });

  // ------------------------------------------------------------ the floating window
  function miniTime() { const t = root.querySelector('.cl-mini-time'); if (t) t.textContent = since ? mmss((Date.now() - since) / 1000) : (STATE[call.state] || ''); }
  function shrink() {
    if (root.classList.contains('mini') || root.classList.contains('over') || call.state === 'incoming') return;
    const a = root.getBoundingClientRect();
    root.classList.add('mini');
    root.setAttribute('aria-modal', 'false'); root.setAttribute('role', 'button'); root.tabIndex = 0;
    root.setAttribute('aria-label', 'بازگشت به تماس با ' + (peer.name || ''));
    if (!root.querySelector('.cl-mini-time')) root.appendChild(el('span', 'cl-mini-time'));
    miniTime();
    const b = root.getBoundingClientRect();
    if (!reduced()) root.animate([{ transform: `translate(${a.left - b.left}px, ${a.top - b.top}px) scale(${a.width / b.width}, ${a.height / b.height})`, transformOrigin: 'top left' }, { transform: 'none', transformOrigin: 'top left' }], { duration: 360, easing: SPRING });
  }
  function restore() {
    if (!root.classList.contains('mini')) return;
    const a = root.getBoundingClientRect();
    root.classList.remove('mini');
    root.setAttribute('aria-modal', 'true'); root.setAttribute('role', 'dialog'); root.removeAttribute('tabindex');
    root.setAttribute('aria-label', 'تماس با ' + (peer.name || ''));
    const b = root.getBoundingClientRect();
    if (!reduced()) root.animate([{ transform: `translate(${a.left - b.left}px, ${a.top - b.top}px) scale(${a.width / b.width}, ${a.height / b.height})`, transformOrigin: 'top left' }, { transform: 'none', transformOrigin: 'top left' }], { duration: 360, easing: SPRING });
    endB.b.focus();
  }
  mini.addEventListener('click', shrink);
  root.addEventListener('click', e => { if (root.classList.contains('mini')) { e.stopPropagation(); restore(); } }, true);
  root.addEventListener('keydown', e => {
    const isMini = root.classList.contains('mini');
    if (isMini && (e.key === 'Enter' || e.key === ' ')) { e.preventDefault(); restore(); return; }
    if (e.key === 'Escape' && !isMini) {
      e.preventDefault();
      if (!replies.hidden) { replies.hidden = true; say.b.focus(); return; }
      shrink();
    }
  });

  // ------------------------------------------------------------ the end
  function close() {
    if (gone) return; gone = true;
    ring.stop(); ring.close(); clearInterval(tick); cancelAnimationFrame(raf); ground.stop();
    const fin = () => root.remove();
    reduced() ? fin() : root.animate([{ opacity: 1 }, { opacity: 0 }], { duration: 220, fill: 'forwards' }).finished.then(fin, fin);
  }
  function ended(reason, talked) {
    ring.stop(); clearInterval(tick); cancelAnimationFrame(raf);
    restore();
    root.classList.add('over'); sharing.classList.remove('on');
    status.replaceChildren(document.createTextNode(ENDED[reason] || ENDED.ended + (talked ? ' · ' + mmss(talked) : '')));
    if (!reduced()) ctl.animate([{ transform: 'none', opacity: 1 }, { transform: 'translateY(120%)', opacity: 0 }], { duration: 260, easing: 'cubic-bezier(.4,0,1,1)', fill: 'forwards' });
    if (talked > 0 && call.id) setTimeout(() => rate(), 500); else setTimeout(close, 1600);
  }
  function rate() {
    const card = el('div', 'cl-card cl-rate');
    card.append(el('h3', '', 'کیفیت تماس چطور بود؟'));
    const stars = el('div', 'cl-stars'); let n = 0;
    for (let i = 1; i <= 5; i++) {
      const s = el('button', 'cl-star'); s.type = 'button'; s.setAttribute('aria-label', i + ' ستاره'); s.innerHTML = svg(IC.star);
      s.addEventListener('click', () => {
        n = i;
        [...stars.children].forEach((x, j) => { const on = j < i; if (on && !x.classList.contains('on') && !reduced()) x.animate([{ transform: 'scale(.6)' }, { transform: 'none' }], { duration: 260, delay: j * 60, easing: SPRING }); x.classList.toggle('on', on); });
        send.disabled = false;
      });
      stars.appendChild(s);
    }
    const row = el('div', 'cl-card-row');
    const skip = el('button', 'btn', 'بستن'), send = el('button', 'btn btn-primary', 'ارسال'); skip.type = send.type = 'button'; send.disabled = true;
    skip.addEventListener('click', close);
    send.addEventListener('click', () => { api(`/api/chat/calls/${call.id}/rate`, { stars: n }).then(() => toast('سپاس؛ نظر شما ثبت شد.')).catch(() => { }); close(); });
    row.append(skip, send); card.append(stars, row);
    root.appendChild(card);
    if (!reduced()) card.animate([{ opacity: 0, transform: 'translate(-50%, -40%)' }, { opacity: 1, transform: 'translate(-50%, -50%)' }], { duration: 300, easing: SPRING });
    stars.firstChild.focus();
  }
  function fail(text) { status.replaceChildren(document.createTextNode(text)); root.classList.add('over'); setTimeout(close, 2200); }

  return {
    local() { paintLocal(false); paintButtons(); if (call.local && !levels && call.remote.getAudioTracks().length) startLevels(); },
    fail, ended, close, restore,
  };
}
