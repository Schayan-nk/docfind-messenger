// chatattach.js: sending files (docs/chat/SPEC.md 6.4, 8.5): the attach
// sheet, the send sheet (caption, reorder, "as a file"), DocFind documents,
// drag and drop, paste, and the sending itself: an optimistic bubble with a
// progress ring, the upload, then the message. An unfinished upload waits in
// the outbox (with its file) and resumes after a reload.
import { uuidv7, reduced, SPRING } from '/chatcore.js';
import { $, el, fa, svg, S, hooks, toast } from '/chatstate.js';
import { Upload, compressPhoto, videoMeta, fmtSize } from '/chatmedia.js';
import { parseComposer } from '/chatfmt.js';

const ALBUM_MAX = 10;
const uploads = new Map(); // cid -> Upload

const kindOf = (f, asFile) => asFile ? 'file' : /^image\/(jpeg|png|gif|webp)$/.test(f.type) ? 'photo' : /^video\/(mp4|webm)$/.test(f.type) ? 'video' : 'file';

// ---------------------------------------------------------------- sending
async function prepare(file, kind) {
  if (kind === 'photo') {
    const blob = await compressPhoto(file);
    let w = 0, h = 0;
    try { const b = await createImageBitmap(blob); w = b.width; h = b.height; } catch { }
    return { blob, finish: { kind }, local: { w, h } };
  }
  if (kind === 'video') {
    const v = await videoMeta(file);
    return { blob: file, finish: { kind, w: v.w || 0, h: v.h || 0, dur: v.dur || 0, poster: v.poster || '' }, local: { w: v.w, h: v.h, dur: v.dur, poster: v.poster ? 'data:image/jpeg;base64,' + v.poster : '' } };
  }
  return { blob: file, finish: { kind: 'file', as_file: true }, local: {} };
}

// sendFiles sends files as one album (photos and videos) or one by one.
export async function sendFiles(chat, files, { caption = '', entities = [], asFile = false } = {}) {
  const items = files.map(f => ({ file: f, kind: kindOf(f, asFile) }));
  const visual = items.every(i => i.kind === 'photo' || i.kind === 'video');
  const album = items.length > 1 && visual ? uuidv7() : '';
  for (let i = 0; i < items.length; i++) {
    const { file, kind } = items[i];
    const p = await prepare(file, kind);
    const last = i === items.length - 1;
    await start(chat, {
      cid: uuidv7(), kind, album, blob: p.blob, name: file.name || 'file', finish: p.finish,
      text: (album ? i === 0 : last) ? caption : '', entities: (album ? i === 0 : last) ? entities : [], local: p.local
    });
  }
}

// sendRecorded sends a voice or round video message (chatrec.js).
export function sendRecorded(chat, blob, finish, local) {
  const ext = blob.type.includes('mp4') ? '.mp4' : blob.type.includes('ogg') ? '.ogg' : '.webm';
  return start(chat, { cid: uuidv7(), kind: finish.kind, album: '', blob, name: finish.kind + ext, finish, text: '', entities: [], local });
}
hooks.sendRecorded = (blob, finish, local) => { const chat = S.chats.get(S.active); if (chat) sendRecorded(chat, blob, finish, local); };

async function start(chat, it) {
  const url = URL.createObjectURL(it.blob);
  const m = {
    cid: it.cid, chat: chat.id, from: S.me, kind: it.kind, text: it.text, entities: it.entities, album: it.album,
    at: new Date().toISOString(), pts: 0, topic: chat.forum ? (S.topic || 0) : 0,
    media: { kind: it.kind, w: it.local.w || 0, h: it.local.h || 0, dur: it.local.dur || it.finish.dur || 0, wave: it.finish.wave, name: it.name, size: it.blob.size },
    local: { url, poster: it.local.poster, progress: 0, name: it.name, size: it.blob.size, wave: it.finish.wave, dur: it.finish.dur }
  };
  const list = S.msgs.get(chat.id) || []; list.push(m); S.msgs.set(chat.id, list);
  S.atBottom = true; hooks.renderMessages({ bottom: true });
  chat.last = { text: it.text || label(it.kind), from: S.me, at: m.at }; hooks.renderList();
  const row = { cid: it.cid, chat: chat.id, kind: it.kind, album: it.album, text: it.text, entities: it.entities, at: m.at, file: it.blob, name: it.name, finish: it.finish, uploadId: '', mediaId: 0, topic: m.topic };
  await S.cache.put('outbox', row);
  run(m, row);
}

