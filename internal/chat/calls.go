package chat

import (
	"context"
	"errors"
	"sync"
	"time"
)

// One-to-one calls (docs/chat/SPEC.md 6.8). The server is the switchboard:
// it checks that the caller may call (the person's «چه کسی می‌تواند با من
// تماس بگیرد» rule, blocks), rings every device of the person called, binds
// the call to the one device that answers, and passes the two browsers'
// offer, answer and ICE candidates between those two devices and nobody
// else. The media goes browser to browser, or through docfind's own TURN
// relay (internal/calls); the server never sees it. Every call is a row
// (callstore.go) and, when it ends, a line in the private chat.

var (
	ErrNoCall      = errors.New("chat: no such call")
	ErrInCall      = errors.New("chat: already in a call")
	ErrCallPrivacy = errors.New("chat: their settings do not allow calls")
)

// How long things may take (variables only so the tests need not wait).
var (
	ringFor       = 45 * time.Second // SPEC 8.9: the ring goes on until answered or 45 s pass
	connectWithin = 40 * time.Second // answered: the media must flow within this
	// A device whose socket dropped has this long to come back. A phone
	// whose person went to another app loses its socket, not its call (the
	// media goes on); a device that is really gone is noticed sooner by the
	// other side, which puts the call down after 30 s without media.
	rejoinWithin = 3 * time.Minute
)

const maxSignal = 32 << 10 // an SDP or a candidate passed between the browsers

// maxGroupSignal: a group call's answer has a part for every stream of every
// other person in it (some 600 bytes each).
const maxGroupSignal = 128 << 10

// ICEServer is one entry of a page's RTCPeerConnection iceServers.
type ICEServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

// CallInfo is a call as one of its two people sees it.
type CallInfo struct {
	ID       int64     `json:"id"`
	Chat     int64     `json:"chat"`
	Peer     CallPeer  `json:"peer"`
	Outgoing bool      `json:"outgoing"`
	Video    bool      `json:"video"`
	State    string    `json:"state"` // ringing | connecting | connected | ended
	Reason   string    `json:"reason,omitempty"`
	Started  time.Time `json:"started_at"`
}

// CallNotice is a call announced to the person called: ringing now, or missed.
type CallNotice struct {
	Call   int64
	Chat   int64
	To     string
	From   string // the caller's name
	Video  bool
	Missed bool
}

// call is a call in progress.
type call struct {
	id, chat       int64
	caller, callee string
	video          bool
	state          string
	started        time.Time
	connected      time.Time
	dev            [2]*conn  // the caller's device, then the callee's once answered
	tab            [2]string // the page on that device (it may come back on a new socket)
	lost           [2]bool   // that device's socket went; it may come back
	timer          *time.Timer
	rejoin         [2]*time.Timer
}

// side is 0 for the caller's device in the call, 1 for the callee's, -1 for
// any other socket.
func (cl *call) side(c *conn) int {
	switch {
	case c != nil && cl.dev[0] == c:
		return 0
	case c != nil && cl.dev[1] == c:
		return 1
	}
	return -1
}

func (cl *call) stop() {
	for _, t := range []*time.Timer{cl.timer, cl.rejoin[0], cl.rejoin[1]} {
		if t != nil {
			t.Stop()
		}
	}
}

// callHub is every call in progress.
type callHub struct {
	mu     sync.Mutex
	byID   map[int64]*call
	byUser map[string]*call // one call per person
}

func (h *callHub) init() {
	if h.byID == nil {
		h.byID, h.byUser = map[int64]*call{}, map[string]*call{}
	}
}

// drop takes cl out of the hub; the caller holds h.mu.
func (h *callHub) drop(cl *call) {
	cl.state = "ended"
	cl.stop()
	delete(h.byID, cl.id)
	for _, u := range []string{cl.caller, cl.callee} {
		if h.byUser[u] == cl {
			delete(h.byUser, u)
		}
	}
}

