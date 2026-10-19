// chatmedia.js: files in the messenger (docs/chat/SPEC.md 6.4, 8.6). Uploads
// go in 512 KB chunks and resume after a lost connection or a reload; photos
// are compressed here first unless sent as a file; each kind of message draws
// itself through hooks.kinds. Names, captions and page titles are text nodes.
import { reduced } from '/chatcore.js';
import { el, fa, svg, S, hooks, toast } from '/chatstate.js';

const CHUNK = 512 * 1024;

export function fmtSize(n) {
  if (n < 1024) return fa(n) + ' بایت';
  if (n < 1024 * 1024) return fa(Math.round(n / 1024)) + ' کیلوبایت';
  if (n < 1024 * 1024 * 1024) return (n / 1048576).toLocaleString('fa-IR', { maximumFractionDigits: 1 }) + ' مگابایت';
  return (n / 1073741824).toLocaleString('fa-IR', { maximumFractionDigits: 2 }) + ' گیگابایت';
}
export function fmtDur(ms) {
  const s = Math.max(0, Math.round((ms || 0) / 1000));
  return fa(Math.floor(s / 60)) + ':' + fa(s % 60).padStart(2, '۰');
}
const mediaURL = (id, thumb) => `/api/chat/media/${id}${thumb ? '/thumb' : ''}`;
const miniURL = b64 => b64 ? 'data:image/jpeg;base64,' + b64 : '';

// ---------------------------------------------------------------- uploads
//
// Upload sends one Blob and finishes it as a media row. It keeps its id in
// the outbox row, so a reload resumes where the last chunk landed.
export class Upload extends EventTarget {
  constructor(blob, name, meta = {}) {
    super();
    Object.assign(this, { blob, name, meta, id: meta.uploadId || '', received: 0, paused: false, cancelled: false });
  }
  emit(type, detail) { this.dispatchEvent(new CustomEvent(type, { detail })); }
  async req(method, url, body, headers = {}) {
    const r = await fetch(url, { method, credentials: 'same-origin', headers, body });
    const j = await r.json().catch(() => ({}));
    if (!r.ok && r.status !== 409) throw Object.assign(new Error(j.error || r.status), { status: r.status, text: j.error });
    return [r.status, j];
  }
  async run() {
    if (!this.id) {
      const [, j] = await this.req('POST', '/api/chat/uploads', JSON.stringify({ name: this.name, size: this.blob.size }), { 'Content-Type': 'application/json' });
      this.id = j.id; this.emit('id', this.id);
    } else {
      const [, j] = await this.req('GET', `/api/chat/uploads/${this.id}`);
      this.received = j.received || 0;
    }
    let tries = 0;
    while (this.received < this.blob.size) {
      if (this.cancelled) throw Object.assign(new Error('cancelled'), { cancelled: true });
      if (this.paused) { await new Promise(r => this.addEventListener('resume', r, { once: true })); continue; }
      try {
        const part = this.blob.slice(this.received, this.received + CHUNK);
        const [status, j] = await this.req('PUT', `/api/chat/uploads/${this.id}`, part, { 'Upload-Offset': String(this.received) });
        this.received = j.received; tries = 0;
        if (status === 409) continue;
        this.emit('progress', this.received / this.blob.size);
      } catch (e) {
        if (e.status && e.status !== 503) throw e;
        await new Promise(r => setTimeout(r, Math.min(30000, 1000 * 2 ** tries++))); // the network went: try again
      }
    }
    const [, j] = await this.req('POST', `/api/chat/uploads/${this.id}/done`, JSON.stringify(this.meta.finish || {}), { 'Content-Type': 'application/json' });
    return j.media;
  }
  pause() { this.paused = true; }
  resume() { this.paused = false; this.emit('resume'); }
  async cancel() {
    this.cancelled = true; this.resume();
    if (this.id) fetch(`/api/chat/uploads/${this.id}`, { method: 'DELETE', credentials: 'same-origin' }).catch(() => { });
  }
}

