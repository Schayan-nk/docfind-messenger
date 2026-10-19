// Package chat is DocFind's messenger: chats, messages, the update log that
// reconnecting devices catch up from, and the WebSocket hub that delivers them.
// The brief is docs/chat/SPEC.md; the choices made against it are in
// docs/chat/DECISIONS.md.
package chat

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxText is one message's limit in UTF-16 code units -- the unit Telegram
// counts in, and the unit entity offsets use, so a browser's string.length and
// this agree.
const MaxText = 4096

// UTF16Len counts s in UTF-16 code units.
func UTF16Len(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// CleanText prepares a message body: valid UTF-8 only, Windows line endings
// folded, control characters other than newline and tab dropped, and trailing
// whitespace trimmed. It never touches the visible characters of the message.
func CleanText(s string) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "�")
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	return strings.TrimRightFunc(s, unicode.IsSpace)
}

// SplitText cuts s into pieces of at most MaxText UTF-16 units. A cut prefers
// the last newline in the window, then the last space, and only cuts mid-word
// when the window has neither. A surrogate pair is never split.
func SplitText(s string) []string {
	var out []string
	for UTF16Len(s) > MaxText {
		runes := []rune(s)
		// The rune index where MaxText units run out.
		n, units := 0, 0
		for n < len(runes) {
			w := 1
			if runes[n] >= 0x10000 {
				w = 2
			}
			if units+w > MaxText {
				break
			}
			units += w
			n++
		}
		cut := n
		if i := lastIndexRune(runes[:n], '\n'); i > n/2 {
			cut = i + 1
		} else if i := lastIndexRune(runes[:n], ' '); i > n/2 {
			cut = i + 1
		}
		piece := strings.TrimRightFunc(string(runes[:cut]), unicode.IsSpace)
		if piece != "" {
			out = append(out, piece)
		}
		s = strings.TrimLeftFunc(string(runes[cut:]), unicode.IsSpace)
	}
	if s = strings.TrimRightFunc(s, unicode.IsSpace); s != "" {
		out = append(out, s)
	}
	return out
}

func lastIndexRune(rs []rune, r rune) int {
	for i := len(rs) - 1; i >= 0; i-- {
		if rs[i] == r {
			return i
		}
	}
	return -1
}

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// ValidClientID accepts the client's message id: a UUID (the page makes
// UUIDv7s, but any well-formed UUID dedupes equally well).
func ValidClientID(s string) bool { return uuidRe.MatchString(s) }

var handleRe = regexp.MustCompile(`^[A-Za-z0-9_]{5,32}$`)

// ValidHandle checks a public @username: 5-32 Latin letters, digits, underscore.
func ValidHandle(s string) bool { return handleRe.MatchString(s) }
