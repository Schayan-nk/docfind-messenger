// chatmenu.js: the messenger's overlays: the message menu with its reaction
// bar, the delete question, the forward sheet, who reacted, and the question
// before a link whose words are not its address opens. One overlay at a
// time; Escape or a press outside closes it.
import { SPRING, reduced } from '/chatcore.js';
import { el, svg, S, fa, vibrate } from '/chatstate.js';

let current = null;
export function closeOverlay() { if (current) { const c = current; current = null; c(); } }
addEventListener('keydown', e => { if (e.key === 'Escape' && current) { e.preventDefault(); closeOverlay(); } }, true);

const ease = 'cubic-bezier(0.2, 0, 0, 1)';
function fadeOut(node, then) {
  if (reduced() || document.hidden) { node.remove(); then && then(); return; }
  node.animate([{ opacity: 1 }, { opacity: 0 }], { duration: 120, easing: 'linear' }).finished.finally(() => { node.remove(); then && then(); });
}
function trapFocus(box) {
  const prev = document.activeElement;
  queueMicrotask(() => { const f = box.querySelector('button:not([disabled]), input, [tabindex="0"]'); f && f.focus(); });
  return () => { if (prev && prev.focus && document.contains(prev)) prev.focus(); };
}

// ------------------------------------------------------------ message menu
//
// The pressed bubble lifts a little above a dimmed, blurred chat; the menu
// grows from the press point and the reactions pop in one after another
// (SPEC 8.4). items: [{icon, label, run, danger}].
export function openMenu({ x, y, bubble, items, reactions, mine = [], onReact }) {
  closeOverlay();
  vibrate(12);
  const back = el('div', 'm-ov m-ctx-back');
  const ghost = bubble ? bubble.cloneNode(true) : null;
  if (ghost) {
    const r = bubble.getBoundingClientRect();
    ghost.classList.add('m-ctx-ghost');
    ghost.removeAttribute('id');
    Object.assign(ghost.style, { left: r.left + 'px', top: r.top + 'px', width: r.width + 'px', height: r.height + 'px' });
    ghost.setAttribute('aria-hidden', 'true');
  }
  const panel = el('div', 'm-ctx'); panel.setAttribute('role', 'menu');
  if (reactions && reactions.length) {
    const bar = el('div', 'm-react-bar'); bar.setAttribute('role', 'group'); bar.setAttribute('aria-label', 'واکنش');
    reactions.forEach((emoji, i) => {
      const b = el('button', 'm-react-opt' + (mine.includes(emoji) ? ' on' : ''), emoji); b.type = 'button';
      b.setAttribute('aria-label', 'واکنش ' + emoji); b.setAttribute('aria-pressed', mine.includes(emoji) ? 'true' : 'false');
      b.addEventListener('click', () => { closeOverlay(); onReact(emoji, !mine.includes(emoji)); });
      bar.appendChild(b);
      if (!reduced()) b.animate([{ transform: 'scale(0)' }, { transform: 'scale(1)' }], { duration: 260, delay: 60 + i * 20, easing: SPRING, fill: 'backwards' });
    });
    panel.appendChild(bar);
  }
  const list = el('div', 'm-ctx-menu');
  for (const it of items) {
    const b = el('button', 'm-ctx-item' + (it.danger ? ' danger' : '')); b.type = 'button'; b.setAttribute('role', 'menuitem');
    b.innerHTML = svg(it.icon);
    b.appendChild(el('span', '', it.label));
    b.addEventListener('click', () => { closeOverlay(); it.run(); });
    list.appendChild(b);
  }
  list.addEventListener('keydown', e => {
    const all = [...list.querySelectorAll('.m-ctx-item')], i = all.indexOf(document.activeElement);
    if (e.key === 'ArrowDown') { e.preventDefault(); all[(i + 1) % all.length].focus(); }
    if (e.key === 'ArrowUp') { e.preventDefault(); all[(i - 1 + all.length) % all.length].focus(); }
  });
  panel.appendChild(list);
  document.body.append(back);
  if (ghost) document.body.append(ghost);
  document.body.append(panel);
  // Placed at the press point, kept inside the window.
  const pw = panel.offsetWidth, ph = panel.offsetHeight, m = 8;
  const left = Math.min(Math.max(m, x - pw / 2), innerWidth - pw - m);
  const top = y + ph + m > innerHeight ? Math.max(m, y - ph) : y;
  Object.assign(panel.style, { left: left + 'px', top: top + 'px', transformOrigin: `${x - left}px ${y - top}px` });
  const restore = trapFocus(list);
  if (!reduced()) {
    back.animate([{ opacity: 0 }, { opacity: 1 }], { duration: 160, easing: 'linear' });
    panel.animate([{ transform: 'scale(.6)', opacity: 0 }, { transform: 'none', opacity: 1 }], { duration: 260, easing: SPRING });
    ghost && ghost.animate([{ transform: 'none' }, { transform: 'scale(1.03)' }], { duration: 200, easing: ease, fill: 'forwards' });
  }
  back.addEventListener('click', closeOverlay);
  back.addEventListener('contextmenu', e => { e.preventDefault(); closeOverlay(); });
  current = () => { fadeOut(back); fadeOut(panel); ghost && fadeOut(ghost); restore(); };
}

