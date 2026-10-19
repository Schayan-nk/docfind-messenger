package calls

import (
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

// The media server with real WebRTC clients on loopback: what one sends,
// the others get, as "<user>|<kind>"; a latecomer too; an admin's mute holds
// the voice back; leaving takes the stream away.

type tclient struct {
	user   string
	pc     *webrtc.PeerConnection
	peer   *Peer
	mic    *webrtc.TrackLocalStaticSample
	mu     sync.Mutex
	got    map[string]*atomic.Int64 // stream id -> packets received
	offers atomic.Int64
	ready  chan struct{}
}

func loopAPI() *webrtc.API {
	se := webrtc.SettingEngine{}
	se.SetIncludeLoopbackCandidate(true)
	se.SetIPFilter(func(ip net.IP) bool { return ip.IsLoopback() })
	se.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4})
	m := &webrtc.MediaEngine{}
	_ = m.RegisterDefaultCodecs()
	return webrtc.NewAPI(webrtc.WithMediaEngine(m), webrtc.WithSettingEngine(se))
}

func join(t *testing.T, s *SFU, room int64, user string) *tclient {
	t.Helper()
	pc, err := loopAPI().NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	c := &tclient{user: user, pc: pc, got: map[string]*atomic.Int64{}, ready: make(chan struct{})}
	c.mic, _ = webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2}, "mic", "local")
	pc.OnTrack(func(tr *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		n := &atomic.Int64{}
		c.mu.Lock()
		c.got[tr.StreamID()] = n
		c.mu.Unlock()
		buf := make([]byte, 1500)
		for {
			if _, _, err := tr.Read(buf); err != nil {
				return
			}
			n.Add(1)
		}
	})
	var once sync.Once
	signals := make(chan map[string]any, 64)
	go func() {
		first := true
		for d := range signals {
			if sdp, ok := d["sdp"].(map[string]string); ok {
				c.offers.Add(1)
				if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: sdp["sdp"]}); err != nil {
					continue
				}
				if first {
					first = false
					_, _ = pc.AddTrack(c.mic)
				}
				ans, err := pc.CreateAnswer(nil)
				if err != nil {
					continue
				}
				_ = pc.SetLocalDescription(ans)
				for c.peer == nil {
					time.Sleep(10 * time.Millisecond)
				}
				_ = c.peer.Answer(ans.SDP)
				once.Do(func() { close(c.ready) })
			}
			if cand, ok := d["cand"].(webrtc.ICECandidateInit); ok {
				_ = pc.AddICECandidate(cand)
			}
		}
	}()
	pc.OnICECandidate(func(x *webrtc.ICECandidate) {
		if x != nil {
			for c.peer == nil {
				time.Sleep(10 * time.Millisecond)
			}
			_ = c.peer.Candidate(x.ToJSON())
		}
	})
	p, err := s.Join(room, user, func(d map[string]any) { signals <- d }, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.peer = p
	t.Cleanup(func() { pc.Close() })
	select {
	case <-c.ready:
	case <-time.After(10 * time.Second):
		t.Fatalf("%s never got an offer", user)
	}
	go func() { // speak: 20 ms of "audio" every 20 ms
		for {
			if err := c.mic.WriteSample(media.Sample{Data: []byte{0xfc, 0xff, 0xfe}, Duration: 20 * time.Millisecond}); err != nil {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	return c
}

// count is how many packets c has had of stream id.
func (c *tclient) count(id string) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if n := c.got[id]; n != nil {
		return n.Load()
	}
	return 0
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(15 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("never: %s", what)
}

func TestTheMediaServerForwardsToEveryoneElse(t *testing.T) {
	s, err := StartSFU(SFUConfig{Listen: "127.0.0.1", PublicIP: net.IPv4(127, 0, 0, 1), Loopback: true}, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	a := join(t, s, 7, "alice")
	b := join(t, s, 7, "bob")
	eventually(t, "bob hears alice", func() bool { return b.count("alice|mic") > 5 })
	eventually(t, "alice hears bob", func() bool { return a.count("bob|mic") > 5 })
	if a.count("alice|mic") != 0 {
		t.Error("alice was sent her own voice")
	}

	// A latecomer gets both voices already going.
	c := join(t, s, 7, "carol")
	eventually(t, "carol hears alice and bob", func() bool { return c.count("alice|mic") > 5 && c.count("bob|mic") > 5 })

	// An admin mutes alice: her voice stops reaching anyone.
	s.Room(7).Mute("alice", true)
	time.Sleep(300 * time.Millisecond)
	before := b.count("alice|mic")
	time.Sleep(600 * time.Millisecond)
	if after := b.count("alice|mic"); after-before > 2 {
		t.Errorf("a muted voice still went through: %d packets", after-before)
	}
	s.Room(7).Mute("alice", false)
	eventually(t, "alice is heard again", func() bool { return b.count("alice|mic") > before+5 })

	// Another room hears nothing of this one.
	d := join(t, s, 8, "dave")
	time.Sleep(500 * time.Millisecond)
	if d.count("alice|mic")+d.count("bob|mic") != 0 {
		t.Error("a voice crossed into another room")
	}

	// Alice leaves: her stream is gone from the others' connection.
	a.peer.Leave()
	eventually(t, "alice's stream is taken away", func() bool {
		for _, snd := range b.peer.pc.GetSenders() {
			if snd.Track() != nil && snd.Track().StreamID() == "alice|mic" {
				return false
			}
		}
		return true
	})
}

func TestASecondJoinReplacesTheFirst(t *testing.T) {
	s, err := StartSFU(SFUConfig{Listen: "127.0.0.1", PublicIP: net.IPv4(127, 0, 0, 1), Loopback: true}, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	first := join(t, s, 9, "alice")
	b := join(t, s, 9, "bob")
	second := join(t, s, 9, "alice") // the same person on a phone
	eventually(t, "the first connection is closed", func() bool {
		return first.peer.pc.ConnectionState() == webrtc.PeerConnectionStateClosed
	})
	eventually(t, "bob hears alice's second device", func() bool { return b.count("alice|mic") > 5 })
	_ = second
	s.Room(9).mu.Lock()
	n := len(s.Room(9).peers)
	s.Room(9).mu.Unlock()
	if n != 2 {
		t.Errorf("%d participants in the room, want 2 (one tile per person)", n)
	}
}

// Once everyone has everyone's streams the offers stop: offering every
// participant on every change bounced offers between them for ever, and
// each new offer restarted the pictures on the pages.
func TestASettledCallStopsOffering(t *testing.T) {
	s, err := StartSFU(SFUConfig{Listen: "127.0.0.1", PublicIP: net.IPv4(127, 0, 0, 1), Loopback: true}, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	a := join(t, s, 11, "alice")
	b := join(t, s, 11, "bob")
	c := join(t, s, 11, "carol")
	eventually(t, "everyone hears everyone", func() bool {
		return a.count("bob|mic") > 5 && a.count("carol|mic") > 5 && b.count("alice|mic") > 5 && c.count("bob|mic") > 5
	})
	time.Sleep(500 * time.Millisecond)
	before := a.offers.Load() + b.offers.Load() + c.offers.Load()
	time.Sleep(1500 * time.Millisecond)
	if n := a.offers.Load() + b.offers.Load() + c.offers.Load() - before; n != 0 {
		t.Errorf("%d more offers in a call where nothing changed", n)
	}
	if n := a.offers.Load(); n > 4 {
		t.Errorf("alice had %d offers for two people joining", n)
	}

	// Nobody's own three carry somebody else: a page only sends on those.
	for _, x := range []*tclient{a, b, c} {
		for i, tr := range x.peer.pc.GetTransceivers()[:len(Kinds)] {
			if tr.Sender() != nil {
				t.Errorf("%s's own stream %d carries %s", x.user, i, tr.Sender().Track().StreamID())
			}
		}
	}

	// Someone leaving frees their place in the others' connections for the
	// next one in, so a long call's offers do not keep growing.
	n := len(b.peer.pc.GetTransceivers())
	a.peer.Leave()
	d := join(t, s, 11, "dave")
	eventually(t, "bob hears dave", func() bool { return b.count("dave|mic") > 5 })
	if m := len(b.peer.pc.GetTransceivers()); m != n {
		t.Errorf("bob's connection went from %d streams to %d for one out and one in", n, m)
	}
	_ = d
}
