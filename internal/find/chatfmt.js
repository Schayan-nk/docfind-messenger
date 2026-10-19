// chatfmt.js: message formatting (docs/chat/SPEC.md 6.3). Entities are
// Telegram's {type, offset, length} in UTF-16 units, which is how a
// JavaScript string is indexed, so offsets slice the text directly. Every
// format is built as elements around text nodes: no message text ever goes
// through innerHTML (security check 5), and a link's target is used only when
// it is http, https, mailto or tel.
import { reduced } from '/chatcore.js';

const el = (tag, cls) => { const e = document.createElement(tag); if (cls) e.className = cls; return e; };

export function safeHref(raw) {
  try {
    const u = new URL(String(raw).trim());
    if (['http:', 'https:', 'mailto:', 'tel:'].includes(u.protocol) && (u.protocol === 'mailto:' || u.protocol === 'tel:' || u.host)) return u.href;
  } catch { }
  return null;
}
const hasScheme = s => /^[a-z][a-z0-9+.-]*:/i.test(s);

function hrefFor(e, text) {
  switch (e.type) {
    case 'text_link': return safeHref(e.url || '');
    case 'url': return safeHref(hasScheme(text) ? text : 'https://' + text);
    case 'email': return safeHref('mailto:' + text);
    case 'phone_number': return safeHref('tel:' + text.replace(/[^\d+]/g, ''));
  }
  return null;
}

// One element per entity. Returns [outer, inner]: children go into inner.
function wrap(e, text, key, state) {
  switch (e.type) {
    case 'bold': return one(el('strong'));
    case 'italic': return one(el('em'));
    case 'underline': return one(el('u'));
    case 'strikethrough': return one(el('s'));
    case 'code': return one(el('code', 'm-code'));
    case 'pre': {
      const pre = el('pre', 'm-pre'), code = el('code');
      if (e.language) pre.dataset.lang = e.language;
      pre.appendChild(code);
      return [pre, code];
    }
    case 'blockquote': return one(el('blockquote', 'm-quote'));
    case 'expandable_blockquote': {
      const q = el('blockquote', 'm-quote exp'); q.dataset.k = key;
      if (state && state.expanded && state.expanded.has(key)) q.classList.add('open');
      q.tabIndex = 0; q.setAttribute('role', 'button'); q.setAttribute('aria-expanded', q.classList.contains('open') ? 'true' : 'false');
      return one(q);
    }
    case 'spoiler': {
      const s = el('span', 'm-spoiler'); s.dataset.k = key;
      if (!(state && state.revealed && state.revealed.has(key))) {
        s.tabIndex = 0; s.setAttribute('role', 'button'); s.setAttribute('aria-label', 'متن پنهان؛ برای دیدن بزنید');
        const cover = el('span', 'm-spoil'); cover.setAttribute('aria-hidden', 'true');
        queueMicrotask(() => s.appendChild(cover));
      }
      return one(s);
    }
    case 'text_link': case 'url': case 'email': case 'phone_number': {
      const href = hrefFor(e, text);
      if (!href) return one(el('span'));
      const a = el('a', 'm-link'); a.href = href; a.rel = 'noopener noreferrer nofollow'; a.target = '_blank';
      // A link whose words are not its address is confirmed before it opens.
      if (e.type === 'text_link' && text.trim() !== href && text.trim() !== e.url) a.dataset.hidden = '1';
      return one(a);
    }
    case 'mention': { const a = el('span', 'm-mention'); a.dataset.handle = text.replace(/^@/, '').toLowerCase(); return one(a); }
    case 'text_mention': { const a = el('span', 'm-mention'); a.dataset.user = e.user || ''; a.tabIndex = 0; a.setAttribute('role', 'link'); return one(a); }
    case 'hashtag': case 'cashtag': { const a = el('span', 'm-tag'); a.dataset.tag = text; return one(a); }
    case 'bot_command': { const a = el('span', 'm-cmd'); a.dataset.cmd = text; return one(a); }
  }
  return one(el('span'));
}
const one = x => [x, x];

const cmp = (a, b) => a.offset - b.offset || b.end - a.end;

// renderText builds a message's text with its formatting. Overlapping spans
// are split so that elements always nest; state carries which spoilers were
// opened and which quotes expanded, so a re-render keeps them as they were.
export function renderText(text, entities, keyBase = '', state = null) {
  const root = document.createDocumentFragment();
  const list = [];
  for (const e of Array.isArray(entities) ? entities : []) {
    if (!e || typeof e.type !== 'string') continue;
    const offset = e.offset | 0, length = e.length | 0;
    if (offset < 0 || length <= 0 || offset + length > text.length) continue;
    list.push({ ...e, offset, end: offset + length, k: keyBase + ':' + offset + ':' + e.type });
  }
  build(root, 0, text.length, list.sort(cmp));
  return root;

  function build(parent, from, to, items) {
    let pos = from;
    while (items.length) {
      const e = items.shift();
      const start = Math.max(e.offset, pos);
      if (start >= e.end) continue;
      if (start > pos) parent.appendChild(document.createTextNode(text.slice(pos, start)));
      const inner = [], rest = [];
      for (const c of items) {
        if (c.offset >= e.end) rest.push(c);
        else if (c.end <= e.end) inner.push(c);
        else { inner.push({ ...c, end: e.end }); rest.push({ ...c, offset: e.end }); }
      }
      items = rest.sort(cmp);
      const [outer, box] = wrap(e, text.slice(start, e.end), e.k, state);
      build(box, start, e.end, inner);
      parent.appendChild(outer);
      pos = e.end;
    }
    if (pos < to) parent.appendChild(document.createTextNode(text.slice(pos, to)));
  }
}

