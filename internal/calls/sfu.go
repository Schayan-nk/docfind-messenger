package calls

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
)

// The media server for group calls (docs/chat/SPEC.md 6.9, asked for on
// 2026-10-14): each participant has one connection to docfind and sends its
// microphone, camera and screen once; docfind forwards each of them to every
// other participant of the room (a selective forwarding unit). Only the
// server makes offers, so renegotiation as people come and go never
// collides. All its traffic uses one UDP and one TCP port (an ICE mux), so
// the firewall opens one port, not a range. The media stays encrypted on
// each leg (DTLS-SRTP); docfind only forwards it.
//
// A participant's streams reach the others as stream ids "<user>|mic",
// "<user>|cam" and "<user>|screen", which is how a page knows whose tile a
// track belongs to.

// Kinds of stream a participant sends, in the order of their transceivers.
var Kinds = [3]string{"mic", "cam", "screen"}

// SFUConfig is where the media server listens and what it announces.
type SFUConfig struct {
	Listen   string // the address to listen on (all of this machine's by default)
	Port     int    // DOCFIND_SFU_PORT: UDP and TCP, 3479 by default
	PublicIP net.IP // the address browsers reach it at (DOCFIND_SFU_IP, else the TURN relay address)
	Loopback bool   // offer loopback candidates (the tests)
}

// SFU is the running media server.
type SFU struct {
	api   *webrtc.API
	cfg   SFUConfig
	udp   net.PacketConn
	tcp   net.Listener
	logf  func(string, ...any)
	mu    sync.Mutex
	rooms map[int64]*Room
}

// Room is one group call's media.
type Room struct {
	id     int64
	sfu    *SFU
	mu     sync.Mutex
	peers  map[string]*Peer
	tracks map[string]*webrtc.TrackLocalStaticRTP // by stream id
	muted  map[string]*atomic.Bool                // a user's microphone held back by an admin
}

// Peer is one participant's connection to the server.
type Peer struct {
	User    string
	room    *Room
	pc      *webrtc.PeerConnection
	send    func(d map[string]any) // to the participant's page: {"sdp": offer} or {"cand": candidate}
	offered bool                   // it has had its first offer
	video   []*webrtc.TrackRemote  // what it sends, for keyframe requests
	once    sync.Once
}

// StartSFU listens on UDP and TCP cfg.Port.
func StartSFU(cfg SFUConfig, logf func(string, ...any)) (*SFU, error) {
	if cfg.Port == 0 && !cfg.Loopback {
		cfg.Port = 3479
	}
	host := cfg.Listen
	if host == "" {
		host = "0.0.0.0"
	}
	addr := net.JoinHostPort(host, strconv.Itoa(cfg.Port))
	udp, err := net.ListenPacket("udp4", addr)
	if err != nil {
		return nil, fmt.Errorf("group calls cannot listen on UDP %s: %w", addr, err)
	}
	tcp, err := net.Listen("tcp4", net.JoinHostPort(host, strconv.Itoa(udp.LocalAddr().(*net.UDPAddr).Port)))
	if err != nil {
		udp.Close()
		return nil, fmt.Errorf("group calls cannot listen on TCP %s: %w", addr, err)
	}
	se := webrtc.SettingEngine{}
	se.SetICEUDPMux(webrtc.NewICEUDPMux(nil, udp))
	se.SetICETCPMux(webrtc.NewICETCPMux(nil, tcp, 8))
	se.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4, webrtc.NetworkTypeTCP4})
	se.SetIncludeLoopbackCandidate(cfg.Loopback)
	if cfg.PublicIP != nil {
		ip := cfg.PublicIP.String()
		se.SetIPFilter(func(x net.IP) bool { return x.String() == ip || (cfg.Loopback && x.IsLoopback()) })
	}
	m := &webrtc.MediaEngine{}
	if err := codecs(m); err != nil {
		udp.Close()
		tcp.Close()
		return nil, err
	}
	reg := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(m, reg); err != nil {
		udp.Close()
		tcp.Close()
		return nil, err
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	s := &SFU{api: webrtc.NewAPI(webrtc.WithMediaEngine(m), webrtc.WithSettingEngine(se), webrtc.WithInterceptorRegistry(reg)),
		cfg: cfg, udp: udp, tcp: tcp, logf: logf, rooms: map[int64]*Room{}}
	go s.keyframes()
	return s, nil
}

// Port is the UDP and TCP port the server listens on.
func (s *SFU) Port() int { return s.udp.LocalAddr().(*net.UDPAddr).Port }

