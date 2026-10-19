// chatplace.js: locations (SPEC 6.4). A location bubble is a small map made
// of DocFind's own cached tiles (/maptile, the couriers page's source) with a
// pin, or a pulsing dot while it is live; without tiles it falls back to a
// plain grid and the coordinates. The picker is the vendored Leaflet, loaded
// on first use. A live location follows this device's position (watchPosition)
// while this page is open, until its time is up or it is stopped.
import { uuidv7, reduced } from '/chatcore.js';
import { el, fa, svg, S, hooks, toast, timeFmt } from '/chatstate.js';
import { openPanel } from '/chatgroup.js';

const PIN = '<path d="M12 22s7-6.2 7-12a7 7 0 1 0-14 0c0 5.8 7 12 7 12z" fill="currentColor" stroke="#fff" stroke-width="1.5"/><circle cx="12" cy="10" r="2.6" fill="#fff" stroke="none"/>';
const ZOOM = 15;
const fail = e => toast((e && e.text) || 'انجام نشد؛ دوباره تلاش کنید.');
const num = (v, d = 5) => Number(v).toLocaleString('fa-IR', { maximumFractionDigits: d, useGrouping: false });

function world(lat, lng, z) {
  const n = 256 * 2 ** z, r = lat * Math.PI / 180;
  return [(lng + 180) / 360 * n, (1 - Math.log(Math.tan(r) + 1 / Math.cos(r)) / Math.PI) / 2 * n];
}
const isLive = p => !!(p && p.live_until && !p.stopped && new Date(p.live_until) > new Date());

// tiles fills box with the tiles around a point, the point at the centre.
function tiles(box, lat, lng) {
  const [px, py] = world(lat, lng, ZOOM), max = 2 ** ZOOM;
  const tx0 = Math.floor((px - 200) / 256), tx1 = Math.floor((px + 200) / 256);
  const ty0 = Math.floor((py - 110) / 256), ty1 = Math.floor((py + 110) / 256);
  let broken = false;
  for (let tx = tx0; tx <= tx1; tx++) for (let ty = ty0; ty <= ty1; ty++) {
    if (ty < 0 || ty >= max) continue;
    const img = el('img'); img.alt = ''; img.decoding = 'async'; img.loading = 'lazy';
    img.src = `/maptile/${ZOOM}/${((tx % max) + max) % max}/${ty}`;
    img.style.left = `calc(50% + ${Math.round(tx * 256 - px)}px)`;
    img.style.top = `calc(50% + ${Math.round(ty * 256 - py)}px)`;
    img.addEventListener('error', () => { if (broken) return; broken = true; box.classList.add('nogrid'); box.querySelectorAll('img').forEach(i => i.remove()); });
    box.appendChild(img);
  }
}

export function drawPlace(m) {
  const p = m.extra || {}, wrap = el('div', 'x-place');
  if (typeof p.lat !== 'number') { wrap.appendChild(el('div', 'x-coord', 'موقعیت در دسترس نیست.')); return wrap; }
  const live = isLive(p) && !m.fwd;
  const map = el('div', 'x-map'); map.setAttribute('role', 'button'); map.tabIndex = 0;
  map.setAttribute('aria-label', live ? 'موقعیت زنده روی نقشه' : 'موقعیت روی نقشه');
  tiles(map, p.lat, p.lng);
  if (live) map.appendChild(el('span', 'x-live-dot'));
  else { const pin = el('span', 'x-pin'); pin.innerHTML = svg(PIN); map.appendChild(pin); }
  const open = () => openView(m);
  map.addEventListener('click', open);
  map.addEventListener('keydown', e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); open(); } });
  wrap.appendChild(map);
  if (p.live_until && !m.fwd) {
    const l = el('div', 'x-live');
    if (live) { l.append(el('b', '', 'موقعیت زنده'), document.createTextNode('تا ' + timeFmt.format(new Date(p.live_until)))); }
    else l.appendChild(document.createTextNode('اشتراک موقعیت زنده پایان یافت'));
    if (p.updated) l.appendChild(el('span', 'x-coord', '، به‌روز ' + timeFmt.format(new Date(p.updated))));
    wrap.appendChild(l);
  }
  wrap.appendChild(el('div', 'x-coord', `${num(p.lat)}، ${num(p.lng)}` + (p.acc ? ` (±${fa(p.acc)} متر)` : '')));
  return wrap;
}

// ------------------------------------------------------------ Leaflet
let leafletP;
function leaflet() {
  return leafletP ||= new Promise((ok, no) => {
    if (window.L) { ok(window.L); return; }
    const css = Object.assign(document.createElement('link'), { rel: 'stylesheet', href: '/vendor/leaflet.css' });
    const js = Object.assign(document.createElement('script'), { src: '/vendor/leaflet.js' });
    js.onload = () => ok(window.L); js.onerror = () => { leafletP = null; no(); };
    document.head.append(css, js);
  });
}
function mapIn(L, box, lat, lng, z) {
  const map = L.map(box, { zoomControl: true, attributionControl: true }).setView([lat, lng], z);
  L.tileLayer('/maptile/{z}/{x}/{y}', { maxZoom: 19, attribution: '© OpenStreetMap' }).addTo(map);
  setTimeout(() => map.invalidateSize(), reduced() ? 0 : 320); // after the panel's slide
  return map;
}
const pinIcon = L => L.divIcon({ className: '', html: `<span class="x-center-pin" style="position:static;transform:none;display:block">${svg(PIN)}</span>`, iconSize: [34, 34], iconAnchor: [17, 34] });