// revealSpoiler opens a spoiler with a circle that grows from the tap point.
export function revealSpoiler(sp, x, y) {
  const cover = sp.querySelector(':scope > .m-spoil');
  sp.removeAttribute('role'); sp.removeAttribute('tabindex'); sp.removeAttribute('aria-label');
  if (!cover) return;
  const r = sp.getBoundingClientRect();
  cover.style.setProperty('--sx', (x - r.left) + 'px');
  cover.style.setProperty('--sy', (y - r.top) + 'px');
  if (reduced() || document.hidden) { cover.remove(); return; }
  const far = Math.hypot(r.width, r.height) + 6;
  cover.animate([{ '--spoil-r': '0px' }, { '--spoil-r': far + 'px' }], { duration: 420, easing: 'cubic-bezier(0.2, 0, 0, 1)', fill: 'forwards' })
    .finished.finally(() => cover.remove());
}

// ------------------------------------------------------------ the composer
//
// The composer is a plain text field; formatting is typed as markers, the
// way Telegram's own clients accept it: **bold**, __italic__, ++underline++,
// ~~strike~~, ||spoiler||, `code`, ```language (newline) block```,
// [words](address), and lines that start with "> " for a quote. A backslash
// before a marker keeps it as text. The selection toolbar and the shortcuts
// only insert these markers.

export const MARKERS = [['**', 'bold'], ['__', 'italic'], ['++', 'underline'], ['~~', 'strikethrough'], ['||', 'spoiler']];
const ESCAPABLE = '*_+~|`[]\\>';

