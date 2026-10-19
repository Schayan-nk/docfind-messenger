package main

import (
	"flag"
	"fmt"
	"path/filepath"

	"github.com/Schayan-nk/docfind-messenger/internal/config"
)

// options is the command line, parsed.
type options struct {
	envFile    string
	envWins    bool
	port       int
	bind       string
	usersFile  string
	trustProxy bool

	// The one-shot commands: each does one thing and exits.
	showVersion   bool
	addUser       string
	addRole       string
	setPhoneOf    string
	phoneIs       string
	setGrantOf    string
	grantPerm     string
	grantState    string
	checkChat     bool
	migrateDownTo int
	confirmLoss   bool
}

func parseOptions() options {
	var o options
	flag.StringVar(&o.envFile, "env-file", ".env", "path to a .env file")
	flag.BoolVar(&o.envWins, "env-file-wins", false, "let the .env file override variables already set in the environment")
	flag.IntVar(&o.port, "port", defaultPort, "port to listen on (0 picks a free one)")
	flag.BoolVar(&o.showVersion, "version", false, "print version and exit")
	flag.StringVar(&o.bind, "bind", "127.0.0.1", "address to listen on")
	flag.StringVar(&o.usersFile, "users", "", "path to the accounts file (required); sessions, pictures and keys are kept beside it")
	flag.BoolVar(&o.trustProxy, "trust-proxy-headers", false, "take the client address from X-Forwarded-For (only behind a reverse proxy you control)")
	flag.StringVar(&o.addUser, "add-user", "", "create or update an account with this name, then exit (use with -users and -role)")
	flag.StringVar(&o.addRole, "role", "admin", "role for -add-user: admin, manager or viewer")
	flag.StringVar(&o.setPhoneOf, "set-phone", "", "account whose mobile to set in the account database, with the server stopped (use with -phone)")
	flag.StringVar(&o.phoneIs, "phone", "", "the mobile number for -set-phone")
	flag.StringVar(&o.setGrantOf, "set-grant", "", "account whose permission override to set, with the server stopped (use with -perm and -state)")
	flag.StringVar(&o.grantPerm, "perm", "", "the permission for -set-grant, e.g. action:manage_users")
	flag.StringVar(&o.grantState, "state", "", "allow, deny or inherit, for -set-grant")
	flag.BoolVar(&o.checkChat, "check-chat", false, "test what the messenger needs (PostgreSQL, ffmpeg, storage, TURN, the socket of the running server), then exit")
	flag.IntVar(&o.migrateDownTo, "migrate-down", 0, "run the down script of one migration version (destroys its data); needs -confirm-data-loss")
	flag.BoolVar(&o.confirmLoss, "confirm-data-loss", false, "with -migrate-down: really run it")
	flag.Parse()
	return o
}

// loadEnv reads the .env file before anything else, so the one-shot commands
// see it too. The real environment wins unless -env-file-wins.
func (o options) loadEnv() {
	if o.envWins {
		_ = config.ForceDotEnv(o.envFile)
	} else {
		_ = config.LoadDotEnv(o.envFile)
	}
}

// runOneShot runs the one-shot command asked for, if any, and reports
// whether there was one.
func (o options) runOneShot() (bool, error) {
	switch {
	case o.addUser != "":
		return true, manageUser(o.usersFile, o.addUser, o.addRole)
	case o.setPhoneOf != "":
		return true, setPhone(o.setPhoneOf, o.phoneIs)
	case o.setGrantOf != "":
		return true, setGrant(o.setGrantOf, o.grantPerm, o.grantState)
	case o.checkChat:
		return true, checkChat(o.port, filepath.Dir(o.usersFile))
	case o.migrateDownTo > 0:
		return true, migrateDown(o.migrateDownTo, o.confirmLoss)
	case o.showVersion:
		fmt.Println("messenger " + version)
		return true, nil
	}
	return false, nil
}
