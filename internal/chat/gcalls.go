package chat

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

// Group voice and video calls (docs/chat/SPEC.md 6.9, asked for on
// 2026-10-14). The media goes through docfind's media server
// (internal/calls/sfu.go): each participant sends once and receives
// everyone else. This file is the switchboard: who may start and join (the
// members of a group or supergroup), who is in, what each sends (microphone,
// camera, screen, a raised hand), an admin holding someone's microphone
// back or ending the call for everyone, the «join» bar every member of the
// group sees while a call goes on, and the lines a call leaves in the chat.

// MediaServer is the group calls' media server (internal/calls, lent by the
// wiring); nil turns group calls off.
type MediaServer interface {
	Join(room int64, user string, send func(map[string]any), gone func()) (MediaPeer, error)
	Mute(room int64, user string, on bool)
	End(room int64)
}

// MediaPeer is one participant's connection to the media server.
type MediaPeer interface {
	Answer(sdp string) error
	CandidateJSON(raw []byte) error
	Leave()
}

const groupCallMax = 30

var (
	ErrNoGroupCalls = errors.New("chat: group calls are off on this server")
	ErrCallFull     = errors.New("chat: the call is full")
)

// GroupMember is one participant as the pages see them.
type GroupMember struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Photo  bool   `json:"photo,omitempty"`
	Mic    bool   `json:"mic"`
	Cam    bool   `json:"cam"`
	Screen bool   `json:"screen"`
	Hand   bool   `json:"hand,omitempty"`
	Held   bool   `json:"held,omitempty"` // an admin holds their microphone back
	Lost   bool   `json:"lost,omitempty"` // their connection dropped; they may come back
}

// GroupCallInfo is a group call as one of its participants sees it.
type GroupCallInfo struct {
	ID      int64         `json:"id"`
	Chat    int64         `json:"chat"`
	By      string        `json:"by"`
	Started time.Time     `json:"started_at"`
	Members []GroupMember `json:"members"`
	Admin   bool          `json:"admin,omitempty"` // the viewer may hold microphones back and end it
}

type gmember struct {
	user                         string
	c                            *conn
	tab                          string
	peer                         MediaPeer
	admin                        bool
	mic, cam, screen, hand, held bool
	lost                         bool
	handAt, joined               time.Time
	rejoin                       *time.Timer
}

type gcall struct {
	id, chat int64
	by       string
	started  time.Time
	members  map[string]*gmember
	ended    bool
}

type gcallHub struct {
	mu     sync.Mutex
	byChat map[int64]*gcall
	byID   map[int64]*gcall
	byUser map[string]*gcall
}

func (h *gcallHub) init() {
	if h.byChat == nil {
		h.byChat, h.byID, h.byUser = map[int64]*gcall{}, map[int64]*gcall{}, map[string]*gcall{}
	}
}

// info is g as viewer sees it; the caller holds the hub's lock.
func (s *Service) gcallInfo(g *gcall, viewer string) *GroupCallInfo {
	in := &GroupCallInfo{ID: g.id, Chat: g.chat, By: g.by, Started: g.started}
	list := make([]*gmember, 0, len(g.members))
	for _, m := range g.members {
		list = append(list, m)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].joined.Before(list[j].joined) })
	for _, m := range list {
		in.Members = append(in.Members, GroupMember{ID: m.user, Name: s.displayName(m.user), Photo: s.hasPhoto(m.user),
			Mic: m.mic && !m.held, Cam: m.cam, Screen: m.screen, Hand: m.hand, Held: m.held, Lost: m.lost})
		if m.user == viewer {
			in.Admin = m.admin || g.by == viewer
		}
	}
	return in
}

