package chat

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// Phase 2: what people do to messages besides sending them. Every action
// starts with the membership check, so a person outside a chat cannot learn
// even whether a message id exists in it (security check 1).

// DefaultReactions is the set every chat accepts until its admins narrow it
// (chats.settings.reactions, phase 4). Kept to what suits a workplace.
var DefaultReactions = []string{
	"👍", "👎", "❤️", "🔥", "👏", "😁", "🤔", "🎉", "🙏", "👌", "💯", "🤝",
	"😢", "😱", "🤩", "😍", "🤣", "⚡", "🏆", "💔", "👀", "😇", "🤗", "🫡",
	"✍️", "😎", "🆒", "✅",
}

func validReaction(e string) bool {
	for _, r := range DefaultReactions {
		if r == e {
			return true
		}
	}
	return false
}

// SendReq is a "send" frame.
type SendReq struct {
	Chat      int64     `json:"chat"`
	CID       string    `json:"cid"`
	Text      string    `json:"text"`
	Entities  []Entity  `json:"entities"`
	ReplyTo   int64     `json:"reply_to"`
	ReplyChat int64     `json:"reply_chat"` // set when the replied message is in another chat
	Quote     *QuoteReq `json:"quote"`
	Silent    bool      `json:"silent"`

	// Phase 3: a file uploaded by the sender (its caption is Text), the
	// client's id for an album, a DocFind document, and "no link preview".
	Media     int64  `json:"media"`
	Album     string `json:"album"`
	Doc       string `json:"doc"`
	NoPreview bool   `json:"no_preview"`
	Topic     int64  `json:"topic"` // a forum's topic; 0 is General
}

// QuoteReq is the part of the replied message a reply quotes.
type QuoteReq struct {
	Text   string `json:"text"`
	Offset int    `json:"offset"`
}

const maxQuote = 1024

// mayPost runs the checks every new message passes: membership, restriction,
// blocking, the channel rule and the rate limit. It returns the members.
func (s *Service) mayPost(ctx context.Context, user string, chatID int64) ([]string, error) {
	return s.mayPostAs(ctx, user, chatID, ActSend, 0, false)
}

// mayPostAs is mayPost for one kind of message: in groups and channels it asks
// can() for act, and applies slow mode, the new-members link rule and the
// forum's topics; elsewhere the private chat rules.
func (s *Service) mayPostAs(ctx context.Context, user string, chatID int64, act Action, topic int64, hasLink bool) ([]string, error) {
	role, err := s.Store.Role(ctx, chatID, user)
	if err != nil {
		return nil, err
	}
	typ, peer, err := s.Store.ChatType(ctx, chatID, user)
	if err != nil {
		return nil, err
	}
	if isGroupish(typ) {
		info, me, err := s.groupCtx(ctx, chatID, user)
		if err != nil {
			return nil, err
		}
		now := s.now()
		if !can(typ, info.Settings, me, act, now) {
			return nil, ErrForbidden
		}
		admin := me.Role == "owner" || me.Role == "admin"
		if info.Slow > 0 && !admin {
			if t, ok := s.Store.LastSentAt(ctx, chatID, user); ok && now.Sub(t) < time.Duration(info.Slow)*time.Second {
				return nil, ErrSlowMode
			}
		}
		if hasLink && !admin && info.Settings.AntiSpamHours > 0 && now.Sub(me.JoinedAt) < time.Duration(info.Settings.AntiSpamHours)*time.Hour {
			return nil, ErrNewLinks
		}
		if info.Forum {
			t, err := s.Store.TopicOf(ctx, chatID, topic)
			if err != nil {
				return nil, ErrNotMember
			}
			if t.Closed && !admin {
				return nil, ErrClosed
			}
		} else if topic != 0 {
			return nil, ErrForbidden
		}
	} else {
		if role == "restricted" || topic != 0 {
			return nil, ErrForbidden
		}
		if typ == "private" && peer != "" {
			if b, err := s.Store.Blocked(ctx, user, peer); err != nil || b {
				return nil, ErrBlocked
			}
		}
		if typ == "secret" { // sealed bodies only, through secret_send (secret.go)
			return nil, ErrForbidden
		}
	}
	if !s.sendLimit.allow(user) {
		return nil, ErrRate
	}
	return s.Store.Members(ctx, chatID)
}

