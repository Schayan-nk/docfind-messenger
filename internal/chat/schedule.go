package chat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Scheduled messages (SPEC 6.3): send at a date and time, or "when online"
// (a private chat, as soon as the other person connects). A scheduled message
// is kept apart from the chat until its time and then sent through SendText,
// so it passes the same checks as a message typed at that moment.

const (
	maxScheduled  = 100 // per person per chat
	scheduleEvery = 5 * time.Second
	minAhead      = time.Minute
	maxAhead      = 366 * 24 * time.Hour
)

// ScheduleReq is a "schedule" frame.
type ScheduleReq struct {
	Chat       int64    `json:"chat"`
	CID        string   `json:"cid"`
	Topic      int64    `json:"topic"`
	Text       string   `json:"text"`
	Entities   []Entity `json:"entities"`
	ReplyTo    int64    `json:"reply_to"`
	Silent     bool     `json:"silent"`
	At         string   `json:"at"` // RFC 3339
	WhenOnline bool     `json:"when_online"`
}

// Scheduled is one waiting message, as its sender sees it.
type Scheduled struct {
	ID         int64           `json:"id"`
	Chat       int64           `json:"chat"`
	CID        string          `json:"cid"`
	Topic      int64           `json:"topic,omitempty"`
	Text       string          `json:"text"`
	Entities   json.RawMessage `json:"entities,omitempty"`
	ReplyTo    int64           `json:"reply_to,omitempty"`
	Silent     bool            `json:"silent,omitempty"`
	At         *time.Time      `json:"at,omitempty"`
	WhenOnline bool            `json:"when_online,omitempty"`
	sender     string
}

// whenFor checks a requested time: at least a minute ahead, at most a year.
func whenFor(at string, now time.Time) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(at))
	if err != nil || t.Before(now.Add(minAhead-5*time.Second)) || t.After(now.Add(maxAhead)) {
		return time.Time{}, ErrBadTime
	}
	return t.UTC(), nil
}

// Schedule keeps a message for later. The checks of the moment (membership,
// rights, blocking) run now as well as when it is sent.
func (s *Service) Schedule(ctx context.Context, user string, r ScheduleReq) (*Scheduled, error) {
	if !ValidClientID(r.CID) {
		return nil, ErrBadID
	}
	text := CleanText(r.Text)
	if strings.TrimSpace(text) == "" {
		return nil, ErrEmpty
	}
	if UTF16Len(text) > MaxText {
		return nil, ErrTooLong
	}
	ents, err := CleanEntities(text, r.Entities)
	if err != nil {
		return nil, err
	}
	hasLink := firstLink(text, AutoEntities(text, ents)) != ""
	if _, err := s.mayPostAs(ctx, user, r.Chat, ActSend, r.Topic, hasLink); err != nil {
		return nil, err
	}
	it := &Scheduled{Chat: r.Chat, CID: r.CID, Topic: r.Topic, Text: text, ReplyTo: r.ReplyTo, Silent: r.Silent, WhenOnline: r.WhenOnline, sender: user}
	it.Entities, _ = json.Marshal(nonNil(ents))
	if r.WhenOnline {
		if typ, _, err := s.Store.ChatType(ctx, r.Chat, user); err != nil || typ != "private" {
			return nil, ErrForbidden
		}
	} else {
		t, err := whenFor(r.At, s.now())
		if err != nil {
			return nil, err
		}
		it.At = &t
	}
	if n, err := s.Store.CountScheduled(ctx, r.Chat, user); err != nil || n >= maxScheduled {
		if err != nil {
			return nil, err
		}
		return nil, ErrTooMany
	}
	if err := s.Store.AddScheduled(ctx, it); err != nil {
		return nil, err
	}
	s.scheduledChanged(ctx, user, r.Chat)
	return it, nil
}

// ScheduledAction cancels one of user's scheduled messages, or sends it now.
func (s *Service) ScheduledAction(ctx context.Context, user string, chatID, id int64, now bool) error {
	if _, err := s.Store.Role(ctx, chatID, user); err != nil {
		return err
	}
	it, err := s.Store.TakeScheduled(ctx, chatID, user, id)
	if err != nil {
		return err
	}
	defer s.scheduledChanged(ctx, user, chatID)
	if now {
		return s.sendScheduled(ctx, it)
	}
	return nil
}

func (s *Service) scheduledChanged(ctx context.Context, user string, chatID int64) {
	n, _ := s.Store.CountScheduled(ctx, chatID, user)
	s.deliver([]string{user}, "scheduled", map[string]any{"chat": chatID, "count": n}, nil)
}

// sendScheduled sends a message whose time has come, as its sender, through
// every check of SendText. A message the rate limit holds back waits a few
// seconds more; one the chat no longer accepts is dropped, and its sender told.
func (s *Service) sendScheduled(ctx context.Context, it *Scheduled) error {
	var ents []Entity
	_ = json.Unmarshal(it.Entities, &ents)
	_, err := s.SendText(ctx, it.sender, SendReq{Chat: it.Chat, CID: it.CID, Text: it.Text, Entities: ents, ReplyTo: it.ReplyTo, Silent: it.Silent, Topic: it.Topic}, nil)
	if errors.Is(err, ErrRate) || errors.Is(err, ErrSlowMode) {
		t := s.now().Add(10 * time.Second).UTC()
		it.At, it.WhenOnline = &t, false
		return s.Store.AddScheduled(ctx, it)
	}
	if err != nil {
		_, text := errorText(err)
		s.deliver([]string{it.sender}, "scheduled_failed", map[string]any{"chat": it.Chat, "text": text}, nil)
	}
	return err
}