// compressPhoto makes a JPEG of at most 2560 pixels (a GIF keeps its frames).
export async function compressPhoto(file) {
  if (!/^image\/(jpeg|png|webp)$/.test(file.type)) return file;
  try {
    const bmp = await createImageBitmap(file, { imageOrientation: 'from-image' });
    const k = Math.min(1, 2560 / Math.max(bmp.width, bmp.height));
    const c = document.createElement('canvas');
    c.width = Math.round(bmp.width * k); c.height = Math.round(bmp.height * k);
    c.getContext('2d').drawImage(bmp, 0, 0, c.width, c.height);
    const blob = await new Promise(r => c.toBlob(r, 'image/jpeg', 0.85));
    return blob && blob.size < file.size ? new File([blob], file.name.replace(/\.\w+$/, '') + '.jpg', { type: 'image/jpeg' }) : file;
  } catch { return file; }
}

// videoMeta reads a video's size, length and a poster frame.
export function videoMeta(blob) {
  return new Promise(resolve => {
    const v = document.createElement('video'); const url = URL.createObjectURL(blob);
    let done = false;
    const finish = out => { if (done) return; done = true; URL.revokeObjectURL(url); resolve(out); };
    v.muted = true; v.playsInline = true; v.preload = 'auto'; v.src = url;
    v.onloadedmetadata = () => { v.currentTime = Math.min(0.1, (v.duration || 1) / 2); };
    v.onseeked = () => {
      const k = Math.min(1, 480 / Math.max(v.videoWidth || 1, v.videoHeight || 1));
      const c = document.createElement('canvas'); c.width = Math.max(1, Math.round(v.videoWidth * k)); c.height = Math.max(1, Math.round(v.videoHeight * k));
      let poster = '';
      try { c.getContext('2d').drawImage(v, 0, 0, c.width, c.height); poster = c.toDataURL('image/jpeg', 0.72).split(',')[1] || ''; } catch { }
      finish({ w: v.videoWidth, h: v.videoHeight, dur: Math.round((v.duration || 0) * 1000), poster });
    };
    v.onerror = () => finish({});
    setTimeout(() => finish({}), 8000);
  });
}

// ---------------------------------------------------------------- the ring
// A progress ring over pending media (SPEC 8.6): draws with the progress, a
// cancel cross in the middle.
export function ring(progress, onCancel) {
  const b = el('button', 'm-ring'); b.type = 'button'; b.setAttribute('aria-label', 'لغو ارسال');
  b.innerHTML = '<svg viewBox="0 0 48 48" aria-hidden="true"><circle class="bg" cx="24" cy="24" r="20"/><circle class="fg" cx="24" cy="24" r="20" pathLength="100"/><path d="M18 18l12 12M30 18L18 30"/></svg>';
  b.querySelector('.fg').style.strokeDashoffset = String(100 - Math.round((progress || 0) * 100));
  b.addEventListener('click', e => { e.stopPropagation(); onCancel && onCancel(); });
  return b;
}

// ---------------------------------------------------------------- bubbles
function box(cls, m) {
  const d = el('div', 'm-media ' + cls);
  const md = m.media || {};
  if (md.w && md.h) d.style.aspectRatio = `${md.w} / ${md.h}`;
  if (md.mini) d.style.backgroundImage = `url("${miniURL(md.mini)}")`;
  return d;
}
function pending(m, node) {
  if (!m.local) return;
  node.appendChild(ring(m.local.progress, () => hooks.cancelUpload && hooks.cancelUpload(m)));
}

// A file's details can be missing from an update logged before 6.8.0: the
// message then shows that it is loading until the chat is opened again.
function missing(m, cls) {
  if (m.local || (m.media && m.media.id)) return null;
  const d = box(cls, m); d.classList.add('m-missing');
  d.appendChild(el('span', 'm-missing-t', 'در حال بارگذاری…'));
  return d;
}

