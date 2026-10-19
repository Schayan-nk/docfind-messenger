// The date picker, shared by the report pages (dashboard.html) and the
// evaluation page, which used to have the markup of a date field without the
// picker behind it and showed two bare text boxes.
//
// Custom rather than <input type="date">, which only speaks Gregorian and
// writes YYYY-MM-DD; most filters here compare against a Jalali date held as
// text, where that value matches nothing at all. A field whose column is a
// real DATETIME says so with data-cal="gregorian".
//
//   docfindDatePicker.attach(input)  opens the picker on click and on focus
//   docfindDatePicker.show(input)    opens it now
//   docfindDatePicker.close()
//
// Choosing a day writes "1404/07/02" into the field and dispatches a change
// event, so a page reacts to a picked date exactly as to a typed one.
(function () {
  'use strict';

  // The standard 33-year-cycle conversion. The browser has no Jalali
  // arithmetic of its own (Intl can format a Jalali date but not parse one
  // back), so it is written out here rather than approximated.
  function gregorianToJalali(gy, gm, gd) {
    const g_d_m = [0, 31, 59, 90, 120, 151, 181, 212, 243, 273, 304, 334];
    let jy = gy <= 1600 ? 0 : 979;
    gy -= gy <= 1600 ? 621 : 1600;
    const gy2 = gm > 2 ? gy + 1 : gy;
    let days = 365 * gy + Math.floor((gy2 + 3) / 4) - Math.floor((gy2 + 99) / 100)
      + Math.floor((gy2 + 399) / 400) - 80 + gd + g_d_m[gm - 1];
    jy += 33 * Math.floor(days / 12053);
    days %= 12053;
    jy += 4 * Math.floor(days / 1461);
    days %= 1461;
    if (days > 365) { jy += Math.floor((days - 1) / 365); days = (days - 1) % 365; }
    const jm = days < 186 ? 1 + Math.floor(days / 31) : 7 + Math.floor((days - 186) / 30);
    const jd = 1 + (days < 186 ? days % 31 : (days - 186) % 30);
    return [jy, jm, jd];
  }

  function jalaliToGregorian(jy, jm, jd) {
    let gy = jy <= 979 ? 621 : 1600;
    jy -= jy <= 979 ? 0 : 979;
    let days = 365 * jy + Math.floor(jy / 33) * 8 + Math.floor(((jy % 33) + 3) / 4)
      + 78 + jd + (jm < 7 ? (jm - 1) * 31 : (jm - 7) * 30 + 186);
    gy += 400 * Math.floor(days / 146097);
    days %= 146097;
    if (days > 36524) {
      gy += 100 * Math.floor(--days / 36524);
      days %= 36524;
      if (days >= 365) days++;
    }
    gy += 4 * Math.floor(days / 1461);
    days %= 1461;
    if (days > 365) { gy += Math.floor((days - 1) / 365); days = (days - 1) % 365; }
    let gd = days + 1;
    const sal_a = [0, 31, (gy % 4 === 0 && gy % 100 !== 0) || gy % 400 === 0 ? 29 : 28,
      31, 30, 31, 30, 31, 31, 30, 31, 30, 31];
    let gm = 0;
    while (gm < 13 && gd > sal_a[gm]) gd -= sal_a[gm++];
    return [gy, gm, gd];
  }

  // Esfand is 30 days in a leap year and 29 otherwise. Rather than reproduce
  // the leap rule, measure the year: it is a leap year exactly when the next
  // Farvardin 1 falls 366 days after this one.
  function jalaliMonthLength(jy, jm) {
    if (jm <= 6) return 31;
    if (jm <= 11) return 30;
    const a = jalaliToGregorian(jy, 1, 1), b = jalaliToGregorian(jy + 1, 1, 1);
    const days = (Date.UTC(b[0], b[1] - 1, b[2]) - Date.UTC(a[0], a[1] - 1, a[2])) / 86400000;
    return days === 366 ? 30 : 29;
  }

  const JALALI_MONTHS = ['فروردین', 'اردیبهشت', 'خرداد', 'تیر', 'مرداد', 'شهریور',
    'مهر', 'آبان', 'آذر', 'دی', 'بهمن', 'اسفند'];
  const GREGORIAN_MONTHS = ['ژانویه', 'فوریه', 'مارس', 'آوریل', 'مه', 'ژوئن',
    'ژوئیه', 'اوت', 'سپتامبر', 'اکتبر', 'نوامبر', 'دسامبر'];
  const WEEKDAYS = ['ش', 'ی', 'د', 'س', 'چ', 'پ', 'ج'];

  const faDay = new Intl.NumberFormat('fa-IR');
  // A year is not a quantity: the ordinary formatter renders 1404 as
  // "۱٬۴۰۴", which reads as one thousand four hundred and four.
  const faYear = new Intl.NumberFormat('fa-IR', { useGrouping: false });
  const pad2 = n => String(n).padStart(2, '0');
  const fmtDate = (y, m, d) => y + '/' + pad2(m) + '/' + pad2(d);

  // Parses "1404/01/01" or "1404-1-1", in either digit set; null for anything
  // else, so a half-typed value leaves the picker on today rather than year 1.
  function parseDate(s) {
    const t = String(s || '').trim()
      .replace(/[۰-۹]/g, c => String(c.charCodeAt(0) - 0x06F0))
      .replace(/[٠-٩]/g, c => String(c.charCodeAt(0) - 0x0660));
    const m = t.match(/^(\d{3,4})[\/\-](\d{1,2})[\/\-](\d{1,2})$/);
    if (!m) return null;
    const y = +m[1], mo = +m[2], d = +m[3];
    if (mo < 1 || mo > 12 || d < 1 || d > 31) return null;
    return [y, mo, d];
  }

  function todayIn(calendar) {
    const n = new Date();
    return calendar === 'gregorian'
      ? [n.getFullYear(), n.getMonth() + 1, n.getDate()]
      : gregorianToJalali(n.getFullYear(), n.getMonth() + 1, n.getDate());
  }

  let openBox = null;
  let anchor = null;

  // Under its field, flipped above when there is no room below, and clamped
  // to the viewport so it is never left half offscreen. Fixed rather than
  // absolute: the widgets clip their rounded corners with overflow:hidden,
  // which cropped the popover away in a short widget.
  function place(box, input) {
    const r = input.getBoundingClientRect();
    const bw = box.offsetWidth, bh = box.offsetHeight, gap = 6, edge = 8;
    let top = r.bottom + gap;
    if (top + bh > innerHeight - edge && r.top - gap - bh >= edge) top = r.top - gap - bh;
    top = Math.max(edge, Math.min(top, innerHeight - bh - edge));
    // Aligned to the edge the reader starts from: the right on these pages.
    const rtl = getComputedStyle(document.documentElement).direction === 'rtl';
    let left = rtl ? r.right - bw : r.left;
    left = Math.max(edge, Math.min(left, innerWidth - bw - edge));
    box.style.top = top + 'px';
    box.style.left = left + 'px';
  }

  function reposition() { if (openBox && anchor) place(openBox, anchor); }

  function close() {
    if (!openBox) return;
    openBox.remove();
    openBox = null;
    anchor = null;
    removeEventListener('scroll', reposition, true);
    removeEventListener('resize', reposition);
  }

  function pick(input, value) {
    input.value = value;
    close();
    input.dispatchEvent(new Event('change', { bubbles: true }));
  }

  function show(input) {
    close();
    const cal = input.dataset.cal === 'gregorian' ? 'gregorian' : 'jalali';
    const months = cal === 'gregorian' ? GREGORIAN_MONTHS : JALALI_MONTHS;
    const monthLen = (y, m) => cal === 'gregorian' ? new Date(y, m, 0).getDate() : jalaliMonthLength(y, m);
    const chosen = parseDate(input.value);
    let [vy, vm] = chosen || todayIn(cal);
    const today = todayIn(cal);

    const box = document.createElement('div');
    box.className = 'datepick';
    box.setAttribute('role', 'dialog');
    box.setAttribute('aria-label', 'انتخاب تاریخ');

    function draw() {
      // The week runs Saturday first in both calendars -- this is a Persian
      // interface whichever calendar the column stores -- so JavaScript's
      // Sunday-first index is rotated by one.
      const g = cal === 'gregorian' ? [vy, vm, 1] : jalaliToGregorian(vy, vm, 1);
      const lead = (new Date(g[0], g[1] - 1, g[2]).getDay() + 1) % 7;
      let cells = '';
      for (let i = 0; i < lead; i++) cells += '<span></span>';
      for (let d = 1, len = monthLen(vy, vm); d <= len; d++) {
        const sel = chosen && chosen[0] === vy && chosen[1] === vm && chosen[2] === d;
        const now = today[0] === vy && today[1] === vm && today[2] === d;
        cells += '<button type="button" class="day' + (sel ? ' sel' : '') + (now ? ' now' : '') +
          '" data-d="' + d + '">' + faDay.format(d) + '</button>';
      }
      box.innerHTML =
        '<div class="dp-head">' +
          '<button type="button" class="dp-nav" data-go="-1" aria-label="ماه قبل">‹</button>' +
          '<span class="dp-title">' + months[vm - 1] + ' ' + faYear.format(vy) + '</span>' +
          '<button type="button" class="dp-nav" data-go="1" aria-label="ماه بعد">›</button>' +
        '</div>' +
        '<div class="dp-week">' + WEEKDAYS.map(w => '<span>' + w + '</span>').join('') + '</div>' +
        '<div class="dp-grid">' + cells + '</div>' +
        '<div class="dp-foot"><button type="button" class="dp-today">امروز</button></div>';
      box.querySelectorAll('.dp-nav').forEach(b => b.addEventListener('click', ev => {
        ev.stopPropagation();
        vm += +b.dataset.go;
        if (vm < 1) { vm = 12; vy--; }
        if (vm > 12) { vm = 1; vy++; }
        draw();
        // A six-row month is taller than a five-row one, so a box flipped
        // above its field is measured and placed again.
        reposition();
      }));
      box.querySelectorAll('.day').forEach(b => b.addEventListener('click', ev => {
        ev.stopPropagation();
        pick(input, fmtDate(vy, vm, +b.dataset.d));
      }));
      box.querySelector('.dp-today').addEventListener('click', ev => {
        ev.stopPropagation();
        pick(input, fmtDate(today[0], today[1], today[2]));
      });
    }
    draw();

    // Inside a modal dialog, the box goes in the dialog: anything in the body
    // is drawn under a modal's top layer, where it can be neither seen nor
    // clicked.
    (input.closest('dialog[open]') || document.body).appendChild(box);
    openBox = box;
    anchor = input;
    place(box, input);
    addEventListener('scroll', reposition, true);
    addEventListener('resize', reposition);
  }

  function attach(input) {
    input.readOnly = true;
    input.classList.add('datefield');
    input.addEventListener('click', ev => { ev.stopPropagation(); show(input); });
    input.addEventListener('focus', () => show(input));
  }

  document.addEventListener('click', e => {
    if (openBox && !openBox.contains(e.target) && !e.target.matches('.datefield')) close();
  });
  document.addEventListener('keydown', e => { if (e.key === 'Escape') close(); });

  window.docfindDatePicker = { attach, show, close, parseDate, gregorianToJalali, jalaliToGregorian };
})();
