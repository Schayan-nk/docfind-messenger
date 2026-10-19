package pgstore

import (
	"context"
	"testing"
	"time"
)

// The schema, tested against a real PostgreSQL.
//
// There is no unit-testable part of a migration. A migration that has only
// ever been reasoned about is a migration that does not work, and the ways
// this one can be wrong -- a type that does not exist, a constraint that does
// not do what it reads like, an index on an expression the planner will not
// use -- are all invisible until a server parses it.

// Every table is named explicitly rather than counted: a count passes when the
// wrong table was created.
func TestMigrateCreatesEveryTable(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"users", "user_prefs", "avatars", "audit", "secrets", "audit_events",
		"app_settings", "notifications", "push_subscriptions", "chats", "chat_members", "chat_messages",
		"chat_calls", "secret_chats"} {
		var exists bool
		err := db.Pool().QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables
			                 WHERE table_schema = current_schema() AND table_name = $1)`,
			table).Scan(&exists)
		if err != nil {
			t.Fatalf("checking %s: %v", table, err)
		}
		if !exists {
			t.Errorf("table %s was not created", table)
		}
	}
}

// Two accounts differing only in case are ONE account. Sign-in is
// case-insensitive, so a second row would be an account nobody can reach whose
// password nevertheless works.
func TestUsernamesAreUniqueIgnoringCase(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Pool().Exec(ctx, `DELETE FROM users WHERE lower(username) = 'casetest'`)
	})
	db.Pool().Exec(ctx, `DELETE FROM users WHERE lower(username) = 'casetest'`)

	ins := `INSERT INTO users (username, password_hash, role) VALUES ($1, 'x', 'viewer')`
	if _, err := db.Pool().Exec(ctx, ins, "CaseTest"); err != nil {
		t.Fatalf("inserting CaseTest: %v", err)
	}
	if _, err := db.Pool().Exec(ctx, ins, "casetest"); err == nil {
		t.Error("casetest was accepted alongside CaseTest")
	}
}

// Preferences and pictures go when the account goes. Without the cascade, the
// next account created under a reused name would inherit a stranger's settings
// and face.
func TestPrefsAndAvatarCascadeWithTheAccount(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Pool().Exec(ctx, `DELETE FROM users WHERE username = 'cascadetest'`) })
	db.Pool().Exec(ctx, `DELETE FROM users WHERE username = 'cascadetest'`)

	if _, err := db.Pool().Exec(ctx,
		`INSERT INTO users (username, password_hash, role) VALUES ('cascadetest','x','viewer')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool().Exec(ctx,
		`INSERT INTO user_prefs (username, prefs) VALUES ('cascadetest', '{"font_scale":120}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool().Exec(ctx,
		`INSERT INTO avatars (username, bytes, content_type) VALUES ('cascadetest', $1, 'image/png')`,
		[]byte{0x89, 0x50, 0x4e, 0x47}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool().Exec(ctx, `DELETE FROM users WHERE username = 'cascadetest'`); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"user_prefs", "avatars"} {
		var n int
		if err := db.Pool().QueryRow(ctx,
			`SELECT count(*) FROM `+table+` WHERE username = 'cascadetest'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("%d row(s) left behind in %s", n, table)
		}
	}
}

// A preference document must survive a round trip as JSON rather than as a
// string that happens to look like it.
func TestPrefsRoundTripAsJSONB(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Pool().Exec(ctx, `DELETE FROM users WHERE username = 'jsonbtest'`) })
	db.Pool().Exec(ctx, `DELETE FROM users WHERE username = 'jsonbtest'`)

	if _, err := db.Pool().Exec(ctx,
		`INSERT INTO users (username, password_hash, role) VALUES ('jsonbtest','x','viewer')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool().Exec(ctx,
		`INSERT INTO user_prefs (username, prefs) VALUES ('jsonbtest', $1)`,
		[]byte(`{"font_scale":115,"density":"compact","reduce_motion":true}`)); err != nil {
		t.Fatal(err)
	}
	// Queried as JSON, not as text: if the column were TEXT this would fail,
	// which is the point.
	var scale int
	if err := db.Pool().QueryRow(ctx,
		`SELECT (prefs->>'font_scale')::int FROM user_prefs WHERE username = 'jsonbtest'`).Scan(&scale); err != nil {
		t.Fatalf("reading prefs as JSON: %v", err)
	}
	if scale != 115 {
		t.Errorf("font_scale came back as %d", scale)
	}
}

// ONE MIGRATOR AT A TIME. This is a regression test for a failure that
// happened: two processes started together, both saw a migration as
// unapplied, both ran it, and the second died on `column "name_fi_source"
// does not exist` because the first had already renamed it. docfind treats a
// failed migration as fatal, so that process does not start.
//
// The original race cannot be reproduced against a database that is already
// migrated -- every migration short-circuits. So the LOCK is asserted
// directly: hold it from another session and Migrate must wait, which proves
// it is taken before any schema is read or written.
func TestMigrateWaitsForAnotherMigrator(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	// Another session takes the lock and keeps it.
	holder, err := db.Pool().Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Release()
	if _, err := holder.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationLock); err != nil {
		t.Fatal(err)
	}

	// Migrate cannot get in. A short deadline, because the assertion is that
	// it BLOCKS -- if the lock were not taken it would return at once.
	short, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	applied, err := db.Migrate(short)
	if err == nil {
		t.Error("Migrate ran while another session held the migration lock")
	}
	if len(applied) != 0 {
		t.Errorf("Migrate applied %v while locked out", applied)
	}

	// Released, it proceeds.
	if _, err := holder.Exec(ctx, `SELECT pg_advisory_unlock($1)`, migrationLock); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Migrate(ctx); err != nil {
		t.Errorf("Migrate still failed after the lock was released: %v", err)
	}
}

// THE LOCK MUST BE GIVEN BACK. Held past the end of Migrate, the first start
// of the day would block every later one -- a deadlock that looks like a hung
// service rather than an error.
func TestMigrateReleasesTheLock(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	other, err := db.Pool().Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Release()
	var got bool
	if err := other.QueryRow(ctx,
		`SELECT pg_try_advisory_lock($1)`, migrationLock).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("the migration lock is still held after Migrate returned")
		return
	}
	other.Exec(ctx, `SELECT pg_advisory_unlock($1)`, migrationLock)
}

// And concurrently, which is how the failure arrived: several processes
// starting at once must all come back without an error.
func TestConcurrentMigrateIsSafe(t *testing.T) {
	db := testDB(t)
	const n = 4
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_, err := db.Migrate(ctx)
			errs <- err
		}()
	}
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Errorf("a concurrent Migrate failed: %v", err)
		}
	}
}

// A version number is the order migrations run in, so a filename without one
// is refused rather than run in whatever position sorting happened to give it.
func TestAFilenameWithoutAVersionIsRefused(t *testing.T) {
	for _, bad := range []string{"users.sql", "_users.sql", "abc_users.sql", "0_users.sql", "-1_x.sql"} {
		if _, err := versionOf(bad); err == nil {
			t.Errorf("%q was accepted as a migration filename", bad)
		}
	}
	for name, want := range map[string]int{
		"001_users.sql": 1, "002_secrets.sql": 2, "017_whatever.sql": 17,
	} {
		got, err := versionOf(name)
		if err != nil {
			t.Errorf("%q: %v", name, err)
			continue
		}
		if got != want {
			t.Errorf("%q parsed as %d, want %d", name, got, want)
		}
	}
}
