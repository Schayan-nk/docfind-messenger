package chat

import (
	"context"
	"encoding/json"
	"time"
)

// The gcall_* socket frames (gcalls.go has the rules).
//
//	gcall_join   {chat, video, tab} -> ack {call, ice}; the media server then offers (gcall_signal)
//	gcall_signal {call, d}          -> d is {sdp: answer} or {cand}: to the media server
//	gcall_media  {call, mic, cam, screen, hand} -> everyone in the call gets gcall_state
//	gcall_leave  {call}
//	gcall_end    {call}             -> its starter or a chat admin: gcall_ended for everyone
//	gcall_mute   {call, user, on}   -> an admin holds someone's microphone back (gcall_held to them)
//	gcall_resume {call, tab}        -> ack {call, ice}: this socket takes the call back after a drop
//
// The server also sends gcall_update {chat, call|null} to every member of a
// chat when a call starts, changes size or ends (the «join» bar).
func (c *conn) handleGroupCall(ctx context.Context, env Envelope) {
	s := c.svc
	var d struct {
		Call   int64           `json:"call"`
		Chat   int64           `json:"chat"`
		User   string          `json:"user"`
		Video  bool            `json:"video"`
		On     bool            `json:"on"`
		Tab    string          `json:"tab"`
		Mic    bool            `json:"mic"`
		Cam    bool            `json:"cam"`
		Screen bool            `json:"screen"`
		Hand   bool            `json:"hand"`
		D      json.RawMessage `json:"d"`
	}
	if len(env.D) > maxGroupSignal+1024 || json.Unmarshal(env.D, &d) != nil {
		c.fail(env.ID, ErrForbidden)
		return
	}
	var ack any
	var err error
	switch env.T {
	case "gcall_join":
		var in *GroupCallInfo
		var ice []ICEServer
		if in, ice, err = s.GroupJoin(ctx, c, d.Chat, d.Video, d.Tab); err == nil {
			ack = map[string]any{"call": in, "ice": ice}
		}
	case "gcall_resume":
		var in *GroupCallInfo
		if in, err = s.GroupResume(c, d.Call, d.Tab); err == nil {
			ack = map[string]any{"call": in, "ice": s.ice(c)}
		}
	case "gcall_signal":
		err = s.groupSignal(c, d.Call, d.D)
	case "gcall_media":
		err = s.groupMedia(c, d.Call, d.Mic, d.Cam, d.Screen, d.Hand)
	case "gcall_leave":
		err = s.GroupLeave(c, d.Call)
	case "gcall_end":
		err = s.GroupEnd(c, d.Call)
	case "gcall_mute":
		err = s.groupMute(c, d.Call, d.User, d.On)
	default:
		return
	}
	if err != nil {
		c.fail(env.ID, err)
		return
	}
	if env.ID != "" {
		c.send("ack", env.ID, ack)
	}
}

// member is c's place in call id, if c is the device in it; the caller
// holds the hub's lock.
func (s *Service) member(id int64, c *conn) (*gcall, *gmember) {
	s.gcalls.init()
	g := s.gcalls.byID[id]
	if g == nil {
		return nil, nil
	}
	m := g.members[c.user]
	if m == nil || m.c != c || m.lost {
		return nil, nil
	}
	return g, m
}

func (s *Service) groupSignal(c *conn, id int64, raw json.RawMessage) error {
	if len(raw) == 0 || len(raw) > maxGroupSignal {
		return ErrTooLong
	}
	s.gcalls.mu.Lock()
	_, m := s.member(id, c)
	var peer MediaPeer
	if m != nil {
		peer = m.peer
	}
	s.gcalls.mu.Unlock()
	if peer == nil {
		return ErrNoCall
	}
	var d struct {
		SDP *struct {
			Type string `json:"type"`
			SDP  string `json:"sdp"`
		} `json:"sdp"`
		Cand json.RawMessage `json:"cand"`
	}
	if json.Unmarshal(raw, &d) != nil {
		return ErrForbidden
	}
	switch {
	case d.SDP != nil && d.SDP.Type == "answer":
		return peer.Answer(d.SDP.SDP)
	case len(d.Cand) > 0:
		return peer.CandidateJSON(d.Cand)
	}
	return nil
}

func (s *Service) groupMedia(c *conn, id int64, mic, cam, screen, hand bool) error {
	s.gcalls.mu.Lock()
	g, m := s.member(id, c)
	if m != nil {
		m.mic, m.cam, m.screen = mic, cam, screen
		if hand && !m.hand {
			m.handAt = s.now()
		}
		m.hand = hand
	}
	s.gcalls.mu.Unlock()
	if m == nil {
		return ErrNoCall
	}
	s.groupState(g)
	return nil
}

