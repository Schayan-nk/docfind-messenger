package chat

import (
	"regexp"
	"sort"
	"strings"
)

// What Telegram links by itself in plain text: web and e-mail addresses,
// @handles, #hashtags, $CASHTAGS, /commands and phone numbers. The server
// finds them, so every client shows the same links and none has to trust
// another client's idea of where a link points.

type autoPattern struct {
	typ string
	re  *regexp.Regexp
	grp int // the submatch that is the entity; 0 is the whole match
}

var autoPatterns = []autoPattern{
	{"url", regexp.MustCompile(`(?i)\b(?:https?://|www\.)[^\s<>"'«»]+`), 0},
	{"email", regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}\b`), 0},
	{"url", regexp.MustCompile(`(?i)\b(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+(?:ir|com|org|net|de|io|co|info|app|dev|eu|ae|tr)\b(?:/[^\s<>"'«»]*)?`), 0},
	{"mention", regexp.MustCompile(`(?:^|[^\w@])(@[A-Za-z][A-Za-z0-9_]{4,31})\b`), 1},
	{"hashtag", regexp.MustCompile(`(?:^|[^\p{L}\p{N}_#])(#[\p{L}_][\p{L}\p{N}_\x{200C}]*)`), 1},
	{"cashtag", regexp.MustCompile(`(?:^|[^\w$])(\$[A-Z]{3,8})\b`), 1},
	{"bot_command", regexp.MustCompile(`(?:^|\s)(/[A-Za-z][A-Za-z0-9_]{0,31}(?:@[A-Za-z][A-Za-z0-9_]{4,31})?)\b`), 1},
	{"phone_number", regexp.MustCompile(`(?:^|[^\d+])((?:\+|00)\d{1,3}[ -]?\d{2,4}[ -]?\d{3,4}[ -]?\d{3,4}|09\d{2}[ -]?\d{3}[ -]?\d{4})\b`), 1},
}

// AutoEntities adds the links found in text to ents wherever they overlap
// nothing the sender formatted (an address inside code stays code), and
// returns the list sorted by offset.
func AutoEntities(text string, ents []Entity) []Entity {
	if text == "" {
		return ents
	}
	u16 := utf16Offsets(text)
	taken := make([][2]int, 0, len(ents))
	for _, e := range ents {
		taken = append(taken, [2]int{e.Offset, e.Offset + e.Length})
	}
	overlaps := func(a, b int) bool {
		for _, t := range taken {
			if a < t[1] && t[0] < b {
				return true
			}
		}
		return false
	}
	out := ents
	for _, p := range autoPatterns {
		for _, m := range p.re.FindAllStringSubmatchIndex(text, maxEntities) {
			from, to := m[2*p.grp], m[2*p.grp+1]
			if from < 0 {
				continue
			}
			if p.typ == "url" {
				to = from + len(trimURLTail(text[from:to]))
			}
			a, b := u16[from], u16[to]
			if b <= a || overlaps(a, b) || len(out) >= maxEntities {
				continue
			}
			taken = append(taken, [2]int{a, b})
			out = append(out, Entity{Type: p.typ, Offset: a, Length: b - a})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Offset < out[j].Offset })
	return out
}

// trimURLTail drops the punctuation that ends a sentence rather than the
// address, and a closing bracket the address never opened.
func trimURLTail(s string) string {
	for s != "" {
		last := s[len(s)-1:]
		switch {
		case strings.ContainsAny(last, `.,;:!?'"`):
			s = s[:len(s)-1]
		case last == ")" && strings.Count(s, "(") < strings.Count(s, ")"):
			s = s[:len(s)-1]
		case strings.HasSuffix(s, "،") || strings.HasSuffix(s, "؛") || strings.HasSuffix(s, "»"):
			_, n := lastRune(s)
			s = s[:len(s)-n]
		default:
			return s
		}
	}
	return s
}

func lastRune(s string) (rune, int) {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i]&0xC0 != 0x80 {
			r := []rune(s[i:])
			return r[0], len(s) - i
		}
	}
	return 0, 1
}

// utf16Offsets maps each byte offset that starts a rune (and the end of the
// string) to its UTF-16 offset, the unit entities are counted in.
func utf16Offsets(s string) []int {
	idx := make([]int, len(s)+1)
	u := 0
	for i, r := range s {
		idx[i] = u
		if r >= 0x10000 {
			u += 2
		} else {
			u++
		}
	}
	idx[len(s)] = u
	return idx
}

// splitEntities gives each piece of a split message the part of the
// formatting that falls inside it, shifted to the piece's own offsets. Pieces
// are substrings of text in order (SplitText cuts, it never rewrites).
func splitEntities(text string, parts []string, ents []Entity) [][]Entity {
	out := make([][]Entity, len(parts))
	if len(parts) == 1 {
		out[0] = ents
		return out
	}
	u16 := utf16Offsets(text)
	cursor := 0
	for i, part := range parts {
		at := strings.Index(text[cursor:], part)
		if at < 0 {
			continue
		}
		from := cursor + at
		to := from + len(part)
		cursor = to
		a, b := u16[from], u16[to]
		for _, e := range ents {
			s, t := max(e.Offset, a), min(e.Offset+e.Length, b)
			if s < t {
				e.Offset, e.Length = s-a, t-s
				out[i] = append(out[i], e)
			}
		}
	}
	return out
}
