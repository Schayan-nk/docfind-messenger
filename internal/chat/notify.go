package chat

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"
)

// Notifications (SPEC 6.10). When a message is stored, notifyMessage works
// out who should hear about it and hands one Notice to Service.Notify, which
// the find package takes to DocFind's push queue (retried, per device, quiet
// hours).
// Nobody is told about their own message, nobody who is looking at the chat
// right now, nobody whose switches or the chat's own settings say no; the
// open page alerts in-app from the same socket update it already gets.

// NotifySettings are a person's own switches. Everything is on until it is
// switched off.
type NotifySettings struct {
	Private  bool `json:"private"`  // private chats
	Groups   bool `json:"groups"`   // groups and supergroups
	Channels bool `json:"channels"` // channels
	Calls    bool `json:"calls"`    // calls (phase 7)
	Preview  bool `json:"preview"`  // show the words, not only «پیام تازه»
	Sound    bool `json:"sound"`    // in-app sound
	Vibrate  bool `json:"vibrate"`  // in-app vibration
	Badge    bool `json:"badge"`    // the unread count on the app's icon
}

func defaultNotify() NotifySettings {
	return NotifySettings{true, true, true, true, true, true, true, true}
}

// parseNotify reads stored switches over the defaults: a switch never stored
// is on.
func parseNotify(b []byte) NotifySettings {
	n := defaultNotify()
	_ = json.Unmarshal(b, &n)
	return n
}

// NotifySettings is user's switches.
func (s *Store) NotifySettings(ctx context.Context, user string) (NotifySettings, error) {
	var b []byte
	err := s.pool.QueryRow(ctx, `SELECT COALESCE((SELECT notify FROM chat_users_ext WHERE user_id = $1), '{}')`, user).Scan(&b)
	return parseNotify(b), err
}

// SetNotifySettings keeps user's switches.
func (s *Store) SetNotifySettings(ctx context.Context, user string, n NotifySettings) error {
	b, _ := json.Marshal(n)
	_, err := s.pool.Exec(ctx, `INSERT INTO chat_users_ext (user_id, notify) VALUES ($1, $2)
		ON CONFLICT (user_id) DO UPDATE SET notify = EXCLUDED.notify`, user, b)
	return err
}

// Sounds a chat can have in-app ("" is the person's default).
var chatSounds = map[string]bool{"": true, "note": true, "chime": true, "pop": true, "none": true}

// NoticeTo is one person a notice reaches.
type NoticeTo struct {
	User    string
	Preview bool // their switch: the words, or only that something came
	Mention bool // named in it, or a reply to them
}

// Notice is one message announced.
type Notice struct {
	Chat     int64
	ChatType string
	Title    string // the chat's title; in a private chat, the sender's name
	Text     string // the preview: "name: words" in a group
	MsgID    int64
	Silent   bool // sent without sound
	To       []NoticeTo
}

// noticeText is the words of a message as a notification may show them.
func noticeText(m *Message) string {
	// The same icons as the chat list, so a notification and the list read
	// alike; a poll or sticker with no words no longer comes as an empty one.
	kind := map[string]string{
		"photo": "📷 عکس", "video": "🎥 ویدیو", "round": "📹 پیام ویدیویی", "voice": "🎤 پیام صوتی",
		"file": "📎 فایل", "doc": "📄 سند", "poll": "📊 نظرسنجی", "location": "📍 موقعیت",
		"sticker": "برچسب", "gif": "🎞️ گیف",
	}[m.Kind]
	var ents []Entity
	_ = json.Unmarshal(m.Entities, &ents)
	text := strings.Join(strings.Fields(m.Text), " ")
	for _, e := range ents {
		if e.Type == "spoiler" { // what is hidden in the chat stays hidden outside it
			text = "پیام"
			break
		}
	}
	if m.Kind == "file" && m.Media != nil && m.Media.Name != "" {
		kind += ": " + m.Media.Name
	}
	switch {
	case text == "" && kind != "":
		text = kind
	case text != "" && kind != "":
		text = kind + "، " + text
	}
	if utf8.RuneCountInString(text) > 120 {
		text = string([]rune(text)[:120]) + "…"
	}
	return text
}

// watching reports whether any of user's devices shows chatID right now.
func (s *Service) watching(user string, chatID int64) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for c := range s.conns[user] {
		if c.watch.Load() == chatID {
			return true
		}
	}
	return false
}

// notifyMessage tells the people who should hear about m. It runs on its
// own: a message is never held back by its notifications.
func (s *Service) notifyMessage(m *Message, mentioned []string) {
	if s.Notify == nil || m == nil || m.Kind == "service" || m.Sender == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		n, err := s.notice(ctx, m, mentioned)
		if err != nil || n == nil || len(n.To) == 0 {
			return
		}
		s.Notify(ctx, *n)
	}()
}

// notice decides who hears about m, and what they are told.
func (s *Service) notice(ctx context.Context, m *Message, mentioned []string) (*Notice, error) {
	var typ, title string
	if err := s.Store.pool.QueryRow(ctx, `SELECT type, title FROM chats WHERE id = $1`, m.ChatID).Scan(&typ, &title); err != nil {
		return nil, err
	}
	if typ == "saved" || typ == "secret" {
		return nil, nil
	}
	named := map[string]bool{}
	for _, u := range mentioned {
		named[u] = true
	}
	if m.Reply != nil && m.Reply.From != "" {
		named[m.Reply.From] = true // a reply to someone names them
	}
	from := s.displayName(m.Sender)
	n := &Notice{Chat: m.ChatID, ChatType: typ, Title: title, Text: noticeText(m), MsgID: m.ID, Silent: m.Silent}
	switch typ {
	case "private":
		n.Title = from
	case "group", "supergroup":
		n.Text = from + ": " + n.Text
	}
	rows, err := s.Store.pool.Query(ctx, `SELECT m.user_id, m.muted_until, m.mentions_only, COALESCE(e.notify, '{}')
		FROM chat_members m LEFT JOIN chat_users_ext e ON e.user_id = m.user_id
		WHERE m.chat_id = $1 AND m.role NOT IN ('left', 'banned') AND m.user_id <> $2`, m.ChatID, m.Sender)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	now := s.now()
	for rows.Next() {
		var user string
		var muted *time.Time
		var mentionsOnly bool
		var raw []byte
		if err := rows.Scan(&user, &muted, &mentionsOnly, &raw); err != nil {
			return nil, err
		}
		set := parseNotify(raw)
		on := map[string]bool{"private": set.Private, "group": set.Groups, "supergroup": set.Groups, "channel": set.Channels}[typ]
		mention := named[user]
		switch {
		case !on: // the person switched this kind of chat off
		case muted != nil && muted.After(now) && !mention: // a muted chat still says when you are named
		case mentionsOnly && !mention:
		case s.watching(user, m.ChatID): // reading it already
		default:
			n.To = append(n.To, NoticeTo{User: user, Preview: set.Preview, Mention: mention})
		}
	}
	return n, rows.Err()
}
