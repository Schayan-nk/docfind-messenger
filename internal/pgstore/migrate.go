package pgstore

import (
	"context"
	"embed"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// The schema travels inside the binary.
//
// Embedded rather than shipped as files beside the exe, for the same reason
// every page and the report catalogue are: a deployment is one file, and a
// schema that CAN be separated from the binary that expects it eventually
// will be -- at which point the binary meets a database shaped for a different
// version of itself.
//
//go:embed migrations/*.sql
var migrationFiles embed.FS

// migrationLock is the advisory-lock key every docfind process migrates under.
//
// Advisory locks share one namespace per database, so the value has to be
// something nothing else would pick. It is not stored anywhere and never
// changes: two binaries of different versions must contend for the SAME key,
// or the lock protects nothing.
const migrationLock int64 = 0x646f6366696e6431 // "docfind1"

// Migrate applies whatever has not been applied, in filename order, and
// returns the names of what it did.
//
// ONE TRANSACTION PER MIGRATION, with the record written inside it. So a
// migration that fails half way leaves the schema at the last COMPLETE
// version rather than at an unknown one, and re-running is safe because the
// failed one was never recorded as done.
//
// Run on every start. That is deliberate: a deployment should not need a
// separate "remember to migrate" step, and the cost when there is nothing to
// do is one SELECT per migration file.
//
// ONE MIGRATOR AT A TIME, under an advisory lock, and this was a real failure
// rather than a precaution. Two processes started together each saw a
// migration as unapplied, both ran it, and the second died on
// `column "name_fi_source" does not exist` -- the first had already renamed
// it. Nothing was corrupted, because each migration is its own transaction,
// but one process exits and docfind treats a failed migration as fatal. It
// surfaced from `go test` running two packages in parallel; a restart racing a
// still-shutting-down instance is the same shape.
//
// The lock also covers the CREATE TABLE below. `IF NOT EXISTS` is not
// concurrency-safe in PostgreSQL: two sessions creating the same table at once
// can fail on a duplicate pg_type row rather than one quietly doing nothing.
func (d *DB) Migrate(ctx context.Context) ([]string, error) {
	if d == nil || d.pool == nil {
		return nil, fmt.Errorf("pgstore: no connection")
	}

	// Everything below runs on ONE connection, because an advisory lock
	// belongs to the session that took it -- taken on a pooled connection and
	// released on another, it would never be released at all.
	conn, err := d.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("pgstore: acquiring a connection to migrate: %w", err)
	}
	defer conn.Release()

	// Blocks until the other migrator finishes. Bounded by ctx, so a caller
	// that passes a deadline gets an error instead of hanging forever behind a
	// process that is stuck.
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationLock); err != nil {
		return nil, fmt.Errorf("pgstore: waiting for the migration lock: %w", err)
	}
	defer func() {
		// context.Background(), not ctx: if ctx is already cancelled -- a
		// timeout part way through -- unlocking with it would fail and leave
		// the lock held until the connection closes.
		conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, migrationLock)
	}()

	// The record of what has been applied. Created first, and idempotently,
	// because it is the one table that cannot be described by a migration.
	if _, err := conn.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER     PRIMARY KEY,
			name    TEXT        NOT NULL,
			at      TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		return nil, fmt.Errorf("pgstore: creating the migration record: %w", err)
	}

	names, err := migrationNames()
	if err != nil {
		return nil, err
	}

	var applied []string
	for _, name := range names {
		version, err := versionOf(name)
		if err != nil {
			return applied, err
		}
		var done bool
		if err := conn.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`,
			version).Scan(&done); err != nil {
			return applied, fmt.Errorf("pgstore: checking migration %s: %w", name, err)
		}
		if done {
			continue
		}
		body, err := migrationFiles.ReadFile("migrations/" + name)
		if err != nil {
			return applied, err
		}

		tx, err := conn.Begin(ctx)
		if err != nil {
			return applied, fmt.Errorf("pgstore: %w", err)
		}
		if _, err := tx.Exec(ctx, string(body)); err != nil {
			tx.Rollback(ctx)
			return applied, fmt.Errorf("pgstore: applying %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO schema_migrations (version, name) VALUES ($1, $2)`,
			version, name); err != nil {
			tx.Rollback(ctx)
			return applied, fmt.Errorf("pgstore: recording %s: %w", name, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return applied, fmt.Errorf("pgstore: committing %s: %w", name, err)
		}
		applied = append(applied, name)
	}
	return applied, nil
}

// migrationNames lists the embedded .sql files in the order they must run.
//
// Sorted by NAME, which works only because every name starts with a
// zero-padded number -- versionOf enforces the number, and the padding is a
// convention this function depends on. 010 sorts before 002 without it.
func migrationNames() ([]string, error) {
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("pgstore: reading the embedded migrations: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// versionOf reads the leading number off a migration filename.
//
// A file without one is REFUSED rather than run in whatever position sorting
// happened to give it. The version is the ordering and the identity: it is
// what schema_migrations records, so a migration that cannot say which number
// it is cannot be recorded as applied either.
func versionOf(name string) (int, error) {
	i := strings.IndexByte(name, '_')
	if i <= 0 {
		return 0, fmt.Errorf("pgstore: %q must start with a version number, e.g. 003_thing.sql", name)
	}
	v, err := strconv.Atoi(name[:i])
	if err != nil || v <= 0 {
		return 0, fmt.Errorf("pgstore: %q must start with a positive version number, e.g. 003_thing.sql", name)
	}
	return v, nil
}
