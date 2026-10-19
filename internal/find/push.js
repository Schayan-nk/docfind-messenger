// Notifications that arrive with docfind closed: this device's half.
//
// The server watches each person's cartable and calendar and sends through the
// device's own push service. For that it needs this device's SUBSCRIPTION -- an
// address at Apple's, Microsoft's, Google's or Mozilla's push service, and the
// keys to encrypt to it. This file gets one and hands it over. Nothing else.
//
// WHAT EACH PLATFORM NEEDS, because the failures are otherwise silent:
//   iPhone / iPad  iOS 16.4 or later, and docfind ADDED TO THE HOME SCREEN and
//                  opened from there. Safari in a tab has no push at all. The
//                  first "allow" has to come from a tap.
//   Windows        Edge keeps receiving with every window closed. Chrome only
//                  while it runs in the background -- see the settings page.
//   Everywhere     the page must be served over HTTPS.
(function () {
  'use strict';

  const FLAG = 'docfind.push.on';

  function supported() {
    return 'serviceWorker' in navigator && 'PushManager' in window && 'Notification' in window;
  }

  function isIOS() {
    const ua = navigator.userAgent || '';
    // iPadOS reports itself as a Mac; the touch points give it away.
    return /iPhone|iPad|iPod/.test(ua) ||
      (/Macintosh/.test(ua) && navigator.maxTouchPoints > 1);
  }

  function standalone() {
    return navigator.standalone === true ||
      (window.matchMedia && window.matchMedia('(display-mode: standalone)').matches);
  }

  // On an iPhone, push exists only inside the home-screen app.
  function needsInstall() { return isIOS() && !standalone(); }

  function deviceLabel() {
    const ua = navigator.userAgent || '';
    if (/iPhone/.test(ua)) return 'iPhone';
    if (/iPad/.test(ua) || (/Macintosh/.test(ua) && navigator.maxTouchPoints > 1)) return 'iPad';
    if (/Android/.test(ua)) return 'Android';
    const browser = /Edg\//.test(ua) ? 'Edge' : /Firefox\//.test(ua) ? 'Firefox'
      : /Chrome\//.test(ua) ? 'Chrome' : /Safari\//.test(ua) ? 'Safari' : 'مرورگر';
    const os = /Windows/.test(ua) ? 'Windows' : /Macintosh/.test(ua) ? 'Mac' : '';
    return os ? browser + ' روی ' + os : browser;
  }

  function b64ToBytes(s) {
    const pad = '='.repeat((4 - s.length % 4) % 4);
    const bin = atob((s + pad).replace(/-/g, '+').replace(/_/g, '/'));
    const out = new Uint8Array(bin.length);
    for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
    return out;
  }

  function sameKey(buf, b64) {
    if (!buf || !b64) return false;
    const a = new Uint8Array(buf), b = b64ToBytes(b64);
    if (a.length !== b.length) return false;
    for (let i = 0; i < a.length; i++) if (a[i] !== b[i]) return false;
    return true;
  }

  async function registration() {
    // serviceWorker.ready never settles if no worker ever registers, and a
    // button that spins for ever is worse than one that says why.
    return Promise.race([
      navigator.serviceWorker.ready,
      new Promise((_, no) => setTimeout(() => no(new Error('no service worker')), 8000)),
    ]);
  }

  async function config() {
    const r = await fetch('/api/push/config', { credentials: 'same-origin' });
    if (!r.ok) return { available: false };
    return r.json();
  }

  async function current() {
    if (!supported()) return null;
    try {
      const reg = await registration();
      return await reg.pushManager.getSubscription();
    } catch (_) { return null; }
  }

  async function register(sub) {
    const j = sub.toJSON();
    const r = await fetch('/api/push/devices', {
      method: 'POST', credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ endpoint: j.endpoint, keys: j.keys, label: deviceLabel() }),
    });
    if (!r.ok) {
      const e = await r.json().catch(() => ({}));
      throw new Error(e.error || ('HTTP ' + r.status));
    }
  }

  // Turn notifications on for this device. `ask` says whether the permission
  // prompt may be shown -- only ever in answer to a tap, which iOS requires and
  // which is the only honest time to ask anyway.
  //
  // Returns '' on success, or the reason it could not, in Persian.
  async function enable(ask) {
    if (!supported()) return 'این مرورگر اعلان پس‌زمینه ندارد.';
    if (needsInstall()) {
      return 'در آیفون، اعلان فقط در برنامه نصب‌شده کار می‌کند: در Safari «Share» و ' +
        'بعد «Add to Home Screen» را بزنید، برنامه را از صفحه اصلی باز کنید و دوباره روشن کنید.';
    }
    let perm = Notification.permission;
    if (perm !== 'granted') {
      if (!ask) return 'اجازه اعلان داده نشده است.';
      perm = await Notification.requestPermission();
    }
    if (perm !== 'granted') return 'اجازه اعلان داده نشد.';

    const cfg = await config().catch(() => ({ available: false }));
    if (!cfg.available || !cfg.public_key) return 'ارسال اعلان روی این سرور روشن نیست.';

    let reg;
    try { reg = await registration(); }
    catch (_) { return 'سرویس‌ورکر برنامه آماده نشد؛ صفحه را دوباره باز کنید.'; }

    let sub = await reg.pushManager.getSubscription();
    // A subscription made for a different server key cannot receive anything
    // from this server: replace it rather than register a dead address.
    if (sub && sub.options && sub.options.applicationServerKey &&
        !sameKey(sub.options.applicationServerKey, cfg.public_key)) {
      try { await sub.unsubscribe(); } catch (_) {}
      sub = null;
    }
    try {
      if (!sub) {
        sub = await reg.pushManager.subscribe({
          userVisibleOnly: true,
          applicationServerKey: b64ToBytes(cfg.public_key),
        });
      }
      await register(sub);
    } catch (e) {
      return 'ثبت این دستگاه انجام نشد: ' + (e && e.message ? e.message : 'خطای نامشخص');
    }
    try { localStorage.setItem(FLAG, '1'); } catch (_) {}
    return '';
  }

  // Keep the server's record of this device current, silently, on every load
  // where permission already exists. Repairs a device the server dropped, and
  // brings anybody who allowed notifications before this feature existed onto
  // it without their having to do anything.
  async function sync() {
    if (!supported() || needsInstall() || Notification.permission !== 'granted') return false;
    return (await enable(false)) === '';
  }

  // Stop this device receiving this account's notifications. For switching
  // off, and for signing out.
  async function forgetDevice() {
    try { localStorage.removeItem(FLAG); } catch (_) {}
    const sub = await current();
    if (!sub) return;
    try {
      await fetch('/api/push/unsubscribe', {
        method: 'POST', credentials: 'same-origin',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ endpoint: sub.endpoint }),
      });
    } catch (_) {}
    try { await sub.unsubscribe(); } catch (_) {}
  }

  // Whether the SERVER is delivering to this device -- in which case the
  // page must not also raise its own notification for the same event.
  function active() {
    try {
      return localStorage.getItem(FLAG) === '1' && 'Notification' in window &&
        Notification.permission === 'granted';
    } catch (_) { return false; }
  }

  async function test() {
    const r = await fetch('/api/push/test', { method: 'POST', credentials: 'same-origin' });
    const j = await r.json().catch(() => ({}));
    if (!r.ok) throw new Error(j.error || ('HTTP ' + r.status));
    return j;
  }

  async function devices() {
    const sub = await current();
    const q = sub ? '?endpoint=' + encodeURIComponent(sub.endpoint) : '';
    const r = await fetch('/api/push/devices' + q, { credentials: 'same-origin' });
    if (!r.ok) throw new Error('HTTP ' + r.status);
    return (await r.json()).devices || [];
  }

  async function removeDevice(id) {
    const r = await fetch('/api/push/devices/' + encodeURIComponent(id), {
      method: 'DELETE', credentials: 'same-origin',
    });
    if (!r.ok) throw new Error('HTTP ' + r.status);
  }

  window.DocfindPush = {
    supported, needsInstall, isIOS, standalone, enable, sync, forgetDevice,
    active, test, devices, removeDevice, config,
  };
})();