// runScheduled is the scheduler's tick: messages whose time has come, "when
// online" messages whose reader is here, and polls whose time is up.
func (s *Service) runScheduled(ctx context.Context) {
	if s.Store == nil {
		return
	}
	for _, it := range s.Store.DueScheduled(ctx, s.now(), 50) {
		s.sendScheduled(ctx, it)
		s.scheduledChanged(ctx, it.sender, it.Chat)
	}
	for _, it := range s.Store.OnlineScheduled(ctx, 200) {
		_, peer, err := s.Store.ChatType(ctx, it.Chat, it.sender)
		if err != nil || peer == "" || !s.Online(peer) {
			continue
		}
		if taken, err := s.Store.TakeScheduled(ctx, it.Chat, it.sender, it.ID); err == nil {
			s.sendScheduled(ctx, taken)
			s.scheduledChanged(ctx, it.sender, it.Chat)
		}
	}
	s.closeDuePolls(ctx)
}

// ScheduledList is GET /api/chat/chats/{id}/scheduled: the reader's own waiting messages.
func (a *API) ScheduledList(w http.ResponseWriter, r *http.Request) {
	chatID, me, ok := a.memberChat(w, r)
	if !ok {
		return
	}
	list, err := a.Svc.Store.ListScheduled(r.Context(), chatID, me)
	if err != nil {
		writeErr(w, err)
		return
	}
	if list == nil {
		list = []*Scheduled{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"scheduled": list})
}

// ---------------------------------------------------------------- store

const schedCols = `id, chat_id, sender, client_msg_id::text, topic_id, text, entities, COALESCE(reply_to_id, 0), silent, send_at, when_online`

func scanScheduled(r rowScanner) (*Scheduled, error) {
	it := &Scheduled{}
	err := r.Scan(&it.ID, &it.Chat, &it.sender, &it.CID, &it.Topic, &it.Text, &it.Entities, &it.ReplyTo, &it.Silent, &it.At, &it.WhenOnline)
	return it, err
}

// AddScheduled stores a waiting message; a resend of the same one is a no-op.
func (s *Store) AddScheduled(ctx context.Context, it *Scheduled) error {
	err := s.pool.QueryRow(ctx, `
		INSERT INTO chat_scheduled (chat_id, sender, client_msg_id, topic_id, text, entities, reply_to_id, silent, send_at, when_online)
		VALUES ($1, $2, $3::uuid, $4, $5, $6, NULLIF($7, 0), $8, $9, $10)
		ON CONFLICT (sender, client_msg_id) DO UPDATE SET send_at = EXCLUDED.send_at, when_online = EXCLUDED.when_online
		RETURNING id`,
		it.Chat, it.sender, it.CID, it.Topic, it.Text, it.Entities, it.ReplyTo, it.Silent, it.At, it.WhenOnline).Scan(&it.ID)
	return err
}

// CountScheduled is how many messages user has waiting in chatID.
func (s *Store) CountScheduled(ctx context.Context, chatID int64, user string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM chat_scheduled WHERE chat_id = $1 AND sender = $2`, chatID, user).Scan(&n)
	return n, err
}

// ListScheduled is user's waiting messages in chatID, soonest first.
func (s *Store) ListScheduled(ctx context.Context, chatID int64, user string) ([]*Scheduled, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+schedCols+` FROM chat_scheduled WHERE chat_id = $1 AND sender = $2 ORDER BY when_online, send_at, id`, chatID, user)
	if err != nil {
		return nil, err
	}
	return collectScheduled(rows)
}

func collectScheduled(rows pgx.Rows) ([]*Scheduled, error) {
	defer rows.Close()
	var out []*Scheduled
	for rows.Next() {
		it, err := scanScheduled(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// TakeScheduled removes one of user's waiting messages and returns it: taking
// it is what makes sure it is sent once, whoever gets there first.
func (s *Store) TakeScheduled(ctx context.Context, chatID int64, user string, id int64) (*Scheduled, error) {
	it, err := scanScheduled(s.pool.QueryRow(ctx, `DELETE FROM chat_scheduled WHERE id = $1 AND chat_id = $2 AND sender = $3 RETURNING `+schedCols, id, chatID, user))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotMember
	}
	return it, err
}

// DueScheduled takes the timed messages whose time has come.
func (s *Store) DueScheduled(ctx context.Context, now time.Time, limit int) []*Scheduled {
	rows, err := s.pool.Query(ctx, `
		DELETE FROM chat_scheduled WHERE id IN (
		  SELECT id FROM chat_scheduled WHERE NOT when_online AND send_at <= $1 ORDER BY send_at LIMIT $2 FOR UPDATE SKIP LOCKED)
		RETURNING `+schedCols, now, limit)
	if err != nil {
		return nil
	}
	list, _ := collectScheduled(rows)
	return list
}

// OnlineScheduled lists (without taking) the "when online" messages.
func (s *Store) OnlineScheduled(ctx context.Context, limit int) []*Scheduled {
	rows, err := s.pool.Query(ctx, `SELECT `+schedCols+` FROM chat_scheduled WHERE when_online ORDER BY id LIMIT $1`, limit)
	if err != nil {
		return nil
	}
	list, _ := collectScheduled(rows)
	return list
}
