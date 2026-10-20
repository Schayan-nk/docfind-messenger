// Package calls is the messenger's own STUN and TURN server (docs/chat/SPEC.md,
// phase 0 item 2, and 6.8): two browsers that cannot reach each other
// directly relay their call through it. It runs inside docfind (pion/turn)
// when DOCFIND_TURN_SECRET is set; nothing foreign is ever used (rule 4).
//
// Credentials follow the TURN REST API: the user name is
// "expiry:username:session" (session: the account's TokenVersion) and the
// password its HMAC-SHA1 under the shared secret, so docfind hands a page a
// pair that works for CredentialTTL and needs no table. A credential is
// refused once it has expired, and also as soon as its account is disabled
// or deleted or its sessions are ended (security check 9).
//
// A relay is a way into the network the server sits in, so it is fenced: no
// peer on loopback, link-local (169.254.169.254 included), multicast, a
// broadcast address (the local networks' own included) or the unspecified
// address, and nothing sent to the server's own addresses except a relay
// that is live right now (two relayed browsers talking to each other).
package calls

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pion/logging"
	"github.com/pion/turn/v4"
)

// CredentialTTL is how long a credential handed to a page stays valid. A
// relay is refreshed with the same credential, so a call longer than this
// loses its relay at the next refresh.
const CredentialTTL = 8 * time.Hour

// Realm is the TURN realm.
const Realm = "docfind"

// perUser caps the relays one person may hold at once: two devices, each
// with a UDP and a TCP relay, and an ICE restart or two.
const perUser = 16

// maxTCP caps the TCP and TLS connections held open at once, signed in or not.
const maxTCP = 512

// Config is the TURN server's settings.
type Config struct {
	Secret  string // DOCFIND_TURN_SECRET: the key credentials are signed with
	Host    string // DOCFIND_TURN_HOST: the name pages reach it by; "" uses the page's own host
	Listen  string // the address to listen on (all of this machine's by default)
	Port    int    // DOCFIND_TURN_PORT: UDP and TCP, 3478 by default
	RelayIP net.IP // DOCFIND_TURN_RELAY_IP: where relays are announced; this machine's first private IPv4 by default
	MinPort int    // DOCFIND_TURN_RELAY_PORTS "min-max": the UDP ports relays use, 49160-49359 by default
	MaxPort int
	TLSPort int    // DOCFIND_TURN_TLS_PORT, 5349 by default; used only with a certificate
	Cert    string // DOCFIND_TURN_TLS_CERT and DOCFIND_TURN_TLS_KEY: PEM files for turns:
	Key     string
	// OwnPorts are this machine's ports a relay may reach besides other
	// relays: the group calls' media server, which relayed browsers must
	// reach too.
	OwnPorts []int
	// ServerOnly (DOCFIND_TURN_PEERS=server) lets a relay reach nothing but
	// this server's own live relays and OwnPorts: for a TURN server open to
	// the internet (fence.go, the security review's F4). Two relayed browsers
	// still meet relay to relay, and the group calls' media server is still
	// reached, while no other host, on the office network or outside, is.
	ServerOnly bool
}

// FromEnv reads the settings; ok is false when DOCFIND_TURN_SECRET is unset.
func FromEnv() (cfg Config, ok bool, err error) {
	cfg = Config{Secret: os.Getenv("DOCFIND_TURN_SECRET"), Host: strings.TrimSpace(os.Getenv("DOCFIND_TURN_HOST")),
		Port: 3478, MinPort: 49160, MaxPort: 49359, TLSPort: 5349,
		Cert: os.Getenv("DOCFIND_TURN_TLS_CERT"), Key: os.Getenv("DOCFIND_TURN_TLS_KEY")}
	if cfg.Secret == "" {
		return cfg, false, nil
	}
	if len(cfg.Secret) < 16 {
		return cfg, false, errors.New("DOCFIND_TURN_SECRET is shorter than 16 characters")
	}
	if p := os.Getenv("DOCFIND_TURN_PORT"); p != "" {
		if cfg.Port, err = strconv.Atoi(p); err != nil || cfg.Port < 1 || cfg.Port > 65535 {
			return cfg, false, fmt.Errorf("DOCFIND_TURN_PORT %q is not a port", p)
		}
	}
	if p := os.Getenv("DOCFIND_TURN_TLS_PORT"); p != "" {
		if cfg.TLSPort, err = strconv.Atoi(p); err != nil || cfg.TLSPort < 1 || cfg.TLSPort > 65535 {
			return cfg, false, fmt.Errorf("DOCFIND_TURN_TLS_PORT %q is not a port", p)
		}
	}
	if r := os.Getenv("DOCFIND_TURN_RELAY_PORTS"); r != "" {
		lo, hi, _ := strings.Cut(r, "-")
		a, e1 := strconv.Atoi(strings.TrimSpace(lo))
		b, e2 := strconv.Atoi(strings.TrimSpace(hi))
		if e1 != nil || e2 != nil || a < 1024 || b > 65535 || b < a {
			return cfg, false, fmt.Errorf("DOCFIND_TURN_RELAY_PORTS %q is not a range like 49160-49359", r)
		}
		cfg.MinPort, cfg.MaxPort = a, b
	}
	if ip := os.Getenv("DOCFIND_TURN_RELAY_IP"); ip != "" {
		if cfg.RelayIP = net.ParseIP(ip).To4(); cfg.RelayIP == nil {
			return cfg, false, fmt.Errorf("DOCFIND_TURN_RELAY_IP %q is not an IPv4 address", ip)
		}
	}
	switch p := strings.ToLower(strings.TrimSpace(os.Getenv("DOCFIND_TURN_PEERS"))); p {
	case "", "any":
	case "server":
		cfg.ServerOnly = true
	default:
		return cfg, false, fmt.Errorf("DOCFIND_TURN_PEERS %q is neither any nor server", p)
	}
	return cfg, true, nil
}