const label = k => ({ photo: 'عکس', video: 'ویدیو', round: 'پیام ویدیویی', voice: 'پیام صوتی', file: 'فایل', doc: 'سند' })[k] || 'فایل';

async function run(m, row) {
  try {
    if (!row.mediaId) {
      const up = new Upload(row.file, row.name, { uploadId: row.uploadId, finish: row.finish });
      uploads.set(row.cid, up);
      up.addEventListener('id', e => { row.uploadId = e.detail; S.cache.put('outbox', row); });
      up.addEventListener('progress', e => paintProgress(m, e.detail));
      const media = await up.run();
      row.mediaId = media.id; await S.cache.put('outbox', row);
    }
    const r = await S.net.request('send', { chat: row.chat, cid: row.cid, text: row.text || '', entities: row.entities || [], media: row.mediaId, album: row.album || '', topic: row.topic || 0 }, 30000);
    uploads.delete(row.cid);
    S.cache.del('outbox', row.cid);
    if (m.local && m.local.url) setTimeout(() => URL.revokeObjectURL(m.local.url), 4000);
    for (const real of (r && r.messages) || []) hooks.accept(real);
  } catch (e) {
    if (e && e.cancelled) return;
    if (e && (e.code === 'offline' || e.code === 'timeout')) return; // stays in the outbox; resumes on reconnect
    m.failed = true; uploads.delete(row.cid); S.cache.del('outbox', row.cid);
    hooks.renderMessages();
    toast((e && (e.text || e.message)) || 'فایل ارسال نشد.');
  }
}

function paintProgress(m, p) {
  if (m.local) m.local.progress = p;
  const node = $('m-msgs')?.querySelector(`[data-key="${m.cid}"] .m-ring .fg`);
  if (node) node.style.strokeDashoffset = String(100 - Math.round(p * 100));
}

hooks.cancelUpload = m => {
  const up = uploads.get(m.cid); if (up) up.cancel();
  uploads.delete(m.cid); S.cache.del('outbox', m.cid);
  const list = S.msgs.get(m.chat) || [];
  S.msgs.set(m.chat, list.filter(x => x.cid !== m.cid));
  if (m.local && m.local.url) URL.revokeObjectURL(m.local.url);
  hooks.renderMessages({ flip: true });
};

// resumeMedia picks up an outbox row with a file after a reload.
hooks.resumeMedia = o => {
  if (uploads.has(o.cid)) return;
  const list = S.msgs.get(o.chat) || [];
  let m = list.find(x => x.cid === o.cid);
  if (!m) {
    const url = URL.createObjectURL(o.file);
    m = { cid: o.cid, chat: o.chat, from: S.me, kind: o.kind, text: o.text, entities: o.entities, album: o.album, at: o.at, pts: 0, topic: o.topic || 0,
      media: { kind: o.kind, name: o.name, size: o.file.size, dur: o.finish.dur, wave: o.finish.wave }, local: { url, progress: 0, name: o.name, size: o.file.size, wave: o.finish.wave, dur: o.finish.dur } };
    list.push(m); S.msgs.set(o.chat, list);
  }
  run(m, o);
};

