package chat

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
)

// Phase 11 (docs/chat/SPEC.md 6.3, 6.4, 8.8): polls, locations, stickers and
// GIFs, scheduled messages and translation. This file has what they share:
// their socket frames, their errors, and the message's "extra" (the poll, the
// point, the pack item a message carries; migration 061).

var (
	ErrPollClosed = errors.New("chat: the poll is closed")
	ErrBadPoll    = errors.New("chat: not a valid poll")
	ErrBadVote    = errors.New("chat: not a valid vote")
	ErrBadTime    = errors.New("chat: not a valid time")
	ErrBadPlace   = errors.New("chat: not a valid location")
	ErrNotLive    = errors.New("chat: the location is no longer live")
	ErrNoItem     = errors.New("chat: no such sticker or GIF")
	ErrTooMany    = errors.New("chat: too many scheduled messages")
)

// extrasText is what the page shows for the phase 11 errors.
func extrasText(err error) (code, text string, ok bool) {
	switch {
	case errors.Is(err, ErrPollClosed):
		return "poll_closed", "این نظرسنجی بسته شده است.", true
	case errors.Is(err, ErrBadPoll):
		return "bad_poll", "نظرسنجی باید یک پرسش و دست‌کم دو گزینهٔ متفاوت داشته باشد.", true
	case errors.Is(err, ErrBadVote):
		return "bad_vote", "این رأی پذیرفته نشد.", true
	case errors.Is(err, ErrBadTime):
		return "bad_time", "زمان ارسال باید دست‌کم یک دقیقه بعد و تا یک سال دیگر باشد.", true
	case errors.Is(err, ErrBadPlace):
		return "bad_place", "موقعیت نامعتبر است.", true
	case errors.Is(err, ErrNotLive):
		return "not_live", "اشتراک زندهٔ این موقعیت تمام شده است.", true
	case errors.Is(err, ErrNoItem):
		return "no_item", "این برچسب یا گیف پیدا نشد.", true
	case errors.Is(err, ErrTooMany):
		return "too_many", "پیام‌های زمان‌بندی‌شدهٔ این گفت‌وگو به سقف رسیده است.", true
	}
	return "", "", false
}

func (c *conn) extrasFail(id string, err error) {
	if code, text, ok := extrasText(err); ok {
		c.send("err", id, map[string]string{"code": code, "text": text})
		return
	}
	c.fail(id, err)
}

// handleExtras answers the phase 11 frames. Each service call checks
// membership before anything else.
func (c *conn) handleExtras(ctx context.Context, env Envelope) {
	s := c.svc
	var ack any
	var err error
	var m *Message
	msgs := func() { ack = map[string]any{"messages": []*Message{m}} }
	switch env.T {
	case "send_poll":
		var d PollReq
		if err = json.Unmarshal(env.D, &d); err == nil {
			if m, err = s.SendPoll(ctx, c.user, d, c); err == nil {
				msgs()
			}
		}
	case "vote":
		var d struct {
			Chat    int64 `json:"chat"`
			Poll    int64 `json:"poll"`
			Options []int `json:"options"`
		}
		if err = json.Unmarshal(env.D, &d); err == nil {
			var st *PollState
			if st, err = s.Vote(ctx, c.user, d.Chat, d.Poll, d.Options); err == nil {
				ack = map[string]any{"poll": st}
			}
		}
	case "close_poll":
		var d struct {
			Chat int64 `json:"chat"`
			Poll int64 `json:"poll"`
		}
		if err = json.Unmarshal(env.D, &d); err == nil {
			err = s.ClosePoll(ctx, c.user, d.Chat, d.Poll)
		}
	case "send_location":
		var d LocationReq
		if err = json.Unmarshal(env.D, &d); err == nil {
			if m, err = s.SendLocation(ctx, c.user, d, c); err == nil {
				msgs()
			}
		}
	case "location_update", "location_stop":
		var d struct {
			Chat int64   `json:"chat"`
			ID   int64   `json:"id"`
			Lat  float64 `json:"lat"`
			Lng  float64 `json:"lng"`
			Acc  float64 `json:"acc"`
		}
		if err = json.Unmarshal(env.D, &d); err == nil {
			err = s.MoveLocation(ctx, c.user, d.Chat, d.ID, d.Lat, d.Lng, d.Acc, env.T == "location_stop")
		}
	case "send_sticker":
		var d StickerReq
		if err = json.Unmarshal(env.D, &d); err == nil {
			if m, err = s.SendSticker(ctx, c.user, d, c); err == nil {
				msgs()
			}
		}
	case "schedule":
		var d ScheduleReq
		if err = json.Unmarshal(env.D, &d); err == nil {
			var it *Scheduled
			if it, err = s.Schedule(ctx, c.user, d); err == nil {
				ack = map[string]any{"item": it}
			}
		}
	case "schedule_cancel", "schedule_now":
		var d struct {
			Chat int64 `json:"chat"`
			ID   int64 `json:"id"`
		}
		if err = json.Unmarshal(env.D, &d); err == nil {
			err = s.ScheduledAction(ctx, c.user, d.Chat, d.ID, env.T == "schedule_now")
		}
	default:
		return
	}
	if err != nil {
		if _, ok := err.(*json.UnmarshalTypeError); ok {
			err = ErrForbidden
		}
		c.extrasFail(env.ID, err)
		return
	}
	c.send("ack", env.ID, ack)
}

