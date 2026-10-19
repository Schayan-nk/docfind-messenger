// chatpackadmin.js: sticker and GIF packs for those who manage DocFind's
// settings (SPEC 8.8: "the admin uploads sticker packs"). Make a pack, add
// files to it, remove files or the pack. The server checks every file by its
// bytes and strips Lottie expressions (internal/chat/lottie.go); this screen
// only carries files and shows what it said.
import { el, fa, svg, toast } from '/chatstate.js';
import { openPanel, ask, row, api } from '/chatgroup.js';

const ICON = { plus: '<path d="M12 5v14M5 12h14"/>', pack: '<rect x="3" y="3" width="18" height="18" rx="4"/><path d="M8 12h8M12 8v8"/>', trash: '<path d="M3 6h18M8 6V4h8v2M19 6l-1 14H6L5 6"/>' };
const fail = e => toast((e && e.text) || 'انجام نشد؛ دوباره تلاش کنید.');
const NAME = { sticker: 'برچسب', gif: 'گیف' };

export async function openPackAdmin(kind, changed) {
  const p = openPanel();
  const list = el('div');
  const paint = async () => {
    let j; try { j = await api('/api/chat/packs?kind=' + kind); } catch (e) { fail(e); return; }
    const rows = [row(ICON.plus, `بستهٔ ${NAME[kind]} تازه`, async () => {
      const v = await ask({ title: `بستهٔ ${NAME[kind]} تازه`, fields: [{ id: 'title', label: 'نام بسته', max: 64 }], ok: 'ساختن' });
      if (!v || !v.title.trim()) return;
      try { const r = await api('/api/chat/packs', { kind, title: v.title.trim() }); changed && changed(); openPack(p, r.pack, kind, changed, paint); } catch (e) { fail(e); }
    })];
    for (const pk of j.packs || []) rows.push(row(ICON.pack, pk.title, () => openPack(p, pk, kind, changed, paint), { sub: `${fa(pk.items.length)} ${NAME[kind]}` }));
    list.replaceChildren(...rows);
  };
  p.push(`بسته‌های ${NAME[kind]}`, list, { refresh: paint });
  paint();
}

function openPack(p, pack, kind, changed, back) {
  const body = el('div', 'x-form');
  const grid = el('div', 'x-grid' + (kind === 'gif' ? ' gifs' : ''));
  const note = el('div', 'x-when', kind === 'sticker'
    ? 'برچسب: WebP تا ۵۱۲ کیلوبایت، TGS (لاتی) تا ۶۴ کیلوبایت، WebM تا ۲۵۶ کیلوبایت.'
    : 'گیف: GIF، MP4 یا WebM تا ۸ مگابایت. کلیدواژه‌ها جستجو را ممکن می‌کنند.');
  const add = el('button', 'btn btn-primary', `افزودن ${NAME[kind]}`); add.type = 'button';
  const del = el('button', 'btn m-danger', 'حذف بسته'); del.type = 'button';
  const paint = async () => {
    let j; try { j = await api('/api/chat/packs?kind=' + kind); } catch (e) { fail(e); return; }
    const cur = (j.packs || []).find(x => x.id === pack.id) || { items: [] };
    grid.replaceChildren(...cur.items.map(it => {
      const t = el('div', 'x-tile');
      if (it.format === 'webp' || it.format === 'gif') { const i = el('img'); i.src = `/api/chat/media/${it.media}`; i.alt = it.emoji || ''; t.appendChild(i); }
      else if (it.format === 'tgs') t.appendChild(el('span', '', (it.emoji || '🙂') + ' TGS'));
      else { const v = el('video'); v.src = `/api/chat/media/${it.media}`; v.muted = true; v.loop = true; v.playsInline = true; v.autoplay = !matchMedia('(prefers-reduced-motion: reduce)').matches; t.appendChild(v); }
      const x = el('button', 'x-del'); x.type = 'button'; x.setAttribute('aria-label', 'حذف'); x.innerHTML = svg('<path d="M18 6L6 18M6 6l12 12"/>');
      x.addEventListener('click', async () => {
        try { await api(`/api/chat/packs/items/${it.id}/delete`, {}); changed && changed(); paint(); } catch (e) { fail(e); }
      });
      t.appendChild(x);
      return t;
    }));
    if (!cur.items.length) grid.replaceChildren(el('div', 'x-empty', 'این بسته هنوز خالی است.'));
  };
  add.addEventListener('click', () => {
    const i = el('input'); i.type = 'file'; i.multiple = true; i.hidden = true;
    i.accept = kind === 'sticker' ? '.webp,.tgs,.webm,image/webp,video/webm' : '.gif,.mp4,.webm,image/gif,video/mp4,video/webm';
    i.addEventListener('change', async () => {
      const files = [...i.files]; i.remove(); if (!files.length) return;
      const v = await ask({ title: `افزودن ${fa(files.length)} ${NAME[kind]}`, fields: kind === 'sticker'
        ? [{ id: 'emoji', label: 'شکلک مربوط (برای پیشنهاد)', value: '🙂', max: 8 }]
        : [{ id: 'keywords', label: 'کلیدواژه‌ها (فارسی یا انگلیسی)', max: 200 }], ok: 'بارگذاری' });
      if (!v) return;
      let ok = 0;
      for (const f of files) {
        const fd = new FormData(); fd.append('file', f); fd.append('emoji', v.emoji || ''); fd.append('keywords', v.keywords || '');
        try {
          const r = await fetch(`/api/chat/packs/${pack.id}/items`, { method: 'POST', credentials: 'same-origin', body: fd });
          const j = await r.json().catch(() => ({}));
          if (!r.ok) { toast(`${f.name}: ${j.error || 'پذیرفته نشد.'}`); continue; }
          ok++;
        } catch { toast('اتصال برقرار نیست؛ دوباره تلاش کنید.'); break; }
      }
      if (ok) { toast(`${fa(ok)} فایل افزوده شد.`); changed && changed(); paint(); }
    });
    document.body.appendChild(i); i.click();
  });
  del.addEventListener('click', async () => {
    const v = await ask({ title: 'حذف بسته', text: `بستهٔ «${pack.title}» حذف شود؟ پیام‌هایی که پیش‌تر فرستاده شده‌اند می‌مانند.`, ok: 'حذف', danger: true });
    if (!v) return;
    try { await api(`/api/chat/packs/${pack.id}/delete`, {}); changed && changed(); p.pop(); back && back(); } catch (e) { fail(e); }
  });
  body.append(note, add, grid, del);
  p.push(pack.title, body, { refresh: paint });
  paint();
}