// --------------------------------------------------------------- dialogs
function modal(title, body, buttons) {
  closeOverlay();
  const back = el('div', 'm-ov m-modal-back');
  const card = el('div', 'm-modal'); card.setAttribute('role', 'dialog'); card.setAttribute('aria-modal', 'true');
  const h = el('h2', 'm-modal-title', title); h.id = 'm-modal-title'; card.setAttribute('aria-labelledby', h.id);
  card.append(h, ...body);
  const row = el('div', 'm-modal-actions');
  for (const b of buttons) row.appendChild(b);
  card.appendChild(row);
  back.appendChild(card);
  document.body.appendChild(back);
  const restore = trapFocus(card);
  if (!reduced()) card.animate([{ transform: 'scale(.9)', opacity: 0 }, { transform: 'none', opacity: 1 }], { duration: 140, easing: ease });
  return { back, card, done: () => { fadeOut(back); restore(); } };
}
const button = (label, cls) => { const b = el('button', 'btn ' + (cls || ''), label); b.type = 'button'; return b; };

// confirmDelete asks before messages go. peer is the other person's name in a
// private chat (the "also delete for" choice); null hides that choice.
export function confirmDelete({ count, peer, forAllOnly }) {
  return new Promise(resolve => {
    const body = [el('p', 'm-modal-text', count > 1 ? `${fa(count)} پیام حذف شود؟` : 'این پیام حذف شود؟')];
    let box = null;
    if (peer && !forAllOnly) {
      const lab = el('label', 'm-checkline');
      box = el('input'); box.type = 'checkbox'; box.id = 'm-del-all';
      lab.append(box, el('span', '', `برای ${peer} هم حذف شود`));
      body.push(lab);
    }
    const no = button('انصراف'), yes = button('حذف', 'm-danger');
    const m = modal(count > 1 ? 'حذف پیام‌ها' : 'حذف پیام', body, [no, yes]);
    let settled = false;
    const finish = v => { if (settled) return; settled = true; current = null; m.done(); resolve(v); };
    no.addEventListener('click', () => finish(null));
    yes.addEventListener('click', () => finish({ forAll: forAllOnly || !!(box && box.checked) }));
    m.back.addEventListener('click', e => { if (e.target === m.back) finish(null); });
    current = () => finish(null);
  });
}

// confirmLink asks before a link whose words hide its address opens.
export function confirmLink(href) {
  return new Promise(resolve => {
    const addr = el('p', 'm-modal-text m-ltr', href); addr.dir = 'ltr';
    const no = button('انصراف'), yes = button('باز کردن', 'btn-primary');
    const m = modal('باز کردن این پیوند؟', [addr], [no, yes]);
    let settled = false;
    const finish = v => { if (settled) return; settled = true; current = null; m.done(); resolve(v); };
    no.addEventListener('click', () => finish(false));
    yes.addEventListener('click', () => finish(true));
    m.back.addEventListener('click', e => { if (e.target === m.back) finish(false); });
    current = () => finish(false);
  });
}

