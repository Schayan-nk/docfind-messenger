// chatcalls.js: the «تماس‌ها» list (docs/chat/SPEC.md 6.8: incoming,
// outgoing, missed, how long, call back), the call settings (who may call
// me; this device always through the relay), and two things the call screen
// borrows: its moving four-colour ground and its sounds (8.9).
import { el, svg, timeFmt, dayFmt, S, hooks, toast } from '/chatstate.js';
import { reduced } from '/chatcore.js';
import { api, openPanel } from '/chatgroup.js';
import { PHONE, VIDEO, callText, mmss } from '/chatcall.js';

const GEAR = '<circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z"/>';
const IN = '<path d="M17 7L7 17M15 17H7V9"/>', OUT = '<path d="M7 17L17 7M9 7h8v8"/>';

// ------------------------------------------------------------ the list
export async function openCalls() {
  const body = el('div', 'cl-list');
  const gear = el('button', 'm-icon-btn'); gear.type = 'button'; gear.setAttribute('aria-label', 'تنظیمات تماس'); gear.title = 'تنظیمات تماس';
  gear.innerHTML = svg(GEAR); gear.addEventListener('click', settings);
  const panel = openPanel();
  panel.push('تماس‌ها', body, { action: gear });
  let before = 0;
  const more = el('button', 'btn m-more', 'تماس‌های قدیمی‌تر'); more.type = 'button'; more.hidden = true;
  more.addEventListener('click', () => page());
  body.append(el('div', 'm-skel'), more);
  async function page() {
    let j;
    try { j = await api('/api/chat/calls' + (before ? '?before=' + before : '')); }
    catch (e) { body.replaceChildren(el('p', 'g-note', e.text)); return; }
    body.querySelector('.m-skel')?.remove();
    const list = j.calls || [];
    if (!before && !list.length) { body.prepend(el('p', 'g-note cl-empty', 'هنوز تماسی نداشته‌اید. از بالای هر گفت‌وگوی خصوصی می‌توانید تماس صوتی یا تصویری بگیرید.')); return; }
    for (const c of list) more.before(row(c, panel));
    if (list.length) before = list[list.length - 1].id;
    more.hidden = list.length < 50;
  }
  page();
}

function row(c, panel) {
  const r = el('div', 'g-row cl-row' + (c.missed ? ' missed' : '')); r.setAttribute('role', 'button'); r.tabIndex = 0;
  const av = hooks.avatar({ title: c.peer.name, peer: { id: c.peer.id, photo: c.peer.photo } }, 44);
  av.classList.add('m-title-btn'); av.setAttribute('role', 'button'); av.setAttribute('aria-label', 'اطلاعات تماس ' + c.peer.name);
  av.addEventListener('click', e => { e.stopPropagation(); if (c.peer.id) hooks.openPerson && hooks.openPerson(c.peer.id); });
  const t = el('div', 'g-row-text');
  const name = el('span', 'g-row-label', c.peer.name); name.dir = 'auto';
  const sub = el('span', 'g-row-sub cl-sub');
  const dir = el('span', 'cl-dir ' + (c.outgoing ? 'out' : 'in')); dir.innerHTML = svg(c.outgoing ? OUT : IN);
  const at = new Date(c.started_at);
  const when = (new Date().toDateString() === at.toDateString() ? '' : dayFmt.format(at) + '، ') + timeFmt.format(at);
  sub.append(dir, document.createTextNode(callText({ video: c.video, reason: c.reason }, c.outgoing).text + ' · ' + when + (c.duration ? ' · ' + mmss(c.duration) : '')));
  t.append(name, sub);
  const again = el('button', 'm-icon-btn'); again.type = 'button';
  again.setAttribute('aria-label', (c.video ? 'تماس تصویری' : 'تماس صوتی') + ' با ' + c.peer.name); again.innerHTML = svg(c.video ? VIDEO : PHONE);
  again.disabled = !c.peer.id;
  again.addEventListener('click', e => { e.stopPropagation(); panel.close(); hooks.startCall(c.peer.id, c.video); });
  r.append(av, t, again);
  const open = () => { if (!c.chat) return; panel.close(); hooks.openChat(c.chat); };
  r.addEventListener('click', open);
  r.addEventListener('keydown', e => { if (e.key === 'Enter') open(); });
  r.addEventListener('contextmenu', e => {
    e.preventDefault();
    import('/chatmenu.js').then(({ openMenu }) => openMenu({ x: e.clientX, y: e.clientY, items: [
      { icon: PHONE, label: 'تماس صوتی', run: () => { panel.close(); hooks.startCall(c.peer.id, false); } },
      { icon: VIDEO, label: 'تماس تصویری', run: () => { panel.close(); hooks.startCall(c.peer.id, true); } },
      { icon: '<path d="M3 6h18M8 6V4h8v2M19 6l-1 14H6L5 6"/>', label: 'حذف از فهرست', danger: true, run: () => api(`/api/chat/calls/${c.id}/hide`, {}).then(() => r.remove()).catch(e2 => toast(e2.text)) },
    ] }));
  });
  return r;
}

