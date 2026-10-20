package calls

import (
	"net"
	"strings"
	"testing"
	"time"
)

const testSecret = "a test secret of some length"

type devNull struct{}

func (devNull) Write(p []byte) (int, error) { return len(p), nil }

// The server's own addresses are reachable only at another live relay;
// a network's broadcast address never.
func TestTheRelayCannotReachTheServerItself(t *testing.T) {
	ports := &portSet{m: map[int]bool{49170: true, 49200: true, 3479: true}}
	c := &guardedConn{own: map[string]bool{"192.168.77.13": true}, bcast: map[string]bool{"192.168.77.255": true}, ports: ports, port: 49170}
	for addr, want := range map[string]bool{
		"192.168.77.13:8300":  false, // docfind
		"192.168.77.13:445":   false, // file sharing
		"192.168.77.13:49200": true,  // another live relay
		"192.168.77.13:49201": false, // in the range, but nobody's relay
		"192.168.77.13:49170": false, // itself
		"192.168.77.255:137":  false, // every host on the office network
		"192.168.77.13:3479":  true,  // the group calls' media server (OwnPorts)
		"192.168.77.20:8300":  true,  // a colleague's browser may use any port
	} {
		a, _ := net.ResolveUDPAddr("udp4", addr)
		if got := c.allowed(a); got != want {
			t.Errorf("%s: allowed %v, want %v", addr, got, want)
		}
	}
}

// A TURN server open to the internet (DOCFIND_TURN_PEERS=server) relays to
// its own live relays and the group calls' media server, and to nothing else:
// no colleague's machine, no other office server, no host outside.
func TestARelayOpenToTheInternetReachesOnlyTheServer(t *testing.T) {
	own, bcast := map[string]bool{"192.168.77.13": true}, map[string]bool{"192.168.77.255": true}
	ports := &portSet{m: map[int]bool{49170: true, 49200: true, 3481: true}}
	c := &guardedConn{own: own, bcast: bcast, ports: ports, port: 49170, serverOnly: true}
	for addr, want := range map[string]bool{
		"192.168.77.13:49200": true,  // another live relay: two relayed browsers
		"192.168.77.13:3481":  true,  // the group calls' media server (OwnPorts)
		"192.168.77.13:8301":  false, // the messenger itself
		"192.168.77.13:49170": false, // itself
		"192.168.77.20:8300":  false, // a colleague's machine
		"192.168.77.1:1433":   false, // another office server
		"10.0.0.5:53":         false,
		"8.8.8.8:3478":        false, // a host outside
		"192.168.77.255:137":  false,
	} {
		a, _ := net.ResolveUDPAddr("udp4", addr)
		if got := c.allowed(a); got != want {
			t.Errorf("%s: allowed %v, want %v", addr, got, want)
		}
	}
	for _, mode := range []bool{true, false} {
		permit := peerPermit(own, bcast, mode)
		for ip, want := range map[string]bool{
			"192.168.77.13":   true,
			"192.168.77.20":   !mode,
			"8.8.8.8":         !mode,
			"127.0.0.1":       false,
			"192.168.77.255":  false,
			"169.254.169.254": false,
		} {
			if got := permit(nil, net.ParseIP(ip)); got != want {
				t.Errorf("server only %v, permission for %s: %v, want %v", mode, ip, got, want)
			}
		}
	}
}

func TestPagesAreToldWhereTheServerIs(t *testing.T) {
	s := &Server{cfg: Config{Secret: testSecret, Port: 3478}, now: time.Now}
	ice := s.ICE("h.jafari", 4, "chat.example.ir")
	if len(ice) != 2 || ice[0].URLs[0] != "stun:chat.example.ir:3478" ||
		strings.Join(ice[1].URLs, " ") != "turn:chat.example.ir:3478?transport=udp turn:chat.example.ir:3478?transport=tcp" {
		t.Fatalf("ICE servers: %+v", ice)
	}
	if exp, user, session, ok := parseName(ice[1].Username); !ok || user != "h.jafari" || session != 4 || exp == 0 || ice[1].Credential != sign(testSecret, ice[1].Username) {
		t.Errorf("credential: %+v", ice[1])
	}
	s.cfg.Host, s.cfg.Cert, s.cfg.Key, s.cfg.TLSPort = "turn.example.ir", "c.pem", "k.pem", 5349
	ice = s.ICE("h.jafari", 4, "chat.example.ir:443")
	if ice[0].URLs[0] != "stun:turn.example.ir:3478" || ice[1].URLs[2] != "turns:turn.example.ir:5349?transport=tcp" {
		t.Errorf("a configured host and TLS: %+v", ice)
	}
	s.cfg.Host = ""
	if ice = s.ICE("h.jafari", 4, "192.168.77.13:8300"); ice[0].URLs[0] != "stun:192.168.77.13:3478" {
		t.Errorf("a page host with a port: %+v", ice[0])
	}
}

func TestTheSettingsAreChecked(t *testing.T) {
	t.Setenv("DOCFIND_TURN_SECRET", "")
	if _, ok, err := FromEnv(); ok || err != nil {
		t.Errorf("no secret: ok %v err %v", ok, err)
	}
	t.Setenv("DOCFIND_TURN_SECRET", testSecret)
	if c, ok, err := FromEnv(); !ok || err != nil || c.Port != 3478 || c.MinPort != 49160 || c.MaxPort != 49359 || c.ServerOnly {
		t.Errorf("defaults: %+v %v %v", c, ok, err)
	}
	t.Setenv("DOCFIND_TURN_PEERS", " Server ")
	if c, ok, err := FromEnv(); !ok || err != nil || !c.ServerOnly {
		t.Errorf("DOCFIND_TURN_PEERS=server: %+v %v %v", c, ok, err)
	}
	t.Setenv("DOCFIND_TURN_PEERS", "")
	for k, v := range map[string]string{
		"DOCFIND_TURN_PORT":        "70000",
		"DOCFIND_TURN_RELAY_PORTS": "500-600",
		"DOCFIND_TURN_RELAY_IP":    "not an address",
		"DOCFIND_TURN_TLS_PORT":    "x",
		"DOCFIND_TURN_PEERS":       "office",
	} {
		t.Run(k, func(t *testing.T) {
			t.Setenv(k, v)
			if _, _, err := FromEnv(); err == nil {
				t.Errorf("%s=%s was accepted", k, v)
			}
		})
	}
	t.Setenv("DOCFIND_TURN_SECRET", "short")
	if _, _, err := FromEnv(); err == nil {
		t.Error("a short secret was accepted")
	}
}