// SendText checks and stores a text message (split if it is long) and
// delivers it. except is the sending socket, which gets an "ack" instead.
func (s *Service) SendText(ctx context.Context, user string, req SendReq, except *conn) ([]*Message, error) {
	if !ValidClientID(req.CID) {
		return nil, ErrBadID
	}
	text := CleanText(req.Text)
	attached := req.Media > 0 || req.Doc != ""
	if strings.TrimSpace(text) == "" && !attached {
		return nil, ErrEmpty
	}
	ents, err := CleanEntities(text, req.Entities)
	if err != nil {
		return nil, err
	}
	if req.Album != "" && !ValidClientID(req.Album) {
		return nil, ErrBadID
	}
	act := ActSend
	if attached {
		act = ActSendMedia
	}
	hasLink := firstLink(text, AutoEntities(text, ents)) != ""
	members, err := s.mayPostAs(ctx, user, req.Chat, act, req.Topic, hasLink)
	if err != nil {
		return nil, err
	}
	first := Outgoing{Silent: req.Silent, Topic: req.Topic}
	if attached {
		if UTF16Len(text) > MaxText {
			return nil, ErrTooLong // a caption is never split
		}
		if err := s.attach(ctx, user, req, &first); err != nil {
			return nil, err
		}
	}
	if req.ReplyTo > 0 {
		if err := s.reply(ctx, user, req, &first); err != nil {
			return nil, err
		}
	}
	parts := SplitText(text)
	if len(parts) == 0 {
		parts = []string{""} // a file or document with no caption is still one message
	}
	pents := splitEntities(text, parts, ents)
	var out []*Message
	var named []string
	fresh := false
	for i, part := range parts {
		id := req.CID
		o := Outgoing{Silent: req.Silent, Topic: req.Topic}
		if i == 0 {
			o = first
		} else {
			id = derivedID(req.CID, i)
		}
		o.Text = part
		o.Entities = AutoEntities(part, pents[i])
		o.Mention = s.mentioned(ctx, part, o.Entities, members)
		o.FromName = s.displayName(user)
		named = append(named, o.Mention...)
		m, up, created, err := s.Store.Send(ctx, req.Chat, user, id, o)
		if err != nil {
			return out, err
		}
		if m.MediaID != nil {
			m.Media = s.mediaOf(ctx, *m.MediaID)
		}
		out = append(out, m)
		if created && up != nil {
			fresh = true
			s.deliver(members, "update", up, except)
			if i == 0 && !req.NoPreview && !attached {
				if link := firstLink(part, o.Entities); link != "" {
					s.previewLater(req.Chat, m.ID, link, members)
				}
			}
		}
	}
	if fresh && len(out) > 0 {
		s.notifyMessage(out[len(out)-1], named)
	}
	return out, nil
}

// attach checks what a message carries besides its text: a file the sender
// uploaded themselves, or a DocFind document the sender may open.
func (s *Service) attach(ctx context.Context, user string, req SendReq, o *Outgoing) error {
	o.Album = req.Album
	if req.Media > 0 {
		m := s.mediaOf(ctx, req.Media)
		if m == nil || m.uploader != user || m.Kind == "preview" {
			return ErrForbidden
		}
		o.MediaID, o.Kind = m.ID, m.Kind
		return nil
	}
	if s.Docs == nil {
		return ErrForbidden
	}
	if _, ok := s.Docs.DocCard(user, req.Doc); !ok {
		return ErrForbidden
	}
	o.DocRef, o.Kind = req.Doc, "doc"
	return nil
}

// reply fills in the reply part of a send: a message of the same chat (with
// an optional quote that must really be in it), or a message of another chat
// the sender is in, whose snapshot travels with the reply so the readers of
// this chat see only what the sender chose to show them.
func (s *Service) reply(ctx context.Context, user string, req SendReq, o *Outgoing) error {
	src := req.Chat
	if req.ReplyChat > 0 {
		src = req.ReplyChat
	}
	if src != req.Chat {
		if _, err := s.Store.Role(ctx, src, user); err != nil {
			return err
		}
	}
	chatID, sender, orig, err := s.Store.MessageText(ctx, req.ReplyTo)
	if err != nil || chatID != src {
		return ErrNotMember
	}
	o.ReplyTo = req.ReplyTo
	q := map[string]any{}
	if req.Quote != nil && req.Quote.Text != "" {
		n := UTF16Len(req.Quote.Text)
		if n > maxQuote || req.Quote.Offset < 0 || Slice16(orig, req.Quote.Offset, req.Quote.Offset+n) != req.Quote.Text {
			return ErrForbidden
		}
		q["text"], q["offset"] = req.Quote.Text, req.Quote.Offset
	}
	if src != req.Chat {
		q["chat"], q["from"] = src, sender
		if _, ok := q["text"]; !ok {
			q["text"] = preview(orig)
		}
	}
	if len(q) > 0 {
		o.Quote, _ = json.Marshal(q)
	}
	return nil
}