// groupMute: an admin holds user's microphone back, or lets it through.
func (s *Service) groupMute(c *conn, id int64, user string, on bool) error {
	s.gcalls.mu.Lock()
	g, me := s.member(id, c)
	var target *gmember
	if g != nil {
		target = g.members[user]
	}
	allowed := me != nil && (me.admin || g.by == c.user) && target != nil && user != c.user
	var to *conn
	if allowed {
		target.held = on
		if on {
			target.hand = false
		}
		to = target.c
	}
	s.gcalls.mu.Unlock()
	if g == nil || target == nil {
		return ErrNoCall
	}
	if !allowed {
		return ErrForbidden
	}
	s.GroupMedia.Mute(id, user, on)
	if to != nil {
		to.send("gcall_held", "", map[string]any{"call": id, "on": on})
	}
	s.groupState(g)
	return nil
}

// GroupResume gives call id back to its person's new socket after theirs
// dropped: only from the page that was in it (tab).
func (s *Service) GroupResume(c *conn, id int64, tab string) (*GroupCallInfo, error) {
	s.gcalls.mu.Lock()
	s.gcalls.init()
	g := s.gcalls.byID[id]
	var m *gmember
	if g != nil {
		m = g.members[c.user]
	}
	if m == nil || (m.c != c && (tab == "" || tab != m.tab)) {
		s.gcalls.mu.Unlock()
		return nil, ErrNoCall
	}
	m.c, m.lost = c, false
	if m.rejoin != nil {
		m.rejoin.Stop()
	}
	in := s.gcallInfo(g, c.user)
	s.gcalls.mu.Unlock()
	s.groupState(g)
	return in, nil
}

// groupState sends everyone in g who is in it now and what each sends.
func (s *Service) groupState(g *gcall) {
	s.gcalls.mu.Lock()
	type out struct {
		c  *conn
		in *GroupCallInfo
	}
	var outs []out
	for u, m := range g.members {
		if m.c != nil && !m.lost {
			outs = append(outs, out{m.c, s.gcallInfo(g, u)})
		}
	}
	s.gcalls.mu.Unlock()
	for _, o := range outs {
		o.c.send("gcall_state", "", o.in)
	}
}

// groupUpdate tells every member of g's chat that a call goes on (and how
// many are in it), or that it ended.
func (s *Service) groupUpdate(g *gcall) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	members, err := s.Store.Members(ctx, g.chat)
	if err != nil {
		return
	}
	s.gcalls.mu.Lock()
	var call any
	if !g.ended {
		users := []string{}
		for u := range g.members {
			if len(users) < 3 {
				users = append(users, u)
			}
		}
		call = map[string]any{"id": g.id, "started_at": g.started, "count": len(g.members), "users": users, "by": g.by}
	}
	s.gcalls.mu.Unlock()
	s.deliver(members, "gcall_update", map[string]any{"chat": g.chat, "call": call}, nil)
}

// gcallsOnConnect tells a new socket about the group calls going on in its
// person's chats, and gives back the one it was in (gcall_active).
func (s *Service) gcallsOnConnect(c *conn) {
	s.gcalls.mu.Lock()
	s.gcalls.init()
	var list []*gcall
	for _, g := range s.gcalls.byID {
		list = append(list, g)
	}
	mine := s.gcalls.byUser[c.user]
	var active *GroupCallInfo
	if mine != nil {
		if m := mine.members[c.user]; m != nil && m.lost {
			active = s.gcallInfo(mine, c.user)
		}
	}
	s.gcalls.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, g := range list {
		if _, err := s.Store.Role(ctx, g.chat, c.user); err != nil {
			continue
		}
		s.gcalls.mu.Lock()
		call := map[string]any{"id": g.id, "started_at": g.started, "count": len(g.members), "by": g.by}
		s.gcalls.mu.Unlock()
		c.send("gcall_update", "", map[string]any{"chat": g.chat, "call": call})
	}
	if active != nil {
		c.send("gcall_active", "", active)
	}
}

// gcallsOnDisconnect: a participant's socket dropped; the call waits for it
// (gcall_resume) as a one-to-one call does, then lets them go.
func (s *Service) gcallsOnDisconnect(c *conn) {
	s.gcalls.mu.Lock()
	s.gcalls.init()
	g := s.gcalls.byUser[c.user]
	var m *gmember
	if g != nil {
		m = g.members[c.user]
	}
	if m == nil || m.c != c {
		s.gcalls.mu.Unlock()
		return
	}
	m.lost = true
	id, user := g.id, c.user
	m.rejoin = time.AfterFunc(rejoinWithin, func() {
		s.gcalls.mu.Lock()
		still := g.members[user] == m && m.lost
		s.gcalls.mu.Unlock()
		if still {
			s.groupLeave(id, user, nil)
		}
	})
	s.gcalls.mu.Unlock()
	s.groupState(g)
}
