package main

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"

	"github.com/Schayan-nk/docfind-messenger/internal/webauthn"
)

// passkeyConfig reads which host passkeys are bound to.
//
// DOCFIND_PASSKEY_RPID is the host name people reach docfind by, e.g.
// chat.example.com. A passkey is bound to it for good -- change it and
// every registered passkey stops working -- and browsers refuse an IP address
// or plain http outright, so nothing is switched on until a real name is set.
//
// DOCFIND_PASSKEY_ORIGINS lists the exact origins pages are served from,
// comma-separated, when that is not simply https://<host> (a port, say:
// https://chat.example.com:8443). Each must be https and on that host.
//
// Unset returns nil and no error: passkeys are off, which is not a fault.
func passkeyConfig() (*webauthn.Relying, error) {
	id := strings.ToLower(strings.TrimSpace(os.Getenv("DOCFIND_PASSKEY_RPID")))
	if id == "" {
		return nil, nil
	}
	if net.ParseIP(strings.Trim(id, "[]")) != nil {
		return nil, fmt.Errorf("DOCFIND_PASSKEY_RPID is an IP address (%s); browsers bind passkeys to a host name only", id)
	}
	if strings.ContainsAny(id, "/:@ ") || !strings.Contains(id, ".") {
		return nil, fmt.Errorf("DOCFIND_PASSKEY_RPID %q is not a host name", id)
	}
	var origins []string
	for _, o := range strings.Split(os.Getenv("DOCFIND_PASSKEY_ORIGINS"), ",") {
		if o = strings.TrimRight(strings.TrimSpace(o), "/"); o != "" {
			origins = append(origins, o)
		}
	}
	if len(origins) == 0 {
		origins = []string{"https://" + id}
	}
	for _, o := range origins {
		u, err := url.Parse(o)
		if err != nil || u.Scheme != "https" || u.Path != "" {
			return nil, fmt.Errorf("DOCFIND_PASSKEY_ORIGINS: %q is not an https origin", o)
		}
		h := strings.ToLower(u.Hostname())
		if h != id && !strings.HasSuffix(h, "."+id) {
			return nil, fmt.Errorf("DOCFIND_PASSKEY_ORIGINS: %q is not on %s", o, id)
		}
	}
	return &webauthn.Relying{ID: id, Origins: origins}, nil
}