// Close stops every room and the listeners.
func (s *SFU) Close() {
	s.mu.Lock()
	rooms := make([]*Room, 0, len(s.rooms))
	for _, r := range s.rooms {
		rooms = append(rooms, r)
	}
	s.mu.Unlock()
	for _, r := range rooms {
		r.Close()
	}
	s.udp.Close()
	s.tcp.Close()
}

// room is call id's room, made on first use.
func (s *SFU) room(id int64) *Room {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rooms[id]
	if r == nil {
		r = &Room{id: id, sfu: s, peers: map[string]*Peer{}, tracks: map[string]*webrtc.TrackLocalStaticRTP{}, muted: map[string]*atomic.Bool{}}
		s.rooms[id] = r
	}
	return r
}

// Join connects user to room id; send carries the server's offers and
// candidates to their page; gone (may be nil) is told when the connection
// fails or closes. A second join by the same user replaces the first (one
// tile per person, whichever device they are on).
func (s *SFU) Join(id int64, user string, send func(d map[string]any), gone func()) (*Peer, error) {
	pc, err := s.api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return nil, err
	}
	for _, k := range []webrtc.RTPCodecType{webrtc.RTPCodecTypeAudio, webrtc.RTPCodecTypeVideo, webrtc.RTPCodecTypeVideo} {
		if _, err := pc.AddTransceiverFromKind(k, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
			pc.Close()
			return nil, err
		}
	}
	r := s.room(id)
	p := &Peer{User: user, room: r, pc: pc, send: send}
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c != nil {
			send(map[string]any{"cand": c.ToJSON()})
		}
	})
	pc.OnConnectionStateChange(func(st webrtc.PeerConnectionState) {
		if st == webrtc.PeerConnectionStateFailed || st == webrtc.PeerConnectionStateClosed {
			p.Leave()
			if gone != nil {
				gone()
			}
		}
	})
	pc.OnTrack(func(remote *webrtc.TrackRemote, recv *webrtc.RTPReceiver) { p.forward(remote, recv) })

	r.mu.Lock()
	old := r.peers[user]
	r.peers[user] = p
	if r.muted[user] == nil {
		r.muted[user] = &atomic.Bool{}
	}
	r.mu.Unlock()
	if old != nil {
		old.close(false)
	}
	r.signal()
	return p, nil
}

// forward reads one of p's streams and writes it to a track every other
// participant receives.
func (p *Peer) forward(remote *webrtc.TrackRemote, recv *webrtc.RTPReceiver) {
	kind := ""
	for i, t := range p.pc.GetTransceivers() {
		if t.Receiver() == recv && i < len(Kinds) {
			kind = Kinds[i]
		}
	}
	if kind == "" {
		return
	}
	id := p.User + "|" + kind
	local, err := webrtc.NewTrackLocalStaticRTP(remote.Codec().RTPCodecCapability, kind, id)
	if err != nil {
		return
	}
	r := p.room
	r.mu.Lock()
	if r.peers[p.User] != p {
		r.mu.Unlock()
		return
	}
	r.tracks[id] = local
	held := r.muted[p.User]
	if remote.Kind() == webrtc.RTPCodecTypeVideo {
		p.video = append(p.video, remote)
	}
	r.mu.Unlock()
	r.signal()
	p.pli(remote)

	buf := make([]byte, 1500)
	for {
		n, _, err := remote.Read(buf)
		if err != nil {
			break
		}
		if kind == "mic" && held != nil && held.Load() {
			continue // an admin muted them: their voice reaches nobody
		}
		_, _ = local.Write(buf[:n]) // one listener's error is not the stream's end
	}
	r.mu.Lock()
	if r.tracks[id] == local {
		delete(r.tracks, id)
	}
	r.mu.Unlock()
	r.signal()
}

// pli asks a camera or screen for a full picture (a newcomer needs one).
func (p *Peer) pli(t *webrtc.TrackRemote) {
	if t.Kind() == webrtc.RTPCodecTypeVideo {
		_ = p.pc.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: uint32(t.SSRC())}})
	}
}

// keyframes asks every video stream for a full picture every 3 s.
func (s *SFU) keyframes() {
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for range t.C {
		s.mu.Lock()
		rooms := make([]*Room, 0, len(s.rooms))
		for _, r := range s.rooms {
			rooms = append(rooms, r)
		}
		s.mu.Unlock()
		if len(rooms) == 0 && s.udp == nil {
			return
		}
		for _, r := range rooms {
			r.mu.Lock()
			for _, p := range r.peers {
				for _, v := range p.video {
					p.pli(v)
				}
			}
			r.mu.Unlock()
		}
	}
}

// signal brings every participant's connection up to date with the room's
// streams. One waiting on an answer is brought up to date when the answer
// comes, and only a real change is offered: offering everyone on every
// change would bounce offers between the participants for ever.
func (r *Room) signal() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.peers {
		r.sync(p)
	}
}