function photo(m) {
  const gone = missing(m, 'm-photo'); if (gone) return gone;
  const d = box('m-photo', m);
  const img = new Image(); img.alt = ''; img.decoding = 'async'; img.loading = 'lazy';
  img.src = m.local ? m.local.url : mediaURL(m.media.id);
  img.addEventListener('load', () => { d.classList.add('ready'); if (!reduced()) img.animate([{ opacity: 0 }, { opacity: 1 }], { duration: 240 }); }, { once: true });
  d.appendChild(img);
  d.dataset.view = '1';
  pending(m, d);
  return d;
}
function video(m) {
  const d = box('m-video', m);
  const md = m.media || {};
  if (md.thumb || m.local) { const img = new Image(); img.alt = ''; img.loading = 'lazy'; img.src = m.local ? (m.local.poster || '') : mediaURL(md.id, true); d.appendChild(img); }
  const play = el('span', 'm-play'); play.innerHTML = svg('<path d="M8 5v14l11-7z" fill="currentColor"/>'); d.appendChild(play);
  if (md.dur) d.appendChild(el('span', 'm-dur', fmtDur(md.dur)));
  d.dataset.view = '1';
  pending(m, d);
  return d;
}
function round(m) {
  const gone = missing(m, 'm-round'); if (gone) return gone;
  const d = el('div', 'm-round');
  const v = document.createElement('video'); v.playsInline = true; v.preload = 'metadata'; v.muted = true; v.loop = false;
  if (m.local) v.src = m.local.url; else { v.src = mediaURL(m.media.id); if (m.media.thumb) v.poster = mediaURL(m.media.id, true); }
  const r = el('span', 'm-round-ring'); r.innerHTML = '<svg viewBox="0 0 100 100" aria-hidden="true"><circle cx="50" cy="50" r="48" pathLength="100"/></svg>';
  d.append(v, r);
  if (m.media && m.media.dur) d.appendChild(el('span', 'm-dur', fmtDur(m.media.dur)));
  d.addEventListener('click', () => {
    if (m.local) return;
    if (v.paused) { v.muted = false; v.currentTime = v.ended ? 0 : v.currentTime; v.play(); d.classList.add('big'); } else { v.pause(); d.classList.remove('big'); }
  });
  v.addEventListener('timeupdate', () => { r.querySelector('circle').style.strokeDashoffset = String(100 - (v.currentTime / (v.duration || 1)) * 100); });
  v.addEventListener('ended', () => d.classList.remove('big'));
  pending(m, d);
  return d;
}
const SPEEDS = [1, 1.5, 2];
function voice(m) {
  const md = m.media || {};
  const d = el('div', 'm-voice');
  const btn = el('button', 'm-voice-play'); btn.type = 'button'; btn.setAttribute('aria-label', 'پخش پیام صوتی');
  btn.innerHTML = svg('<path d="M8 5v14l11-7z" fill="currentColor"/>', 'i-play') + svg('<path d="M7 5h3v14H7zM14 5h3v14h-3z" fill="currentColor"/>', 'i-pause');
  const wave = el('div', 'm-wave'); wave.setAttribute('aria-hidden', 'true');
  const levels = (md.wave && md.wave.length ? md.wave : (m.local && m.local.wave) || []).slice(0, 64);
  for (const v of levels.length ? levels : new Array(40).fill(6)) { const i = el('i'); i.style.height = (12 + (v / 31) * 88) + '%'; wave.appendChild(i); }
  const time = el('span', 'm-voice-time', fmtDur(md.dur || (m.local && m.local.dur)));
  const speed = el('button', 'm-voice-speed', '۱×'); speed.type = 'button'; speed.setAttribute('aria-label', 'سرعت پخش');
  d.append(btn, wave, time, speed);
  if (m.local) { pending(m, d); return d; }
  let audio = null, si = 0;
  const paint = () => {
    const p = audio && audio.duration ? audio.currentTime / audio.duration : 0;
    const n = Math.round(p * wave.children.length);
    [...wave.children].forEach((b, i) => b.classList.toggle('on', i < n));
    time.textContent = fmtDur(audio && !audio.paused ? audio.currentTime * 1000 : md.dur);
  };
  btn.addEventListener('click', () => {
    if (!audio) {
      audio = new Audio(mediaURL(md.id)); audio.playbackRate = SPEEDS[si];
      audio.addEventListener('timeupdate', paint);
      audio.addEventListener('ended', () => { d.classList.remove('playing'); paint(); });
    }
    if (audio.paused) { hooks.stopOthers && hooks.stopOthers(audio); audio.play(); d.classList.add('playing'); } else { audio.pause(); d.classList.remove('playing'); }
  });
  speed.addEventListener('click', () => {
    si = (si + 1) % SPEEDS.length;
    if (audio) audio.playbackRate = SPEEDS[si];
    const label = fa(SPEEDS[si]) + '×';
    if (reduced()) { speed.textContent = label; return; }
    speed.animate([{ transform: 'translateY(0)', opacity: 1 }, { transform: 'translateY(-60%)', opacity: 0 }], { duration: 90 }).finished.then(() => {
      speed.textContent = label; speed.animate([{ transform: 'translateY(60%)', opacity: 0 }, { transform: 'none', opacity: 1 }], { duration: 130 });
    });
  });
  return d;
}
const extOf = n => ((n || '').match(/\.([a-z0-9]{1,5})$/i) || [, ''])[1].toUpperCase();
function file(m) {
  const md = m.media || {};
  const d = el('div', 'm-file');
  const icon = el('a', 'm-file-icon'); icon.setAttribute('aria-label', 'دریافت فایل');
  icon.appendChild(el('span', '', extOf(md.name || (m.local && m.local.name)) || 'فایل'));
  if (!m.local) { icon.href = mediaURL(md.id) + '?download=1'; icon.setAttribute('download', md.name || ''); }
  const info = el('div', 'm-file-info');
  const name = el('b', '', md.name || (m.local && m.local.name) || 'فایل'); name.dir = 'auto';
  info.append(name, el('span', '', fmtSize(md.size || (m.local && m.local.size) || 0)));
  d.append(icon, info);
  pending(m, d);
  return d;
}
function doc(m) {
  const d = el('div', 'm-doccard');
  const icon = el('span', 'm-doc-icon'); icon.innerHTML = svg('<path d="M14 3H6a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9z"/><path d="M14 3v6h6"/>');
  const info = el('div', 'm-file-info');
  const card = m.doc;
  if (!card) { info.append(el('b', '', 'سند آرشیو'), el('span', '', 'در حال بارگذاری…')); hooks.loadDoc && hooks.loadDoc(m); }
  else if (card.denied) { d.classList.add('denied'); info.append(el('b', '', 'سند آرشیو'), el('span', '', 'دسترسی به این سند را ندارید.')); }
  else {
    const a = el('a', '', card.title); a.href = card.url; a.dir = 'auto'; a.target = '_blank'; a.rel = 'noopener';
    const t = el('b'); t.appendChild(a);
    info.append(t, el('span', '', card.type || 'سند آرشیو'));
  }
  d.append(icon, info);
  return d;
}
Object.assign(hooks.kinds, { photo, video, round, voice, file, doc });

