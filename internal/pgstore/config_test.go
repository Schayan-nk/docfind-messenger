package pgstore

import (
	"strings"
	"testing"
)

// The environment is the only place these come from, like every other optional
// subsystem here. A half-configured database is OFF, and says which variable
// is missing -- the failure this guards against is somebody believing they
// switched it on. That has happened: a DONCFIND_ for a DOCFIND_ starts the
// application perfectly and reads none of the line.
func TestFromEnvReadsTheDocfindVariables(t *testing.T) {
	t.Setenv("DOCFIND_PG_HOST", "127.0.0.1")
	t.Setenv("DOCFIND_PG_PORT", "5432")
	t.Setenv("DOCFIND_PG_USER", "docfind")
	t.Setenv("DOCFIND_PG_PASSWORD", "secret")
	t.Setenv("DOCFIND_PG_DATABASE", "docfind")

	c := FromEnv()
	if !c.Enabled() {
		t.Fatalf("a fully configured database is not enabled; still wants %v", c.Validate())
	}
	if c.Host != "127.0.0.1" || c.Port != 5432 || c.Database != "docfind" {
		t.Errorf("wrong config: %+v", c)
	}
}

// The port is the one setting nobody changes, so requiring it would be four
// variables where three would do.
func TestAPortIsNotRequiredAndDefaultsTo5432(t *testing.T) {
	t.Setenv("DOCFIND_PG_HOST", "127.0.0.1")
	t.Setenv("DOCFIND_PG_USER", "docfind")
	t.Setenv("DOCFIND_PG_PASSWORD", "secret")
	t.Setenv("DOCFIND_PG_DATABASE", "docfind")
	t.Setenv("DOCFIND_PG_PORT", "")

	if p := FromEnv().Port; p != 5432 {
		t.Errorf("port defaulted to %d, want 5432", p)
	}
}

// A port that is not a port falls back rather than producing a config that
// cannot connect and does not say why.
func TestAnUnusablePortFallsBackRatherThanBreaking(t *testing.T) {
	t.Setenv("DOCFIND_PG_HOST", "127.0.0.1")
	t.Setenv("DOCFIND_PG_USER", "docfind")
	t.Setenv("DOCFIND_PG_PASSWORD", "secret")
	t.Setenv("DOCFIND_PG_DATABASE", "docfind")
	for _, bad := range []string{"no", "-1", "0", "70000", "5432x"} {
		t.Setenv("DOCFIND_PG_PORT", bad)
		if p := FromEnv().Port; p != 5432 {
			t.Errorf("port %q produced %d, want the 5432 fallback", bad, p)
		}
	}
}

// A missing variable is NAMED. "The database is off" without saying why is the
// support question this exists to avoid.
func TestValidateNamesEveryMissingVariable(t *testing.T) {
	for _, k := range []string{"DOCFIND_PG_HOST", "DOCFIND_PG_USER",
		"DOCFIND_PG_PASSWORD", "DOCFIND_PG_DATABASE"} {
		t.Setenv(k, "")
	}
	c := FromEnv()
	if c.Enabled() {
		t.Fatal("an empty environment produced an enabled database")
	}
	missing := strings.Join(c.Validate(), " ")
	for _, want := range []string{"DOCFIND_PG_HOST", "DOCFIND_PG_USER",
		"DOCFIND_PG_PASSWORD", "DOCFIND_PG_DATABASE"} {
		if !strings.Contains(missing, want) {
			t.Errorf("Validate() does not mention %s: %q", want, missing)
		}
	}
}

// Whitespace at the edge of a value is somebody's editor, not part of the
// address. The password is the exception and is NOT trimmed: a space at the
// edge of a password is a character in it, and trimming it turns a working
// credential into a sign-in failure nobody can explain. The mailbox settings
// document the same rule for the same reason.
func TestValuesAreTrimmedExceptThePassword(t *testing.T) {
	t.Setenv("DOCFIND_PG_HOST", "  127.0.0.1  ")
	t.Setenv("DOCFIND_PG_USER", "  docfind\t")
	t.Setenv("DOCFIND_PG_DATABASE", " docfind ")
	t.Setenv("DOCFIND_PG_PASSWORD", "  spaced  ")

	c := FromEnv()
	if c.Host != "127.0.0.1" || c.User != "docfind" || c.Database != "docfind" {
		t.Errorf("values were not trimmed: %+v", c)
	}
	if c.Password != "  spaced  " {
		t.Errorf("the password was trimmed to %q; a space in a password is a character in it", c.Password)
	}
}

// Loopback by default. The database sits on the same box as docfind, so there
// is no network segment for TLS to protect -- and requiring a certificate for
// a connection that never leaves the machine is a setting that gets disabled
// in a hurry at the worst moment.
func TestSSLModeDefaultsToDisableAndIsOverridable(t *testing.T) {
	t.Setenv("DOCFIND_PG_SSLMODE", "")
	if got := FromEnv().SSLMode; got != "disable" {
		t.Errorf("sslmode defaulted to %q, want disable", got)
	}
	t.Setenv("DOCFIND_PG_SSLMODE", "require")
	if got := FromEnv().SSLMode; got != "require" {
		t.Errorf("sslmode override produced %q", got)
	}
}

// The DSN carries the password because pgx needs it. Redacted() is what goes
// in a log line, and it must not. A DSN gets logged by accident exactly once
// and is then in a log file for ever.
func TestTheDSNCarriesThePasswordAndRedactedDoesNot(t *testing.T) {
	c := Config{Host: "127.0.0.1", Port: 5432, User: "docfind",
		Password: "hunter2", Database: "docfind", SSLMode: "disable"}
	if !strings.Contains(c.DSN(), "hunter2") {
		t.Error("the DSN does not carry the password; pgx needs it")
	}
	if strings.Contains(c.Redacted(), "hunter2") {
		t.Errorf("Redacted() leaked the password: %s", c.Redacted())
	}
	// Still useful as a log line: it has to say WHERE, or it says nothing.
	for _, want := range []string{"127.0.0.1", "5432", "docfind"} {
		if !strings.Contains(c.Redacted(), want) {
			t.Errorf("Redacted() does not mention %s: %s", want, c.Redacted())
		}
	}
}

// A password with URL-significant characters must survive into the DSN.
// Measured elsewhere in this application: the live passwords in use here
// contain @ and # and $, and a DSN built by naive concatenation turns one of
// those into a host name and fails with an error about the wrong address.
func TestAPasswordWithURLCharactersSurvivesTheDSN(t *testing.T) {
	c := Config{Host: "127.0.0.1", Port: 5432, User: "docfind",
		Password: "P@s$word#12/34?x", Database: "docfind", SSLMode: "disable"}
	dsn := c.DSN()
	// The host must still be the host: an unescaped @ would move it.
	if !strings.Contains(dsn, "@127.0.0.1:5432/docfind") {
		t.Errorf("the password broke the DSN structure: %s", dsn)
	}
	if strings.Contains(dsn, "P@s$word#12/34?x") {
		t.Error("the password went in unescaped; @ # / and ? each change the meaning of a URL")
	}
}