// ------------------------------------------------------------ settings
async function settings() {
  const body = el('div', 'g-form');
  const who = el('div', 'g-block'); who.setAttribute('role', 'radiogroup'); who.setAttribute('aria-label', 'چه کسی می‌تواند با من تماس بگیرد');
  who.appendChild(el('div', 'g-sec', 'چه کسی می‌تواند با من تماس بگیرد'));
  let priv = {};
  try { priv = await api('/api/chat/privacy'); } catch { }
  const mode = () => (priv.calls && priv.calls.mode) || 'everybody';
  const opts = [['everybody', 'همه'], ['contacts', 'مخاطبان من'], ['nobody', 'هیچ‌کس']];
  const rows = opts.map(([v, label]) => {
    const b = el('button', 'g-row' + (mode() === v ? ' on' : '')); b.type = 'button'; b.setAttribute('role', 'radio'); b.setAttribute('aria-checked', String(mode() === v));
    b.append(el('span', 'g-row-label', label));
    b.addEventListener('click', async () => {
      const next = { ...priv, calls: { ...(priv.calls || {}), mode: v } };
      try { await api('/api/chat/privacy', next); priv = next; rows.forEach((x, i) => { const on = opts[i][0] === v; x.classList.toggle('on', on); x.setAttribute('aria-checked', String(on)); }); }
      catch (e) { toast(e.text || 'ذخیره نشد.'); }
    });
    return b;
  });
  who.append(...rows);
  const dev = el('div', 'g-block');
  dev.appendChild(el('div', 'g-sec', 'این دستگاه'));
  const l = el('label', 'g-switch'), i = el('input'); i.type = 'checkbox'; i.id = 'cl-relay'; i.setAttribute('role', 'switch');
  try { i.checked = localStorage.getItem('docfind.calls.relay') === '1'; } catch { }
  const tx = el('span', 'g-row-text'); tx.append(el('span', 'g-row-label', 'همیشه از راه سرور واسط'), el('span', 'g-row-sub', 'برای شبکه‌هایی که تماس مستقیم را می‌بندند؛ کمی کندتر.'));
  i.addEventListener('change', () => { try { localStorage.setItem('docfind.calls.relay', i.checked ? '1' : '0'); } catch { } });
  l.append(tx, i); dev.appendChild(l);
  body.append(who, dev, el('p', 'g-note', 'کسانی را که مسدود کرده‌اید نمی‌توانند با شما تماس بگیرند. خاموش کردن اعلان تماس‌ها در تنظیمات اعلان (زنگوله) است.'));
  openPanel().push('تنظیمات تماس', body);
}

