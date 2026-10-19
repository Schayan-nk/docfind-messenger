// chatviewer.js: the media viewer (docs/chat/SPEC.md 8.3, 8.6). The picture
// grows out of its bubble; arrows, keys and a swipe move between the chat's
// photos and videos; a double click or the wheel zooms; dragging down closes
// it, following the finger, decided by the release speed.
import { reduced } from '/chatcore.js';
import { el, svg, S, hooks, nameOf, dayFmt, timeFmt } from '/chatstate.js';
import { mediaURL } from '/chatmedia.js';

let open = null;

const viewable = chatID => (S.msgs.get(chatID) || []).filter(m => m.media && !m.local && (m.kind === 'photo' || m.kind === 'video'));

export function openViewer(m, from) {
  if (open) open.close();
  const list = viewable(m.chat);
  let i = Math.max(0, list.findIndex(x => x.id === m.id));
  const back = el('div', 'm-viewer'); back.setAttribute('role', 'dialog'); back.setAttribute('aria-modal', 'true'); back.setAttribute('aria-label', 'نمایش رسانه');
  const top = el('div', 'm-viewer-top');
  const who = el('div', 'm-viewer-who');
  const dl = el('a', 'm-icon-btn'); dl.setAttribute('aria-label', 'دریافت'); dl.title = 'دریافت'; dl.innerHTML = svg('<path d="M12 3v12M7 10l5 5 5-5M5 21h14"/>');
  const x = el('button', 'm-icon-btn'); x.type = 'button'; x.setAttribute('aria-label', 'بستن'); x.innerHTML = svg('<path d="M18 6L6 18M6 6l12 12"/>');
  top.append(who, dl, x);
  const stage = el('div', 'm-viewer-stage');
  const prev = el('button', 'm-viewer-nav prev'); prev.type = 'button'; prev.setAttribute('aria-label', 'قبلی'); prev.innerHTML = svg('<path d="M9 18l6-6-6-6"/>');
  const next = el('button', 'm-viewer-nav next'); next.type = 'button'; next.setAttribute('aria-label', 'بعدی'); next.innerHTML = svg('<path d="M15 18l-6-6 6-6"/>');
  back.append(top, stage, prev, next);
  document.body.appendChild(back);
  let zoom = 1, node = null;

  const show = (dir, fromRect) => {
    const cur = list[i]; if (!cur) return;
    const d = new Date(cur.at);
    who.replaceChildren(Object.assign(el('b', '', nameOf(cur.from, S.chats.get(cur.chat))), { dir: 'auto' }), el('span', '', dayFmt.format(d) + '، ' + timeFmt.format(d)));
    dl.href = mediaURL(cur.media.id) + '?download=1';
    let n;
    if (cur.kind === 'video') { n = document.createElement('video'); n.controls = true; n.autoplay = true; n.playsInline = true; n.src = mediaURL(cur.media.id); if (cur.media.thumb) n.poster = mediaURL(cur.media.id, true); }
    else { n = new Image(); n.alt = ''; n.src = mediaURL(cur.media.id); }
    n.className = 'm-viewer-media';
    const old = node; node = n; zoom = 1;
    stage.appendChild(n);
    if (old) {
      if (reduced()) old.remove();
      else {
        old.animate([{ transform: 'none', opacity: 1 }, { transform: `translateX(${dir * 30}%)`, opacity: 0 }], { duration: 240, easing: 'cubic-bezier(0.2,0,0,1)' }).finished.then(() => old.remove());
        n.animate([{ transform: `translateX(${-dir * 30}%)`, opacity: 0 }, { transform: 'none', opacity: 1 }], { duration: 240, easing: 'cubic-bezier(0.2,0,0,1)' });
      }
    } else if (fromRect && !reduced()) {
      // The thumbnail grows into the viewer (shared element).
      requestAnimationFrame(() => {
        const to = n.getBoundingClientRect();
        if (!to.width) return;
        const sx = fromRect.width / to.width, sy = fromRect.height / to.height;
        n.animate([{ transform: `translate(${fromRect.left - to.left}px, ${fromRect.top - to.top}px) scale(${sx}, ${sy})`, transformOrigin: 'top left' }, { transform: 'none', transformOrigin: 'top left' }], { duration: 300, easing: 'cubic-bezier(0.2,0,0,1)' });
      });
    }
    prev.hidden = i <= 0; next.hidden = i >= list.length - 1;
  };
  const go = dir => { const j = i + dir; if (j < 0 || j >= list.length) return; i = j; show(dir); };
  const setZoom = z => { zoom = Math.min(4, Math.max(1, z)); if (node) node.style.transform = zoom === 1 ? '' : `scale(${zoom})`; };
  const close = () => {
    if (!open) return; open = null;
    removeEventListener('keydown', keys, true);
    if (reduced()) { back.remove(); return; }
    back.animate([{ opacity: 1 }, { opacity: 0 }], { duration: 160 }).finished.then(() => back.remove());
  };
  const keys = e => {
    if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); close(); }
    if (e.key === 'ArrowLeft') go(1);   // RTL: left is the next one
    if (e.key === 'ArrowRight') go(-1);
  };
  addEventListener('keydown', keys, true);
  x.addEventListener('click', close);
  prev.addEventListener('click', () => go(-1));
  next.addEventListener('click', () => go(1));
  stage.addEventListener('dblclick', () => setZoom(zoom > 1 ? 1 : 2));
  stage.addEventListener('wheel', e => { if (node && node.tagName === 'IMG') { e.preventDefault(); setZoom(zoom - e.deltaY / 400); } }, { passive: false });
  stage.addEventListener('click', e => { if (e.target === stage) close(); });
  // Drag down to close, sideways to move on: the release speed decides.
  let sx = 0, sy = 0, t0 = 0, dx = 0, dy = 0, drag = false;
  stage.addEventListener('pointerdown', e => { if (zoom > 1 || e.target.tagName === 'VIDEO') return; drag = true; sx = e.clientX; sy = e.clientY; t0 = performance.now(); dx = dy = 0; stage.setPointerCapture(e.pointerId); });
  stage.addEventListener('pointermove', e => {
    if (!drag || !node) return;
    dx = e.clientX - sx; dy = e.clientY - sy;
    if (Math.abs(dy) > Math.abs(dx)) { node.style.transform = `translateY(${Math.max(0, dy)}px) scale(${1 - Math.min(0.3, Math.max(0, dy) / 1200)})`; back.style.backgroundColor = `rgb(0 0 0 / ${0.92 - Math.min(0.6, Math.max(0, dy) / 600)})`; }
    else node.style.transform = `translateX(${dx}px)`;
  });
  stage.addEventListener('pointerup', () => {
    if (!drag || !node) return; drag = false;
    const ms = Math.max(1, performance.now() - t0);
    node.style.transform = ''; back.style.backgroundColor = '';
    if (dy > 120 || dy / ms > 0.6) close();
    else if (Math.abs(dx) > 80 || Math.abs(dx) / ms > 0.5) go(dx > 0 ? 1 : -1);
  });
  open = { close };
  show(0, from ? from.getBoundingClientRect() : null);
  x.focus();
  if (!reduced()) back.animate([{ backgroundColor: 'rgb(0 0 0 / 0)' }, { backgroundColor: 'rgb(0 0 0 / .92)' }], { duration: 240 });
}
hooks.openViewer = openViewer;
