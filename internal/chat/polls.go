package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Polls (SPEC 6.4): anonymous or public, one answer or several, quiz mode
// with a correct answer and an explanation, and a closing time. A member sees
// the results once they have voted or the poll is closed; the creator always.
// In an anonymous poll nobody, the creator included, learns who chose what.

const (
	maxPollOptions  = 10
	maxPollQuestion = 300
	maxPollOption   = 100
	maxPollExplain  = 200
	maxPollOpen     = 30 * 24 * time.Hour
)

// PollReq is a "send_poll" frame.
type PollReq struct {
	Chat        int64    `json:"chat"`
	CID         string   `json:"cid"`
	Topic       int64    `json:"topic"`
	Silent      bool     `json:"silent"`
	ReplyTo     int64    `json:"reply_to"`
	Question    string   `json:"question"`
	Options     []string `json:"options"`
	Anonymous   bool     `json:"anonymous"`
	Multiple    bool     `json:"multiple"`
	Quiz        bool     `json:"quiz"`
	Correct     int      `json:"correct"`
	Explanation string   `json:"explanation"`
	CloseIn     int      `json:"close_in"` // seconds from now; 0 keeps it open until someone closes it
}

// PollDef is what a poll message carries in its extra: the fixed part.
type PollDef struct {
	ID        int64      `json:"id"`
	Question  string     `json:"question"`
	Options   []string   `json:"options"`
	Anonymous bool       `json:"anonymous"`
	Multiple  bool       `json:"multiple,omitempty"`
	Quiz      bool       `json:"quiz,omitempty"`
	CloseAt   *time.Time `json:"close_at,omitempty"`
	Forwarded bool       `json:"forwarded,omitempty"` // a copy in another chat: shown, not voted on
}

// PollState is the changing part, as one reader may see it.
type PollState struct {
	ID          int64      `json:"id"`
	Chat        int64      `json:"chat"`
	Msg         int64      `json:"msg"`
	Closed      bool       `json:"closed"`
	CloseAt     *time.Time `json:"close_at,omitempty"`
	Voted       bool       `json:"voted"`
	Mine        []int      `json:"mine"`
	Counts      []int      `json:"counts,omitempty"` // only once the reader may see results
	Total       int        `json:"total"`
	Correct     *int       `json:"correct,omitempty"`
	Explanation string     `json:"explanation,omitempty"`
	MayClose    bool       `json:"may_close,omitempty"`
	Public      bool       `json:"public,omitempty"`
}

// pollRow is a poll as stored.
type pollRow struct {
	ID, ChatID, MsgID  int64
	Creator            string
	Anonymous          bool
	Multiple, Quiz     bool
	Correct            *int
	Explanation        string
	ClosesAt, ClosedAt *time.Time
	NOpts              int
}

func (p *pollRow) closed(now time.Time) bool {
	return p.ClosedAt != nil || (p.ClosesAt != nil && !now.Before(*p.ClosesAt))
}

// cleanPoll checks a poll request and returns its fixed part.
func cleanPoll(r PollReq) (PollDef, error) {
	d := PollDef{Question: strings.TrimSpace(CleanText(r.Question)), Anonymous: r.Anonymous, Multiple: r.Multiple && !r.Quiz, Quiz: r.Quiz}
	if d.Question == "" || UTF16Len(d.Question) > maxPollQuestion || len(r.Options) < 2 || len(r.Options) > maxPollOptions {
		return d, ErrBadPoll
	}
	seen := map[string]bool{}
	for _, o := range r.Options {
		o = strings.TrimSpace(strings.Join(strings.Fields(CleanText(o)), " "))
		k := Normalize(o)
		if o == "" || UTF16Len(o) > maxPollOption || seen[k] {
			return d, ErrBadPoll
		}
		seen[k] = true
		d.Options = append(d.Options, o)
	}
	if r.Quiz && (r.Correct < 0 || r.Correct >= len(d.Options) || r.Multiple) {
		return d, ErrBadPoll
	}
	if UTF16Len(r.Explanation) > maxPollExplain || (!r.Quiz && strings.TrimSpace(r.Explanation) != "") {
		return d, ErrBadPoll
	}
	if r.CloseIn < 0 || (r.CloseIn > 0 && (r.CloseIn < 5 || time.Duration(r.CloseIn)*time.Second > maxPollOpen)) {
		return d, ErrBadPoll
	}
	return d, nil
}

