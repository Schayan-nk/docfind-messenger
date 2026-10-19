package pgstore

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DB is a live connection pool to docfind's own database.
type DB struct {
	pool *pgxpool.Pool
	cfg  Config
}

// connectTimeout bounds one start-up attempt.
//
// Short, and deliberately so. docfind must START even when the database does
// not answer -- it falls back to a read-only snapshot and says so on the
// health page -- so a long timeout here would turn a database outage into a
// server that appears hung at boot, which is a worse failure than the one it
// is waiting for.
const connectTimeout = 8 * time.Second

// Open connects and verifies the connection before returning.
//
// VERIFIED, NOT LAZY. pgxpool will happily hand back a pool that has never
// spoken to anything; the first query then fails in the middle of somebody's
// request instead of at start-up, where the log says so and the fallback
// engages. So this pings before returning, and a failure here is how the
// caller learns to use the snapshot.
func Open(ctx context.Context, c Config) (*DB, error) {
	if !c.Enabled() {
		return nil, fmt.Errorf("pgstore: not configured; still wants %v", c.Validate())
	}
	pc, err := pgxpool.ParseConfig(c.DSN())
	if err != nil {
		// The DSN is not echoed: it carries the password.
		return nil, fmt.Errorf("pgstore: the connection settings for %s are unusable: %w",
			c.Redacted(), err)
	}
	// Small on purpose. This is one application on the same box as the
	// database, and the read path is an in-memory map rather than a query per
	// request -- see the Backend comment in internal/auth. A large pool here
	// would reserve connections nothing uses.
	pc.MaxConns = 8
	pc.MinConns = 1
	pc.MaxConnLifetime = time.Hour
	pc.MaxConnIdleTime = 15 * time.Minute
	pc.ConnConfig.ConnectTimeout = connectTimeout
	// Every query charged to the request that caused it, and slow ones
	// logged with their statement text (trace.go).
	pc.ConnConfig.Tracer = queryTracer{slow: slowQueryThreshold()}

	ctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return nil, fmt.Errorf("pgstore: connecting to %s: %w", c.Redacted(), err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pgstore: %s did not answer: %w", c.Redacted(), err)
	}
	return &DB{pool: pool, cfg: c}, nil
}

// Close releases every connection.
//
// Safe on a nil receiver and safe twice: the fallback closes a pool it may
// already have closed, and a double Close that panicked would take the process
// down at shutdown -- the one moment nobody is watching the log.
func (d *DB) Close() {
	if d == nil || d.pool == nil {
		return
	}
	d.pool.Close()
}

// Ping asks whether the database is still answering.
//
// An error rather than a panic on a nil or closed pool, because this is what
// the health check and the account backend call, and both may hold a handle
// to something that has since gone away.
func (d *DB) Ping(ctx context.Context) error {
	if d == nil || d.pool == nil {
		return fmt.Errorf("pgstore: no connection")
	}
	return d.pool.Ping(ctx)
}

// Pool is the handle for queries.
//
// Exported because the stores that use this run their own SQL: this package
// owns the CONNECTION, and each store owns its own statements. Hiding the pool
// behind a query method per table would put every table's SQL in one file,
// which is the file that then grows past five hundred lines.
//
// Nil-safe on the receiver, like Ping and Close, and this one was caught by
// its own test rather than by reasoning: `return d.pool` dereferences a nil
// DB, so the first caller holding a handle to a database that never opened
// would have panicked instead of getting an error. A nil pool returned here
// does NOT fail politely at the query: pgx panics on a nil *Pool, so a store
// that may hold an unopened database checks for nil itself (audit.go does).
func (d *DB) Pool() *pgxpool.Pool {
	if d == nil {
		return nil
	}
	return d.pool
}

// Redacted is the address this is connected to, safe to log.
func (d *DB) Redacted() string {
	if d == nil {
		return "postgres://(not connected)"
	}
	return d.cfg.Redacted()
}
