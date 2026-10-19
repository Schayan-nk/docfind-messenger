package pgstore

import (
	"context"
	"strings"
	"testing"
	"time"
)

// testDB is the handle the database tests share, or a skip.
//
// SKIPPED RATHER THAN MOCKED, deliberately. A mock of a database proves the
// mock works. The only questions worth asking in this package are whether pgx
// reaches a real PostgreSQL with the DSN this code builds and whether the
// schema this code ships actually applies -- and nothing but a real server
// answers either. So these tests skip loudly when DOCFIND_PG_* is unset, and
// a green run with skips in it is not evidence that the database works.
func testDB(t *testing.T) *DB {
	t.Helper()
	c := FromEnv()
	if !c.Enabled() {
		t.Skip("no DOCFIND_PG_* configured; set them to run the database tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	db, err := Open(ctx, c)
	if err != nil {
		t.Fatalf("connecting to %s: %v", c.Redacted(), err)
	}
	t.Cleanup(db.Close)
	return db
}

func TestOpenConnectsAndPings(t *testing.T) {
	db := testDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
	if !strings.Contains(db.Redacted(), "postgres://") {
		t.Errorf("Redacted() is not an address: %q", db.Redacted())
	}
}

// An incomplete config is refused here rather than turned into a connection
// attempt against an address nobody configured.
func TestOpenRefusesAnIncompleteConfig(t *testing.T) {
	_, err := Open(context.Background(), Config{})
	if err == nil {
		t.Fatal("an empty config was accepted")
	}
	// The error must name what is missing, for the same reason Validate does.
	if !strings.Contains(err.Error(), "DOCFIND_PG_") {
		t.Errorf("the error does not say what is missing: %v", err)
	}
}

// VERIFIED AT OPEN, NOT LAZILY. A pool that connects on first use moves the
// failure out of start-up -- where the log says so and the fallback engages --
// and into the middle of somebody's request.
func TestOpenFailsRatherThanReturningAPoolThatCannotConnect(t *testing.T) {
	c := FromEnv()
	if !c.Enabled() {
		t.Skip("no DOCFIND_PG_* configured")
	}
	c.Password += "-definitely-wrong"
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	started := time.Now()
	db, err := Open(ctx, c)
	if err == nil {
		db.Close()
		t.Fatal("a wrong password produced a usable pool")
	}
	// Bounded: docfind must start even when the database refuses it, so this
	// cannot sit there until a browser gives up.
	if d := time.Since(started); d > 15*time.Second {
		t.Errorf("took %v to reject a bad password", d)
	}
}

// The nil-receiver paths, which need NO database and must therefore not sit
// behind the skip.
//
// Split out for that reason alone: the first version of this file checked the
// nil case and then called testDB, so on a machine with no database
// configured the whole test skipped and the nil safety this file exists to
// assert was never once executed. A test that only runs where it is least
// needed is not a test.
func TestANilDBAnswersRatherThanPanicking(t *testing.T) {
	var nilDB *DB
	if err := nilDB.Ping(context.Background()); err == nil {
		t.Error("Ping on a nil DB returned no error")
	}
	// Must not panic. The fallback closes a handle it may never have opened.
	nilDB.Close()
	nilDB.Close()
	if s := nilDB.Redacted(); s == "" {
		t.Error("Redacted() on a nil DB returned an empty string; a log line needs something")
	}
	if p := nilDB.Pool(); p != nil {
		t.Error("Pool() on a nil DB returned something")
	}
}

// A closed pool answers rather than panicking. Healthy() in the account
// backend calls Ping on whatever it holds, including after a shutdown, and
// Close runs twice on the path where the fallback gives up on a database it
// had already released.
func TestAClosedPoolAnswersRatherThanPanicking(t *testing.T) {
	db := testDB(t)
	db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := db.Ping(ctx); err == nil {
		t.Error("Ping on a closed pool returned no error")
	}
	db.Close() // twice, deliberately
}
