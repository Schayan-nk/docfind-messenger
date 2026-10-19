// Two-step sign-in, on the settings page: turning it on -- with Google
// Authenticator (or any authenticator app), or with a code by email -- the
// recovery codes, and turning it off.
//
// The server is the boundary (twostep.go, twostepmail.go): every change here
// asks for the password again, and turning it off asks for a code as well.
// This file only draws the card and says what happened.
(function () {
  'use strict';

  const esc = s => String(s ?? '').replace(/[&<>"']/g, c =>
    ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));

  const HEAD = `<h2 class="card-head">
      <svg class="ic" aria-hidden="true"><use href="#i-key-round"/></svg>
      <span>ورود دومرحله‌ای</span><span class="spacer"></span><span id="tsChip"></span>
    </h2>`;
  const MSG = '<div class="msg notice hidden" id="tsMsg" role="status"></div>';

  function mount(card, api, say) {
    if (!card) return;
    let state = null;

    const post = async (path, body) => {
      const r = await api(path, { method: 'POST', body: JSON.stringify(body || {}) });
      const d = await r.json().catch(() => ({}));
      if (!r.ok) throw new Error(d.error || 'انجام نشد.');
      return d;
    };
    const msg = (text, kind) => say(card.querySelector('#tsMsg'), text, kind);
    const val = id => { const el = card.querySelector(id); return el ? el.value : ''; };
    const bind = (id, fn) => { const b = card.querySelector(id); if (b) b.addEventListener('click', fn); };

    // The recovery codes, shown once: the server keeps only their hashes.
    function showCodes(codes) {
      card.innerHTML = HEAD + `<div class="card-body">
        <p style="margin:0 0 var(--s3)">این ده کد پشتیبان را جایی امن نگه دارید. هر کد یک بار کار می‌کند و
          اگر گوشی یا ایمیل در دسترس نباشد، تنها راه ورود همین‌هاست. <b>دوباره نمایش داده نمی‌شوند.</b></p>
        <ol dir="ltr" style="columns:2;font-family:ui-monospace,monospace;margin:0 0 var(--s3)">
          ${codes.map(c => `<li>${esc(c)}</li>`).join('')}</ol>
        <button class="btn" type="button" id="tsCopy">رونوشت کدها</button>
        <button class="btn btn-primary" type="button" id="tsDone">نگه داشتم</button>${MSG}</div>`;
      bind('#tsCopy', async () => {
        try { await navigator.clipboard.writeText(codes.join('\n')); msg('رونوشت شد.', true); }
        catch (_) { msg('رونوشت انجام نشد؛ کدها را از روی صفحه بنویسید.'); }
      });
      bind('#tsDone', load);
    }

    // Google Authenticator, step two: the QR code, the key, the first code.
    function showAppSetup(d) {
      card.innerHTML = HEAD + `<div class="card-body">
        <p style="margin:0 0 var(--s3)">در Google Authenticator (یا Microsoft Authenticator یا هر برنامهٔ مشابه)
          این کد را اسکن کنید، یا کلید زیر را دستی وارد کنید. بعد کد شش‌رقمی‌ای را که برنامه نشان می‌دهد بنویسید.</p>
        <div id="tsQR" style="width:200px;max-width:100%;margin:0 0 var(--s3)"></div>
        <div class="hint" style="margin:0 0 var(--s3)">کلید: <code dir="ltr" style="user-select:all">${esc(d.secret)}</code></div>
        <div class="grid">
          <div><label class="label" for="tsCode">کد شش‌رقمی</label>
            <input class="input" id="tsCode" inputmode="numeric" autocomplete="one-time-code" dir="ltr" maxlength="6"></div>
          <div><button class="btn btn-primary" type="button" id="tsConfirm">روشن کردن</button>
            <button class="btn" type="button" id="tsCancel">انصراف</button></div>
        </div>${MSG}</div>`;
      // The server's own SVG, parsed as an image document and adopted: only
      // a picture -- it carries a rect and a path and nothing else.
      const svg = new DOMParser().parseFromString(d.qr_svg, 'image/svg+xml').documentElement;
      if (svg && svg.nodeName === 'svg') {
        svg.setAttribute('role', 'img');
        svg.setAttribute('aria-label', 'کد QR راه‌اندازی');
        card.querySelector('#tsQR').appendChild(document.importNode(svg, true));
      }
      card.querySelector('#tsCode').focus();
      bind('#tsCancel', load);
      bind('#tsConfirm', async () => {
        try { showCodes((await post('/api/account/two-step/confirm', { code: val('#tsCode') })).recovery_codes || []); }
        catch (e) { msg(e.message); }
      });
    }

    // Email or SMS, step two: the code that came to the address or the phone.
    function showEmailSetup(sentTo, bySMS) {
      card.innerHTML = HEAD + `<div class="card-body">
        <p style="margin:0 0 var(--s3)">${bySMS
          ? `کدی با پیامک به <span dir="ltr">${esc(sentTo)}</span> فرستاده شد. آن را اینجا وارد کنید.`
          : `کدی به ${esc(sentTo)} فرستاده شد (از docfind@example.com).
          آن را اینجا وارد کنید؛ اگر نرسید، پوشهٔ هرزنامه را هم ببینید.`}</p>
        <div class="grid">
          <div><label class="label" for="tsCode">کد شش‌رقمی</label>
            <input class="input" id="tsCode" inputmode="numeric" autocomplete="one-time-code" dir="ltr" maxlength="6"></div>
          <div><button class="btn btn-primary" type="button" id="tsConfirm">روشن کردن</button>
            <button class="btn" type="button" id="tsCancel">انصراف</button></div>
        </div>${MSG}</div>`;
      card.querySelector('#tsCode').focus();
      bind('#tsCancel', load);
      bind('#tsConfirm', async () => {
        try { showCodes((await post('/api/account/two-step/' + (bySMS ? 'sms' : 'email') + '/confirm', { code: val('#tsCode') })).recovery_codes || []); }
        catch (e) { msg(e.message); }
      });
    }

    function paintOff() {
      const mailOK = state.email_available;
      card.innerHTML = HEAD + `<div class="card-body">
        <p style="margin:0 0 var(--s3)">اختیاری: با روشن کردن این گزینه، ورود علاوه بر گذرواژه یک کد شش‌رقمی هم می‌خواهد؛
          گذرواژهٔ دزدیده‌شده به‌تنهایی کافی نخواهد بود. کد از یکی از این دو راه می‌آید:</p>
        <div class="grid">
          <div><label class="label" for="tsPw">گذرواژه</label>
            <input class="input" id="tsPw" type="password" autocomplete="current-password" dir="ltr"></div>
          <div><button class="btn btn-primary" type="button" id="tsBegin">با Google Authenticator</button></div>
        </div>
        ${mailOK ? `<div class="grid" style="margin-top:var(--s3)">
          <div><label class="label" for="tsMail">یا با ایمیل، به این نشانی</label>
            <input class="input" id="tsMail" type="email" dir="ltr" autocomplete="email" value="${esc(state.email)}"></div>
          <div><button class="btn" type="button" id="tsBeginMail">با ایمیل</button></div>
        </div>
        <p class="hint" style="margin:var(--s2) 0 0">ایمیل از برنامهٔ گوشی ضعیف‌تر است: هر کس به صندوق ایمیل شما دسترسی داشته باشد،
          مرحلهٔ دوم را هم دارد.</p>` : ''}
        ${state.sms_available ? `<div class="grid" style="margin-top:var(--s3)">
          <div><label class="label" for="tsPhone">یا با پیامک، به این شماره</label>
            <input class="input" id="tsPhone" type="tel" dir="ltr" autocomplete="tel" placeholder="09121234567" value="${esc(state.phone || '')}"></div>
          <div><button class="btn" type="button" id="tsBeginSMS">با پیامک</button></div>
        </div>` : ''}${MSG}</div>`;
      card.querySelector('#tsChip').innerHTML = '<span class="chip">خاموش</span>';
      bind('#tsBegin', async () => {
        try { showAppSetup(await post('/api/account/two-step/begin', { password: val('#tsPw') })); }
        catch (e) { msg(e.message); }
      });
      bind('#tsBeginMail', async () => {
        try {
          const d = await post('/api/account/two-step/email/begin', { password: val('#tsPw'), email: val('#tsMail') });
          showEmailSetup(d.sent_to);
        } catch (e) { msg(e.message); }
      });
      bind('#tsBeginSMS', async () => {
        try {
          const d = await post('/api/account/two-step/sms/begin', { password: val('#tsPw'), phone: val('#tsPhone') });
          showEmailSetup(d.sent_to, true);
        } catch (e) { msg(e.message); }
      });
    }

    function paintOn() {
      const bySMS = state.method === 'sms';
      const byMail = state.method === 'email' || bySMS; // a code that is sent
      card.innerHTML = HEAD + `<div class="card-body">
        <p style="margin:0 0 var(--s3)">${bySMS
          ? `ورود با گذرواژه و کدی انجام می‌شود که با پیامک به <b dir="ltr">${esc(state.phone)}</b> فرستاده می‌شود.`
          : byMail
          ? `ورود با گذرواژه و کدی انجام می‌شود که به <b dir="ltr">${esc(state.email)}</b> فرستاده می‌شود.`
          : 'ورود با گذرواژه و کد شش‌رقمی Google Authenticator انجام می‌شود.'}
          <b>${esc(state.recovery_left)}</b> کد پشتیبان استفاده‌نشده دارید.</p>
        <div class="grid">
          <div><label class="label" for="tsPw">گذرواژه</label>
            <input class="input" id="tsPw" type="password" autocomplete="current-password" dir="ltr"></div>
          <div><label class="label" for="tsOff">${bySMS ? 'کد پیامک یا کد پشتیبان' : byMail ? 'کد ایمیل یا کد پشتیبان' : 'کد برنامه یا کد پشتیبان'}</label>
            <input class="input" id="tsOff" autocomplete="one-time-code" dir="ltr" maxlength="12"></div>
          <div>${byMail ? '<button class="btn" type="button" id="tsSend">فرستادن کد</button>' : ''}
            <button class="btn" type="button" id="tsRenew">کدهای پشتیبان تازه</button>
            <button class="btn btn-danger" type="button" id="tsDisable">خاموش کردن</button></div>
        </div>${MSG}</div>`;
      card.querySelector('#tsChip').innerHTML = '<span class="chip chip-ok">روشن</span>';
      bind('#tsSend', async () => {
        try { msg('کد به ' + (await post('/api/account/two-step/send-code')).sent_to + ' فرستاده شد.', true); }
        catch (e) { msg(e.message); }
      });
      bind('#tsRenew', async () => {
        try { showCodes((await post('/api/account/two-step/recovery', { password: val('#tsPw') })).recovery_codes || []); }
        catch (e) { msg(e.message); }
      });
      bind('#tsDisable', async () => {
        try {
          await post('/api/account/two-step/disable', { password: val('#tsPw'), code: val('#tsOff') });
          await load();
          msg('ورود دومرحله‌ای خاموش شد.', true);
        } catch (e) { msg(e.message); }
      });
    }

    async function load() {
      try {
        const r = await api('/api/account/two-step');
        if (!r.ok) { card.hidden = true; return; }
        state = await r.json();
        card.hidden = false;
        (state.on ? paintOn : paintOff)();
      } catch (_) { card.hidden = true; }
    }
    load();
  }

  window.DocfindTwoStep = { mount };
})();
