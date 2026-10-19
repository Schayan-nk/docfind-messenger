// Package pgstore holds docfind's own database.
//
// OPTIONAL, exactly as the mailbox and the reporting database are. With
// nothing configured docfind runs on its JSON files as it always has, and the
// health page says the database is off rather than broken -- a component
// nobody configured is not a fault, and colouring it red sends somebody
// looking for a problem that does not exist.
//
// THIS IS NOT A REPORTING DATABASE. Reports read databases that belong to
// other applications, whose schema this one only queries. This one is the
// server's own -- accounts, permissions, preferences, credentials -- and it is
// the only database here whose shape this application decides.
package pgstore

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// defaultPort is PostgreSQL's, and is used when the variable is absent or
// unusable.
const defaultPort = 5432

// Config is how to reach the database.
type Config struct {
	Host string
	Port int
	User string
	// Password is NOT trimmed. A space at the edge of a password is a
	// character in it, and trimming one turns a working credential into a
	// sign-in failure that looks like a wrong password. The mailbox settings
	// carry the same warning for the same reason.
	Password string
	Database string
	// SSLMode is "disable" by default, and that is correct here rather than
	// lax: the database listens on loopback on the same box as docfind, so
	// there is no network segment for TLS to protect. Configurable because a
	// later move to another host would need it -- and a setting that has to be
	// added in a hurry is a setting that gets added wrong.
	SSLMode string
}

// FromEnv reads the settings, returning a Config that may be incomplete.
//
// Incomplete rather than an error, so the caller can ask Validate what is
// missing and say so in a log line. A subsystem that refuses to describe its
// own misconfiguration is one somebody debugs by guessing.
func FromEnv() Config {
	c := Config{
		Host: strings.TrimSpace(os.Getenv("DOCFIND_PG_HOST")),
		User: strings.TrimSpace(os.Getenv("DOCFIND_PG_USER")),
		// Deliberately not trimmed. See the field comment.
		Password: os.Getenv("DOCFIND_PG_PASSWORD"),
		Database: strings.TrimSpace(os.Getenv("DOCFIND_PG_DATABASE")),
		SSLMode:  strings.TrimSpace(os.Getenv("DOCFIND_PG_SSLMODE")),
		Port:     defaultPort,
	}
	if c.SSLMode == "" {
		c.SSLMode = "disable"
	}
	// A port that is not a port falls back rather than being carried forward
	// as zero: a zero port produces a connection error about an address nobody
	// configured, which is a worse clue than the default quietly working.
	if v := strings.TrimSpace(os.Getenv("DOCFIND_PG_PORT")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 65535 {
			c.Port = n
		}
	}
	return c
}

// Enabled reports whether there is enough here to connect.
func (c Config) Enabled() bool { return len(c.Validate()) == 0 }

// Validate names what is missing, in the operator's own variable names.
//
// The VARIABLE names rather than field names, because the person reading this
// is looking at a .env file and not at this struct.
func (c Config) Validate() []string {
	var missing []string
	for _, f := range []struct{ env, val string }{
		{"DOCFIND_PG_HOST", c.Host},
		{"DOCFIND_PG_USER", c.User},
		{"DOCFIND_PG_PASSWORD", c.Password},
		{"DOCFIND_PG_DATABASE", c.Database},
	} {
		if strings.TrimSpace(f.val) == "" {
			missing = append(missing, f.env)
		}
	}
	return missing
}

// DSN is the connection string, password included, for pgx alone.
//
// BUILT WITH net/url RATHER THAN CONCATENATED. The passwords in use on this
// estate contain @ and # and $ and /, and every one of those changes the
// meaning of a URL: an unescaped @ moves the host, a # truncates at the
// fragment, a / invents a database name. Concatenation produces a connection
// error that names the wrong address, which is a long way from the real
// problem.
func (c Config) DSN() string {
	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(c.User, c.Password),
		Host:   net.JoinHostPort(c.Host, strconv.Itoa(c.Port)),
		Path:   "/" + c.Database,
	}
	q := url.Values{}
	q.Set("sslmode", c.SSLMode)
	u.RawQuery = q.Encode()
	return u.String()
}

// Redacted is the address this points at, with no password, for a log line.
//
// A separate method rather than a flag on DSN, because the safe one has to be
// the one that is easy to reach for. It still names the host, the port and the
// database: a redaction that removes the useful part is a log line that says
// nothing.
func (c Config) Redacted() string {
	return fmt.Sprintf("postgres://%s@%s/%s?sslmode=%s",
		url.User(c.User).String(),
		net.JoinHostPort(c.Host, strconv.Itoa(c.Port)),
		url.PathEscape(c.Database), url.QueryEscape(c.SSLMode))
}
