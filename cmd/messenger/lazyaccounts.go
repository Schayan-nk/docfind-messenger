package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Schayan-nk/docfind-messenger/internal/auth"
	"github.com/Schayan-nk/docfind-messenger/internal/pgstore"
)

// lazyAccounts is the account database when it could not be reached at
// start-up: every Load or Save tries to connect again, and once one succeeds
// it is the database from then on.
//
// It exists so that docfind started during a database outage still has the
// REAL store behind its snapshot fallback -- and so that the outage ending is
// noticed without a restart. Before it, a database down at start-up sent
// docfind to the old accounts file, which has not been the source of truth
// since 5.20.0: an account removed or disabled since then could sign in
// there, and anything changed there never reached the database.
type lazyAccounts struct {
	cfg pgstore.Config

	mu  sync.Mutex
	db  *pgstore.DB
	acc *pgstore.Accounts
}

func (l *lazyAccounts) open() (*pgstore.Accounts, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.acc != nil {
		return l.acc, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := pgstore.Open(ctx, l.cfg)
	if err != nil {
		return nil, err
	}
	if _, err := db.Migrate(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("applying the account database schema: %w", err)
	}
	l.db, l.acc = db, pgstore.NewAccounts(db)
	return l.acc, nil
}

func (l *lazyAccounts) Load() ([]*auth.User, error) {
	a, err := l.open()
	if err != nil {
		return nil, err
	}
	return a.Load()
}

func (l *lazyAccounts) Save(users []*auth.User) error {
	a, err := l.open()
	if err != nil {
		return err
	}
	return a.Save(users)
}

func (l *lazyAccounts) Healthy() bool {
	l.mu.Lock()
	a := l.acc
	l.mu.Unlock()
	return a != nil && a.Healthy()
}

func (l *lazyAccounts) Describe() string {
	l.mu.Lock()
	a := l.acc
	l.mu.Unlock()
	if a != nil {
		return a.Describe()
	}
	return "PostgreSQL " + l.cfg.Redacted()
}

// DB is the database once it has been reached, or nil.
func (l *lazyAccounts) DB() *pgstore.DB {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.db
}

// keepAccountsCurrent ends emergency mode when the database comes back.
//
// While accounts are served from the snapshot, every 30 seconds the store is
// reloaded; the fallback tries the database first, so the reload that finds
// it up replaces the snapshot's accounts with the live ones and new sign-ins
// are ordinary again. Emergency sessions already issued run out on their own
// within the hour.
func keepAccountsCurrent(ctx context.Context, users *auth.Store) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if !users.Degraded() {
			continue
		}
		if err := users.Refresh(); err != nil {
			logf("accounts: still unavailable: %v", err)
			continue
		}
		if !users.Degraded() {
			logf("accounts: the database is back -- emergency sign-in is over. " +
				"Restart docfind to switch the database's other features back on " +
				"(notifications, the audit trail, the calendar snapshot).")
		}
	}
}