async function openView(m) {
  const p = m.extra, box = el('div', 'x-bigmap');
  openPanel().push(isLive(p) ? 'موقعیت زنده' : 'موقعیت', box);
  try { const L = await leaflet(); L.marker([p.lat, p.lng], { icon: pinIcon(L), keyboard: false }).addTo(mapIn(L, box, p.lat, p.lng, 16)); }
  catch { box.appendChild(el('div', 'x-empty', `${num(p.lat, 6)}، ${num(p.lng, 6)}`)); }
}

// ------------------------------------------------------------ the picker
function here() {
  return new Promise((ok, no) => {
    if (!navigator.geolocation) { no(); return; }
    navigator.geolocation.getCurrentPosition(ok, no, { enableHighAccuracy: true, timeout: 12000, maximumAge: 30000 });
  });
}

export async function openPlacePicker(chat) {
  const p = openPanel(), body = el('div', 'x-form');
  const box = el('div', 'x-bigmap'); const pin = el('span', 'x-center-pin'); pin.innerHTML = svg(PIN); box.appendChild(pin);
  const status = el('div', 'x-when', 'نقشه را بکشید تا نشانه روی جای درست بنشیند.');
  const sendHere = el('button', 'btn btn-primary', 'ارسال این نقطه'); sendHere.type = 'button';
  const liveSel = el('select', 'input'); liveSel.setAttribute('aria-label', 'مدت موقعیت زنده');
  for (const [v, t] of [[900, '۱۵ دقیقه'], [3600, '۱ ساعت'], [28800, '۸ ساعت']]) { const o = el('option', '', 'موقعیت زنده: ' + t); o.value = v; liveSel.appendChild(o); }
  const sendLive = el('button', 'btn', 'اشتراک موقعیت زندهٔ من'); sendLive.type = 'button';
  body.append(box, status, sendHere, liveSel, sendLive);
  p.push('ارسال موقعیت', body);
  let map = null, gps = null;
  try { map = mapIn(await leaflet(), box, 35.6997, 51.338, 12); } catch { status.textContent = 'نقشه بارگذاری نشد؛ موقعیت دستگاه فرستاده می‌شود.'; }
  here().then(pos => { gps = pos.coords; if (map) map.setView([gps.latitude, gps.longitude], 16); status.textContent = `دقت موقعیت دستگاه: ${fa(Math.round(gps.accuracy))} متر`; })
    .catch(() => { status.textContent = 'دسترسی به موقعیت دستگاه داده نشد؛ نقطه را روی نقشه انتخاب کنید.'; });
  const send = async (lat, lng, acc, live) => {
    try {
      const r = await S.net.request('send_location', { chat: chat.id, cid: uuidv7(), topic: chat.forum ? (S.topic || 0) : 0, lat, lng, acc, live });
      p.close();
      for (const m of r.messages || []) { hooks.accept(m); if (live) startLive(m); }
    } catch (e) { fail(e); }
  };
  sendHere.addEventListener('click', () => {
    if (map) { const c = map.getCenter(), near = gps && map.distance(c, [gps.latitude, gps.longitude]) < 15; send(c.lat, c.lng, near ? gps.accuracy : 0, 0); }
    else if (gps) send(gps.latitude, gps.longitude, gps.accuracy, 0);
    else toast('موقعیتی برای ارسال نیست.');
  });
  sendLive.addEventListener('click', async () => {
    try { const pos = gps ? { coords: gps } : await here(); send(pos.coords.latitude, pos.coords.longitude, pos.coords.accuracy, Number(liveSel.value)); }
    catch { toast('برای موقعیت زنده، دسترسی به موقعیت دستگاه لازم است.'); }
  });
}

// ------------------------------------------------------------ live sharing
const sharing = new Map(); // message id -> watch id

function startLive(m) {
  if (!navigator.geolocation || sharing.has(m.id)) return;
  const until = new Date(m.extra.live_until).getTime();
  let last = 0;
  const w = navigator.geolocation.watchPosition(pos => {
    if (Date.now() >= until) { stopHere(m.id); return; }
    if (Date.now() - last < 10000) return;
    last = Date.now();
    S.net.request('location_update', { chat: m.chat, id: m.id, lat: pos.coords.latitude, lng: pos.coords.longitude, acc: pos.coords.accuracy })
      .catch(e => { if (e && e.code === 'not_live') stopHere(m.id); });
  }, () => { }, { enableHighAccuracy: true, maximumAge: 5000 });
  sharing.set(m.id, w);
  setTimeout(() => stopHere(m.id), Math.max(0, until - Date.now()));
}
function stopHere(id) { const w = sharing.get(id); if (w != null) { navigator.geolocation.clearWatch(w); sharing.delete(id); } }

// placeMenu offers to stop one's own live location.
export function placeMenu(m, chat, items) {
  if (m.kind !== 'location' || m.from !== S.me || !isLive(m.extra) || m.fwd) return;
  items.push({ icon: '<rect x="6" y="6" width="12" height="12" rx="2"/>', label: 'توقف اشتراک موقعیت', run: async () => {
    stopHere(m.id);
    try { await S.net.request('location_stop', { chat: m.chat, id: m.id }); } catch (e) { fail(e); }
  } });
}
