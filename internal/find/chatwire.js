// chatwire.js: the open chat's gestures and its message menu (docs/chat/
// SPEC.md 8.4): right click or long press, swipe to reply, double click for a
// heart, spoilers, quotes, links, mentions, reaction chips and the viewer.
import { reduced, SPRING } from '/chatcore.js';
import { S, hooks, vibrate } from '/chatstate.js';
import { revealSpoiler } from '/chatfmt.js';
import { openMenu, confirmLink, showReactors } from '/chatmenu.js';
import { I, keyOf, copy, heart, doReact, doPin, doForward, doDelete, toggleSelect, jumpTo } from '/chatpane.js';

// -------------------------------------------------------------- the menu
function menuFor(m, chat, x, y, node) {
  const mine = m.from === S.me;
  const items = [];
  const selText = quoteFrom(node, m);
  if (m.pts) items.push({ icon: I.reply, label: selText ? 'پاسخ با نقل قول' : 'پاسخ', run: () => hooks.setDraft('reply', m, selText) });
  if (mine && m.pts && !m.fwd) items.push({ icon: I.edit, label: 'ویرایش', run: () => hooks.setDraft('edit', m) });
  items.push({ icon: I.copy, label: selText ? 'رونوشت بخش انتخاب‌شده' : 'رونوشت متن', run: () => copy(selText ? selText.text : m.text) });
  if (m.id) items.push({ icon: I.pin, label: m.pinned || (S.pins.get(chat.id) || []).some(p => p.id === m.id) ? 'برداشتن سنجاق' : 'سنجاق کردن', run: () => doPin(m, !((S.pins.get(chat.id) || []).some(p => p.id === m.id))) });
  if (m.id && chat.type !== 'secret') items.push({ icon: I.fwd, label: 'هدایت', run: () => doForward([m.id], chat) });
  if (m.id) items.push({ icon: I.check, label: 'انتخاب', run: () => toggleSelect(m) });
  if (m.id) items.push({ icon: I.trash, label: 'حذف', danger: true, run: () => doDelete([m.id], chat) });
  hooks.menuItems && hooks.menuItems(m, chat, items);
  openMenu({ x, y, bubble: node, items, reactions: m.id ? S.reactions : null, mine: m.mine || [], onReact: (e, on) => doReact(m, e, on) });
}

// quoteFrom: the part of the message the person selected, as a quote.
function quoteFrom(node, m) {
  const s = getSelection();
  if (!s || s.isCollapsed || !node || !node.contains(s.anchorNode) || !node.contains(s.focusNode)) return null;
  const t = s.toString().trim();
  if (!t || t.length > 1024) return null;
  const at = (m.text || '').indexOf(t);
  return at >= 0 ? { text: t, offset: at } : null;
}

