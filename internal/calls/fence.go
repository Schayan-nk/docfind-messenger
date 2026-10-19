package calls

import (
	"net"
	"strings"
	"sync"

	"github.com/pion/turn/v4"
)

// permitted decides whether a relay may talk to peer at all: never to
// loopback, link-local (the cloud metadata address 169.254.169.254 among
// them), multicast, broadcast or the unspecified address.
//
// NOTE: this deliberately does NOT block RFC1918/private addresses, because on
// the office network a legitimate relay peer IS a colleague's browser at a
// 192.168.77.x address. Once this TURN server is reachable from the internet
// that same leniency lets an authenticated client relay into internal hosts
// (the databases on .1/.4/.13) -- a pivot that must be closed at the network
// layer by isolating the TURN host so its relay egress cannot reach those
// servers, or by serving TURN on the public interface only so private peers
// are never legitimate. See the security review's F4.
func permitted(_ net.Addr, peer net.IP) bool {
	if peer == nil || peer.IsLoopback() || peer.IsUnspecified() || peer.IsMulticast() ||
		peer.IsLinkLocalUnicast() || peer.IsLinkLocalMulticast() || peer.IsInterfaceLocalMulticast() {
		return false
	}
	if v4 := peer.To4(); v4 != nil && (v4[0] == 0 || v4.Equal(net.IPv4bcast)) {
		return false
	}
	return true
}

// portSet is the relay ports live right now.
type portSet struct {
	mu sync.Mutex
	m  map[int]bool
}

func (p *portSet) set(port int, on bool) {
	p.mu.Lock()
	if on {
		p.m[port] = true
	} else {
		delete(p.m, port)
	}
	p.mu.Unlock()
}

func (p *portSet) has(port int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.m[port]
}

// guardedRelay makes relays whose sends to the server's own addresses are
// dropped unless they go to another live relay: two relayed browsers reach
// each other that way, and nothing else on this machine is reachable (not
// even something else that happens to use a port in the relay range).
type guardedRelay struct {
	turn.RelayAddressGeneratorPortRange
	own, bcast map[string]bool
	ports      *portSet
}

func (g *guardedRelay) AllocatePacketConn(network string, port int) (net.PacketConn, net.Addr, error) {
	c, a, err := g.RelayAddressGeneratorPortRange.AllocatePacketConn(network, port)
	if err != nil {
		return nil, nil, err
	}
	p := 0
	if u, ok := a.(*net.UDPAddr); ok {
		p = u.Port
		g.ports.set(p, true)
	}
	return &guardedConn{PacketConn: c, own: g.own, bcast: g.bcast, ports: g.ports, port: p}, a, nil
}

type guardedConn struct {
	net.PacketConn
	own, bcast map[string]bool
	ports      *portSet
	port       int
	once       sync.Once
}

func (c *guardedConn) Close() error {
	c.once.Do(func() {
		if c.port != 0 {
			c.ports.set(c.port, false)
		}
	})
	return c.PacketConn.Close()
}

func (c *guardedConn) WriteTo(p []byte, to net.Addr) (int, error) {
	if !c.allowed(to) {
		return len(p), nil // dropped, as UDP would lose it
	}
	return c.PacketConn.WriteTo(p, to)
}

func (c *guardedConn) allowed(to net.Addr) bool {
	u, ok := to.(*net.UDPAddr)
	if !ok {
		return false
	}
	if !permitted(nil, u.IP) || c.bcast[u.IP.String()] {
		return false
	}
	if c.own[u.IP.String()] {
		return u.Port != c.port && c.ports.has(u.Port)
	}
	return true
}

// ownAddresses is every address of this machine, and the broadcast address
// of each IPv4 network it is on (a relay there would reach every host).
func ownAddresses() (own, bcast map[string]bool) {
	own, bcast = map[string]bool{}, map[string]bool{}
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		own[n.IP.String()] = true
		if v4 := n.IP.To4(); v4 != nil {
			own[v4.String()] = true
			if m := n.Mask; len(m) == net.IPv4len || len(m) == net.IPv6len {
				m = m[len(m)-4:]
				b := make(net.IP, 4)
				for i := range b {
					b[i] = v4[i] | ^m[i]
				}
				bcast[b.String()] = true
			}
		}
	}
	return own, bcast
}

// limitListener holds at most cap(slots) connections open at once; the
// next one waits for a slot.
type limitListener struct {
	net.Listener
	slots chan struct{}
}

func (l limitListener) Accept() (net.Conn, error) {
	l.slots <- struct{}{}
	c, err := l.Listener.Accept()
	if err != nil {
		<-l.slots
		return nil, err
	}
	return &slotConn{Conn: c, slots: l.slots}, nil
}

type slotConn struct {
	net.Conn
	slots chan struct{}
	once  sync.Once
}

func (c *slotConn) Close() error {
	c.once.Do(func() { <-c.slots })
	return c.Conn.Close()
}

// OfficeIPv4 is this machine's first private IPv4 address (where browsers on
// the office network reach it), else its first non-loopback one.
func OfficeIPv4() net.IP { return privateIPv4() }

// privateIPv4 is this machine's first private IPv4 address, else its first
// non-loopback one.
func privateIPv4() net.IP {
	addrs, _ := net.InterfaceAddrs()
	var any net.IP
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		v4 := n.IP.To4()
		if v4 == nil || v4.IsLoopback() || v4.IsLinkLocalUnicast() {
			continue
		}
		if v4.IsPrivate() {
			return v4
		}
		if any == nil {
			any = v4
		}
	}
	return any
}

// logWriter turns the server's log lines into docfind's.
type logWriter func(string, ...any)

func (w logWriter) Write(p []byte) (int, error) {
	if w != nil {
		w("turn: %s", strings.TrimSpace(string(p)))
	}
	return len(p), nil
}