// ---------------------------------------------------------------- sheets
function sheet(title) {
  const back = el('div', 'm-ov m-sheet-back');
  const s = el('div', 'm-sheet'); s.setAttribute('role', 'dialog'); s.setAttribute('aria-modal', 'true'); s.setAttribute('aria-label', title);
  s.append(el('div', 'm-sheet-grip'), el('h2', 'm-modal-title', title));
  back.appendChild(s); document.body.appendChild(back);
  if (!reduced()) { back.animate([{ opacity: 0 }, { opacity: 1 }], { duration: 200 }); s.animate([{ transform: 'translateY(100%)' }, { transform: 'none' }], { duration: 360, easing: SPRING }); }
  const close = () => {
    if (reduced()) { back.remove(); return; }
    s.animate([{ transform: 'none' }, { transform: 'translateY(100%)' }], { duration: 220, easing: 'cubic-bezier(.4,0,1,1)', fill: 'forwards' });
    back.animate([{ opacity: 1 }, { opacity: 0 }], { duration: 220, fill: 'forwards' }).finished.then(() => back.remove());
  };
  back.addEventListener('click', e => { if (e.target === back) close(); });
  back.addEventListener('keydown', e => { if (e.key === 'Escape') { e.stopPropagation(); close(); } });
  return { s, close };
}

function pick(accept, multiple, then) {
  const i = el('input'); i.type = 'file'; i.accept = accept; i.multiple = multiple; i.hidden = true;
  i.addEventListener('change', () => { const f = [...i.files]; i.remove(); if (f.length) then(f); });
  document.body.appendChild(i); i.click();
}

// openAttach is the attach sheet: photo or video, or a file.
export function openAttach(chat) {
  const { s, close } = sheet('پیوست');
  const row = el('div', 'm-attach-row');
  const opts = [
    ['عکس یا ویدیو', '<rect x="3" y="3" width="18" height="18" rx="2"/><circle cx="8.5" cy="8.5" r="1.5"/><path d="M21 15l-5-5L5 21"/>', () => pick('image/*,video/*', true, f => openSend(chat, f))],
    ['فایل', '<path d="M14 3H6a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9z"/><path d="M14 3v6h6"/>', () => pick('*/*', true, f => openSend(chat, f, true))],
  ];
  opts.forEach(([label, icon, run], i) => {
    const b = el('button', 'm-attach-opt'); b.type = 'button';
    const ic = el('span', 'm-attach-icon'); ic.innerHTML = svg(icon);
    b.append(ic, el('span', '', label));
    b.addEventListener('click', () => { close(); run(); });
    row.appendChild(b);
    if (!reduced()) ic.animate([{ transform: 'scale(0)' }, { transform: 'scale(1)' }], { duration: 260, delay: 80 + i * 30, easing: SPRING, fill: 'backwards' });
  });
  s.appendChild(row);
  row.querySelector('button').focus();
}
hooks.openAttach = openAttach;