// GroupJoin puts c's person into chatID's group call, starting one if none
// is going on.
func (s *Service) GroupJoin(ctx context.Context, c *conn, chatID int64, video bool, tab string) (*GroupCallInfo, []ICEServer, error) {
	if s.GroupMedia == nil {
		return nil, nil, ErrNoGroupCalls
	}
	role, err := s.Store.Role(ctx, chatID, c.user)
	if err != nil {
		return nil, nil, err
	}
	if typ, _, err := s.Store.ChatType(ctx, chatID, c.user); err != nil {
		return nil, nil, err
	} else if typ != "group" && typ != "supergroup" {
		return nil, nil, ErrForbidden
	}
	s.calls.mu.Lock()
	s.calls.init()
	inCall := s.calls.byUser[c.user] != nil
	s.calls.mu.Unlock()
	if inCall {
		return nil, nil, ErrInCall
	}

	var g *gcall
	created := false
	for tries := 0; ; tries++ {
		s.gcalls.mu.Lock()
		s.gcalls.init()
		g = s.gcalls.byChat[chatID]
		if g == nil {
			g = &gcall{chat: chatID, by: c.user, members: map[string]*gmember{}}
			s.gcalls.byChat[chatID] = g
			s.gcalls.mu.Unlock()
			id, at, err := s.Store.GroupCallCreate(ctx, chatID, c.user)
			s.gcalls.mu.Lock()
			if err != nil {
				delete(s.gcalls.byChat, chatID)
				s.gcalls.mu.Unlock()
				return nil, nil, err
			}
			g.id, g.started, created = id, at, true
			s.gcalls.byID[id] = g
			break
		}
		if g.id != 0 {
			break
		}
		s.gcalls.mu.Unlock() // someone else is starting it this moment
		if tries > 40 {
			return nil, nil, ErrRate
		}
		time.Sleep(50 * time.Millisecond)
	}
	// The hub's lock is held here.
	if other := s.gcalls.byUser[c.user]; other != nil && other != g {
		s.gcalls.mu.Unlock()
		return nil, nil, ErrInCall
	}
	m := g.members[c.user]
	if m == nil && len(g.members) >= groupCallMax {
		s.gcalls.mu.Unlock()
		return nil, nil, ErrCallFull
	}
	var oldConn *conn
	var oldPeer MediaPeer
	if m != nil { // the same person from another device takes their place over
		oldConn, oldPeer = m.c, m.peer
		if m.rejoin != nil {
			m.rejoin.Stop()
		}
	} else {
		m = &gmember{user: c.user, joined: s.now(), mic: true}
		g.members[c.user] = m
	}
	m.c, m.tab, m.cam, m.lost, m.peer = c, tab, video, false, nil
	m.admin = role == "owner" || role == "admin"
	s.gcalls.byUser[c.user] = g
	id := g.id
	s.gcalls.mu.Unlock()

	if oldConn != nil && oldConn != c {
		oldConn.send("gcall_moved", "", map[string]any{"call": id})
	}
	_ = s.Store.GroupCallJoin(ctx, id, c.user, video, s.now())
	user := c.user
	send := func(d map[string]any) {
		s.gcalls.mu.Lock()
		var to *conn
		if mm := g.members[user]; mm != nil && !mm.lost {
			to = mm.c
		}
		s.gcalls.mu.Unlock()
		if to != nil {
			to.send("gcall_signal", "", map[string]any{"call": id, "d": d})
		}
	}
	var peer MediaPeer
	peer, err = s.GroupMedia.Join(id, user, send, func() { s.groupGone(id, user, peer) })
	if err != nil {
		s.groupLeave(id, user, nil)
		return nil, nil, err
	}
	s.gcalls.mu.Lock()
	if g.members[user] == m {
		m.peer = peer
	}
	in := s.gcallInfo(g, user)
	s.gcalls.mu.Unlock()
	if oldPeer != nil && oldPeer != peer {
		oldPeer.Leave()
	}
	if created {
		s.service(ctx, chatID, user, 0, map[string]any{"action": "gcall", "call": id, "reason": "started", "video": video})
		s.Logf("messenger: group call %d started", id)
	}
	s.groupState(g)
	s.groupUpdate(g)
	return in, s.ice(c), nil
}

