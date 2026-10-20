// photoedit.js: crop, zoom and turn a picture before it is used -- required
// for a profile picture (a square shown as a circle), offered for a photo
// about to be sent. One finger moves it, two fingers or the slider zoom it,
// the button turns it a quarter; the picture always covers the frame, so the
// result never has an empty edge. Resolves with the edited File, or null when
// the person gives up; rejects when the browser cannot open the picture at
// all (a HEIC on Windows), so the caller can say so instead of doing nothing.
//
//   const f = await editPhoto(file, { shape: 'circle', size: 512 });   // profile
//   const f = await editPhoto(file, { aspects: true });                // a photo

const STYLE = `
.pe { position: fixed; inset: 0; z-index: 2147483000; display: grid; grid-template-rows: auto minmax(0, 1fr) auto;
  background: #0b0f14; color: #f2f5f8; font: inherit; -webkit-user-select: none; user-select: none; -webkit-touch-callout: none;
  padding: env(safe-area-inset-top, 0px) 0 env(safe-area-inset-bottom, 0px); }
.pe-top { display: flex; align-items: center; gap: 8px; padding: 10px 12px; }
.pe-top h2 { flex: 1; margin: 0; font-size: 16px; font-weight: 700; text-align: center; }
.pe-btn { min-height: 40px; padding: 0 16px; border-radius: 999px; border: 0; font: inherit; font-weight: 700; cursor: pointer;
  background: rgb(255 255 255 / .1); color: inherit; }
.pe-btn.go { background: var(--accent, #4f8cff); color: #fff; }
.pe-btn:focus-visible, .pe-chip:focus-visible, .pe-icon:focus-visible, .pe-zoom:focus-visible { outline: 2px solid #fff; outline-offset: 2px; }
.pe-stage { position: relative; overflow: hidden; touch-action: none; cursor: grab; }
.pe-stage:active { cursor: grabbing; }
.pe-stage canvas { position: absolute; inset: 0; width: 100%; height: 100%; display: block; }
.pe-tools { display: grid; gap: 10px; padding: 10px 16px 14px; }
.pe-row { display: flex; align-items: center; gap: 10px; }
.pe-chips { display: flex; gap: 6px; overflow-x: auto; scrollbar-width: none; padding-bottom: 2px; }
.pe-chip { flex: none; min-height: 34px; padding: 0 12px; border-radius: 999px; border: 1px solid rgb(255 255 255 / .22);
  background: transparent; color: inherit; font: inherit; cursor: pointer; }
.pe-chip[aria-pressed="true"] { background: #f2f5f8; color: #0b0f14; border-color: #f2f5f8; }
.pe-icon { flex: none; width: 40px; height: 40px; display: grid; place-items: center; border-radius: 50%; border: 0;
  background: rgb(255 255 255 / .1); color: inherit; cursor: pointer; }
.pe-icon svg { width: 22px; height: 22px; fill: none; stroke: currentColor; stroke-width: 2; stroke-linecap: round; stroke-linejoin: round; }
.pe-zoom { flex: 1; accent-color: var(--accent, #4f8cff); }
.pe-hint { margin: 0; font-size: 13px; opacity: .7; text-align: center; }
`;

const ASPECTS = [['اصلی', 0], ['مربع', 1], ['۴:۳', 4 / 3], ['۳:۴', 3 / 4], ['۱۶:۹', 16 / 9], ['۹:۱۶', 9 / 16]];
const MAX_SIDE = 2560; // a photo's own pixels are kept up to this; sending compresses it further
const ZOOM_MAX = 5;    // times the smallest zoom that still covers the frame

function load(file) {
  return new Promise((resolve, reject) => {
    const url = URL.createObjectURL(file);
    const img = new Image();
    img.decoding = 'async';
    img.onload = () => resolve({ img, url });
    img.onerror = () => { URL.revokeObjectURL(url); reject(new Error('not a picture')); };
    img.src = url; // the browser applies the camera's orientation (EXIF) itself
  });
}

function node(tag, cls, text) {
  const n = document.createElement(tag);
  if (cls) n.className = cls;
  if (text != null) n.textContent = text;
  return n;
}