// sendKind stores one message of a phase 11 kind after the ordinary checks
// (membership, the chat's rights for act, blocking, the rate) and delivers it.
func (s *Service) sendKind(ctx context.Context, user string, chatID, topic int64, cid string, silent bool, replyTo int64, act Action, o Outgoing, except *conn) (*Message, error) {
	if !ValidClientID(cid) {
		return nil, ErrBadID
	}
	members, err := s.mayPostAs(ctx, user, chatID, act, topic, false)
	if err != nil {
		return nil, err
	}
	if replyTo > 0 {
		if err := s.reply(ctx, user, SendReq{Chat: chatID, ReplyTo: replyTo}, &o); err != nil {
			return nil, err
		}
	}
	m, _, err := s.post(ctx, user, members, chatID, topic, cid, silent, o, except)
	return m, err
}

// post stores and delivers a message whose checks have been made.
func (s *Service) post(ctx context.Context, user string, members []string, chatID, topic int64, cid string, silent bool, o Outgoing, except *conn) (*Message, bool, error) {
	o.Topic, o.Silent, o.FromName = topic, silent, s.displayName(user)
	m, up, created, err := s.Store.Send(ctx, chatID, user, cid, o)
	if err != nil {
		return nil, false, err
	}
	if m.MediaID != nil {
		m.Media = s.mediaOf(ctx, *m.MediaID)
	}
	if created && up != nil {
		if m.Media != nil { // the logged update was made before the file was looked up
			live := *up
			live.Payload, _ = json.Marshal(m)
			up = &live
		}
		s.deliver(members, "update", up, except)
	}
	return m, created, nil
}

// SetExtra replaces what a message carries and logs it as an "extra" update,
// so every device (and every device that reconnects later) follows.
func (s *Store) SetExtra(ctx context.Context, chatID, msgID int64, extra json.RawMessage) (*Update, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	pts, err := bump(ctx, tx, chatID)
	if err != nil {
		return nil, err
	}
	tag, err := tx.Exec(ctx, `UPDATE chat_messages SET extra = $3 WHERE id = $2 AND chat_id = $1 AND deleted_at IS NULL`, chatID, msgID, extra)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotMember
	}
	up, err := addUpdate(ctx, tx, chatID, pts, "extra", map[string]any{"id": msgID, "extra": extra})
	if err != nil {
		return nil, err
	}
	return up, tx.Commit(ctx)
}

// messageIn is one message of chatID as user may see it, or ErrNotMember.
func (s *Service) messageIn(ctx context.Context, chatID int64, user string, id int64) (*Message, error) {
	list, err := s.Store.ByIDs(ctx, chatID, user, []int64{id})
	if err != nil || len(list) == 0 || list[0].Deleted {
		if errors.Is(err, pgx.ErrNoRows) || err == nil {
			return nil, ErrNotMember
		}
		return nil, err
	}
	return list[0], nil
}

// forwardExtra is what a forwarded message keeps of its extra: a sticker or a
// GIF as it was, a location as a fixed point (a live one stays with its
// sender), and a poll as its question only (votes stay in the poll's chat).
func forwardExtra(m *Message) json.RawMessage {
	if len(m.Extra) == 0 {
		return nil
	}
	switch m.Kind {
	case "location":
		var p Place
		if json.Unmarshal(m.Extra, &p) != nil {
			return nil
		}
		p.Until, p.Stopped, p.Updated = nil, false, nil
		b, _ := json.Marshal(p)
		return b
	case "poll":
		var d PollDef
		if json.Unmarshal(m.Extra, &d) != nil {
			return nil
		}
		d.Forwarded = true
		b, _ := json.Marshal(d)
		return b
	}
	return m.Extra
}