// groupGone: a participant's media connection failed or closed by itself.
func (s *Service) groupGone(id int64, user string, peer MediaPeer) {
	s.gcalls.mu.Lock()
	g := s.gcalls.byID[id]
	stale := g == nil || g.members[user] == nil || g.members[user].peer != peer || peer == nil
	s.gcalls.mu.Unlock()
	if !stale {
		s.groupLeave(id, user, nil)
	}
}

// GroupLeave takes c's person out of call id.
func (s *Service) GroupLeave(c *conn, id int64) error {
	if !s.groupLeave(id, c.user, c) {
		return ErrNoCall
	}
	return nil
}

// groupLeave takes user out of call id (only through conn c, unless nil);
// the last one out ends it.
func (s *Service) groupLeave(id int64, user string, c *conn) bool {
	s.gcalls.mu.Lock()
	s.gcalls.init()
	g := s.gcalls.byID[id]
	if g == nil || (g.id == 0) {
		s.gcalls.mu.Unlock()
		return false
	}
	m := g.members[user]
	if m == nil || (c != nil && m.c != c) {
		s.gcalls.mu.Unlock()
		return false
	}
	delete(g.members, user)
	if s.gcalls.byUser[user] == g {
		delete(s.gcalls.byUser, user)
	}
	if m.rejoin != nil {
		m.rejoin.Stop()
	}
	empty := len(g.members) == 0
	s.gcalls.mu.Unlock()
	if m.peer != nil {
		m.peer.Leave()
	}
	_ = s.Store.GroupCallLeave(context.Background(), id, user, s.now())
	if empty {
		s.groupEnd(g)
		return true
	}
	s.groupState(g)
	s.groupUpdate(g)
	return true
}

// GroupEnd ends call id for everyone: its starter or an admin of the chat.
func (s *Service) GroupEnd(c *conn, id int64) error {
	s.gcalls.mu.Lock()
	s.gcalls.init()
	g := s.gcalls.byID[id]
	ok := g != nil && g.members[c.user] != nil && (g.members[c.user].admin || g.by == c.user)
	s.gcalls.mu.Unlock()
	if g == nil {
		return ErrNoCall
	}
	if !ok {
		return ErrForbidden
	}
	s.groupEnd(g)
	return nil
}

// groupEnd closes g: everyone out, the room closed, the line in the chat.
func (s *Service) groupEnd(g *gcall) {
	s.gcalls.mu.Lock()
	if g.ended {
		s.gcalls.mu.Unlock()
		return
	}
	g.ended = true
	delete(s.gcalls.byID, g.id)
	if s.gcalls.byChat[g.chat] == g {
		delete(s.gcalls.byChat, g.chat)
	}
	var conns []*conn
	var peers []MediaPeer
	for u, m := range g.members {
		if s.gcalls.byUser[u] == g {
			delete(s.gcalls.byUser, u)
		}
		if m.rejoin != nil {
			m.rejoin.Stop()
		}
		if m.c != nil {
			conns = append(conns, m.c)
		}
		if m.peer != nil {
			peers = append(peers, m.peer)
		}
	}
	g.members = map[string]*gmember{}
	s.gcalls.mu.Unlock()
	for _, p := range peers {
		p.Leave()
	}
	if s.GroupMedia != nil {
		s.GroupMedia.End(g.id)
	}
	for _, c := range conns {
		c.send("gcall_ended", "", map[string]any{"call": g.id, "chat": g.chat})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	now := s.now()
	_ = s.Store.CallEnded(ctx, g.id, "ended", now)
	s.service(ctx, g.chat, g.by, 0, map[string]any{"action": "gcall", "call": g.id, "reason": "ended", "duration": int(now.Sub(g.started).Seconds())})
	s.groupUpdate(g)
	s.Logf("messenger: group call %d ended", g.id)
}