// Credentials signs a user name and password for user's session (their
// TokenVersion), valid until now+ttl.
func Credentials(secret, user string, session int, now time.Time, ttl time.Duration) (string, string) {
	name := strconv.FormatInt(now.Add(ttl).Unix(), 10) + ":" + user + ":" + strconv.Itoa(session)
	return name, sign(secret, name)
}

// parseName splits a credential's user name; ok is false for any other shape.
func parseName(name string) (exp int64, user string, session int, ok bool) {
	e, rest, ok1 := strings.Cut(name, ":")
	i := strings.LastIndexByte(rest, ':')
	if !ok1 || i <= 0 {
		return 0, "", 0, false
	}
	exp, err1 := strconv.ParseInt(e, 10, 64)
	session, err2 := strconv.Atoi(rest[i+1:])
	return exp, rest[:i], session, err1 == nil && err2 == nil
}

func sign(secret, name string) string {
	m := hmac.New(sha1.New, []byte(secret))
	m.Write([]byte(name))
	return base64.StdEncoding.EncodeToString(m.Sum(nil))
}

// ICEServer is one entry of a page's RTCPeerConnection iceServers.
type ICEServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

// Server is the running STUN and TURN server.
type Server struct {
	cfg    Config
	srv    *turn.Server
	now    func() time.Time
	udp    net.Addr // where it listens for UDP
	mu     sync.Mutex
	allocs map[string]int // relays held, by account
	ports  *portSet       // the relay ports live now
}

