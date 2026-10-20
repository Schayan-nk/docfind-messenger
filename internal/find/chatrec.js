// chatrec.js: voice and round video messages (docs/chat/SPEC.md 6.4, 8.5),
// loaded the first time the record button is pressed. Hold to record; slide
// towards the start to cancel; slide up to lock and record hands-free. A
// short tap switches the button between voice and round video.
import { reduced } from '/chatcore.js';
import { el, svg, S, hooks, toast, vibrate } from '/chatstate.js';
import { fmtDur } from '/chatmedia.js';

const HOLD_MS = 220, CANCEL_PX = 110, LOCK_PX = 70, ROUND_MAX = 60000, VOICE_MAX = 30 * 60000;

function pickType(kind) {
  const list = kind === 'voice'
    ? ['audio/webm;codecs=opus', 'audio/ogg;codecs=opus', 'audio/mp4', 'audio/webm']
    : ['video/webm;codecs=vp9,opus', 'video/webm;codecs=vp8,opus', 'video/webm', 'video/mp4'];
  return list.find(t => window.MediaRecorder && MediaRecorder.isTypeSupported(t)) || '';
}

// begin decides between a tap (switch mode) and a hold (record).
export function begin(e, btn, chat, mode, setMode) {
  const x0 = e.clientX, y0 = e.clientY;
  let up = false;
  const onUp = () => { up = true; };
  btn.addEventListener('pointerup', onUp, { once: true });
  btn.setPointerCapture && btn.setPointerCapture(e.pointerId);
  setTimeout(() => {
    btn.removeEventListener('pointerup', onUp);
    if (up) { setMode(mode === 'voice' ? 'round' : 'voice'); return; }
    record(btn, chat, mode, x0, y0, e.pointerId).catch(err => {
      toast(err && err.name === 'NotAllowedError' ? 'دسترسی به میکروفون یا دوربین داده نشده است.' : 'ضبط ممکن نشد.');
    });
  }, HOLD_MS);
}

async function record(btn, chat, kind, x0, y0, pointerId) {
  const type = pickType(kind);
  if (!type) { toast('این مرورگر ضبط را پشتیبانی نمی‌کند.'); return; }
  const stream = await navigator.mediaDevices.getUserMedia(kind === 'voice'
    ? { audio: { echoCancellation: true, noiseSuppression: true } }
    : { audio: { echoCancellation: true, noiseSuppression: true }, video: { facingMode: 'user', width: { ideal: 480 }, height: { ideal: 480 } } });
  vibrate(15);
  const rec = new MediaRecorder(stream, { mimeType: type });
  const parts = [];
  rec.ondataavailable = ev => { if (ev.data && ev.data.size) parts.push(ev.data); };
  // Levels for the live waveform and the one stored with a voice message.
  const ac = new (window.AudioContext || window.webkitAudioContext)();
  const an = ac.createAnalyser(); an.fftSize = 512;
  ac.createMediaStreamSource(stream).connect(an);
  const buf = new Uint8Array(an.fftSize), levels = [];
  const level = () => { an.getByteTimeDomainData(buf); let s = 0; for (const v of buf) s += (v - 128) ** 2; return Math.min(1, Math.sqrt(s / buf.length) / 40); };

  const ui = overlay(kind, stream);
  const t0 = performance.now();
  let locked = false, done = false, raf = 0, typingAt = 0;
  // A round video's poster is taken while the camera is live, once it has
  // warmed up. Taken after the tracks stopped, it was a black frame: the
  // bubble stayed a black circle, for both people, until it was tapped.
  let poster = '';
  const tick = () => {
    const ms = performance.now() - t0;
    const l = level(); levels.push(l);
    ui.update(ms, l);
    if (kind === 'round' && !poster && ms > 450) poster = ui.poster();
    if (Date.now() - typingAt > 3000) { typingAt = Date.now(); S.net.send('typing', { chat: chat.id, action: kind === 'voice' ? 'voice' : 'video' }); }
    if (ms >= (kind === 'round' ? ROUND_MAX : VOICE_MAX)) { finish(true); return; }
    raf = requestAnimationFrame(tick);
  };
  rec.start(250);
  raf = requestAnimationFrame(tick);

  const move = ev => {
    if (locked || done) return;
    const dx = ev.clientX - x0, dy = y0 - ev.clientY;
    ui.drag(dx, dy);
    if (dx > CANCEL_PX) finish(false);          // RTL: away from the button, towards the start
    else if (dy > LOCK_PX) { locked = true; vibrate(12); ui.lock(() => finish(true), () => finish(false)); }
  };
  const release = () => { if (!locked && !done) finish(true); };
  btn.addEventListener('pointermove', move);
  btn.addEventListener('pointerup', release, { once: true });
  btn.addEventListener('pointercancel', () => { if (!locked) finish(false); }, { once: true });

  function finish(send) {
    if (done) return; done = true;
    if (kind === 'round' && !poster) poster = ui.poster(); // the camera is still on here
    cancelAnimationFrame(raf);
    btn.removeEventListener('pointermove', move);
    try { btn.releasePointerCapture && btn.releasePointerCapture(pointerId); } catch { }
    S.net.send('typing', { chat: chat.id, action: 'cancel' });
    const dur = Math.round(performance.now() - t0);
    rec.onstop = async () => {
      stream.getTracks().forEach(t => t.stop()); ac.close();
      if (!send) { ui.cancel(); return; }
      ui.close();
      if (dur < 600) { toast('برای ضبط، دکمه را نگه دارید.'); return; }
      const blob = new Blob(parts, { type: type.split(';')[0] });
      const wave = downsample(levels, 64);
      if (kind === 'voice') hooks.sendRecorded(blob, { kind: 'voice', dur, wave }, { dur });
      else hooks.sendRecorded(blob, { kind: 'round', dur, w: ui.size || 480, h: ui.size || 480, poster }, { dur, poster: poster ? 'data:image/jpeg;base64,' + poster : '' });
    };
    rec.stop();
  }
}

