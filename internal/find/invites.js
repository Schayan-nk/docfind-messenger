// Registration invite codes on the settings users page (registerroutes.go).
// An administrator mints a code, hands out its link, and sees and revokes the
// ones outstanding. The card hides itself where registration is off, so a
// server started with DOCFIND_REGISTRATION=off shows nothing here.
(function () {
  'use strict';
  const esc = s => String(s == null ? '' : s).replace(/[&<>"]/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c]));

  async function api(path, opts) {
    const r = await fetch(path, Object.assign(
      { credentials: 'same-origin', headers: { 'Content-Type': 'application/json' } }, opts || {}));
    const d = await r.json().catch(() => ({}));
    if (!r.ok) throw new Error(d.error || 'انجام نشد.');
    return d;
  }

  const ROLE = { viewer: 'بیننده', manager: 'مدیر', admin: 'مدیر ارشد' };
  const linkFor = code => location.origin + '/register?i=' + encodeURIComponent(code);

  function status(iv) {
    if (iv.revoked) return ['لغو‌شده', 'bad'];
    if (iv.expires_at && iv.expires_at.slice(0, 4) !== '0001' && new Date(iv.expires_at) < new Date()) return ['منقضی', 'bad'];
    if (iv.max_uses > 0 && iv.uses >= iv.max_uses) return ['مصرف‌شده', ''];
    const left = iv.max_uses > 0 ? (iv.max_uses - iv.uses).toLocaleString('fa') + ' بار مانده' : 'بی‌شمار';
    return ['فعال · ' + left, 'ok'];
  }

  async function copy(text, btn) {
    try {
      await navigator.clipboard.writeText(text);
    } catch (_) {
      const t = document.createElement('textarea');
      t.value = text; t.style.position = 'fixed'; t.style.opacity = '0';
      document.body.appendChild(t); t.select();
      try { document.execCommand('copy'); } catch (__) {}
      t.remove();
    }
    if (btn) { const was = btn.textContent; btn.textContent = 'کپی شد'; setTimeout(() => { btn.textContent = was; }, 1200); }
  }

  function mount(card) {
    if (!card) return;
    const q = id => card.querySelector('#' + id);
    const msg = (text, kind) => {
      const m = q('invMsg');
      m.textContent = text || '';
      m.className = 'msg notice' + (text ? (kind === 'ok' ? ' notice-ok' : kind === '' ? '' : ' notice-bad') : ' hidden');
    };

    async function load() {
      let d;
      try { d = await api('/api/admin/invites'); } catch (_) { card.hidden = true; return; }
      if (!d.enabled) { card.hidden = true; return; }
      card.hidden = false;
      const list = q('invList');
      const codes = d.invites || [];
      if (!codes.length) {
        list.innerHTML = '<p class="hint" style="margin:0">هنوز کدی ساخته نشده است.</p>';
        return;
      }
      list.innerHTML = codes.map(iv => {
        const [label, kind] = status(iv);
        const chip = kind === 'ok' ? 'chip-ok' : kind === 'bad' ? 'chip-bad' : '';
        const canRevoke = !iv.revoked;
        return '<div class="dep-row">' +
          '<div style="min-width:0">' +
            '<code dir="ltr" style="font-size:var(--text-base)">' + esc(iv.code) + '</code> ' +
            '<span class="chip ' + chip + '" style="margin-inline-start:6px">' + esc(label) + '</span>' +
            '<div class="hint" style="margin:2px 0 0">' + esc(ROLE[iv.role] || iv.role) +
              (iv.note ? ' · ' + esc(iv.note) : '') + '</div>' +
          '</div>' +
          '<div style="display:flex;gap:6px;flex:none">' +
            '<button type="button" class="btn btn-sm" data-copy="' + esc(iv.code) + '">کپی نشانی</button>' +
            (canRevoke ? '<button type="button" class="btn btn-sm" data-revoke="' + esc(iv.code) + '">لغو</button>' : '') +
          '</div></div>';
      }).join('');
      list.querySelectorAll('[data-copy]').forEach(b => b.addEventListener('click', () => copy(linkFor(b.dataset.copy), b)));
      list.querySelectorAll('[data-revoke]').forEach(b => b.addEventListener('click', async () => {
        b.disabled = true;
        try { await api('/api/admin/invites?code=' + encodeURIComponent(b.dataset.revoke), { method: 'DELETE' }); await load(); }
        catch (e) { msg(e.message, 'bad'); b.disabled = false; }
      }));
    }

    q('invMint').addEventListener('click', async () => {
      msg('');
      const btn = q('invMint');
      btn.disabled = true;
      try {
        const d = await api('/api/admin/invites', {
          method: 'POST',
          body: JSON.stringify({
            role: q('invRole').value,
            max_uses: Math.max(0, parseInt(q('invUses').value, 10) || 0),
            days: Math.max(0, parseInt(q('invDays').value, 10) || 0),
            note: q('invNote').value.trim(),
          }),
        });
        q('invNote').value = '';
        await load();
        // Put the fresh link in front of the person who just made it, ready to copy.
        msg('کد ساخته شد. نشانی برای همرسانی: ' + d.link, 'ok');
        copy(d.link);
      } catch (e) {
        msg(e.message, 'bad');
      } finally {
        btn.disabled = false;
      }
    });

    load();
  }

  window.DocfindInvites = { mount };
})();
