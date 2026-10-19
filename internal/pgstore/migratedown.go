package pgstore

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"strings"
)

// Down scripts: migrations/down/NNN_name.down.sql reverses NNN_name.sql.
//
// They travel in the binary like the up scripts but are never run by Migrate.
// Undoing a migration destroys what it created -- for the chat tables that is
// every message -- so it happens only when an operator names the version
// (docfind -migrate-down 44 -confirm-data-loss).
//
//go:embed migrations/down/*.sql
var downFiles embed.FS

// DownScript returns the reversal for one migration version, or an error when
// that version has none.
func DownScript(version int) (string, string, error) {
	entries, err := fs.ReadDir(downFiles, "migrations/down")
	if err != nil {
		return "", "", err
	}
	prefix := fmt.Sprintf("%03d_", version)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix) && strings.HasSuffix(e.Name(), ".down.sql") {
			body, err := downFiles.ReadFile("migrations/down/" + e.Name())
			return e.Name(), string(body), err
		}
	}
	return "", "", fmt.Errorf("pgstore: migration %d has no down script", version)
}

// MigrateDown runs the down script for one version, in one transaction, under
// the same advisory lock as Migrate so it cannot race a starting server.
func (d *DB) MigrateDown(ctx context.Context, version int) (string, error) {
	name, body, err := DownScript(version)
	if err != nil {
		return "", err
	}
	conn, err := d.pool.Acquire(ctx)
	if err != nil {
		return "", fmt.Errorf("pgstore: %w", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationLock); err != nil {
		return "", fmt.Errorf("pgstore: taking the migration lock: %w", err)
	}
	defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, migrationLock)
	tx, err := conn.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("pgstore: %w", err)
	}
	if _, err := tx.Exec(ctx, body); err != nil {
		tx.Rollback(ctx)
		return "", fmt.Errorf("pgstore: running %s: %w", name, err)
	}
	return name, tx.Commit(ctx)
}
