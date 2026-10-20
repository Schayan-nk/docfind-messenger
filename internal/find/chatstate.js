// chatstate.js: what the messenger page's modules share. The state lives in
// one object; the modules lend each other functions through hooks, so none
// of them imports another's internals.
import { Net, enter } from '/chatcore.js';

export const $ = id => document.getElementById(id);
export const el = (tag, cls, text) => { const e = document.createElement(tag); if (cls) e.className = cls; if (text != null) e.textContent = text; return e; };
export const fa = n => Number(n).toLocaleString('fa-IR');
export const timeFmt = new Intl.DateTimeFormat('fa-IR', { hour: '2-digit', minute: '2-digit' });
export const dayFmt = new Intl.DateTimeFormat('fa-IR-u-ca-persian', { day: 'numeric', month: 'long' });
export const dayKey = d => new Date(d).toDateString();
export const svg = (paths, cls = '') => `<svg class="${cls}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${paths}</svg>`;

export const S = {
  me: '', chats: new Map(), msgs: new Map(), active: 0, filter: '', folder: 'all',
  typing: new Map(), // chat -> {user, until}
  conn: 'connecting', // the socket: connecting | offline | updating | online
  cache: null, net: new Net(), loadingOlder: false, atBottom: true,
  reactions: ['👍', '❤️', '🔥', '👏', '😁', '🤔', '🎉', '🙏', '👌', '💯', '🤝', '😢', '😱', '🤩', '👎'],
  pins: new Map(),    // chat -> pinned messages, newest pin first
  pinAt: new Map(),   // chat -> index of the pin the bar shows
  sel: new Set(),     // selected message ids (selection mode when not empty)
  revealed: new Set(), expanded: new Set(), // spoilers opened, quotes expanded (this visit)
  draft: { mode: '', msg: null, quote: null, fwd: null }, // reply | edit | forward, above the composer
  mentions: new Map(), // display name -> username for the @names in the composer
  photos: new Set(),   // colleagues with a DocFind picture (/api/account/photo)
};
export const photoURL = u => '/api/account/photo?u=' + encodeURIComponent(u);

// facePhoto puts user's DocFind picture over an avatar's letter (which stays
// beneath, for when the picture cannot load).
//
// A list redraw makes every row anew, and a picture asked for lazily and
// decoded off the main thread showed the row's letter for a frame or two each
// time -- a face flickering to its initial on every new message (the
// recording of 2026-10-19). A picture shown once on this page is drawn at once
// from then on; one still on its way hides the letter rather than flashing it.
const faceShown = new Set();
export function facePhoto(a, user, has) {
  if (!user) return a;
  if (has) S.photos.add(user);
  if (!S.photos.has(user)) return a;
  const img = new Image(); img.className = 'm-av-img'; img.alt = '';
  if (faceShown.has(user)) img.decoding = 'sync';
  else {
    img.decoding = 'async'; img.loading = 'lazy';
    a.classList.add('m-av-wait');
    img.addEventListener('load', () => { faceShown.add(user); a.classList.remove('m-av-wait'); }, { once: true });
  }
  img.src = photoURL(user);
  img.addEventListener('error', () => { img.remove(); a.classList.remove('m-av-wait'); S.photos.delete(user); faceShown.delete(user); }, { once: true });
  a.prepend(img);
  return a;
}

export const hooks = { kinds: {} };

// Who a username is, as this person sees them.
export function nameOf(user, chat) {
  if (!user) return '';
  if (user === S.me) return 'شما';
  if (chat && chat.peer && chat.peer.id === user) return chat.peer.name || chat.title || user;
  for (const c of S.chats.values()) if (c.peer && c.peer.id === user) return c.peer.name || c.title;
  return user;
}

export function toast(text) {
  const t = el('div', 'toast m-toast', text); t.setAttribute('role', 'status');
  document.body.appendChild(t); enter(t, 0.95); setTimeout(() => t.remove(), 4000);
}

export const vibrate = ms => { try { navigator.vibrate && navigator.vibrate(ms); } catch { } };