// openSend shows what is about to be sent, with a caption.
export function openSend(chat, files, asFileDefault = false) {
  files = files.slice(0, 50);
  if (!asFileDefault && files.length > ALBUM_MAX && files.every(f => /^(image|video)\//.test(f.type))) { toast(`در هر آلبوم حداکثر ${fa(ALBUM_MAX)} عکس یا ویدیو؛ بقیه جدا فرستاده می‌شوند.`); }
  const { s, close } = sheet(files.length > 1 ? `${fa(files.length)} پیوست` : 'پیوست');
  const grid = el('div', 'm-send-grid');
  const urls = [];
  const paint = () => {
    grid.replaceChildren(...files.map((f, i) => {
      const c = el('div', 'm-send-item'); c.draggable = true; c.dataset.i = String(i);
      if (/^image\//.test(f.type)) { const u = URL.createObjectURL(f); urls.push(u); const img = new Image(); img.src = u; img.alt = ''; c.appendChild(img); }
      else { const n = el('span', 'm-send-name', f.name); n.dir = 'auto'; c.append(el('span', 'm-send-ext', (f.name.split('.').pop() || '').toUpperCase().slice(0, 5)), n, el('span', 'm-send-size', fmtSize(f.size))); }
      const x = el('button', 'm-send-x'); x.type = 'button'; x.setAttribute('aria-label', 'برداشتن'); x.innerHTML = svg('<path d="M18 6L6 18M6 6l12 12"/>');
      x.addEventListener('click', () => { files.splice(i, 1); files.length ? paint() : close(); });
      c.appendChild(x);
      c.addEventListener('dragstart', e => e.dataTransfer.setData('text/plain', String(i)));
      c.addEventListener('dragover', e => e.preventDefault());
      c.addEventListener('drop', e => { e.preventDefault(); const from = Number(e.dataTransfer.getData('text/plain')); const [f2] = files.splice(from, 1); files.splice(i, 0, f2); paint(); });
      return c;
    }));
  };
  paint();
  const cap = el('textarea', 'input m-send-caption'); cap.rows = 1; cap.placeholder = 'توضیح'; cap.dir = 'auto'; cap.setAttribute('aria-label', 'توضیح'); cap.id = 'm-send-caption';
  const lab = el('label', 'm-checkline'); const asFile = el('input'); asFile.type = 'checkbox'; asFile.id = 'm-send-asfile'; asFile.checked = asFileDefault;
  lab.append(asFile, el('span', '', 'بدون فشرده‌سازی، به‌صورت فایل'));
  const go = el('button', 'btn btn-primary', 'ارسال'); go.type = 'button';
  go.addEventListener('click', () => {
    close(); urls.forEach(u => URL.revokeObjectURL(u));
    const parsed = parseComposer(cap.value, new Map());
    const chunks = [];
    for (let i = 0; i < files.length; i += ALBUM_MAX) chunks.push(files.slice(i, i + ALBUM_MAX));
    chunks.forEach((c, n) => sendFiles(chat, c, { caption: n === 0 ? parsed.text : '', entities: n === 0 ? parsed.entities : [], asFile: asFile.checked }));
  });
  cap.addEventListener('keydown', e => { if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); go.click(); } });
  const foot = el('div', 'm-modal-actions'); foot.appendChild(go);
  s.append(grid, cap, lab, foot);
  cap.focus();
}

// Drag and drop onto the chat, and pictures pasted into the message bar.
export function wireDrop(pane) {
  let depth = 0;
  const hint = () => pane.querySelector('.m-drop') || pane.appendChild(Object.assign(el('div', 'm-drop', 'فایل‌ها را اینجا رها کنید'), {}));
  pane.addEventListener('dragenter', e => { if (!S.active || ![...(e.dataTransfer.types || [])].includes('Files')) return; e.preventDefault(); depth++; hint().classList.add('on'); });
  pane.addEventListener('dragover', e => { if (S.active && [...(e.dataTransfer.types || [])].includes('Files')) e.preventDefault(); });
  pane.addEventListener('dragleave', () => { if (--depth <= 0) { depth = 0; pane.querySelector('.m-drop')?.classList.remove('on'); } });
  pane.addEventListener('drop', e => {
    depth = 0; pane.querySelector('.m-drop')?.classList.remove('on');
    const files = [...(e.dataTransfer.files || [])]; if (!files.length || !S.active) return;
    e.preventDefault(); openSend(S.chats.get(S.active), files);
  });
  pane.addEventListener('paste', e => {
    const files = [...((e.clipboardData && e.clipboardData.files) || [])];
    if (!files.length || !S.active || !e.target.closest('.m-compose')) return;
    e.preventDefault(); openSend(S.chats.get(S.active), files);
  });
}
hooks.wireDrop = wireDrop;

hooks.loadDoc = async m => {
  if (m.docLoading || !m.id) return;
  m.docLoading = true;
  const r = await fetch(`/api/chat/chats/${m.chat}/messages/${m.id}/doc`, { credentials: 'same-origin' }).catch(() => null);
  m.doc = r && r.ok ? (await r.json()).doc : { denied: true };
  hooks.repaint && hooks.repaint(m);
};
