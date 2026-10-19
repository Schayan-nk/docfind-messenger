package chat

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Persian normalisation for search (SPEC 5.2), the same as migration 048's
// chat_norm and chat_fold: the index is built by PostgreSQL, the query here,
// and TestNormalizeMatchesSQL checks that the two agree.

// normMap: Arabic yeh, alef maksura, yeh with hamza and kaf to Persian; teh
// marbuta and heh with yeh to heh; hamza forms of alef to alef; Arabic and
// Persian digits to ASCII.
var normMap = map[rune]rune{
	0x064A: 0x06CC, 0x0649: 0x06CC, 0x0626: 0x06CC, 0x0643: 0x06A9, 0x0629: 0x0647, 0x06C0: 0x0647,
	0x0623: 0x0627, 0x0625: 0x0627, 0x0671: 0x0627,
	0x0660: '0', 0x0661: '1', 0x0662: '2', 0x0663: '3', 0x0664: '4',
	0x0665: '5', 0x0666: '6', 0x0667: '7', 0x0668: '8', 0x0669: '9',
	0x06F0: '0', 0x06F1: '1', 0x06F2: '2', 0x06F3: '3', 0x06F4: '4',
	0x06F5: '5', 0x06F6: '6', 0x06F7: '7', 0x06F8: '8', 0x06F9: '9',
}

// normSpace: Persian and typographic punctuation, which separates words.
var normSpace = map[rune]bool{
	0x060C: true, 0x061B: true, 0x061F: true, 0x066A: true, 0x066B: true, 0x066C: true, 0x06D4: true,
	0x00AB: true, 0x00BB: true, 0x2026: true, 0x2013: true, 0x2014: true, 0x2018: true, 0x2019: true,
	0x201C: true, 0x201D: true, 0x00A0: true, 0x202F: true, 0x2022: true, 0x00B7: true,
}

// normGone: tatweel, zero-width characters, direction marks and harakat
// (U+064B-U+065F, and the superscript alef).
func normGone(r rune) bool {
	switch r {
	case 0x0640, 0x200B, 0x200C, 0x200D, 0x200E, 0x200F, 0xFEFF, 0x0670:
		return true
	}
	return r >= 0x064B && r <= 0x065F
}

// Normalize is the form search compares in, and chat_norm in SQL (which leaves
// lower case to the full-text functions): "كتاب", "کتاب" and "ک‌تاب" are one.
func Normalize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case normGone(r):
		case normSpace[r]:
			b.WriteByte(' ')
		default:
			if m, ok := normMap[r]; ok {
				r = m
			}
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

// Fold is chat_fold: every character outside ASCII spelled in ASCII, so the
// full-text parser keeps Persian words whatever the database's locale.
func Fold(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r < utf8.RuneSelf:
			b.WriteRune(r)
		case r < 0x10000:
			fmt.Fprintf(&b, "z%04x", r)
		default:
			fmt.Fprintf(&b, "y%06x", r)
		}
	}
	return b.String()
}

// searchQuery turns what someone typed into to_tsquery text: every word must
// be there, each as a prefix. Words are runs of ASCII letters and digits of
// the folded text, so nothing a person types can reach tsquery syntax. ""
// when there is no word.
func searchQuery(q string) string {
	words := strings.FieldsFunc(strings.ToLower(Fold(Normalize(q))), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	})
	var parts []string
	for _, w := range words {
		if len(w) > 200 {
			w = w[:200]
		}
		parts = append(parts, w+":*")
		if len(parts) == 8 {
			break
		}
	}
	return strings.Join(parts, " & ")
}