// Start listens on UDP and TCP cfg.Port (and TLS cfg.TLSPort when a
// certificate is given). active says whether an account may still use a
// relay; logf receives the server's warnings.
func Start(cfg Config, active func(user string, session int) bool, logf func(string, ...any)) (*Server, error) {
	if cfg.RelayIP == nil {
		cfg.RelayIP = privateIPv4()
	}
	if cfg.RelayIP == nil {
		return nil, errors.New("no IPv4 address to announce relays at; set DOCFIND_TURN_RELAY_IP")
	}
	s := &Server{cfg: cfg, now: time.Now, allocs: map[string]int{}, ports: &portSet{m: map[int]bool{}}}
	own, bcast := ownAddresses()
	own[cfg.RelayIP.String()] = true
	for _, p := range cfg.OwnPorts {
		s.ports.set(p, true) // always open to relays, like a live relay
	}
	permit := peerPermit(own, bcast, cfg.ServerOnly)
	relay := func() turn.RelayAddressGenerator {
		return &guardedRelay{RelayAddressGeneratorPortRange: turn.RelayAddressGeneratorPortRange{
			RelayAddress: cfg.RelayIP, Address: "0.0.0.0", MinPort: uint16(cfg.MinPort), MaxPort: uint16(cfg.MaxPort),
		}, own: own, bcast: bcast, ports: s.ports, serverOnly: cfg.ServerOnly}
	}
	host := cfg.Listen
	if host == "" {
		host = "0.0.0.0"
	}
	addr := net.JoinHostPort(host, strconv.Itoa(cfg.Port))
	udp, err := net.ListenPacket("udp4", addr)
	if err != nil {
		return nil, fmt.Errorf("TURN cannot listen on UDP %s: %w", addr, err)
	}
	tcpL, err := net.Listen("tcp4", addr)
	if err != nil {
		udp.Close()
		return nil, fmt.Errorf("TURN cannot listen on TCP %s: %w", addr, err)
	}
	tcpSlots := make(chan struct{}, maxTCP)
	tcp := limitListener{tcpL, tcpSlots}
	s.udp = udp.LocalAddr()
	listeners := []turn.ListenerConfig{{Listener: tcp, RelayAddressGenerator: relay(), PermissionHandler: permit}}
	if cfg.Cert != "" && cfg.Key != "" {
		pair, err := tls.LoadX509KeyPair(cfg.Cert, cfg.Key)
		if err != nil {
			udp.Close()
			tcp.Close()
			return nil, fmt.Errorf("TURN certificate: %w", err)
		}
		tl, err := tls.Listen("tcp4", net.JoinHostPort(host, strconv.Itoa(cfg.TLSPort)), &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12})
		if err != nil {
			udp.Close()
			tcp.Close()
			return nil, fmt.Errorf("TURN cannot listen on TLS %d: %w", cfg.TLSPort, err)
		}
		listeners = append(listeners, turn.ListenerConfig{Listener: limitListener{tl, tcpSlots}, RelayAddressGenerator: relay(), PermissionHandler: permit})
	}
	lf := &logging.DefaultLoggerFactory{Writer: logWriter(logf), DefaultLogLevel: logging.LogLevelWarn, ScopeLevels: map[string]logging.LogLevel{}}
	s.srv, err = turn.NewServer(turn.ServerConfig{
		Realm:             Realm,
		LoggerFactory:     lf,
		AuthHandler:       s.auth(active),
		QuotaHandler:      s.quota,
		EventHandler:      turn.EventHandler{OnAllocationCreated: s.created, OnAllocationDeleted: s.deleted},
		PacketConnConfigs: []turn.PacketConnConfig{{PacketConn: udp, RelayAddressGenerator: relay(), PermissionHandler: permit}},
		ListenerConfigs:   listeners,
	})
	if err != nil {
		udp.Close()
		for _, l := range listeners {
			l.Listener.Close()
		}
		return nil, err
	}
	return s, nil
}

// Config is what the server runs with (the relay address resolved).
func (s *Server) Config() Config { return s.cfg }

// ICE is what a page of user's session, opened at pageHost, gives its
// RTCPeerConnection.
func (s *Server) ICE(user string, session int, pageHost string) []ICEServer {
	host := s.cfg.Host
	if host == "" {
		host = pageHost
		if h, _, err := net.SplitHostPort(pageHost); err == nil {
			host = h
		}
	}
	if host == "" {
		return nil
	}
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	name, pass := Credentials(s.cfg.Secret, user, session, s.now(), CredentialTTL)
	at := host + ":" + strconv.Itoa(s.cfg.Port)
	urls := []string{"turn:" + at + "?transport=udp", "turn:" + at + "?transport=tcp"}
	if s.cfg.Cert != "" && s.cfg.Key != "" {
		urls = append(urls, "turns:"+host+":"+strconv.Itoa(s.cfg.TLSPort)+"?transport=tcp")
	}
	return []ICEServer{{URLs: []string{"stun:" + at}}, {URLs: urls, Username: name, Credential: pass}}
}

// auth checks a credential: signed by the secret, not expired, and its
// account and session still active.
func (s *Server) auth(active func(string, int) bool) turn.AuthHandler {
	return func(name, realm string, _ net.Addr) ([]byte, bool) {
		t, user, session, ok := parseName(name)
		if !ok || user == "" || t < s.now().Unix() || (active != nil && !active(user, session)) {
			return nil, false
		}
		return turn.GenerateAuthKey(name, realm, sign(s.cfg.Secret, name)), true
	}
}

func account(name string) string { _, u, _, _ := parseName(name); return u }

func (s *Server) quota(name, _ string, _ net.Addr) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.allocs[account(name)] < perUser
}

func (s *Server) created(_, _ net.Addr, _, name, _ string, _ net.Addr, _ int) {
	s.mu.Lock()
	s.allocs[account(name)]++
	s.mu.Unlock()
}

func (s *Server) deleted(_, _ net.Addr, _, name, _ string) {
	s.mu.Lock()
	if u := account(name); s.allocs[u] > 1 {
		s.allocs[u]--
	} else {
		delete(s.allocs, u)
	}
	s.mu.Unlock()
}
