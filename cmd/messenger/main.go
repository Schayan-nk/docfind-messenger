// Command messenger is docfind's messenger on its own: one-to-one and group
// chats, files, voice messages, secret chats, and voice and video calls
// (one-to-one through the browsers or a TURN relay, groups through the
// built-in media server), with accounts, two-step sign-in, passkeys and
// notifications on people's own devices.
//
// Everything is kept in PostgreSQL (DOCFIND_PG_*); the accounts file given
// with -users holds the sign-in state beside it. Run with -add-user first to
// create the first administrator.
package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Schayan-nk/docfind-messenger/internal/auth"
	"github.com/Schayan-nk/docfind-messenger/internal/find"
)

// isLoopbackHost reports whether an address only this machine can reach.
func isLoopbackHost(host string) bool {
	switch strings.ToLower(host) {
	case "127.0.0.1", "::1", "localhost":
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

var version = "dev"

const defaultPort = 8300

// startPushWorkers starts the notification sender; startNotifications sets
// it when notifications are configured, and it does nothing otherwise.
var startPushWorkers = func() {}

func main() {
	if err := run(); err != nil {
		logf("error: %s", err.Error())
		os.Exit(1)
	}
}

// run is the messenger from start to finish: a one-shot command, or the
// server -- the accounts, the messenger, notifications -- until a signal ends
// it.
func run() error {
	o := parseOptions()
	o.loadEnv()
	if handled, err := o.runOneShot(); handled {
		return err
	}
	find.SetVersion(version)
	startAppLog(o.usersFile)
	defer stopAppLog()

	if o.usersFile == "" {
		return fmt.Errorf("the messenger needs accounts: pass -users <path to the accounts file> (and create the first one with -add-user)")
	}
	token, err := accessToken(true)
	if err != nil {
		return err
	}
	acc, err := openAccounts(o.usersFile)
	if err != nil {
		return err
	}
	if acc.pgDB != nil {
		defer acc.pgDB.Close()
	}
	ln, url, err := listen(o, acc.users, token)
	if err != nil {
		return err
	}

	app := find.NewServer(token)
	app.UsePermissions(auth.NewCatalogue())
	var closers cleanups
	defer func() { closers.run() }()
	closers.add(useAccounts(app, acc, o))

	srv, err := newHTTPServer(app)
	if err != nil {
		return err
	}
	// The notification sender, once everything it reads is wired.
	startPushWorkers()
	go serve(srv, ln)
	logf("ready: %s", url)

	waitForSignal()
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(shutdown)
}

// waitForSignal blocks until the program is interrupted.
func waitForSignal() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	<-ctx.Done()
}
