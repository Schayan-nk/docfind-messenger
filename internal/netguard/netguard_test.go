package netguard

import (
	"context"
	"errors"
	"net/netip"
	"testing"
)

type fakeDNS map[string][]string

func (f fakeDNS) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	var out []netip.Addr
	for _, s := range f[host] {
		out = append(out, netip.MustParseAddr(s))
	}
	if len(out) == 0 {
		return nil, errors.New("no such host")
	}
	return out, nil
}

func TestEveryDisguiseOfAnInternalAddressIsRefused(t *testing.T) {
	old := Resolver
	Resolver = fakeDNS{
		"mail.example.com":   {"203.0.113.10"},
		"rebind.example.com": {"10.0.0.5"},
		"split.example.com":  {"203.0.113.10", "192.168.77.13"},
		"localhost":          {"127.0.0.1", "::1"},
		"metadata.internal":  {"169.254.169.254"},
	}
	defer func() { Resolver = old }()
	ctx := context.Background()

	for _, c := range []struct {
		host   string
		policy Policy
		ok     bool
	}{
		{"mail.example.com", Public, true},
		{"203.0.113.10", Public, true},
		{"rebind.example.com", Public, false},
		{"split.example.com", Public, false}, // one private answer is enough
		{"192.168.77.4", Public, false},
		{"192.168.77.4", LAN, true},
		{"localhost", LAN, false},
		{"127.0.0.1", LAN, false},
		{"::1", LAN, false},
		{"::ffff:127.0.0.1", LAN, false}, // an IPv4-mapped loopback
		{"127.0.0.1", Loopback, true},
		{"metadata.internal", Loopback, false},
		{"169.254.169.254", Loopback, false},
		{"0.0.0.0", Loopback, false},
		{"100.64.1.1", Public, false},
		{"nowhere.example.com", LAN, false},
		{"", LAN, false},
	} {
		err := CheckHost(ctx, c.host, c.policy)
		if (err == nil) != c.ok {
			t.Errorf("%q under policy %d: err = %v, want ok=%v", c.host, c.policy, err, c.ok)
		}
		if err != nil && !errors.Is(err, ErrRefused) {
			t.Errorf("%q: a refusal that is not ErrRefused: %v", c.host, err)
		}
	}
	// A name that does not resolve says so, so a proxied caller can tell it
	// from a name that resolves somewhere forbidden.
	if err := CheckHost(ctx, "nowhere.example.com", LAN); !errors.Is(err, ErrUnresolved) {
		t.Errorf("an unresolvable name: err = %v, want ErrUnresolved", err)
	}
	if err := CheckHost(ctx, "localhost", LAN); errors.Is(err, ErrUnresolved) {
		t.Errorf("a resolvable forbidden name was reported as unresolved: %v", err)
	}
	// An explicitly allowed host is accepted whatever it resolves to.
	if err := CheckHost(ctx, "rebind.example.com", Public, "REBIND.example.com"); err != nil {
		t.Errorf("an allowed host was refused: %v", err)
	}
}

// The dialler hook sees the address after DNS, which is what defeats a name
// that resolves differently when checked and when used.
func TestTheDialHookChecksTheRealAddress(t *testing.T) {
	ctl := Control(Public)
	if err := ctl("tcp", "10.1.2.3:995", nil); err == nil {
		t.Error("a private address passed the dial hook")
	}
	if err := ctl("tcp", "203.0.113.10:995", nil); err != nil {
		t.Errorf("a public address was refused: %v", err)
	}
	if err := ctl("tcp", "[::1]:995", nil); err == nil {
		t.Error("IPv6 loopback passed the dial hook")
	}
}
