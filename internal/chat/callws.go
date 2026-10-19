package chat

import (
	"context"
	"encoding/json"
	"time"
)

// The call_* socket frames (calls.go has the rules).
//
//	call_start   {peer, video, tab}   -> ack {call, ice}; the person called gets call_incoming
//	call_accept  {call, tab}          -> ack {call, ice}; the caller gets call_accepted
//	call_decline {call, text?}        -> the caller gets call_ended (declined), text as a message
//	call_hangup  {call, reason?}      -> both get call_ended
//	call_signal  {call, d}            -> the other device gets call_signal {call, d} (offer, answer, ICE)
//	call_media   {call, d}            -> the other device gets call_media {call, d} (mic, camera, screen)
//	call_state   {call, state}        -> "connected" starts the duration
//	call_resume  {call, tab}          -> ack {call, ice}: this socket takes the call back after a drop
//
// tab is the page instance, random per page load.
//
// Only the two devices in a call may signal in it (security check 10's
// one-to-one counterpart): any other socket, the same person's other
// devices included, is refused.
func (c *conn) handleCall(ctx context.Context, env Envelope) {
	s := c.svc
	var d struct {
		Call   int64           `json:"call"`
		Peer   string          `json:"peer"`
		Video  bool            `json:"video"`
		Text   string          `json:"text"`
		Reason string          `json:"reason"`
		State  string          `json:"state"`
		Tab    string          `json:"tab"` // the page instance (a reconnect takes its call back)
		D      json.RawMessage `json:"d"`
	}
	if len(env.D) > maxSignal+1024 || json.Unmarshal(env.D, &d) != nil {
		c.fail(env.ID, ErrForbidden)
		return
	}
	var ack any
	var err error
	switch env.T {
	case "call_start":
		var in *CallInfo
		var ice []ICEServer
		if in, ice, err = s.CallStart(ctx, c, d.Peer, d.Video, d.Tab); err == nil {
			ack = map[string]any{"call": in, "ice": ice}
		}
	case "call_accept":
		var in *CallInfo
		var ice []ICEServer
		if in, ice, err = s.CallAccept(ctx, c, d.Call, d.Tab); err == nil {
			ack = map[string]any{"call": in, "ice": ice}
		}
	case "call_resume":
		var in *CallInfo
		var ice []ICEServer
		if in, ice, err = s.CallResume(c, d.Call, d.Tab); err == nil {
			ack = map[string]any{"call": in, "ice": ice}
			// A caller whose socket was down when the call was answered has
			// not heard so; it makes the offer on this.
			if in.Outgoing && in.State == "connecting" {
				defer c.send("call_accepted", "", in)
			}
		}
	case "call_decline":
		err = s.CallDecline(ctx, c.user, d.Call, d.Text)
	case "call_hangup":
		err = s.CallHangup(ctx, c, d.Call, d.Reason)
	case "call_signal", "call_media":
		err = s.callRelay(ctx, c, env.T, d.Call, d.D)
	case "call_state":
		err = s.callState(ctx, c, d.Call, d.State)
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

// callRelay passes what one browser of a call says to the other.
func (s *Service) callRelay(ctx context.Context, c *conn, t string, id int64, d json.RawMessage) error {
	if len(d) == 0 || len(d) > maxSignal {
		return ErrTooLong
	}
	s.calls.mu.Lock()
	s.calls.init()
	cl := s.calls.byID[id]
	side := -1
	if cl != nil {
		side = cl.side(c)
	}
	if side < 0 || cl.state == "ringing" {
		s.calls.mu.Unlock()
		return ErrNoCall
	}
	other := cl.dev[1-side]
	upgrade := false
	if t == "call_media" && !cl.video {
		var m struct {
			Cam bool `json:"cam"`
		}
		if json.Unmarshal(d, &m) == nil && m.Cam {
			cl.video, upgrade = true, true // a voice call became a video call
		}
	}
	s.calls.mu.Unlock()
	if upgrade {
		_ = s.Store.CallVideo(ctx, id)
	}
	if other != nil {
		other.send(t, "", map[string]any{"call": id, "d": d})
	}
	return nil
}

// callState takes a device's report of its connection. The first
// "connected" starts the call's duration.
func (s *Service) callState(ctx context.Context, c *conn, id int64, state string) error {
	s.calls.mu.Lock()
	s.calls.init()
	cl := s.calls.byID[id]
	if cl == nil || cl.side(c) < 0 {
		s.calls.mu.Unlock()
		return ErrNoCall
	}
	first := false
	if state == "connected" && cl.state == "connecting" {
		cl.state, cl.connected, first = "connected", s.now(), true
		cl.timer.Stop()
	}
	at := cl.connected
	s.calls.mu.Unlock()
	if first {
		_ = s.Store.CallConnected(ctx, id, at)
	}
	return nil
}

// CallResume gives a call back to a person's new socket after theirs dropped
// (a network change). Only the page that holds the call (tab) may take it
// back, even before the server has noticed its old socket is gone; another
// page or device of the same person cannot take the call over (it would
// have no media, and could only end it). The other device is told, and may
// restart ICE.
func (s *Service) CallResume(c *conn, id int64, tab string) (*CallInfo, []ICEServer, error) {
	s.calls.mu.Lock()
	s.calls.init()
	cl := s.calls.byID[id]
	side := -1
	if cl != nil {
		switch {
		case c.user == cl.caller:
			side = 0
		case c.user == cl.callee && cl.state != "ringing":
			side = 1
		}
	}
	if side < 0 || (cl.dev[side] != c && cl.tab[side] != "" && tab != cl.tab[side]) ||
		(cl.dev[side] != nil && !cl.lost[side] && cl.dev[side] != c && (tab == "" || tab != cl.tab[side])) {
		s.calls.mu.Unlock()
		return nil, nil, ErrNoCall
	}
	cl.dev[side], cl.lost[side] = c, false
	if tab != "" {
		cl.tab[side] = tab
	}
	if t := cl.rejoin[side]; t != nil {
		t.Stop()
	}
	other, otherLost := cl.dev[1-side], cl.lost[1-side]
	in := s.callInfo(cl, c.user)
	s.calls.mu.Unlock()
	if other != nil && !otherLost {
		other.send("call_back", "", map[string]any{"call": id})
	}
	return in, s.ice(c), nil
}

// callsOnConnect tells a new socket about the call its person is in: a call
// ringing for them rings here too; a call whose device dropped can be taken
// back (call_active).
func (s *Service) callsOnConnect(c *conn) {
	s.calls.mu.Lock()
	s.calls.init()
	cl := s.calls.byUser[c.user]
	var t string
	var in *CallInfo
	if cl != nil && cl.id != 0 {
		switch {
		case cl.callee == c.user && cl.state == "ringing":
			t, in = "call_incoming", s.callInfo(cl, c.user)
		case (cl.caller == c.user && cl.lost[0]) || (cl.callee == c.user && cl.lost[1]):
			t, in = "call_active", s.callInfo(cl, c.user)
		}
	}
	s.calls.mu.Unlock()
	if in != nil {
		c.send(t, "", in)
	}
}

// callsOnDisconnect: a device in a call lost its socket. The call waits for
// it (call_resume) before it ends.
func (s *Service) callsOnDisconnect(c *conn) {
	s.calls.mu.Lock()
	s.calls.init()
	cl := s.calls.byUser[c.user]
	side := -1
	if cl != nil {
		side = cl.side(c)
	}
	if side < 0 {
		s.calls.mu.Unlock()
		return
	}
	cl.lost[side] = true
	id := cl.id
	cl.rejoin[side] = time.AfterFunc(rejoinWithin, func() {
		s.calls.mu.Lock()
		if s.calls.byID[id] != cl || !cl.lost[side] {
			s.calls.mu.Unlock()
			return
		}
		// How it ends is decided now: it may have been answered meanwhile.
		reason := "failed"
		if cl.state == "ringing" {
			reason = "cancelled"
		}
		s.calls.drop(cl)
		s.calls.mu.Unlock()
		s.finish(cl, reason)
	})
	s.calls.mu.Unlock()
}