function inline(s) {
  let text = ''; const ents = [];
  let i = 0;
  while (i < s.length) {
    const c = s[i];
    if (c === '\\' && i + 1 < s.length && ESCAPABLE.includes(s[i + 1])) { text += s[i + 1]; i += 2; continue; }
    if (s.startsWith('```', i)) {
      const end = s.indexOf('```', i + 3);
      if (end > i + 3) {
        let body = s.slice(i + 3, end), language = '';
        const m = body.match(/^([A-Za-z0-9_+#-]{1,32})\n/);
        if (m) { language = m[1]; body = body.slice(m[0].length); }
        body = body.replace(/^\n/, '').replace(/\n$/, '');
        if (body) { ents.push({ type: 'pre', offset: text.length, length: body.length, ...(language ? { language } : {}) }); text += body; }
        i = end + 3; continue;
      }
    }
    if (c === '`') {
      const end = s.indexOf('`', i + 1);
      if (end > i + 1 && !s.slice(i + 1, end).includes('\n')) {
        const body = s.slice(i + 1, end);
        ents.push({ type: 'code', offset: text.length, length: body.length }); text += body; i = end + 1; continue;
      }
    }
    if (c === '[') {
      const mid = s.indexOf('](', i + 1);
      const end = mid > 0 ? s.indexOf(')', mid + 2) : -1;
      if (mid > i + 1 && end > mid + 2 && !s.slice(i, end).includes('\n')) {
        const url = s.slice(mid + 2, end).trim();
        const href = safeHref(hasScheme(url) ? url : 'https://' + url);
        const r = inline(s.slice(i + 1, mid));
        if (href && r.text) {
          const off = text.length;
          ents.push({ type: 'text_link', offset: off, length: r.text.length, url: href });
          for (const e of r.ents) ents.push({ ...e, offset: e.offset + off });
          text += r.text; i = end + 1; continue;
        }
      }
    }
    const mk = MARKERS.find(([m]) => s.startsWith(m, i));
    if (mk) {
      const end = s.indexOf(mk[0], i + 2);
      if (end > i + 2) {
        const r = inline(s.slice(i + 2, end));
        if (r.text.trim()) {
          const off = text.length;
          ents.push({ type: mk[1], offset: off, length: r.text.length });
          for (const e of r.ents) ents.push({ ...e, offset: e.offset + off });
          text += r.text; i = end + 2; continue;
        }
      }
    }
    text += c; i++;
  }
  return { text, ents };
}

// parseComposer returns {text, entities} for what is in the composer.
// mentions maps a display name chosen from the @ list to its username: each
// "@name" still in the text becomes a link to that person.
export function parseComposer(src, mentions = new Map()) {
  const parts = [];
  for (const line of src.replace(/\r\n?/g, '\n').split('\n')) {
    const quote = /^>\s?/.test(line);
    const body = quote ? line.replace(/^>\s?/, '') : line;
    const last = parts[parts.length - 1];
    if (last && last.quote === quote) last.s += '\n' + body; else parts.push({ quote, s: body });
  }
  let text = ''; let ents = [];
  parts.forEach((p, n) => {
    if (n) text += '\n';
    const r = inline(p.s), off = text.length;
    if (p.quote && r.text) ents.push({ type: 'blockquote', offset: off, length: r.text.length });
    for (const e of r.ents) ents.push({ ...e, offset: e.offset + off });
    text += r.text;
  });
  for (const [name, user] of mentions) {
    const needle = '@' + name;
    for (let at = text.indexOf(needle); at >= 0; at = text.indexOf(needle, at + needle.length)) {
      const end = at + needle.length;
      if (!ents.some(e => (e.type === 'code' || e.type === 'pre' || e.type === 'text_link') && at < e.offset + e.length && e.offset < end)) ents.push({ type: 'text_mention', offset: at, length: needle.length, user });
    }
  }
  // Leading and trailing blanks go; the offsets follow the start.
  const lead = text.length - text.trimStart().length;
  text = text.trim();
  ents = ents.map(e => ({ ...e, offset: e.offset - lead }))
    .map(e => ({ ...e, length: Math.min(e.offset + e.length, text.length) - Math.max(e.offset, 0), offset: Math.max(e.offset, 0) }))
    .filter(e => e.length > 0).sort((a, b) => a.offset - b.offset).slice(0, 100);
  return { text, entities: ents };
}

// toComposer puts a sent message back into marker form, for editing.
export function toComposer(text, entities, mentions) {
  const opens = new Map(), closes = new Map();
  const add = (m, at, s, outerFirst) => { if (!m.has(at)) m.set(at, []); m.get(at).push({ s, outerFirst }); };
  const kept = (Array.isArray(entities) ? entities : []).filter(e => e && e.length > 0).sort((a, b) => a.offset - b.offset || b.length - a.length);
  const quoteStarts = new Set();
  for (const e of kept) {
    const end = e.offset + e.length;
    const mk = MARKERS.find(([, t]) => t === e.type);
    if (mk) { add(opens, e.offset, mk[0], e.length); add(closes, end, mk[0], -e.length); }
    else if (e.type === 'code') { add(opens, e.offset, '`', e.length); add(closes, end, '`', -e.length); }
    else if (e.type === 'pre') { add(opens, e.offset, '```' + (e.language || '') + '\n', e.length); add(closes, end, '\n```', -e.length); }
    else if (e.type === 'text_link') { add(opens, e.offset, '[', e.length); add(closes, end, '](' + (e.url || '') + ')', -e.length); }
    else if (e.type === 'blockquote' || e.type === 'expandable_blockquote') {
      quoteStarts.add(e.offset);
      for (let i = e.offset; i < end; i++) if (text[i] === '\n') quoteStarts.add(i + 1);
    }
    else if (e.type === 'text_mention' && mentions) mentions.set(text.slice(e.offset + 1, end), e.user);
  }
  const code = kept.filter(e => e.type === 'code' || e.type === 'pre');
  const inCode = i => code.some(e => i >= e.offset && i < e.offset + e.length);
  let out = '';
  for (let i = 0; i <= text.length; i++) {
    for (const c of (closes.get(i) || []).sort((a, b) => b.outerFirst - a.outerFirst)) out += c.s;
    if (i === text.length) break;
    if (quoteStarts.has(i)) out += '> ';
    for (const o of (opens.get(i) || []).sort((a, b) => b.outerFirst - a.outerFirst)) out += o.s;
    const ch = text[i];
    if (!inCode(i) && (ch === '`' || ch === '[' || ch === '\\' || (ch === text[i + 1] && '*_+~|'.includes(ch)))) out += '\\';
    out += ch;
  }
  return out;
}

// wrapSelection puts a marker pair around the field's selection (or the word
// at the caret), and keeps the same words selected.
export function wrapSelection(ta, open, close = open) {
  let a = ta.selectionStart, b = ta.selectionEnd;
  const v = ta.value;
  if (a === b) {
    while (a > 0 && !/\s/.test(v[a - 1])) a--;
    while (b < v.length && !/\s/.test(v[b])) b++;
  }
  if (a === b) { ta.setRangeText(open + close, a, b, 'end'); ta.selectionStart = ta.selectionEnd = a + open.length; }
  else {
    ta.setRangeText(open + v.slice(a, b) + close, a, b, 'select');
    ta.selectionStart = a + open.length; ta.selectionEnd = b + open.length;
  }
  ta.dispatchEvent(new Event('input', { bubbles: true }));
  ta.focus();
}

// plain gives the text of a message as one line, for previews and copies.
export const plain = s => String(s || '').replace(/\s+/g, ' ').trim();