// -------------------------------------------------------- forward sheet
//
// A sheet from the bottom (SPEC 8.3): pick the chat, choose whether the
// sender's name goes with the messages. avatar(chat) draws a row's picture.
export function pickChat({ chats, avatar }) {
  return new Promise(resolve => {
    closeOverlay();
    const back = el('div', 'm-ov m-sheet-back');
    const sheet = el('div', 'm-sheet'); sheet.setAttribute('role', 'dialog'); sheet.setAttribute('aria-modal', 'true'); sheet.setAttribute('aria-label', 'هدایت به');
    const grip = el('div', 'm-sheet-grip'); grip.setAttribute('aria-hidden', 'true');
    const title = el('h2', 'm-modal-title', 'هدایت به…');
    const q = el('input', 'input'); q.type = 'search'; q.placeholder = 'جستجوی گفت‌وگو'; q.setAttribute('aria-label', 'جستجوی گفت‌وگو'); q.id = 'm-fwd-q';
    const lab = el('label', 'm-checkline'); const show = el('input'); show.type = 'checkbox'; show.checked = true; show.id = 'm-fwd-show';
    lab.append(show, el('span', '', 'نام فرستنده نمایش داده شود'));
    const list = el('div', 'm-sheet-list'); list.setAttribute('role', 'list');
    const paint = () => {
      const f = q.value.trim().toLowerCase();
      list.replaceChildren(...chats.filter(c => !f || (c.title || '').toLowerCase().includes(f)).map(c => {
        const r = el('button', 'm-row'); r.type = 'button'; r.setAttribute('role', 'listitem');
        const n = el('div', 'm-row-name', c.title); n.dir = 'auto';
        r.append(avatar(c), n);
        r.addEventListener('click', () => finish({ chat: c.id, hideSender: !show.checked }));
        return r;
      }));
    };
    q.addEventListener('input', paint);
    sheet.append(grip, title, q, lab, list);
    back.appendChild(sheet);
    document.body.appendChild(back);
    paint();
    const restore = trapFocus(sheet);
    if (!reduced()) {
      back.animate([{ opacity: 0 }, { opacity: 1 }], { duration: 200, easing: 'linear' });
      sheet.animate([{ transform: 'translateY(100%)' }, { transform: 'none' }], { duration: 360, easing: SPRING });
    }
    // Drag down to close; the release speed decides (SPEC 8.1).
    let y0 = 0, t0 = 0, dy = 0, drag = false;
    grip.addEventListener('pointerdown', e => { drag = true; y0 = e.clientY; t0 = performance.now(); dy = 0; sheet.style.transition = 'none'; grip.setPointerCapture(e.pointerId); });
    grip.addEventListener('pointermove', e => { if (!drag) return; dy = Math.max(0, e.clientY - y0); sheet.style.transform = `translateY(${dy}px)`; });
    grip.addEventListener('pointerup', () => {
      if (!drag) return; drag = false;
      const v = dy / Math.max(1, performance.now() - t0);
      if (dy > sheet.offsetHeight * 0.35 || v > 0.6) finish(null);
      else { sheet.style.transition = ''; sheet.style.transform = ''; }
    });
    let settled = false;
    function finish(v) {
      if (settled) return; settled = true; current = null; restore();
      if (reduced()) { back.remove(); resolve(v); return; }
      sheet.animate([{ transform: getComputedStyle(sheet).transform === 'none' ? 'none' : getComputedStyle(sheet).transform }, { transform: 'translateY(100%)' }], { duration: 220, easing: 'cubic-bezier(.4,0,1,1)', fill: 'forwards' });
      fadeOut(back, () => resolve(v));
    }
    back.addEventListener('click', e => { if (e.target === back) finish(null); });
    current = () => finish(null);
  });
}

// ------------------------------------------------------- who reacted
export function showReactors(anchor, data) {
  closeOverlay();
  const back = el('div', 'm-ov m-pop-back');
  const pop = el('div', 'm-pop'); pop.setAttribute('role', 'dialog'); pop.setAttribute('aria-label', 'واکنش‌ها');
  const rows = Object.entries(data || {});
  if (!rows.length) pop.appendChild(el('div', 'm-pop-row', 'هنوز واکنشی نیست.'));
  for (const [emoji, people] of rows) for (const p of people) {
    const r = el('div', 'm-pop-row');
    const n = el('span', '', p.id === S.me ? 'شما' : p.name); n.dir = 'auto';
    r.append(el('span', 'm-pop-emoji', emoji), n);
    pop.appendChild(r);
  }
  document.body.append(back, pop);
  const a = anchor.getBoundingClientRect();
  const left = Math.min(Math.max(8, a.left), innerWidth - pop.offsetWidth - 8);
  const top = a.bottom + pop.offsetHeight + 8 > innerHeight ? a.top - pop.offsetHeight - 6 : a.bottom + 6;
  Object.assign(pop.style, { left: left + 'px', top: top + 'px' });
  if (!reduced()) pop.animate([{ transform: 'scale(.9)', opacity: 0 }, { transform: 'none', opacity: 1 }], { duration: 140, easing: ease });
  back.addEventListener('click', closeOverlay);
  current = () => { fadeOut(back); fadeOut(pop); };
}