// wire: the open chat's gestures, through one set of listeners on the list.
export function wirePane(box) {
  const msgOf = node => {
    const b = node && node.closest('.m-bubble'); if (!b) return [null, null];
    const list = S.msgs.get(S.active) || [];
    return [list.find(m => keyOf(m) === b.dataset.key) || null, b];
  };
  box.addEventListener('contextmenu', e => {
    const [m, b] = msgOf(e.target); if (!m) return;
    if (e.target.closest('a.m-link')) return; // the browser's own link menu
    e.preventDefault();
    if (S.sel.size) { toggleSelect(m); return; }
    menuFor(m, S.chats.get(S.active), e.clientX, e.clientY, b);
  });
  box.addEventListener('click', async e => {
    const t = e.target;
    const sp = t.closest('.m-spoiler[role="button"]');
    if (sp) { e.preventDefault(); S.revealed.add(sp.dataset.k); revealSpoiler(sp, e.clientX, e.clientY); return; }
    const [m, b] = msgOf(t);
    if (S.sel.size && m) { e.preventDefault(); toggleSelect(m); return; }
    const view = t.closest('[data-view]');
    if (view && hooks.openViewer) {
      const key = view.dataset.key || (b && b.dataset.key);
      const list = S.msgs.get(S.active) || [];
      const target = list.find(x => keyOf(x) === key) || m;
      if (target && target.media && !target.local) { hooks.openViewer(target, view); return; }
    }
    const q = t.closest('.m-quote.exp');
    if (q) { const open = !q.classList.contains('open'); q.classList.toggle('open', open); q.setAttribute('aria-expanded', open ? 'true' : 'false'); open ? S.expanded.add(q.dataset.k) : S.expanded.delete(q.dataset.k); return; }
    const a = t.closest('a.m-link');
    if (a && a.dataset.hidden) { e.preventDefault(); if (await confirmLink(a.href)) window.open(a.href, '_blank', 'noopener,noreferrer'); return; }
    const men = t.closest('.m-mention[data-user]');
    if (men && men.dataset.user && men.dataset.user !== S.me) { hooks.start(men.dataset.user); return; }
    const j = t.closest('.m-reply[data-jump]');
    if (j) { jumpTo(Number(j.dataset.jump)); return; }
    const chip = t.closest('.m-chip');
    if (chip && m) { doReact(m, chip.dataset.emoji, !chip.classList.contains('on')); return; }
    if (m && m.failed && b.dataset.failed) { hooks.resend(m); }
  });
  box.addEventListener('keydown', e => {
    const sp = e.target.closest && e.target.closest('.m-spoiler[role="button"]');
    if (sp && (e.key === 'Enter' || e.key === ' ')) { e.preventDefault(); const r = sp.getBoundingClientRect(); S.revealed.add(sp.dataset.k); revealSpoiler(sp, r.left + r.width / 2, r.top + r.height / 2); }
  });
  box.addEventListener('dblclick', e => {
    const [m] = msgOf(e.target); if (!m || !m.id || S.sel.size || e.target.closest('a, .m-chip, .m-reply')) return;
    getSelection()?.removeAllRanges();
    heart(e.clientX, e.clientY);
    if (!(m.mine || []).includes('❤️')) doReact(m, '❤️', true);
  });
  // Who reacted: a long press or right click on a chip.
  box.addEventListener('contextmenu', async e => {
    const chip = e.target.closest('.m-chip'); if (!chip) return;
    e.preventDefault(); e.stopImmediatePropagation();
    const [m] = msgOf(chip); if (!m || !m.id) return;
    const r = await fetch(`/api/chat/chats/${m.chat}/messages/${m.id}/reactions`, { credentials: 'same-origin' }).catch(() => null);
    if (r && r.ok) showReactors(chip, (await r.json()).reactions);
  }, true);
  // Touch: a long press opens the menu; a swipe toward the start replies.
  let press = null;
  box.addEventListener('pointerdown', e => {
    if (e.pointerType === 'mouse') return;
    const [m, b] = msgOf(e.target); if (!m) return;
    press = { m, b, x: e.clientX, y: e.clientY, t: performance.now(), dx: 0, swiping: false, timer: setTimeout(() => { if (press && !press.swiping) { const p = press; press = null; S.sel.size ? toggleSelect(p.m) : menuFor(p.m, S.chats.get(S.active), p.x, p.y, p.b); } }, 450) };
  }, { passive: true });
  box.addEventListener('pointermove', e => {
    if (!press) return;
    const dx = e.clientX - press.x, dy = e.clientY - press.y;
    if (!press.swiping && Math.abs(dy) > 10) { clearTimeout(press.timer); press = null; return; }
    if (!press.swiping && dx > 12 && Math.abs(dx) > Math.abs(dy) * 1.5 && press.m.pts && !S.sel.size) { press.swiping = true; clearTimeout(press.timer); }
    if (press.swiping) {
      press.dx = Math.max(0, Math.min(dx, 90));
      press.b.style.transform = `translateX(${press.dx}px)`;
      press.b.classList.toggle('m-swipe-armed', press.dx > 60);
      if (press.dx > 60 && !press.armed) { press.armed = true; vibrate(15); }
    }
  }, { passive: true });
  const release = () => {
    if (!press) return;
    clearTimeout(press.timer);
    const p = press; press = null;
    if (!p.swiping) return;
    const v = p.dx / Math.max(1, performance.now() - p.t);
    p.b.classList.remove('m-swipe-armed');
    if (!reduced()) p.b.animate([{ transform: `translateX(${p.dx}px)` }, { transform: 'none' }], { duration: 260, easing: SPRING });
    p.b.style.transform = '';
    if (p.dx > 60 || v > 0.5) hooks.setDraft('reply', p.m);
  };
  box.addEventListener('pointerup', release);
  box.addEventListener('pointercancel', release);
}
