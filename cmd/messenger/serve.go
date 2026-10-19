package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Schayan-nk/docfind-messenger/internal/auth"
	"github.com/Schayan-nk/docfind-messenger/internal/find"
)

// accessToken is the token the address carries, or none with -no-token.
func accessToken(noToken bool) (string, error) {
	if noToken {
		return "", nil
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// listen opens the port, and the address to open the window at.
//
// Until accounts existed this program was safe because it listened on
// loopback only: the access token defaults to off, so binding anywhere else
// without accounts would publish the whole archive to anyone who could reach
// the port. These two settings have to move together, so the bind refuses
// rather than trusting whoever runs it to remember.
func listen(o options, users *auth.Store, token string) (net.Listener, string, error) {
	listenOn := strings.TrimSpace(o.bind)
	if listenOn == "" {
		listenOn = "127.0.0.1"
	}
	if !isLoopbackHost(listenOn) {
		if users == nil {
			return nil, "", fmt.Errorf("refusing to listen on %s without accounts: pass -users to enable sign-in, "+
				"or leave -bind at 127.0.0.1", listenOn)
		}
		if users.Count() == 0 {
			return nil, "", fmt.Errorf("refusing to listen on %s while %s holds no accounts: start once on 127.0.0.1 "+
				"and create the first administrator, then reopen it to the network", listenOn, o.usersFile)
		}
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(listenOn, strconv.Itoa(o.port)))
	if err != nil {
		return nil, "", fmt.Errorf("listening: %w", err)
	}
	host := listenOn
	if isLoopbackHost(listenOn) {
		host = "localhost"
	}
	url := fmt.Sprintf("http://%s:%d/", host, ln.Addr().(*net.TCPAddr).Port)
	if token != "" {
		url += "?t=" + token
	}
	return ln, url, nil
}

// newHTTPServer is the server, with timeouts that bound what one slow or
// hostile client can hold.
func newHTTPServer(app *find.Server) (*http.Server, error) {
	tlsCfg, err := serverTLS()
	if err != nil {
		return nil, err
	}
	if tlsCfg != nil {
		logf("serving HTTPS with the certificate in DOCFIND_TLS_CERT")
	}
	return &http.Server{
		Handler:      app.Handler(),
		TLSConfig:    tlsCfg,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 5 * time.Minute, // a large download must not be cut off
		// The headers have to arrive promptly even though a body may be slow:
		// without this a client can hold a connection open indefinitely by
		// sending one header line at a time.
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    64 << 10,
	}, nil
}

// serve answers requests on ln until the server is shut down.
func serve(srv *http.Server, ln net.Listener) {
	run := srv.Serve
	if srv.TLSConfig != nil {
		// The certificate is already in TLSConfig, so no file names here.
		run = func(l net.Listener) error { return srv.ServeTLS(l, "", "") }
	}
	if err := run(ln); err != nil && err != http.ErrServerClosed {
		logf("server error: %v", err)
	}
}