// sync adds the room's new streams to p's connection, drops the gone ones,
// and offers p the result when anything changed (or p has had no offer yet).
// r.mu is held.
func (r *Room) sync(p *Peer) {
	if p.pc.ConnectionState() == webrtc.PeerConnectionStateClosed || p.pc.SignalingState() != webrtc.SignalingStateStable {
		return
	}
	changed := !p.offered
	have := map[string]bool{}
	for _, snd := range p.pc.GetSenders() {
		t := snd.Track()
		if t == nil {
			continue
		}
		if cur, ok := r.tracks[t.StreamID()]; !ok || webrtc.TrackLocal(cur) != t {
			_ = p.pc.RemoveTrack(snd) // gone, or replaced by a rejoin
			changed = true
			continue
		}
		have[t.StreamID()] = true
	}
	for id, t := range r.tracks {
		if have[id] || strings.HasPrefix(id, p.User+"|") {
			continue
		}
		snd, err := r.sfu.carry(p.pc, t)
		if err != nil {
			continue
		}
		changed = true
		go func() { // the sender's RTCP must be read for the interceptors
			b := make([]byte, 1500)
			for {
				if _, _, err := snd.Read(b); err != nil {
					return
				}
			}
		}()
	}
	if !changed {
		return
	}
	offer, err := p.pc.CreateOffer(nil)
	if err != nil {
		return
	}
	if err := p.pc.SetLocalDescription(offer); err != nil {
		return
	}
	p.offered = true
	p.send(map[string]any{"sdp": map[string]string{"type": "offer", "sdp": offer.SDP}})
}

// Answer takes the page's answer to the last offer, then offers what changed
// in the room meanwhile. The page now receives what the offer added, and
// each picture needs a full frame to start from.
func (p *Peer) Answer(sdp string) error {
	if err := p.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: sdp}); err != nil {
		return err
	}
	r := p.room
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sync(p)
	for _, o := range r.peers {
		for _, v := range o.video {
			o.pli(v)
		}
	}
	return nil
}

// Candidate takes one of the page's ICE candidates.
func (p *Peer) Candidate(c webrtc.ICECandidateInit) error { return p.pc.AddICECandidate(c) }

// CandidateJSON takes a candidate as the page sent it.
func (p *Peer) CandidateJSON(raw []byte) error {
	var c webrtc.ICECandidateInit
	if err := json.Unmarshal(raw, &c); err != nil {
		return err
	}
	return p.pc.AddICECandidate(c)
}

// Mute holds back (or lets through) user's microphone in room id.
func (s *SFU) Mute(id int64, user string, on bool) {
	if r := s.Room(id); r != nil {
		r.Mute(user, on)
	}
}

// End closes room id for everyone in it.
func (s *SFU) End(id int64) {
	if r := s.Room(id); r != nil {
		r.Close()
	}
}

// Leave takes p out of its room; the others stop receiving its streams.
func (p *Peer) Leave() { p.close(true) }

func (p *Peer) close(resignal bool) {
	p.once.Do(func() {
		r := p.room
		r.mu.Lock()
		if r.peers[p.User] == p {
			delete(r.peers, p.User)
			for _, k := range Kinds {
				delete(r.tracks, p.User+"|"+k)
			}
		}
		empty := len(r.peers) == 0
		r.mu.Unlock()
		_ = p.pc.Close()
		if empty {
			r.sfu.mu.Lock()
			if r.sfu.rooms[r.id] == r {
				delete(r.sfu.rooms, r.id)
			}
			r.sfu.mu.Unlock()
		} else if resignal {
			r.signal()
		}
	})
}

// Mute holds back (or lets through again) user's microphone in this room.
func (r *Room) Mute(user string, on bool) {
	r.mu.Lock()
	m := r.muted[user]
	if m == nil {
		m = &atomic.Bool{}
		r.muted[user] = m
	}
	r.mu.Unlock()
	m.Store(on)
}

// Close ends the room for everyone in it.
func (r *Room) Close() {
	r.mu.Lock()
	peers := make([]*Peer, 0, len(r.peers))
	for _, p := range r.peers {
		peers = append(peers, p)
	}
	r.mu.Unlock()
	for _, p := range peers {
		p.close(false)
	}
	r.sfu.mu.Lock()
	if r.sfu.rooms[r.id] == r {
		delete(r.sfu.rooms, r.id)
	}
	r.sfu.mu.Unlock()
}

// Room is call id's room, or nil.
func (s *SFU) Room(id int64) *Room {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rooms[id]
}
