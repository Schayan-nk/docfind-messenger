// chatgcallui.js: the group call screen (docs/chat/SPEC.md 6.9, motion 8.9):
// a tile per participant (their camera, else their picture or letter), a
// ring around whoever is speaking, the shared screen on a stage above the
// grid, raised hands, who joined and who left, the participants panel (an
// admin holds a microphone back or ends the call for everyone), and the
// small floating window while the person goes back to their chats.
import { el, svg, fa, S, toast, facePhoto } from '/chatstate.js';
import { reduced, flip } from '/chatcore.js';
import { mmss, VIDEO } from '/chatcall.js';

const IC = {
  mic: '<path d="M12 1a3 3 0 0 0-3 3v8a3 3 0 0 0 6 0V4a3 3 0 0 0-3-3z"/><path d="M19 10v2a7 7 0 0 1-14 0v-2M12 19v4M8 23h8"/>',
  micOff: '<path d="M1 1l22 22M9 9v3a3 3 0 0 0 5.1 2.1M15 9.3V4a3 3 0 0 0-5.9-.6"/><path d="M17 16.9A7 7 0 0 1 5 12v-2m14 0v2a7 7 0 0 1-.1 1.2M12 19v4M8 23h8"/>',
  camOff: '<path d="M1 1l22 22M16 16v1a2 2 0 0 1-2 2H3a2 2 0 0 1-2-2V7a2 2 0 0 1 2-2h2m5.7 0H14a2 2 0 0 1 2 2v3.3l1 1L23 7v10"/>',
  screen: '<rect x="2" y="3" width="20" height="14" rx="2"/><path d="M8 21h8M12 17v4"/>',
  hand: '<path d="M18 11V6a2 2 0 0 0-4 0v5M14 10V4a2 2 0 0 0-4 0v6M10 10.5V6a2 2 0 0 0-4 0v8"/><path d="M18 8a2 2 0 1 1 4 0v6a8 8 0 0 1-8 8h-2c-2.8 0-4.5-.86-5.99-2.34l-3.6-3.6a2 2 0 0 1 2.83-2.82L7 15"/>',
  people: '<path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M22 21v-2a4 4 0 0 0-3-3.9M16 3.1a4 4 0 0 1 0 7.8"/>',
  down: '<path d="M6 9l6 6 6-6"/>',
  x: '<path d="M18 6L6 18M6 6l12 12"/>',
  leave: '<path d="M10.7 13.3a16 16 0 0 0 3.4 2.6l1.3-1.3a2 2 0 0 1 2.1-.4c.9.3 1.8.6 2.7.7a2 2 0 0 1 1.7 2v3a2 2 0 0 1-2.2 2A19.8 19.8 0 0 1 2.1 4.2 2 2 0 0 1 4.1 2h3a2 2 0 0 1 2 1.7c.1.9.4 1.8.7 2.7a2 2 0 0 1-.5 2.1L8 9.8" transform="rotate(135 12 12)"/>',
};
const hueOf = s => { let h = 0; for (const c of s) h = (h * 31 + c.codePointAt(0)) % 360; return h; };

function button(icon, label, cls = '') {
  const w = el('div', 'cl-btn ' + cls);
  const b = el('button', 'cl-round'); b.type = 'button'; b.setAttribute('aria-label', label); b.innerHTML = svg(icon);
  w.append(b, el('span', 'cl-label', label));
  return { w, b, set(icon2, label2, on, off) { b.innerHTML = svg(icon2); b.setAttribute('aria-label', label2); w.lastChild.textContent = label2; w.classList.toggle('on', !!on); b.disabled = !!off; } };
}