func (s *Service) callInfo(cl *call, viewer string) *CallInfo {
	in := &CallInfo{ID: cl.id, Chat: cl.chat, Outgoing: viewer == cl.caller, Video: cl.video, State: cl.state, Started: cl.started}
	peer := cl.caller
	if in.Outgoing {
		peer = cl.callee
	}
	in.Peer = CallPeer{ID: peer, Name: s.displayName(peer), Photo: s.hasPhoto(peer)}
	return in
}

func (s *Service) ice(c *conn) []ICEServer {
	if s.ICE == nil {
		return nil
	}
	return s.ICE(c.user, c.ver, c.host)
}

// CallStart rings peer from c's device.
func (s *Service) CallStart(ctx context.Context, c *conn, peer string, video bool, tab string) (*CallInfo, []ICEServer, error) {
	if peer == "" || peer == c.user {
		return nil, nil, ErrForbidden
	}
	if !s.callLimit.allow(c.user) {
		return nil, nil, ErrRate
	}
	s.gcalls.mu.Lock()
	s.gcalls.init()
	inGroup := s.gcalls.byUser[c.user] != nil || s.gcalls.byUser[peer] != nil
	s.gcalls.mu.Unlock()
	if inGroup {
		return nil, nil, ErrInCall
	}
	if u, err := s.users.Get(peer); err != nil || u.Disabled {
		return nil, nil, ErrNoUser
	}
	if blocked, err := s.Store.Blocked(ctx, c.user, peer); err != nil {
		return nil, nil, err
	} else if blocked {
		return nil, nil, ErrBlocked
	}
	profiles, err := s.Store.Profiles(ctx, []string{peer})
	if err != nil {
		return nil, nil, err
	}
	contact, _ := s.Store.IsContact(ctx, peer, c.user)
	if !profiles[peer].Privacy.Calls.Allows(c.user, contact) {
		return nil, nil, ErrCallPrivacy
	}
	chatID, err := s.Store.OpenPrivate(ctx, c.user, peer)
	if err != nil {
		return nil, nil, err
	}
	// Both people are reserved before the row is written, so two calls
	// started at once cannot both ring.
	cl := &call{chat: chatID, caller: c.user, callee: peer, video: video, state: "ringing"}
	cl.dev[0], cl.tab[0] = c, tab
	s.calls.mu.Lock()
	s.calls.init()
	if s.calls.byUser[c.user] != nil {
		s.calls.mu.Unlock()
		return nil, nil, ErrInCall
	}
	busy := s.calls.byUser[peer] != nil
	s.calls.byUser[c.user] = cl
	if !busy {
		s.calls.byUser[peer] = cl
	}
	s.calls.mu.Unlock()

	id, at, err := s.Store.CallCreate(ctx, chatID, c.user, peer, video)
	s.calls.mu.Lock()
	cl.id, cl.started = id, at
	if err != nil || busy {
		s.calls.drop(cl)
		s.calls.mu.Unlock()
		if err != nil {
			return nil, nil, err
		}
		// Recorded as a call that found them busy: a line in the chat and a
		// missed call for them, «مشغول» for the caller.
		in := s.callInfo(cl, c.user)
		in.State, in.Reason = "ended", "busy"
		go s.finish(cl, "busy")
		return in, nil, nil
	}
	s.calls.byID[id] = cl
	cl.timer = time.AfterFunc(ringFor, func() { s.callTimeout(id, "ringing", "missed") })
	ring, mine := s.callInfo(cl, peer), s.callInfo(cl, c.user)
	s.calls.mu.Unlock()

	s.deliver([]string{peer}, "call_incoming", ring, nil)
	s.notifyCall(cl, false)
	s.Logf("messenger: call %d ringing", id)
	return mine, s.ice(c), nil
}

