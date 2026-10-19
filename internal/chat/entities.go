package chat

import (
	"errors"
	"net/url"
	"sort"
	"strings"
)

// Entity is one formatting span, Telegram's model: a type over [Offset,
// Offset+Length) in UTF-16 code units of the message text. The page renders
// each span by building elements around text nodes; nothing here is markup.
type Entity struct {
	Type   string `json:"type"`
	Offset int    `json:"offset"`
	Length int    `json:"length"`
	URL    string `json:"url,omitempty"`      // text_link
	User   string `json:"user,omitempty"`     // text_mention: a DocFind username
	Lang   string `json:"language,omitempty"` // pre: code block language
}

var entityTypes = map[string]bool{
	"bold": true, "italic": true, "underline": true, "strikethrough": true,
	"code": true, "pre": true, "spoiler": true, "text_link": true,
	"blockquote": true, "expandable_blockquote": true,
	"mention": true, "text_mention": true, "hashtag": true, "cashtag": true,
	"bot_command": true, "email": true, "phone_number": true, "url": true,
}

const maxEntities = 100

// ErrEntities is a formatting list the server will not store.
var ErrEntities = errors.New("chat: bad formatting")

// CleanEntities checks the client's formatting against the text it formats:
// known types only, spans inside the text, at most 100 of them, and a link's
// target only http, https, mailto or tel (a javascript: URL in a "link with
// custom text" is the classic stored-XSS route, security check 5). It returns
// the list sorted by offset.
func CleanEntities(text string, in []Entity) ([]Entity, error) {
	if len(in) == 0 {
		return nil, nil
	}
	if len(in) > maxEntities {
		return nil, ErrEntities
	}
	n := UTF16Len(text)
	out := make([]Entity, 0, len(in))
	for _, e := range in {
		if !entityTypes[e.Type] || e.Offset < 0 || e.Length <= 0 || e.Offset+e.Length > n {
			return nil, ErrEntities
		}
		switch e.Type {
		case "text_link":
			u, err := url.Parse(strings.TrimSpace(e.URL))
			if err != nil || len(e.URL) > 2048 {
				return nil, ErrEntities
			}
			switch strings.ToLower(u.Scheme) {
			case "http", "https":
				if u.Host == "" {
					return nil, ErrEntities
				}
			case "mailto", "tel":
			default:
				return nil, ErrEntities
			}
			e.URL = u.String()
		case "text_mention":
			if e.User == "" || len(e.User) > 100 {
				return nil, ErrEntities
			}
		case "pre":
			if len(e.Lang) > 32 || strings.ContainsAny(e.Lang, "<>\"' ") {
				return nil, ErrEntities
			}
		}
		if e.Type != "text_link" {
			e.URL = ""
		}
		if e.Type != "text_mention" {
			e.User = ""
		}
		if e.Type != "pre" {
			e.Lang = ""
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Offset < out[j].Offset })
	return out, nil
}

// Slice16 returns the part of s between UTF-16 offsets [from, to).
func Slice16(s string, from, to int) string {
	var b strings.Builder
	u := 0
	for _, r := range s {
		w := 1
		if r >= 0x10000 {
			w = 2
		}
		if u >= from && u+w <= to {
			b.WriteRune(r)
		}
		u += w
		if u >= to {
			break
		}
	}
	return b.String()
}

// MentionedHandles lists the @handles a message's mention entities name,
// lower-cased and without the @.
func MentionedHandles(text string, ents []Entity) []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range ents {
		if e.Type != "mention" {
			continue
		}
		h := strings.ToLower(strings.TrimPrefix(Slice16(text, e.Offset, e.Offset+e.Length), "@"))
		if ValidHandle(h) && !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	return out
}