export async function editPhoto(file, opts = {}) {
  const circle = opts.shape === 'circle';
  let aspect = circle ? 1 : (opts.aspect || 0); // 0: the picture's own shape
  const { img, url } = await load(file);
  if (!document.getElementById('pe-style')) {
    const st = node('style'); st.id = 'pe-style'; st.textContent = STYLE; document.head.appendChild(st);
  }

  return new Promise(resolve => {
    const prevFocus = document.activeElement;
    const box = node('div', 'pe'); box.dir = 'rtl';
    box.setAttribute('role', 'dialog'); box.setAttribute('aria-modal', 'true');
    const title = opts.title || (circle ? 'برش عکس نمایه' : 'ویرایش عکس');
    box.setAttribute('aria-label', title);
    const top = node('div', 'pe-top');
    const cancel = node('button', 'pe-btn', 'انصراف'); cancel.type = 'button';
    const ok = node('button', 'pe-btn go', 'تأیید'); ok.type = 'button';
    top.append(cancel, node('h2', '', title), ok);
    const stage = node('div', 'pe-stage');
    const cv = node('canvas'); cv.setAttribute('aria-hidden', 'true');
    stage.appendChild(cv);
    const tools = node('div', 'pe-tools');
    let chips = null;
    if (!circle && opts.aspects) {
      chips = node('div', 'pe-chips'); chips.setAttribute('role', 'group'); chips.setAttribute('aria-label', 'نسبت برش');
      for (const [label, a] of ASPECTS) {
        const c = node('button', 'pe-chip', label); c.type = 'button'; c.dataset.a = String(a);
        c.setAttribute('aria-pressed', String(a === aspect));
        c.addEventListener('click', () => { aspect = a; for (const x of chips.children) x.setAttribute('aria-pressed', String(x === c)); layout(true); });
        chips.appendChild(c);
      }
      tools.appendChild(chips);
    }
    const row = node('div', 'pe-row');
    const turn = node('button', 'pe-icon'); turn.type = 'button'; turn.setAttribute('aria-label', 'چرخاندن');
    turn.innerHTML = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M3 12a9 9 0 1 0 3-6.7"/><path d="M3 4v5h5"/></svg>';
    const zoom = node('input', 'pe-zoom'); zoom.type = 'range'; zoom.min = '1'; zoom.max = String(ZOOM_MAX); zoom.step = '0.01'; zoom.value = '1';
    zoom.setAttribute('aria-label', 'بزرگ‌نمایی');
    const reset = node('button', 'pe-icon'); reset.type = 'button'; reset.setAttribute('aria-label', 'بازگرداندن');
    reset.innerHTML = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 4h6M4 4v6M20 20h-6M20 20v-6M4 4l6 6M20 20l-6-6"/></svg>';
    row.append(turn, zoom, reset);
    tools.append(row, node('p', 'pe-hint', 'با کشیدن جابه‌جا کنید؛ با دو انگشت یا نوار، بزرگ و کوچک کنید.'));
    box.append(top, stage, tools);
    document.body.appendChild(box);
    const bodyOverflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';

    // The picture: quarter turns q, zoom s (screen px per picture px), and its
    // centre's offset (ox, oy) from the frame's centre, in screen px.
    let q = 0, s = 1, ox = 0, oy = 0, W = 0, H = 0, fw = 0, fh = 0, dpr = 1;
    const iw = img.naturalWidth, ih = img.naturalHeight;
    const turned = () => (q % 2 ? [ih, iw] : [iw, ih]);
    const minScale = () => { const [rw, rh] = turned(); return Math.max(fw / rw, fh / rh); };
    const clamp = () => {
      const [rw, rh] = turned();
      s = Math.min(Math.max(s, minScale()), minScale() * ZOOM_MAX);
      const mx = Math.max(0, (rw * s - fw) / 2), my = Math.max(0, (rh * s - fh) / 2);
      ox = Math.min(mx, Math.max(-mx, ox)); oy = Math.min(my, Math.max(-my, oy));
      zoom.value = String(s / minScale());
    };
    function layout(fresh) {
      const r = stage.getBoundingClientRect();
      W = Math.max(1, r.width); H = Math.max(1, r.height); dpr = Math.min(3, window.devicePixelRatio || 1);
      cv.width = Math.round(W * dpr); cv.height = Math.round(H * dpr);
      const [rw, rh] = turned();
      const a = aspect || rw / rh;
      const pad = 24, aw = W - pad * 2, ah = H - pad * 2;
      if (aw / ah > a) { fh = ah; fw = ah * a; } else { fw = aw; fh = aw / a; }
      if (fresh) { s = minScale(); ox = 0; oy = 0; }
      clamp(); draw();
    }
    function place(ctx) { // the picture, centred on the frame plus its offset
      ctx.translate(ox, oy); ctx.rotate(-q * Math.PI / 2); ctx.scale(s, s);
      ctx.drawImage(img, -iw / 2, -ih / 2);
    }
    let raf = 0;
    function draw() {
      raf = 0;
      const ctx = cv.getContext('2d');
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
      ctx.clearRect(0, 0, W, H);
      ctx.save(); ctx.translate(W / 2, H / 2); place(ctx); ctx.restore();
      const x = (W - fw) / 2, y = (H - fh) / 2;
      ctx.save();
      ctx.beginPath(); ctx.rect(0, 0, W, H);
      if (circle) { ctx.moveTo(W / 2 + fw / 2, H / 2); ctx.arc(W / 2, H / 2, fw / 2, 0, Math.PI * 2, true); } else ctx.rect(x + fw, y, -fw, fh);
      ctx.fillStyle = 'rgba(0,0,0,.6)'; ctx.fill('evenodd');
      ctx.strokeStyle = 'rgba(255,255,255,.9)'; ctx.lineWidth = 1.5;
      ctx.beginPath();
      if (circle) ctx.arc(W / 2, H / 2, fw / 2, 0, Math.PI * 2); else ctx.rect(x, y, fw, fh);
      ctx.stroke();
      if (dragging) { // thirds while moving, to line things up
        ctx.strokeStyle = 'rgba(255,255,255,.35)'; ctx.lineWidth = 1; ctx.beginPath();
        for (const k of [1, 2]) { ctx.moveTo(x + fw * k / 3, y); ctx.lineTo(x + fw * k / 3, y + fh); ctx.moveTo(x, y + fh * k / 3); ctx.lineTo(x + fw, y + fh * k / 3); }
        ctx.stroke();
      }
      ctx.restore();
    }
    const redraw = () => { if (!raf) raf = requestAnimationFrame(draw); };
    function zoomAt(f, cx, cy) { // keep the point under (cx, cy) where it is
      const before = s; s *= f; clamp();
      const k = s / before, px = cx - W / 2, py = cy - H / 2;
      ox = px - (px - ox) * k; oy = py - (py - oy) * k; clamp(); redraw();
    }

    // Fingers and the mouse.
    const pts = new Map(); let dragging = false, last = null;
    const spread = () => { const [a, b] = [...pts.values()]; return Math.hypot(a.x - b.x, a.y - b.y); };
    const mid = () => { const [a, b] = [...pts.values()]; return { x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 }; };
    stage.addEventListener('pointerdown', e => {
      stage.setPointerCapture(e.pointerId);
      const r = stage.getBoundingClientRect();
      pts.set(e.pointerId, { x: e.clientX - r.left, y: e.clientY - r.top });
      dragging = true; last = pts.size === 2 ? { d: spread(), m: mid() } : null; redraw();
    });
    stage.addEventListener('pointermove', e => {
      if (!pts.has(e.pointerId)) return;
      const r = stage.getBoundingClientRect();
      const prev = pts.get(e.pointerId), now = { x: e.clientX - r.left, y: e.clientY - r.top };
      pts.set(e.pointerId, now);
      if (pts.size === 1) { ox += now.x - prev.x; oy += now.y - prev.y; clamp(); redraw(); return; }
      if (pts.size === 2 && last) {
        const d = spread(), m = mid();
        ox += m.x - last.m.x; oy += m.y - last.m.y;
        zoomAt(d / (last.d || d), m.x, m.y);
        last = { d, m };
      }
    });
    const lift = e => { pts.delete(e.pointerId); last = pts.size === 2 ? { d: spread(), m: mid() } : null; if (!pts.size) { dragging = false; redraw(); } };
    stage.addEventListener('pointerup', lift);
    stage.addEventListener('pointercancel', lift);
    stage.addEventListener('wheel', e => {
      e.preventDefault();
      const r = stage.getBoundingClientRect();
      zoomAt(Math.exp(-e.deltaY * 0.0015), e.clientX - r.left, e.clientY - r.top);
    }, { passive: false });
    zoom.addEventListener('input', () => zoomAt(minScale() * Number(zoom.value) / s, W / 2, H / 2));
    turn.addEventListener('click', () => { q = (q + 1) % 4; layout(true); });
    reset.addEventListener('click', () => { q = 0; layout(true); });

    const onResize = () => layout(false);
    window.addEventListener('resize', onResize);
    const onKey = e => {
      if (e.key === 'Escape') { e.preventDefault(); done(false); return; }
      if (e.key === 'Enter' && e.target === document.body) { e.preventDefault(); done(true); return; }
      const step = e.shiftKey ? 40 : 10;
      const moves = { ArrowLeft: [step, 0], ArrowRight: [-step, 0], ArrowUp: [0, step], ArrowDown: [0, -step] };
      if (moves[e.key] && e.target !== zoom) { e.preventDefault(); ox += moves[e.key][0]; oy += moves[e.key][1]; clamp(); redraw(); }
      else if (e.key === '+' || e.key === '=') { e.preventDefault(); zoomAt(1.1, W / 2, H / 2); }
      else if (e.key === '-') { e.preventDefault(); zoomAt(1 / 1.1, W / 2, H / 2); }
      else if (e.key === 'r' || e.key === 'R') { e.preventDefault(); q = (q + 1) % 4; layout(true); }
      else if (e.key === 'Tab') { // stay inside the dialog
        const f = [...box.querySelectorAll('button, input')];
        const i = f.indexOf(document.activeElement);
        if (e.shiftKey && i <= 0) { e.preventDefault(); f[f.length - 1].focus(); }
        else if (!e.shiftKey && i === f.length - 1) { e.preventDefault(); f[0].focus(); }
      }
    };
    document.addEventListener('keydown', onKey);

    function render() { // the frame's contents at full quality
      const srcW = fw / s, srcH = fh / s; // the frame, in the picture's own pixels
      let outW, outH;
      if (opts.size) { outW = opts.size; outH = Math.round(opts.size / (fw / fh)); }
      else { const k = Math.min(1, MAX_SIDE / Math.max(srcW, srcH)); outW = Math.max(1, Math.round(srcW * k)); outH = Math.max(1, Math.round(srcH * k)); }
      const out = node('canvas'); out.width = outW; out.height = outH;
      const ctx = out.getContext('2d');
      ctx.fillStyle = '#fff'; ctx.fillRect(0, 0, outW, outH); // a transparent picture lands on white, not black
      ctx.imageSmoothingQuality = 'high';
      ctx.translate(outW / 2, outH / 2); ctx.scale(outW / fw, outH / fh); place(ctx);
      return new Promise(r => out.toBlob(r, 'image/jpeg', 0.9));
    }
    async function done(keep) {
      document.removeEventListener('keydown', onKey);
      window.removeEventListener('resize', onResize);
      let result = null;
      if (keep) {
        ok.disabled = true;
        const blob = await render();
        if (blob) result = new File([blob], (file.name || 'photo').replace(/\.\w+$/, '') + '.jpg', { type: 'image/jpeg' });
      }
      box.remove(); URL.revokeObjectURL(url);
      document.body.style.overflow = bodyOverflow;
      if (prevFocus && prevFocus.focus) prevFocus.focus();
      resolve(result);
    }
    cancel.addEventListener('click', () => done(false));
    ok.addEventListener('click', () => done(true));
    requestAnimationFrame(() => { layout(true); ok.focus(); });
  });
}
