package chat

import (
	"context"
	"encoding/json"

	"time"
)

// Forwarding (phase 2): messages of one chat copied into another.

// ForwardReq is a "forward" frame: messages of one chat into another.
type ForwardReq struct {
	From       int64   `json:"from"`
	IDs        []int64 `json:"ids"`
	To         int64   `json:"to"`
	CID        string  `json:"cid"`
	HideSender bool    `json:"hide_sender"`
}

// Forward copies messages into another chat, oldest first, each with a "from"
// header unless the sender hid it. A message that was itself forwarded keeps
// its first author, as in Telegram. Nothing leaves a secret chat.
func (s *Service) Forward(ctx context.Context, user string, req ForwardReq, except *conn) ([]*Message, error) {
	if !ValidClientID(req.CID) || len(req.IDs) == 0 || len(req.IDs) > 100 {
		return nil, ErrBadID
	}
	if _, err := s.Store.Role(ctx, req.From, user); err != nil {
		return nil, err
	}
	if typ, _, err := s.Store.ChatType(ctx, req.From, user); err != nil || typ == "secret" {
		return nil, ErrForbidden
	}
	members, err := s.mayPost(ctx, user, req.To)
	if err != nil {
		return nil, err
	}
	src, err := s.Store.ByIDs(ctx, req.From, user, req.IDs)
	if err != nil {
		return nil, err
	}
	if len(src) == 0 {
		return nil, ErrNotMember
	}
	var senders []string
	var last *Message
	for _, m := range src {
		senders = append(senders, m.Sender)
	}
	profiles, _ := s.Store.Profiles(ctx, senders)
	var out []*Message
	for i, m := range src {
		id := req.CID
		if i > 0 {
			id = derivedID(req.CID, i)
		}
		var ents []Entity
		_ = json.Unmarshal(m.Entities, &ents)
		o := Outgoing{Text: m.Text, Entities: ents, Kind: m.Kind, DocRef: m.DocRef, Album: forwardAlbum(req.CID, m.Album), Extra: forwardExtra(m)}
		if m.MediaID != nil {
			o.MediaID = *m.MediaID
		}
		if !req.HideSender {
			o.Fwd = m.Fwd
			if len(o.Fwd) == 0 {
				o.Fwd = s.fwdHeader(m, profiles[m.Sender])
			}
		}
		o.FromName = s.displayName(user)
		fm, up, created, err := s.Store.Send(ctx, req.To, user, id, o)
		if err != nil {
			return out, err
		}
		if fm.MediaID != nil {
			fm.Media = s.mediaOf(ctx, *fm.MediaID)
		}
		out = append(out, fm)
		if created && up != nil {
			s.deliver(members, "update", up, except)
			last = fm
		}
	}
	if last != nil { // one notice for what was forwarded together
		s.notifyMessage(last, nil)
	}
	return out, nil
}

// fwdHeader is the "forwarded from" line. The name always shows; the link to
// the account only when the author's forwards privacy is "everybody".
func (s *Service) fwdHeader(m *Message, p Profile) json.RawMessage {
	h := map[string]any{"at": m.Created.UTC().Format(time.RFC3339)}
	name := m.Sender
	if u, err := s.users.Get(m.Sender); err == nil && u.DisplayName != "" {
		name = u.DisplayName
	}
	h["name"] = name
	if mode := p.Privacy.Forwards.Mode; mode == "" || mode == "everybody" {
		h["from"] = m.Sender
	}
	b, _ := json.Marshal(h)
	return b
}

// forwardAlbum keeps forwarded album items together under an id derived
// from the forward's own, so two forwards of one album stay two albums.
func forwardAlbum(cid, album string) string {
	if album == "" {
		return ""
	}
	return derivedID(cid, 1000+int(album[0]))
}