// mentioned resolves a message's @handles and name links to the members of
// the chat they name. Nobody outside the chat is ever notified.
func (s *Service) mentioned(ctx context.Context, text string, ents []Entity, members []string) []string {
	in := map[string]bool{}
	for _, m := range members {
		in[m] = true
	}
	var names []string
	if hs := MentionedHandles(text, ents); len(hs) > 0 {
		names, _ = s.Store.UsersByHandle(ctx, hs)
	}
	for _, e := range ents {
		if e.Type == "text_mention" {
			names = append(names, e.User)
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, n := range names {
		if in[n] && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

// Edit changes the text of one's own message.
func (s *Service) Edit(ctx context.Context, user string, chatID, msgID int64, text string, ents []Entity, except *conn) error {
	if _, err := s.Store.Role(ctx, chatID, user); err != nil {
		return err
	}
	c, _, _, err := s.Store.msgOwner(ctx, msgID)
	if err != nil || c != chatID {
		return ErrNotMember
	}
	text = CleanText(text)
	if strings.TrimSpace(text) == "" && !s.Store.hasAttachment(ctx, msgID) {
		return ErrEmpty // a photo's caption may be emptied; a text message may not
	}
	if UTF16Len(text) > MaxText {
		return ErrTooLong
	}
	ents, err = CleanEntities(text, ents)
	if err != nil {
		return err
	}
	ents = AutoEntities(text, ents)
	members, err := s.Store.Members(ctx, chatID)
	if err != nil {
		return err
	}
	up, err := s.Store.Edit(ctx, msgID, user, text, ents, s.mentioned(ctx, text, ents, members))
	if err != nil {
		return err
	}
	s.deliver(members, "update", up, except)
	return nil
}

// Delete removes messages for the person (forAll=false) or for everybody.
// For everybody: either side in a private chat (no time limit, as in
// Telegram), the owner of Saved Messages, and in groups the sender or an
// admin; in channels only admins.
func (s *Service) Delete(ctx context.Context, user string, chatID int64, ids []int64, forAll bool, except *conn) error {
	role, err := s.Store.Role(ctx, chatID, user)
	if err != nil {
		return err
	}
	if !forAll {
		if err := s.Store.HideForMe(ctx, user, chatID, ids); err != nil {
			return err
		}
		s.deliver([]string{user}, "hidden", map[string]any{"chat": chatID, "ids": ids}, except)
		return nil
	}
	typ, _, err := s.Store.ChatType(ctx, chatID, user)
	if err != nil {
		return err
	}
	_ = role
	var info *ChatInfo
	var me *Member
	if isGroupish(typ) {
		if info, me, err = s.groupCtx(ctx, chatID, user); err != nil {
			return err
		}
	}
	may := func(sender string) bool {
		switch typ {
		case "private", "saved", "secret":
			return true
		}
		if can(typ, info.Settings, me, ActDeleteOthers, s.now()) {
			return true
		}
		return sender == user && (typ != "channel" || can(typ, info.Settings, me, ActSend, s.now()))
	}
	up, err := s.Store.DeleteForAll(ctx, chatID, ids, may)
	if err != nil || up == nil {
		return err
	}
	members, err := s.Store.Members(ctx, chatID)
	if err != nil {
		return err
	}
	s.deliver(members, "update", up, except)
	return nil
}

// Pin pins or unpins a message: anyone in a private chat (for both), admins
// in groups and channels until phase 4 adds the members' pin permission.
func (s *Service) Pin(ctx context.Context, user string, chatID, msgID int64, on bool, except *conn) error {
	role, err := s.Store.Role(ctx, chatID, user)
	if err != nil {
		return err
	}
	typ, _, err := s.Store.ChatType(ctx, chatID, user)
	if err != nil {
		return err
	}
	if isGroupish(typ) {
		info, me, err := s.groupCtx(ctx, chatID, user)
		if err != nil {
			return err
		}
		if !can(typ, info.Settings, me, ActPin, s.now()) {
			return ErrForbidden
		}
	} else if typ != "private" && typ != "saved" && typ != "secret" && role != "owner" && role != "admin" {
		return ErrForbidden
	}
	up, err := s.Store.Pin(ctx, chatID, msgID, user, on)
	if err != nil || up == nil {
		return err
	}
	members, err := s.Store.Members(ctx, chatID)
	if err != nil {
		return err
	}
	s.deliver(members, "update", up, except)
	return nil
}

// React sets or clears the person's reaction on a message.
func (s *Service) React(ctx context.Context, user string, chatID, msgID int64, emoji string, on bool, except *conn) error {
	if _, err := s.Store.Role(ctx, chatID, user); err != nil {
		return err
	}
	if !validReaction(emoji) {
		return ErrForbidden
	}
	if typ, _, err := s.Store.ChatType(ctx, chatID, user); err == nil && isGroupish(typ) {
		info, me, err := s.groupCtx(ctx, chatID, user)
		if err != nil {
			return err
		}
		if !can(typ, info.Settings, me, ActReact, s.now()) || !info.Settings.allowsReaction(emoji) {
			return ErrForbidden
		}
	}
	if !s.sendLimit.allow(user) {
		return ErrRate
	}
	up, err := s.Store.React(ctx, chatID, msgID, user, emoji, on)
	if err != nil || up == nil {
		return err
	}
	members, err := s.Store.Members(ctx, chatID)
	if err != nil {
		return err
	}
	s.deliver(members, "update", up, except)
	return nil
}