// CallAccept answers a ringing call on c's device. The callee's other
// devices stop ringing; the caller's device is told and makes the offer.
func (s *Service) CallAccept(ctx context.Context, c *conn, id int64, tab string) (*CallInfo, []ICEServer, error) {
	s.calls.mu.Lock()
	s.calls.init()
	cl := s.calls.byID[id]
	if cl == nil || cl.callee != c.user || cl.state != "ringing" {
		s.calls.mu.Unlock()
		return nil, nil, ErrNoCall
	}
	cl.state, cl.dev[1], cl.tab[1] = "connecting", c, tab
	cl.timer.Stop()
	cl.timer = time.AfterFunc(connectWithin, func() { s.callTimeout(id, "connecting", "failed") })
	caller, lost := cl.dev[0], cl.lost[0]
	toCaller, mine := s.callInfo(cl, cl.caller), s.callInfo(cl, c.user)
	s.calls.mu.Unlock()

	_ = s.Store.CallAnswered(ctx, id, c.user, s.now())
	if caller != nil && !lost {
		caller.send("call_accepted", "", toCaller)
	}
	s.deliver([]string{c.user}, "call_taken", map[string]any{"call": id}, c)
	return mine, s.ice(c), nil
}

// CallDecline refuses a ringing call, from any of the callee's devices or a
// notification; text, when given, goes to the caller as a message.
func (s *Service) CallDecline(ctx context.Context, user string, id int64, text string) error {
	s.calls.mu.Lock()
	s.calls.init()
	cl := s.calls.byID[id]
	if cl == nil || cl.callee != user || cl.state != "ringing" {
		s.calls.mu.Unlock()
		return ErrNoCall
	}
	s.calls.drop(cl)
	s.calls.mu.Unlock()
	s.finish(cl, "declined")
	if text != "" {
		if _, err := s.SendText(ctx, user, SendReq{Chat: cl.chat, CID: uuidV7(), Text: text}, nil); err != nil {
			return err
		}
	}
	return nil
}

// CallHangup ends a call from one of its two devices. A ringing call put
// down by the caller is cancelled; put down by the callee, declined.
func (s *Service) CallHangup(ctx context.Context, c *conn, id int64, reason string) error {
	s.calls.mu.Lock()
	s.calls.init()
	cl := s.calls.byID[id]
	if cl == nil {
		s.calls.mu.Unlock()
		return ErrNoCall
	}
	side := cl.side(c)
	if side < 0 {
		s.calls.mu.Unlock()
		if cl.callee == c.user {
			return s.CallDecline(ctx, c.user, id, "")
		}
		return ErrNoCall
	}
	r := "ended"
	switch {
	case cl.state == "ringing":
		r = "cancelled"
	case reason == "failed":
		r = "failed"
	}
	s.calls.drop(cl)
	s.calls.mu.Unlock()
	s.finish(cl, r)
	return nil
}

// callTimeout ends call id with reason if it is still in state.
func (s *Service) callTimeout(id int64, state, reason string) {
	s.calls.mu.Lock()
	cl := s.calls.byID[id]
	if cl == nil || cl.state != state {
		s.calls.mu.Unlock()
		return
	}
	s.calls.drop(cl)
	s.calls.mu.Unlock()
	s.finish(cl, reason)
}

// finish records how a call ended, leaves its line in the chat, tells both
// people's devices and, for a call they never answered, the person called.
func (s *Service) finish(cl *call, reason string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	now := s.now()
	dur := 0
	if !cl.connected.IsZero() {
		dur = int(now.Sub(cl.connected).Seconds())
	}
	_ = s.Store.CallEnded(ctx, cl.id, reason, now)
	s.service(ctx, cl.chat, cl.caller, 0, map[string]any{"action": "call", "call": cl.id, "video": cl.video, "reason": reason, "duration": dur})
	s.deliver([]string{cl.caller, cl.callee}, "call_ended", map[string]any{"call": cl.id, "reason": reason, "duration": dur}, nil)
	if missedReason(reason) {
		s.notifyCall(cl, true)
	}
	s.Logf("messenger: call %d ended (%s, %d s)", cl.id, reason, dur)
}

// notifyCall hands the person called a push: ringing now, or missed. Their
// «تماس‌ها» switch turns both off.
func (s *Service) notifyCall(cl *call, missed bool) {
	if s.NotifyCall == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if set, err := s.Store.NotifySettings(ctx, cl.callee); err == nil && !set.Calls {
		return
	}
	s.NotifyCall(context.WithoutCancel(ctx), CallNotice{Call: cl.id, Chat: cl.chat, To: cl.callee, From: s.displayName(cl.caller), Video: cl.video, Missed: missed})
}