// ------------------------------------------------------------ the call screen's ground (8.9)
// Four colour blobs drifting on a tiny canvas the page stretches over the
// screen; each call state has its own four, cross-faded in a second.
const SETS = {
  neutral: ['#17384C', '#2A4F7A', '#3E3F78', '#235A6E'],
  green: ['#12493F', '#1E7A62', '#2F8F6F', '#185462'],
  orange: ['#5C2E17', '#A04E1D', '#C9601F', '#6B3A22'],
};
const rgb = h => [1, 3, 5].map(i => parseInt(h.slice(i, i + 2), 16));
export function paintGround(cv) {
  const g = cv.getContext('2d'), W = cv.width, H = cv.height;
  let from = SETS.neutral.map(rgb), to = from, t0 = 0, raf = 0, on = true;
  const mix = (a, b, k) => a.map((x, i) => Math.round(x + (b[i] - x) * k));
  function draw(now) {
    const k = Math.min(1, (now - t0) / 1000), cur = from.map((c, i) => mix(c, to[i], k));
    g.fillStyle = `rgb(${cur[0]})`; g.fillRect(0, 0, W, H);
    const s = reduced() ? 0 : now / 7000;
    for (let i = 0; i < 4; i++) {
      const x = W * (0.5 + 0.34 * Math.cos(s + i * 1.57)), y = H * (0.5 + 0.34 * Math.sin(s * 1.3 + i * 1.57));
      const gr = g.createRadialGradient(x, y, 0, x, y, W * 0.75);
      gr.addColorStop(0, `rgba(${cur[i]}, 1)`); gr.addColorStop(1, `rgba(${cur[i]}, 0)`);
      g.fillStyle = gr; g.fillRect(0, 0, W, H);
    }
    if (on && (!reduced() || k < 1)) raf = requestAnimationFrame(draw);
  }
  raf = requestAnimationFrame(draw);
  return {
    set(name) {
      const now = performance.now(), k = Math.min(1, (now - t0) / 1000);
      from = from.map((c, i) => mix(c, to[i], k)); to = (SETS[name] || SETS.neutral).map(rgb); t0 = now;
      cancelAnimationFrame(raf); raf = requestAnimationFrame(draw);
    },
    stop() { on = false; cancelAnimationFrame(raf); },
  };
}

// ------------------------------------------------------------ the call's sounds
// Made with Web Audio, like the messenger's alerts: the ring of an incoming
// call (with a vibration) and the caller's ringback (425 Hz, one second in
// four).
export class Ring {
  ctx() { try { this.ac = this.ac || new (window.AudioContext || window.webkitAudioContext)(); this.ac.resume(); } catch { } return this.ac; }
  tone(notes, type = 'sine', vol = 0.16) {
    const ac = this.ctx(); if (!ac) return;
    const t0 = ac.currentTime + 0.02;
    this.live = this.live || new Set();
    for (const [f, at, dur] of notes) {
      const o = ac.createOscillator(), gn = ac.createGain();
      this.live.add(o); o.onended = () => this.live.delete(o);
      o.type = type; o.frequency.value = f;
      gn.gain.setValueAtTime(0, t0 + at); gn.gain.linearRampToValueAtTime(vol, t0 + at + 0.03);
      gn.gain.setValueAtTime(vol, t0 + at + dur - 0.05); gn.gain.linearRampToValueAtTime(0, t0 + at + dur);
      o.connect(gn).connect(ac.destination); o.start(t0 + at); o.stop(t0 + at + dur + 0.05);
    }
  }
  incoming() {
    this.stop();
    const once = () => {
      if (S.notify && S.notify.sound !== false) this.tone([[784, 0, 0.3], [988, 0.35, 0.3], [784, 0.9, 0.3], [988, 1.25, 0.3]], 'triangle');
      try { if (navigator.vibrate && (!S.notify || S.notify.vibrate !== false)) navigator.vibrate([400, 200, 400]); } catch { }
    };
    once(); this.t = setInterval(once, 3000); this.until = setTimeout(() => this.stop(), 45000);
  }
  back() { this.stop(); const once = () => this.tone([[425, 0, 1]], 'sine', 0.08); once(); this.t = setInterval(once, 4000); }
  stop() {
    clearInterval(this.t); clearTimeout(this.until);
    for (const o of this.live || []) { try { o.stop(); } catch { } }
    if (this.live) this.live.clear();
    try { navigator.vibrate && navigator.vibrate(0); } catch { }
  }
  close() { this.stop(); try { this.ac && this.ac.close(); } catch { } this.ac = null; }
}