// previewCard is a link preview under (or above) the text (SPEC 8.4: fades
// in and slides 8 px).
hooks.previewCard = m => {
  const p = m.preview; if (!p || !p.url) return null;
  const a = el('a', 'm-preview'); a.href = p.url; a.target = '_blank'; a.rel = 'noopener noreferrer nofollow';
  if (p.site) a.appendChild(el('span', 'm-preview-site', p.site));
  if (p.title) { const t = el('b', '', p.title); t.dir = 'auto'; a.appendChild(t); }
  if (p.desc) { const t = el('span', 'm-preview-desc', p.desc); t.dir = 'auto'; a.appendChild(t); }
  if (p.image) { const img = new Image(); img.alt = ''; img.loading = 'lazy'; img.src = mediaURL(p.image); a.appendChild(img); }
  if (m.fresh && !reduced()) a.animate([{ opacity: 0, transform: 'translateY(8px)' }, { opacity: 1, transform: 'none' }], { duration: 240, easing: 'cubic-bezier(0.2,0,0,1)' });
  return a;
};

// albumGrid lays out up to ten photos and videos sent together.
hooks.albumGrid = (msgs) => {
  const g = el('div', 'm-album n' + Math.min(msgs.length, 10));
  for (const m of msgs) {
    const cell = (m.kind === 'video' ? video : photo)(m);
    cell.dataset.key = m.cid || String(m.pts);
    g.appendChild(cell);
  }
  return g;
};

// stopOthers pauses any other voice message playing.
let playing = null;
hooks.stopOthers = a => { if (playing && playing !== a) playing.pause(); playing = a; };

export { mediaURL, miniURL };