// SendPoll makes a poll and its message.
func (s *Service) SendPoll(ctx context.Context, user string, r PollReq, except *conn) (*Message, error) {
	if !ValidClientID(r.CID) {
		return nil, ErrBadID
	}
	def, err := cleanPoll(r)
	if err != nil {
		return nil, err
	}
	members, err := s.mayPostAs(ctx, user, r.Chat, ActSendPoll, r.Topic, false)
	if err != nil {
		return nil, err
	}
	if m, err := s.Store.byClientID(ctx, r.Chat, user, r.CID); err == nil {
		return m, nil // a resend after a lost answer
	}
	if r.CloseIn > 0 {
		t := s.now().Add(time.Duration(r.CloseIn) * time.Second).UTC().Truncate(time.Second)
		def.CloseAt = &t
	}
	var correct *int
	if def.Quiz {
		c := r.Correct
		correct = &c
	}
	id, err := s.Store.CreatePoll(ctx, r.Chat, user, def, correct, strings.TrimSpace(CleanText(r.Explanation)))
	if err != nil {
		return nil, err
	}
	def.ID = id
	extra, _ := json.Marshal(def)
	o := Outgoing{Kind: "poll", Extra: extra}
	if r.ReplyTo > 0 {
		if err := s.reply(ctx, user, SendReq{Chat: r.Chat, ReplyTo: r.ReplyTo}, &o); err != nil {
			s.Store.DropPoll(ctx, id)
			return nil, err
		}
	}
	m, created, err := s.post(ctx, user, members, r.Chat, r.Topic, r.CID, r.Silent, o, except)
	if err != nil || !created {
		s.Store.DropPoll(ctx, id) // nothing points at it (a concurrent resend won)
		return m, err
	}
	return m, s.Store.LinkPoll(ctx, id, m.ID)
}

// pollIn is poll pollID of chatID for a member of chatID.
func (s *Service) pollIn(ctx context.Context, user string, chatID, pollID int64) (*pollRow, string, error) {
	role, err := s.Store.Role(ctx, chatID, user)
	if err != nil {
		return nil, "", err
	}
	p, err := s.Store.Poll(ctx, pollID)
	if err != nil || p.ChatID != chatID || p.MsgID == 0 {
		return nil, "", ErrNotMember
	}
	return p, role, nil
}

// Vote records user's choice; an empty choice takes the vote back (not in a
// quiz, whose answer is final).
func (s *Service) Vote(ctx context.Context, user string, chatID, pollID int64, options []int) (*PollState, error) {
	p, role, err := s.pollIn(ctx, user, chatID, pollID)
	if err != nil {
		return nil, err
	}
	if p.closed(s.now()) {
		return nil, ErrPollClosed
	}
	seen := map[int]bool{}
	var opts []int
	for _, o := range options {
		if o < 0 || o >= p.NOpts || seen[o] {
			return nil, ErrBadVote
		}
		seen[o] = true
		opts = append(opts, o)
	}
	if len(opts) > 1 && !p.Multiple || (p.Quiz && len(opts) != 1) {
		return nil, ErrBadVote
	}
	if !s.sendLimit.allow(user) {
		return nil, ErrRate
	}
	up, err := s.Store.Vote(ctx, p, user, opts)
	if err != nil {
		return nil, err
	}
	if up != nil {
		if members, err := s.Store.Members(ctx, chatID); err == nil {
			s.deliver(members, "update", up, nil)
		}
	}
	return s.pollState(ctx, user, role, p)
}

