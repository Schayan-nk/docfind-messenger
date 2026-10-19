// What each role gets by default, edited on the settings page (asked for on
// 1405/07/12): the manager and viewer templates -- and roles an administrator
// makes here -- as checkboxes, grouped like
// the permission matrix, saved whole (PUT /api/permissions/role) and in force
// on everybody's next request. Administrators hold everything and are not
// listed. Items held only by name, and administrator-only ones, are shown but
// cannot be ticked: no template may give them.
(function () {
  'use strict';
  // Mounted by the settings page once the users section is drawn: it comes
  // from a <template>, so the card is not there when this file loads.
  window.DocfindRoles = { mount };
  function mount(card) {
  if (!card) return;
  const $ = s => card.querySelector(s);
  const esc = s => String(s ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  // Every role's name, from the server: the built-in ones and those an
  // administrator made (asked for on 1405/07/12, roletemplates.go).
  let NAMES = { manager: 'مدیر', viewer: 'بیننده' };
  let data = null;

  async function api(path, opts = {}) {
    const r = await fetch(path, { credentials: 'same-origin', headers: { 'Content-Type': 'application/json' }, ...opts });
    const d = await r.json().catch(() => ({}));
    if (!r.ok) throw new Error(d.error || 'انجام نشد.');
    return d;
  }
  function note(text, ok) {
    const m = $('#roleMsg');
    m.textContent = text;
    m.className = 'msg notice ' + (ok ? 'notice-ok' : 'notice-bad');
  }

  function draw() {
    const role = $('#roleSel').value;
    const has = new Set(data.templates[role] || []);
    const def = new Set((data.defaults || {})[role] || []);
    $('#roleState').textContent = (data.edited || {})[role]
      ? 'این نقش ویرایش شده است؛ با «بازگشت به پیش‌فرض» به حالت اول برمی‌گردد.'
      : 'پیش‌فرض اصلی نرم‌افزار.';
    $('#roleBody').innerHTML = (data.groups || []).map(g => {
      const rows = g.items.map(it => {
        const locked = it.admin_only || it.explicit;
        const why = it.admin_only ? ' (فقط مدیر ارشد)' : it.explicit ? ' (فقط با اجازهٔ فردی)' : '';
        const changed = !locked && has.has(it.id) !== def.has(it.id) ? ' style="font-weight:700"' : '';
        return `<label class="role-row"${changed}><input type="checkbox" data-id="${esc(it.id)}" ${has.has(it.id) ? 'checked' : ''} ${locked ? 'disabled' : ''}>
          ${esc(it.label)}<span class="hint" style="margin:0">${why}</span></label>`;
      }).join('');
      return `<div class="perm-group"><h3>${esc(g.name)}</h3>${rows}</div>`;
    }).join('');
  }

  // The role menu from the server's list (the administrator is not edited),
  // and the list handed to the rest of the settings page for its own menus.
  function fillRoles(keep) {
    const roles = (data.roles || []).filter(r => r.id !== 'admin');
    NAMES = Object.fromEntries((data.roles || []).map(r => [r.id, r.name]));
    const sel = $('#roleSel');
    sel.innerHTML = roles.map(r => `<option value="${esc(r.id)}">${esc(r.name)}${r.custom ? '' : ' (اصلی)'}${r.users ? ' · ' + Number(r.users).toLocaleString('fa-IR') + ' نفر' : ''}</option>`).join('');
    if (keep && roles.some(r => r.id === keep)) sel.value = keep;
    const custom = !!(data.roles || []).find(r => r.id === sel.value && r.custom);
    $('#roleRename').hidden = $('#roleDel').hidden = !custom;
    window.DOCFIND_ROLES = data.roles || [];
    window.dispatchEvent(new CustomEvent('docfind:roles'));
  }

  async function load(keep) {
    try {
      data = await api('/api/permissions');
      fillRoles(keep || $('#roleSel').value);
      draw();
    } catch (e) { note(e.message); }
  }

  $('#roleSel').addEventListener('change', () => { fillRoles($('#roleSel').value); draw(); });
  // A role of one's own: it starts with what بیننده has, then is edited here.
  $('#roleAdd').addEventListener('click', async () => {
    const name = $('#roleNew').value.trim();
    if (!name) { note('نام نقش تازه را بنویسید.'); return; }
    try {
      const d = await api('/api/permissions/roles', { method: 'POST', body: JSON.stringify({ name }) });
      $('#roleNew').value = '';
      await load(d.id);
      note('نقش «' + d.name + '» ساخته شد، با دسترسی‌های بیننده؛ دسترسی‌هایش را تیک بزنید و ذخیره کنید.', true);
    } catch (e) { note(e.message); }
  });
  $('#roleRename').addEventListener('click', async () => {
    const id = $('#roleSel').value, name = $('#roleNew').value.trim();
    if (!name) { note('نام تازه را در کادر «نام نقش» بنویسید.'); return; }
    try {
      await api('/api/permissions/roles', { method: 'PATCH', body: JSON.stringify({ id, name }) });
      $('#roleNew').value = '';
      await load(id);
      note('نام نقش عوض شد.', true);
    } catch (e) { note(e.message); }
  });
  $('#roleDel').addEventListener('click', async () => {
    const id = $('#roleSel').value;
    if (!confirm('نقش «' + NAMES[id] + '» حذف شود؟')) return;
    try {
      await api('/api/permissions/roles?id=' + encodeURIComponent(id), { method: 'DELETE' });
      await load('viewer');
      note('نقش حذف شد.', true);
    } catch (e) { note(e.message); }
  });
  $('#roleSave').addEventListener('click', async () => {
    const role = $('#roleSel').value;
    const ids = Array.from(card.querySelectorAll('input[data-id]:checked:not(:disabled)')).map(i => i.dataset.id);
    $('#roleSave').disabled = true;
    try {
      const d = await api('/api/permissions/role', { method: 'PUT', body: JSON.stringify({ role, ids }) });
      data.templates[role] = d.template;
      data.edited[role] = d.edited;
      draw();
      note('پیش‌فرض نقش «' + NAMES[role] + '» ذخیره شد و از همین حالا برای همهٔ این نقش اعمال می‌شود.', true);
    } catch (e) { note(e.message); } finally { $('#roleSave').disabled = false; }
  });
  $('#roleReset').addEventListener('click', async () => {
    const role = $('#roleSel').value;
    try {
      const d = await api('/api/permissions/role', { method: 'PUT', body: JSON.stringify({ role, reset: true }) });
      data.templates[role] = d.template;
      data.edited[role] = d.edited;
      draw();
      note('نقش «' + NAMES[role] + '» به پیش‌فرض اصلی برگشت.', true);
    } catch (e) { note(e.message); }
  });
  load();
  }
})();