function downsample(levels, n) {
  if (!levels.length) return [];
  const out = [];
  for (let i = 0; i < n; i++) {
    const a = Math.floor(i * levels.length / n), b = Math.max(a + 1, Math.floor((i + 1) * levels.length / n));
    let m = 0; for (let j = a; j < b; j++) m = Math.max(m, levels[j] || 0);
    out.push(Math.round(m * 31));
  }
  return out;
}

// overlay is the recording UI over the message bar (voice) or in the middle
// of the chat (round video).
function overlay(kind, stream) {
  const foot = document.getElementById('m-foot');
  const bar = el('div', 'm-rec'); bar.setAttribute('role', 'status');
  const dot = el('span', 'm-rec-dot');
  const time = el('span', 'm-rec-time', fmtDur(0));
  const live = el('div', 'm-rec-wave'); for (let i = 0; i < 28; i++) live.appendChild(el('i'));
  const slide = el('span', 'm-rec-slide', 'برای لغو بکشید ‹');
  const lockI = el('span', 'm-rec-lock'); lockI.innerHTML = svg('<rect x="5" y="11" width="14" height="10" rx="2"/><path d="M8 11V7a4 4 0 0 1 8 0v4"/>');
  bar.append(dot, time, live, slide, lockI);
  foot.appendChild(bar);
  foot.classList.add('recording');
  let circle = null, video = null, size = 0;
  if (kind === 'round') {
    circle = el('div', 'm-rec-round');
    video = document.createElement('video'); video.muted = true; video.playsInline = true; video.autoplay = true; video.srcObject = stream;
    const ringEl = el('span', 'm-round-ring'); ringEl.innerHTML = '<svg viewBox="0 0 100 100" aria-hidden="true"><circle cx="50" cy="50" r="48" pathLength="100"/></svg>';
    circle.append(video, ringEl);
    document.getElementById('m-chat').appendChild(circle);
    video.addEventListener('loadedmetadata', () => { size = Math.min(video.videoWidth, video.videoHeight) || 480; });
    if (!reduced()) circle.animate([{ transform: 'scale(.2)', opacity: 0 }, { transform: 'none', opacity: 1 }], { duration: 300, easing: 'cubic-bezier(0.34,1.56,0.64,1)' });
  }
  if (!reduced()) bar.animate([{ opacity: 0, transform: 'translateY(8px)' }, { opacity: 1, transform: 'none' }], { duration: 200 });
  const remove = () => { bar.remove(); circle && circle.remove(); foot.classList.remove('recording'); };
  return {
    get size() { return size; },
    update(ms, l) {
      time.textContent = fmtDur(ms);
      const bars = live.children; for (let i = 0; i < bars.length - 1; i++) bars[i].style.transform = bars[i + 1].style.transform;
      bars[bars.length - 1].style.transform = `scaleY(${0.15 + l * 0.85})`;
      if (circle) circle.querySelector('circle').style.strokeDashoffset = String(100 - (ms / ROUND_MAX) * 100);
    },
    drag(dx, dy) { slide.style.transform = `translateX(${Math.max(0, dx)}px)`; slide.style.opacity = String(1 - Math.min(1, Math.max(0, dx) / CANCEL_PX)); lockI.style.transform = `translateY(${-Math.max(0, dy)}px)`; },
    lock(send, cancel) {
      slide.remove(); lockI.remove();
      const stop = el('button', 'btn btn-primary m-rec-send', 'ارسال'); stop.type = 'button'; stop.addEventListener('click', send);
      const del = el('button', 'm-icon-btn m-rec-del'); del.type = 'button'; del.setAttribute('aria-label', 'دور انداختن'); del.innerHTML = svg('<path d="M3 6h18"/><path d="M8 6V4h8v2"/><path d="M6 6l1 14h10l1-14"/>');
      del.addEventListener('click', cancel);
      bar.append(del, stop);
      if (!reduced()) stop.animate([{ transform: 'scale(.6)' }, { transform: 'none' }], { duration: 260, easing: 'cubic-bezier(0.34,1.56,0.64,1)' });
    },
    poster() {
      if (!video || !video.videoWidth) return '';
      const c = document.createElement('canvas'); const s = Math.min(video.videoWidth, video.videoHeight);
      c.width = c.height = Math.min(320, s);
      c.getContext('2d').drawImage(video, (video.videoWidth - s) / 2, (video.videoHeight - s) / 2, s, s, 0, 0, c.width, c.height);
      return c.toDataURL('image/jpeg', 0.7).split(',')[1] || '';
    },
    cancel() {
      const mic = document.querySelector('#m-compose .m-send');
      if (mic && !reduced()) mic.animate([{ transform: 'translateX(0)' }, { transform: 'translateX(-6px)' }, { transform: 'translateX(6px)' }, { transform: 'translateX(0)' }], { duration: 300 });
      if (reduced()) { remove(); return; }
      bar.animate([{ opacity: 1, transform: 'none' }, { opacity: 0, transform: 'scale(.9)' }], { duration: 180 }).finished.then(remove);
    },
    close: remove,
  };
}