// ClosePoll ends a poll: its creator, or an administrator of the chat.
func (s *Service) ClosePoll(ctx context.Context, user string, chatID, pollID int64) error {
	p, role, err := s.pollIn(ctx, user, chatID, pollID)
	if err != nil {
		return err
	}
	if p.Creator != user && role != "owner" && role != "admin" {
		return ErrForbidden
	}
	return s.closePoll(ctx, p)
}

func (s *Service) closePoll(ctx context.Context, p *pollRow) error {
	up, err := s.Store.ClosePoll(ctx, p)
	if err != nil || up == nil {
		return err
	}
	members, err := s.Store.Members(ctx, p.ChatID)
	if err == nil {
		s.deliver(members, "update", up, nil)
	}
	return nil
}

// closeDuePolls closes the polls whose time has come (the scheduler's tick).
func (s *Service) closeDuePolls(ctx context.Context) {
	due, err := s.Store.DuePolls(ctx, s.now(), 100)
	if err != nil {
		return
	}
	for _, p := range due {
		_ = s.closePoll(ctx, p)
	}
}

// pollState is the poll as viewer may see it.
func (s *Service) pollState(ctx context.Context, viewer, role string, p *pollRow) (*PollState, error) {
	counts, total, mine, err := s.Store.PollCounts(ctx, p, viewer)
	if err != nil {
		return nil, err
	}
	st := &PollState{ID: p.ID, Chat: p.ChatID, Msg: p.MsgID, Closed: p.closed(s.now()), CloseAt: p.ClosesAt, Voted: len(mine) > 0, Mine: mine, Total: total, Public: !p.Anonymous}
	if st.Mine == nil {
		st.Mine = []int{}
	}
	st.MayClose = !st.Closed && (p.Creator == viewer || role == "owner" || role == "admin")
	if st.Voted || st.Closed || p.Creator == viewer {
		st.Counts = counts
		if p.Quiz && (st.Voted || st.Closed) {
			st.Correct, st.Explanation = p.Correct, p.Explanation
		}
	}
	return st, nil
}

// PollGet is GET /api/chat/polls/{id}?chat=N: the poll as the reader may see it.
func (a *API) PollGet(w http.ResponseWriter, r *http.Request) {
	p, role, me, ok := a.pollOf(w, r)
	if !ok {
		return
	}
	st, err := a.Svc.pollState(r.Context(), me, role, p)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"poll": st})
}

// PollVoters is GET /api/chat/polls/{id}/voters?chat=N: who chose what, for a
// public poll, once the reader may see its results.
func (a *API) PollVoters(w http.ResponseWriter, r *http.Request) {
	p, role, me, ok := a.pollOf(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	st, err := a.Svc.pollState(ctx, me, role, p)
	if err != nil || p.Anonymous || st.Counts == nil {
		writeErr(w, ErrForbidden)
		return
	}
	by, err := a.Svc.Store.PollVoters(ctx, p.ID, 100)
	if err != nil {
		writeErr(w, err)
		return
	}
	out := map[string][]*Person{}
	for idx, users := range by {
		for _, u := range users {
			out[strconv.Itoa(idx)] = append(out[strconv.Itoa(idx)], &Person{ID: u, Name: a.Svc.displayName(u)})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"voters": out})
}

func (a *API) pollOf(w http.ResponseWriter, r *http.Request) (*pollRow, string, string, bool) {
	me := a.User(r)
	id, err1 := strconv.ParseInt(r.PathValue("id"), 10, 64)
	chatID, err2 := strconv.ParseInt(r.URL.Query().Get("chat"), 10, 64)
	if err1 != nil || err2 != nil {
		writeErr(w, ErrNotMember)
		return nil, "", "", false
	}
	p, role, err := a.Svc.pollIn(r.Context(), me, chatID, id)
	if err != nil {
		writeErr(w, ErrNotMember)
		return nil, "", "", false
	}
	return p, role, me, true
}