export function open(call, chat, act) {
  for (const old of document.querySelectorAll('.cl-root.over')) old.remove();
  const root = el('div', 'cl-root gc');
  root.setAttribute('role', 'dialog'); root.setAttribute('aria-modal', 'true'); root.setAttribute('aria-label', 'تماس گروهی ' + (chat.title || ''));
  const top = el('div', 'gc-top');
  const mini = el('button', 'cl-mini-btn'); mini.type = 'button'; mini.setAttribute('aria-label', 'بازگشت به گفت‌وگوها'); mini.innerHTML = svg(IC.down);
  const head = el('div', 'gc-title');
  const title = el('b', '', chat.title || 'تماس گروهی'); title.dir = 'auto';
  const sub = el('span', 'gc-sub', 'در حال اتصال…'); sub.setAttribute('aria-live', 'polite');
  head.append(title, sub);
  const pplB = el('button', 'cl-mini-btn'); pplB.type = 'button'; pplB.setAttribute('aria-label', 'شرکت‌کنندگان'); pplB.innerHTML = svg(IC.people);
  top.append(mini, head, pplB);
  const stage = el('div', 'gc-stage'); stage.hidden = true;
  const stageV = el('video'); Object.assign(stageV, { autoplay: true, playsInline: true, muted: true });
  const stageL = el('span', 'gc-stage-l');
  stage.append(stageV, stageL);
  const grid = el('div', 'gc-grid');
  const ctl = el('div', 'cl-controls gc-controls');
  const mic = button(IC.mic, 'بی‌صدا'), cam = button(VIDEO, 'دوربین'), share = button(IC.screen, 'اشتراک صفحه'), hand = button(IC.hand, 'دست'), out = button(IC.leave, 'خروج', 'end');
  share.w.hidden = !(navigator.mediaDevices && navigator.mediaDevices.getDisplayMedia);
  ctl.append(cam.w, share.w, hand.w, mic.w, out.w);
  const people = el('aside', 'gc-people'); people.hidden = true; people.setAttribute('aria-label', 'شرکت‌کنندگان');
  const miniCard = el('div', 'gc-minicard');
  root.append(top, stage, grid, ctl, people, miniCard);
  document.body.appendChild(root);
  if (!reduced()) root.animate([{ opacity: 0, transform: 'scale(1.03)' }, { opacity: 1, transform: 'none' }], { duration: 240, easing: 'cubic-bezier(0.2,0,0,1)' });

  const tiles = new Map(); // user -> { t, v, name, ic, level, audio }
  let members = [], admin = false, since = 0, first = true, gone = false, raf = 0;
  const tick = setInterval(paintSub, 1000);

  function paintSub() {
    const n = members.length;
    sub.textContent = n ? `${fa(n)} نفر` + (since ? ' · ' + mmss((Date.now() - since) / 1000) : '') : 'در حال اتصال…';
    miniCard.textContent = (chat.title || '') + ' · ' + sub.textContent;
  }

  // ------------------------------------------------------------ tiles
  function tileFor(m) {
    let x = tiles.get(m.id);
    if (x) return x;
    const t = el('div', 'gc-tile' + (m.id === S.me ? ' me' : '')); t.dataset.key = m.id;
    const v = el('video'); Object.assign(v, { autoplay: true, playsInline: true, muted: true });
    const av = el('div', 'cl-av', [...(m.name || '؟').trim()][0] || '؟'); av.style.background = `hsl(${hueOf(m.id)} 45% 38%)`;
    facePhoto(av, m.id, m.photo);
    const name = el('div', 'gc-name'); const nm = el('span', '', m.id === S.me ? 'شما' : m.name); nm.dir = 'auto';
    const ic = el('span', 'gc-ic'); name.append(ic, nm);
    t.append(v, av, name);
    x = { t, v, name: nm, ic, level: null, audio: null, hand: null };
    tiles.set(m.id, x);
    if (m.id === S.me && call.local) { v.srcObject = call.local; v.play().catch(() => { }); }
    attachAll(m.id);
    if (!first && !reduced()) t.animate([{ transform: 'scale(.8)', opacity: 0 }, { transform: 'none', opacity: 1 }], { duration: 260, easing: 'cubic-bezier(0.2,0,0,1)' });
    return x;
  }
  function attachAll(user) { for (const k of ['mic', 'cam', 'screen']) attach(user, k); }
  function attach(user, kind) {
    const r = call.remote.get(user), x = tiles.get(user);
    if (!r || !r[kind] || !x) return;
    if (kind === 'cam' && x.v.srcObject !== r.cam) { x.v.srcObject = r.cam; x.v.play().catch(() => { }); }
    if (kind === 'mic' && (!x.audio || x.audio.srcObject !== r.mic)) {
      if (!x.audio) { x.audio = el('audio'); x.t.appendChild(x.audio); } // goes with the tile
      x.audio.autoplay = true; x.audio.srcObject = r.mic; x.audio.play().catch(() => { });
      x.level = call.analyser(r.mic);
    }
  }
  call.addEventListener('remote', e => { attach(e.detail.user, e.detail.kind); paint(); });
  call.addEventListener('local', () => { paint(); paintButtons(); });

  function paint() {
    const byId = new Map(members.map(m => [m.id, m]));
    for (const [id, x] of tiles) {
      const m = byId.get(id); if (!m) continue;
      const me = id === S.me;
      const camOn = me ? call.cam : (m.cam && !!(call.remote.get(id) || {}).cam);
      x.t.classList.toggle('cam', !!camOn);
      x.t.classList.toggle('lost', !!m.lost);
      const micOn = me ? call.mic && !call.held : m.mic;
      x.ic.innerHTML = micOn ? '' : svg(IC.micOff); x.ic.classList.toggle('off', !micOn);
      const hasHand = me ? call.hand : m.hand;
      if (hasHand && !x.hand) { x.hand = el('span', 'gc-hand', '✋'); x.hand.setAttribute('aria-label', 'دست بالا'); x.t.appendChild(x.hand); }
      if (!hasHand && x.hand) { x.hand.remove(); x.hand = null; }
    }
    // The shared screen goes on the stage.
    const sharer = members.find(m => m.screen && m.id !== S.me && (call.remote.get(m.id) || {}).screen);
    const was = !stage.hidden;
    stage.hidden = !sharer; root.classList.toggle('staged', !!sharer);
    if (sharer) {
      const s = call.remote.get(sharer.id).screen;
      if (stageV.srcObject !== s) { stageV.srcObject = s; stageV.play().catch(() => { }); }
      stageL.textContent = 'صفحهٔ ' + sharer.name;
      if (!was && !reduced()) stage.animate([{ transform: 'scale(.6)', opacity: 0 }, { transform: 'none', opacity: 1 }], { duration: 380, easing: 'cubic-bezier(0.2,0,0,1)' });
    }
  }
  function speaking() {
    for (const x of tiles.values()) x.t.classList.toggle('speaking', !!(x.level && x.level() > 0.06));
    raf = requestAnimationFrame(speaking);
  }
  raf = requestAnimationFrame(speaking);

  // state: who is in now and what each sends.
  function state(info) {
    if (gone) return;
    const before = new Set(members.map(m => m.id));
    members = info.members || []; admin = !!info.admin;
    since = Date.parse(info.started_at) || since;
    const now = new Set(members.map(m => m.id));
    if (!first) {
      for (const m of members) if (!before.has(m.id) && m.id !== S.me) toast(m.name + ' به تماس پیوست');
      for (const id of before) if (!now.has(id) && id !== S.me) { const x = tiles.get(id); toast((x ? x.name.textContent : id) + ' از تماس بیرون رفت'); }
    }
    flip(grid, () => {
      for (const [id, x] of tiles) if (!now.has(id)) { if (x.audio) { x.audio.srcObject = null; } x.t.remove(); tiles.delete(id); }
      for (const m of members) grid.appendChild(tileFor(m).t);
    });
    grid.dataset.n = String(Math.min(members.length, 9));
    first = false;
    paint(); paintSub(); paintButtons();
    if (!people.hidden) paintPeople();
  }

  // ------------------------------------------------------------ controls
  function paintButtons() {
    mic.set(call.mic && !call.held ? IC.mic : IC.micOff, call.held ? 'بسته از سوی مدیر' : call.mic ? 'بی‌صدا' : 'صدا', !call.mic || call.held, call.held);
    cam.set(call.cam ? VIDEO : IC.camOff, 'دوربین', call.cam);
    share.set(IC.screen, call.screen ? 'پایان اشتراک' : 'اشتراک صفحه', call.screen);
    hand.set(IC.hand, call.hand ? 'پایین آوردن' : 'دست', call.hand);
  }
  mic.b.addEventListener('click', () => { if (!call.setMic(!call.mic)) toast('مدیر تماس میکروفون شما را بسته است.'); });
  cam.b.addEventListener('click', () => call.setCam(!call.cam).catch(() => toast('دوربین در دسترس نیست.')));
  share.b.addEventListener('click', () => (call.screen ? call.stopScreen() : call.shareScreen()).catch(() => { }));
  hand.b.addEventListener('click', () => call.setHand(!call.hand));
  out.b.addEventListener('click', () => act.leave());

  // ------------------------------------------------------------ participants
  function paintPeople() {
    const h = el('div', 'gc-people-h');
    const x = el('button', 'm-icon-btn'); x.type = 'button'; x.setAttribute('aria-label', 'بستن'); x.innerHTML = svg(IC.x);
    x.addEventListener('click', () => { people.hidden = true; });
    h.append(el('h3', '', `شرکت‌کنندگان (${fa(members.length)})`), x);
    const list = el('div', 'gc-people-list');
    const sorted = [...members].sort((a, b) => (b.hand ? 1 : 0) - (a.hand ? 1 : 0));
    for (const m of sorted) {
      const r = el('div', 'gc-prow');
      const av = hooks_avatar(m);
      const n = el('span', 'gc-pname', m.id === S.me ? 'شما' : m.name); n.dir = 'auto';
      const st = el('span', 'gc-pstate'); st.innerHTML = (m.hand ? '✋ ' : '') + (m.mic ? '' : svg(IC.micOff));
      r.append(av, n, st);
      if (admin && m.id !== S.me) {
        const b = el('button', 'btn btn-sm', m.held ? 'باز کردن میکروفون' : 'بستن میکروفون'); b.type = 'button';
        b.addEventListener('click', () => act.mute(m.id, !m.held));
        r.appendChild(b);
      }
      list.appendChild(r);
    }
    const foot = el('div', 'gc-people-f');
    if (admin) {
      const e = el('button', 'btn gc-endall', 'پایان تماس برای همه'); e.type = 'button';
      e.addEventListener('click', () => act.end());
      foot.appendChild(e);
    }
    people.replaceChildren(h, list, foot);
  }
  function hooks_avatar(m) {
    const a = el('div', 'm-av', [...(m.name || '؟').trim()][0] || '؟'); a.style.background = `hsl(${hueOf(m.id)} 45% 38%)`;
    a.style.width = a.style.height = '36px'; a.style.fontSize = '14px';
    return facePhoto(a, m.id, m.photo);
  }
  pplB.addEventListener('click', () => {
    people.hidden = !people.hidden;
    if (!people.hidden) { paintPeople(); if (!reduced()) people.animate([{ transform: 'translateX(-100%)' }, { transform: 'none' }], { duration: 260, easing: 'cubic-bezier(0.2,0,0,1)' }); }
  });

  // ------------------------------------------------------------ the floating window
  function shrink() {
    if (root.classList.contains('mini') || gone) return;
    root.classList.add('mini'); root.setAttribute('aria-modal', 'false'); root.setAttribute('role', 'button'); root.tabIndex = 0;
    paintSub();
  }
  function restore() {
    if (!root.classList.contains('mini')) return;
    root.classList.remove('mini'); root.setAttribute('aria-modal', 'true'); root.setAttribute('role', 'dialog'); root.removeAttribute('tabindex');
  }
  mini.addEventListener('click', shrink);
  root.addEventListener('click', e => { if (root.classList.contains('mini')) { e.stopPropagation(); restore(); } }, true);
  root.addEventListener('keydown', e => {
    const isMini = root.classList.contains('mini');
    if (isMini && (e.key === 'Enter' || e.key === ' ')) { e.preventDefault(); restore(); return; }
    if (e.key === 'Escape' && !isMini) { e.preventDefault(); if (!people.hidden) people.hidden = true; else shrink(); }
  });

  // ------------------------------------------------------------ the end
  function close() {
    if (gone) return; gone = true;
    clearInterval(tick); cancelAnimationFrame(raf);
    for (const x of tiles.values()) if (x.audio) x.audio.srcObject = null;
    const fin = () => root.remove();
    reduced() ? fin() : root.animate([{ opacity: 1 }, { opacity: 0 }], { duration: 200, fill: 'forwards' }).finished.then(fin, fin);
  }
  function ended() {
    restore();
    root.classList.add('over');
    sub.textContent = 'تماس گروهی پایان یافت';
    setTimeout(close, 1400);
  }
  function fail(text) { toast(text); close(); }
  paintButtons(); paintSub();
  out.b.focus();
  return {
    state, ended, close, restore, fail,
    local() { const x = tiles.get(S.me); if (x && call.local) { x.v.srcObject = call.local; x.v.play().catch(() => { }); } paintButtons(); },
  };
}
