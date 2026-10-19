// Passkeys: signing in with the phone's or the computer's own lock (face,
// fingerprint, PIN) instead of a password and a code.
//
// Two halves. On the settings page, the list of this account's passkeys, and
// adding and removing one. On the sign-in page, one button. The server does
// every check (internal/webauthn, passkeyroutes.go); this file converts
// between the browser's ArrayBuffers and the base64url the server speaks.
(function () {
  'use strict';

  const esc = s => String(s ?? '').replace(/[&<>"']/g, c =>
    ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));

  function b64u(buf) {
    const bytes = new Uint8Array(buf);
    let s = '';
    for (const b of bytes) s += String.fromCharCode(b);
    return btoa(s).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  }
  function unb64u(str) {
    const s = atob(String(str).replace(/-/g, '+').replace(/_/g, '/'));
    const out = new Uint8Array(s.length);
    for (let i = 0; i < s.length; i++) out[i] = s.charCodeAt(i);
    return out.buffer;
  }

  const supported = () => !!(window.PublicKeyCredential && navigator.credentials && window.isSecureContext);

  async function postJSON(path, body) {
    const r = await fetch(path, {
      method: 'POST', credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body || {}),
    });
    const d = await r.json().catch(() => ({}));
    if (!r.ok) throw new Error(d.error || 'انجام نشد.');
    return d;
  }

  // A browser that was cancelled, or that has no passkey for this site,
  // throws NotAllowedError -- which is the person changing their mind, not a
  // fault, and is said as such.
  function explain(e) {
    if (e && e.name === 'NotAllowedError') return 'لغو شد، یا کلید عبوری برای این سایت روی این دستگاه نیست.';
    if (e && e.name === 'InvalidStateError') return 'این دستگاه همین حالا یک کلید عبور برای این حساب دارد.';
    return (e && e.message) || 'انجام نشد.';
  }

  // ------------------------------------------------------------ sign-in

  async function signIn() {
    const opts = await postJSON('/api/login/passkey/options');
    const pk = opts.publicKey;
    pk.challenge = unb64u(pk.challenge);
    let a;
    try {
      a = await navigator.credentials.get({ publicKey: pk });
    } catch (e) {
      throw new Error(explain(e));
    }
    return postJSON('/api/login/passkey', {
      id: b64u(a.rawId),
      client_data: b64u(a.response.clientDataJSON),
      authenticator_data: b64u(a.response.authenticatorData),
      signature: b64u(a.response.signature),
      user_handle: a.response.userHandle ? b64u(a.response.userHandle) : '',
    });
  }

  // ------------------------------------------------------------ settings

  function mount(card, api, say) {
    if (!card) return;
    const msg = (t, k) => say(card.querySelector('#pkMsg'), t, k);
    const when = s => esc(s ? new Date(s).toLocaleDateString('fa-IR') : '—');

    async function paint() {
      const r = await api('/api/account/passkeys');
      if (!r.ok) { card.hidden = true; return; }
      const d = await r.json();
      if (!d.available) { card.hidden = true; return; }
      card.hidden = false;
      const rows = (d.passkeys || []).map(k => `<tr>
          <td>${esc(k.name)}</td><td>${when(k.created_at)}</td><td>${when(k.last_used)}</td>
          <td><button class="btn btn-sm btn-danger" type="button" data-rm="${esc(k.id)}">حذف</button></td></tr>`).join('');
      card.innerHTML = `<h2 class="card-head">
          <svg class="ic" aria-hidden="true"><use href="#i-key-round"/></svg><span>کلیدهای عبور</span></h2>
        <div class="card-body">
          <p style="margin:0 0 var(--s3)">با کلید عبور، ورود با قفل خود گوشی یا رایانه (چهره، اثر انگشت یا PIN) انجام می‌شود،
            بدون گذرواژه و بدون کد. هر دستگاه کلید خودش را دارد.</p>
          ${rows ? `<div class="tablewrap"><table class="table"><thead><tr><th>نام</th><th>ساخته شده</th><th>آخرین استفاده</th><th></th></tr></thead>
            <tbody>${rows}</tbody></table></div>` : '<p class="hint">هنوز کلید عبوری ثبت نشده است.</p>'}
          ${supported() ? `<div class="grid" style="margin-top:var(--s3)">
            <div><label class="label" for="pkName">نام این دستگاه</label>
              <input class="input" id="pkName" maxlength="60" placeholder="مثلاً گوشی من"></div>
            <div><label class="label" for="pkPw">گذرواژه</label>
              <input class="input" id="pkPw" type="password" autocomplete="current-password" dir="ltr"></div>
            <div><button class="btn btn-primary" type="button" id="pkAdd">افزودن کلید عبور</button></div>
          </div>` : '<p class="hint">این مرورگر یا این نشانی (بدون HTTPS) کلید عبور را پشتیبانی نمی‌کند.</p>'}
          <div class="msg notice hidden" id="pkMsg" role="status"></div>
        </div>`;
      const add = card.querySelector('#pkAdd');
      if (add) add.addEventListener('click', register);
      card.querySelectorAll('[data-rm]').forEach(b => b.addEventListener('click', () => remove(b.dataset.rm)));
    }

    async function register() {
      const password = card.querySelector('#pkPw').value;
      const name = card.querySelector('#pkName').value;
      try {
        const opts = await postJSON('/api/account/passkeys/options', { password });
        const pk = opts.publicKey;
        pk.challenge = unb64u(pk.challenge);
        pk.user.id = unb64u(pk.user.id);
        pk.excludeCredentials = (pk.excludeCredentials || []).map(c => ({ ...c, id: unb64u(c.id) }));
        let cred;
        try {
          cred = await navigator.credentials.create({ publicKey: pk });
        } catch (e) { throw new Error(explain(e)); }
        await postJSON('/api/account/passkeys', {
          name,
          client_data: b64u(cred.response.clientDataJSON),
          attestation: b64u(cred.response.attestationObject),
        });
        await paint();
        msg('کلید عبور افزوده شد.', true);
      } catch (e) { msg(e.message); }
    }

    async function remove(id) {
      const pw = card.querySelector('#pkPw');
      const password = pw ? pw.value : '';
      if (!password) { msg('برای حذف، گذرواژه را در کادر بالا بنویسید.'); return; }
      try {
        await postJSON('/api/account/passkeys/remove', { id, password });
        await paint();
        msg('کلید عبور حذف شد.', true);
      } catch (e) { msg(e.message); }
    }

    paint().catch(() => { card.hidden = true; });
  }

  window.DocfindPasskeys = { mount, signIn, supported };
})();
