// The person editor on the settings page: one «ویرایش» on a user's row opens
// a window with tabs -- name and role, contact, password, picture,
// permissions, the account itself -- where there used to be ten buttons on
// every row, each with its own small form.
//
// It runs inside settings.html and uses that page's helpers (api, say, esc,
// roleList, loadUsers, openPerms, me, allUsers). Every save goes to a route
// that already existed for the old buttons, and the server checks each one;
// nothing here decides what an administrator may do.
//
// THE USER NAME IS SHOWN, NOT EDITED. It is the key of the account in every
// store the server keeps -- grants, preferences, chats, the audit log -- so
// renaming it would orphan all of that.
(function () {
  'use strict';

  const TABS = [
    ['name', 'نام و نقش'], ['contact', 'اطلاعات تماس'], ['password', 'گذرواژه'],
    ['images', 'تصویر'], ['access', 'دسترسی‌ها'], ['account', 'حساب'],
  ];
  const CSS = `
    .ue-tabs { display: flex; flex-wrap: wrap; gap: 4px; border-bottom: 1px solid var(--line); margin: 6px 0 10px; }
    .ue-tabs button { background: none; border: 0; color: var(--fg-muted); font: inherit; padding: 8px 12px;
      border-bottom: 2px solid transparent; cursor: pointer; }
    .ue-tabs button[aria-selected="true"] { color: var(--fg); border-bottom-color: var(--accent); }
    .ue-tabs button:focus-visible { outline: 2px solid var(--ring); outline-offset: 2px; }
    .ue-pane { display: grid; gap: 10px; overflow-y: auto; flex: 1 1 auto; padding-bottom: 6px; }
    .ue-pane .grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 220px), 1fr)); gap: 10px; }
    .ue-pics { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 240px), 1fr)); gap: 14px; }
    .ue-pic { border: 1px solid var(--line); border-radius: 10px; padding: 12px; display: grid; gap: 8px; justify-items: start; }
    .ue-pic img { max-width: 100%; max-height: 140px; border-radius: 8px; background: #fff; }
    .ue-pic .ue-none { color: var(--fg-muted); font-size: var(--text-sm); }
    .ue-row { display: flex; flex-wrap: wrap; gap: 8px; align-items: center; }
    .ue-facts { display: grid; grid-template-columns: max-content 1fr; gap: 6px 14px; font-size: var(--text-sm); }
    .ue-facts dt { color: var(--fg-muted); }`;

  let dlg = null;
  let u = null; // the account as the users list last read it
  let tab = 'name';

  const el = id => document.getElementById(id);
  // settings.html's own `me`: whoever is signed in.
  const isSelf = () => !!(u && typeof me !== 'undefined' && me && u.username === me.username);

  function build() {
    if (dlg) return dlg;
    const st = document.createElement('style');
    st.textContent = CSS;
    document.head.appendChild(st);
    dlg = document.createElement('dialog');
    dlg.className = 'perm-dlg';
    dlg.id = 'ueDlg';
    dlg.setAttribute('aria-labelledby', 'ueHead');
    dlg.innerHTML = `<form method="dialog">
      <h2 class="card-head" style="margin-top:0" id="ueHead">ویرایش <span id="ueWho"></span></h2>
      <nav class="ue-tabs" role="tablist" aria-label="بخش‌های ویرایش کاربر">${TABS.map(([id, label]) =>
        `<button type="button" role="tab" id="ue-t-${id}" data-tab="${id}" aria-controls="uePane">${label}</button>`).join('')}</nav>
      <div class="ue-pane" id="uePane" role="tabpanel"></div>
      <div class="msg notice hidden" id="ueMsg" role="status"></div>
      <div class="perm-foot">
        <button type="button" class="btn btn-accent" id="ueSave">ذخیره</button>
        <button value="cancel" class="btn">بستن</button>
      </div>
    </form>`;
    document.body.appendChild(dlg);
    dlg.querySelector('.ue-tabs').addEventListener('click', e => {
      const b = e.target.closest('button[data-tab]');
      if (b) show(b.dataset.tab);
    });
    // Arrow keys move between tabs, as a tab list does.
    dlg.querySelector('.ue-tabs').addEventListener('keydown', e => {
      if (e.key !== 'ArrowLeft' && e.key !== 'ArrowRight') return;
      const ids = TABS.map(t => t[0]);
      const step = (e.key === 'ArrowLeft') === (document.dir !== 'ltr') ? 1 : -1;
      const next = ids[(ids.indexOf(tab) + step + ids.length) % ids.length];
      show(next);
      el('ue-t-' + next).focus();
      e.preventDefault();
    });
    el('ueSave').addEventListener('click', () => save());
    dlg.addEventListener('close', () => { if (changed) loadUsers(); changed = false; });
    return dlg;
  }

  let changed = false;
  // A never-used account carries Go's zero time, not an empty field.
  const signedIn = t => !!t && new Date(t).getFullYear() > 2000;

  function note(text, kind) { say(el('ueMsg'), text, kind); }
  function field(id, label, value, opts = {}) {
    return `<div><label class="label" for="${id}">${label}</label>
      <input class="input" id="${id}" value="${esc(value || '')}"${opts.ltr ? ' dir="ltr"' : ''}${opts.type ? ` type="${opts.type}"` : ''}${opts.ro ? ' readonly' : ''}${opts.auto ? ` autocomplete="${opts.auto}"` : ''}></div>`;
  }

  function show(id) {
    tab = id;
    for (const [t] of TABS) el('ue-t-' + t).setAttribute('aria-selected', String(t === id));
    el('uePane').setAttribute('aria-labelledby', 'ue-t-' + id);
    el('ueMsg').classList.add('hidden');
    const self = isSelf();
    // Tabs that act at once have no save of their own.
    el('ueSave').hidden = id === 'images' || id === 'access' || id === 'account';
    const pane = el('uePane');
    if (id === 'name') {
      const roles = roleList().map(([r, n]) =>
        `<option value="${esc(r)}"${u.role === r ? ' selected' : ''}>${esc(n)}</option>`).join('');
      pane.innerHTML = `<div class="grid">
          ${field('ueUser', 'نام کاربری', u.username, { ltr: true, ro: true })}
          ${field('ueName', 'نام نمایشی', u.display_name)}
          <div><label class="label" for="ueRole">نقش</label>
            <select class="select" id="ueRole"${self ? ' disabled' : ''}>${roles}</select></div>
        </div>
        <p class="hint" style="margin:0">نام کاربری شناسهٔ حساب در همهٔ داده‌های برنامه است و عوض نمی‌شود.
          ${self ? 'نقش خودتان را از این‌جا نمی‌توانید تغییر دهید.' : 'تغییر نقش، کاربر را از همهٔ دستگاه‌ها خارج می‌کند.'}</p>`;
    } else if (id === 'contact') {
      pane.innerHTML = `<div class="grid">
          ${field('ueEmail', 'ایمیل', u.email, { ltr: true, type: 'email' })}
          ${field('ueEmail2', 'ایمیل دوم', u.other_email, { ltr: true, type: 'email' })}
          ${field('uePhone', 'تلفن همراه', u.phone, { ltr: true, type: 'tel' })}
        </div>
        <p class="hint" style="margin:0">تلفن همراه برای پیامک‌ها و ورود دومرحله‌ای به کار می‌رود؛ مثل ۰۹۱۲۱۲۳۴۵۶۷ بنویسید.</p>`;
    } else if (id === 'password') {
      pane.innerHTML = `<div class="grid">
          ${field('uePw', 'گذرواژهٔ تازه', '', { ltr: true, type: 'password', auto: 'new-password' })}
          ${field('uePw2', 'تکرار گذرواژه', '', { ltr: true, type: 'password', auto: 'new-password' })}
        </div>
        <p class="hint" style="margin:0">دست‌کم ۱۰ نویسه. با تغییر گذرواژه، کاربر از همهٔ دستگاه‌ها خارج می‌شود. خالی بگذارید تا عوض نشود.</p>
        <label class="check"><input type="checkbox" id="ueUpd"${u.update_required ? ' checked' : ''}> در ورود بعدی گذرواژهٔ تازه بگیرد</label>
        ${self ? '' : `<div class="ue-row">
          <span class="hint" style="margin:0">ورود دومرحله‌ای: ${u.two_step ? 'روشن' : 'خاموش'}؛ کلیدهای عبور: ${faNum.format(u.passkeys || 0)}</span>
          ${u.two_step ? '<button type="button" class="btn btn-sm" id="ue2fa">برداشتن ورود دومرحله‌ای</button>' : ''}
          ${u.passkeys ? '<button type="button" class="btn btn-sm" id="uePk">حذف کلیدهای عبور</button>' : ''}
        </div>`}`;
      if (el('ue2fa')) el('ue2fa').onclick = () => reset('/api/users/two-step/reset',
        `ورود دومرحله‌ای ${u.username} برداشته شود؟ از همهٔ دستگاه‌ها خارج می‌شود و باید دوباره راه‌اندازی کند.`,
        'ورود دومرحله‌ای برداشته شد.', { two_step: false });
      if (el('uePk')) el('uePk').onclick = () => reset('/api/users/passkeys/reset',
        `همهٔ کلیدهای عبور ${u.username} حذف شود؟ از همهٔ دستگاه‌ها خارج می‌شود.`,
        'کلیدهای عبور حذف شد.', { passkeys: 0 });
    } else if (id === 'images') {
      const q = '?u=' + encodeURIComponent(u.username) + '&v=' + Date.now();
      pane.innerHTML = `<div class="ue-pics">
          <div class="ue-pic"><b>تصویر</b>
            ${u.has_avatar ? `<img alt="تصویر ${esc(u.username)}" src="/api/account/photo${q}">` : '<span class="ue-none">تصویری ندارد.</span>'}
            <div class="ue-row"><button type="button" class="btn btn-sm" data-pick="photo">انتخاب تصویر</button>
              ${u.has_avatar ? '<button type="button" class="btn btn-sm" data-clear="photo">حذف تصویر</button>' : ''}</div></div>
        </div>
        <p class="hint" style="margin:0">JPEG، PNG، GIF یا WebP تا ۴ مگابایت.</p>`;
      pane.querySelectorAll('[data-pick]').forEach(b => { b.onclick = () => pick(b.dataset.pick); });
      pane.querySelectorAll('[data-clear]').forEach(b => { b.onclick = () => clearImage(b.dataset.clear); });
    } else if (id === 'access') {
      pane.innerHTML = self
        ? '<p class="hint" style="margin:0">دسترسی‌های خودتان را از این‌جا نمی‌توانید تغییر دهید.</p>'
        : `<div class="ue-row"><button type="button" class="btn" id="uePerms">دسترسی‌ها…</button></div>
        <p class="hint" style="margin:0">آنچه این کاربر بیش یا کمتر از پیش‌فرض نقشش می‌تواند.</p>`;
      if (el('uePerms')) el('uePerms').onclick = () => openPerms(u.username);
    } else if (id === 'account') {
      pane.innerHTML = `<dl class="ue-facts">
          <dt>وضعیت</dt><dd>${u.disabled ? '<span class="chip chip-bad">غیرفعال</span>' : '<span class="chip chip-ok">فعال</span>'}</dd>
          <dt>ساخته‌شده</dt><dd>${esc(ago(u.created_at))}</dd>
          <dt>آخرین ورود</dt><dd>${signedIn(u.last_login) ? esc(ago(u.last_login)) : 'هرگز'}</dd>
        </dl>
        ${self ? '<p class="hint" style="margin:0">حساب خودتان را از این‌جا نمی‌توانید غیرفعال یا حذف کنید.</p>' : `<div class="ue-row">
          <button type="button" class="btn" id="ueToggle">${u.disabled ? 'فعال کردن' : 'غیرفعال کردن'}</button>
          <button type="button" class="btn btn-danger" id="ueDel">حذف کاربر</button>
        </div>`}`;
      if (el('ueToggle')) el('ueToggle').onclick = toggle;
      if (el('ueDel')) el('ueDel').onclick = remove;
    }
  }

  async function call(path, method, body) {
    const r = await api(path, { method, body: body === undefined ? undefined : JSON.stringify(body) });
    const d = await r.json().catch(() => ({}));
    if (!r.ok) throw new Error(d.error || 'انجام نشد.');
    return d;
  }

  async function save() {
    const btn = el('ueSave');
    btn.disabled = true;
    try {
      if (tab === 'name') {
        const role = el('ueRole').value;
        await call('/api/users/profile', 'PUT', { username: u.username, display_name: el('ueName').value,
          english_name: u.english_name || '', personnel_code: u.personnel_code || '' });
        // The role only when it changed: PUT /api/users signs the person out.
        if (!isSelf() && role !== u.role) await call('/api/users', 'PUT', { username: u.username, role });
        Object.assign(u, { display_name: el('ueName').value.trim(), role });
        note('ذخیره شد.', true);
      } else if (tab === 'contact') {
        await call('/api/users/profile', 'PUT', { username: u.username, email: el('ueEmail').value,
          second_email: el('ueEmail2').value });
        const phone = el('uePhone').value.trim();
        if (phone !== (u.phone || '')) await call('/api/users/phone', 'PUT', { username: u.username, phone });
        Object.assign(u, { email: el('ueEmail').value.trim(), other_email: el('ueEmail2').value.trim(), phone });
        note('ذخیره شد.', true);
      } else if (tab === 'password') {
        const pw = el('uePw').value, pw2 = el('uePw2').value;
        if (pw || pw2) {
          if (pw.length < 10) throw new Error('گذرواژه باید دست‌کم ۱۰ نویسه باشد.');
          if (pw !== pw2) throw new Error('دو گذرواژه یکی نیستند.');
          await call('/api/users/password', 'POST', { username: u.username, password: pw });
        }
        await call('/api/users/profile', 'PUT', { username: u.username, update_password: el('ueUpd').checked });
        u.update_required = el('ueUpd').checked;
        el('uePw').value = el('uePw2').value = '';
        note(pw ? 'گذرواژه عوض شد؛ کاربر از همهٔ دستگاه‌ها خارج شد.' : 'ذخیره شد.', true);
      }
      changed = true;
    } catch (err) {
      note(err.message || 'انجام نشد.');
    } finally {
      btn.disabled = false;
    }
  }

  async function reset(path, question, done, patch) {
    if (!confirm(question)) return;
    try {
      await call(path + '?u=' + encodeURIComponent(u.username), 'POST');
      Object.assign(u, patch);
      changed = true;
      show('password');
      note(done, true);
    } catch (err) { note(err.message); }
  }

  async function toggle() {
    try {
      await call('/api/users', 'PUT', { username: u.username, disabled: !u.disabled });
      u.disabled = !u.disabled;
      changed = true;
      show('account');
      note(u.disabled ? 'حساب غیرفعال شد و از همهٔ دستگاه‌ها خارج شد.' : 'حساب فعال شد.', true);
    } catch (err) { note(err.message); }
  }

  async function remove() {
    if (!confirm(`${u.username} حذف شود؟ این کار برگشت‌پذیر نیست.`)) return;
    try {
      await call('/api/users?username=' + encodeURIComponent(u.username), 'DELETE');
      changed = true;
      dlg.close();
      say(el('listMsg'), `${u.username} حذف شد.`, true);
    } catch (err) { note(err.message); }
  }

  // A picture, chosen and sent at once.
  function pick() {
    const input = document.createElement('input');
    input.type = 'file';
    input.accept = 'image/png,image/jpeg,image/gif,image/webp';
    input.onchange = async () => {
      const f = input.files && input.files[0];
      if (!f) return;
      if (f.size > 4 * 1024 * 1024) { note('حجم تصویر بیش از ۴ مگابایت است.'); return; }
      note('در حال ارسال…', 'info');
      const r = await api('/api/account/photo?u=' + encodeURIComponent(u.username), { method: 'POST',
        headers: { 'Content-Type': f.type || 'application/octet-stream' }, body: f });
      const d = await r.json().catch(() => ({}));
      if (!r.ok) { note(d.error || 'ارسال انجام نشد.'); return; }
      u.has_avatar = true;
      changed = true;
      show('images');
      note('تصویر ذخیره شد.', true);
    };
    input.click();
  }

  async function clearImage() {
    if (!confirm('تصویر حذف شود؟')) return;
    try {
      await call('/api/account/photo?u=' + encodeURIComponent(u.username), 'DELETE');
      u.has_avatar = false;
      changed = true;
      show('images');
      note('تصویر حذف شد.', true);
    } catch (err) { note(err.message); }
  }

  function open(username, startTab) {
    const found = (typeof allUsers !== 'undefined' ? allUsers : []).find(x => x.username === username);
    if (!found) return;
    u = Object.assign({}, found);
    build();
    el('ueWho').textContent = u.display_name && u.display_name !== u.username
      ? u.display_name + ' (' + u.username + ')' : u.username;
    show(startTab || 'name');
    if (!dlg.open) dlg.showModal();
  }

  window.DocfindUserEdit = { open };
})();
