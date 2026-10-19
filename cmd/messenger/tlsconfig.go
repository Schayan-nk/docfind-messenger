package main

import (
	"crypto/tls"
	"fmt"
	"os"
	"strings"
)

// serverTLS returns the HTTPS configuration when docfind is to terminate TLS
// itself, or nil to serve plain HTTP as before.
//
// OPTIONAL. The usual arrangement is a reverse proxy in front doing TLS, and
// that keeps working unchanged. These two settings are for a deployment with
// nothing in front, where the alternative is passwords and session tokens
// crossing the network in the clear:
//
//	DOCFIND_TLS_CERT  path to the certificate (PEM, full chain)
//	DOCFIND_TLS_KEY   path to its private key (PEM)
//
// Both or neither: one without the other is a mistake, and it is reported
// rather than quietly serving HTTP. A certificate that will not load stops
// start-up for the same reason -- a server that someone believes is on HTTPS
// must not come up on HTTP instead.
//
// TLS 1.2 is the floor; Go's defaults choose the cipher suites, which is the
// right call -- a hand-written list is what goes stale.
func serverTLS() (*tls.Config, error) {
	cert := strings.TrimSpace(os.Getenv("DOCFIND_TLS_CERT"))
	key := strings.TrimSpace(os.Getenv("DOCFIND_TLS_KEY"))
	switch {
	case cert == "" && key == "":
		return nil, nil
	case cert == "" || key == "":
		return nil, fmt.Errorf("HTTPS needs both DOCFIND_TLS_CERT and DOCFIND_TLS_KEY; only one is set")
	}
	pair, err := tls.LoadX509KeyPair(cert, key)
	if err != nil {
		return nil, fmt.Errorf("loading the HTTPS certificate: %w", err)
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{pair},
	}, nil
}
