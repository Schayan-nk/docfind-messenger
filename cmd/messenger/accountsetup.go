package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Schayan-nk/docfind-messenger/internal/auth"
	"github.com/Schayan-nk/docfind-messenger/internal/pgstore"
	"github.com/Schayan-nk/docfind-messenger/internal/secretbox"
)

// accountSetup is what opening the accounts produced: the store, docfind's
// own database when one is configured and reachable, and the key sessions
// are signed with.
type accountSetup struct {
	users      *auth.Store
	pgDB       *pgstore.DB
	sessionKey []byte
}

// openAccounts opens the accounts, when a file is configured. Loaded before
// binding on purpose: the safety check there needs to know whether anyone
// can actually sign in before it agrees to listen on a public address.
func openAccounts(usersFile string) (accountSetup, error) {
	var acc accountSetup
	if usersFile == "" {
		return acc, nil
	}
	// The signing key first. It sits beside the accounts file; losing it signs
	// everybody out, leaking it lets anyone mint an administrator token, which
	// is why LoadOrCreateKey writes it 0600. Loaded before the accounts
	// because the account snapshot is signed with a key derived from it -- a
	// snapshot nobody can verify is never used.
	key, err := auth.LoadOrCreateKey(filepath.Join(filepath.Dir(usersFile), "token.key"))
	if err != nil {
		return acc, err
	}
	acc.sessionKey = key
	snapshotPath := dataFilePath(usersFile, "users.snapshot.json")

	backend, lazy, db, err := openAccountBackend(usersFile, snapshotPath, key)
	if err != nil {
		return acc, err
	}
	acc.pgDB = db

	if backend != nil {
		acc.users, err = auth.OpenStoreWith(usersFile, backend)
		// FAIL CLOSED. The database is down and the snapshot cannot be trusted
		// -- missing, unsigned, altered or too old. Opening the accounts anyway
		// would mean the stale file or an empty store, and an empty store
		// offers the next visitor the first-administrator screen. So docfind
		// waits, trying the database again, and serves nothing until it can
		// say who is who.
		for err != nil && lazy != nil {
			logf("accounts cannot be opened, so docfind is not serving yet: %v -- trying again in 30 s", err)
			time.Sleep(30 * time.Second)
			acc.users, err = auth.OpenStoreWith(usersFile, backend)
		}
		// Reached during that wait: the database is up after all, and
		// everything that needs it can start as usual.
		if lazy != nil && acc.pgDB == nil {
			if db := lazy.DB(); db != nil && !acc.users.Degraded() {
				acc.pgDB = db
				logf("account database reached; accounts are stored in %s", pgstore.FromEnv().Redacted())
			}
		}
	} else {
		acc.users, err = auth.OpenStore(usersFile)
	}
	if err != nil {
		return acc, err
	}
	if acc.users.Degraded() {
		logf("EMERGENCY SIGN-IN: accounts come from the snapshot. Sessions last an hour and " +
			"cannot change users, permissions or settings until the database is back.")
	}
	// The key two-step sign-in secrets are sealed with, beside the accounts
	// file (secondfactor.key, DPAPI-wrapped on Windows) and so on this machine
	// rather than in the database. One that will not open is logged and not
	// fatal: nobody can then turn the step on, and anybody who has it on
	// cannot finish signing in -- which fails closed for them and leaves
	// everybody else working.
	if box, serr := secretbox.OpenFileBox(filepath.Join(filepath.Dir(usersFile), "secondfactor")); serr != nil {
		logf("two-step sign-in is unavailable: opening its key: %v", serr)
	} else {
		acc.users.UseSealer(box)
	}
	if backend != nil {
		go keepAccountsCurrent(context.Background(), acc.users)
	}
	return acc, nil
}

// openAccountBackend opens docfind's own database, when one is configured,
// and the account backend over it.
//
// OPTIONAL, like the mailbox and the reporting database. With nothing
// configured the accounts file is the store exactly as before (a nil
// backend), and the health page says the database is off rather than broken.
//
// A DATABASE THAT WILL NOT OPEN IS NOT FATAL. The signed snapshot is served
// instead, with the database retried behind it (lazy). A schema that will not
// apply is the one thing worth refusing over.
func openAccountBackend(usersFile, snapshotPath string, key []byte) (auth.Backend, *lazyAccounts, *pgstore.DB, error) {
	pc := pgstore.FromEnv()
	if !pc.Enabled() {
		if missing := pc.Validate(); len(missing) > 0 && len(missing) < 4 {
			// Partly configured is worth a line, because somebody believed they
			// switched it on. Silent when absent, loud when half-done -- the
			// same rule the mailbox settings follow.
			logf("account database is off: still need %s", strings.Join(missing, ", "))
		}
		return nil, nil, nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	db, err := pgstore.Open(ctx, pc)
	cancel()
	if err != nil {
		// NOT the accounts file. That file stopped being the source of truth
		// when accounts moved to the database, and falling back to it let in
		// whoever had been removed since. The snapshot is used instead --
		// signed, recent, read-only, and every sign-in from it an emergency
		// session -- with the database retried behind it (lazyAccounts,
		// keepAccountsCurrent).
		logf("account database unavailable at start-up: %v -- "+
			"serving accounts read-only from the signed snapshot, with emergency sign-in", err)
		lazy := &lazyAccounts{cfg: pc}
		return auth.NewFallback(lazy, snapshotPath, auth.SnapshotKey(key)), lazy, nil, nil
	}

	applied, err := db.Migrate(context.Background())
	if err != nil {
		// Running against a half-known schema produces errors that look like
		// data problems and get debugged as such.
		db.Close()
		return nil, nil, nil, fmt.Errorf("applying the account database schema: %w", err)
	}
	if len(applied) > 0 {
		logf("account database schema applied: %s", strings.Join(applied, ", "))
	}
	if slices.Contains(applied, "041_user_grants.sql") {
		restoreGrants(db, snapshotPath, key)
	}
	if n, ierr := pgstore.ImportAccounts(context.Background(), db, usersFile); ierr != nil {
		logf("importing %s into the database: %v", usersFile, ierr)
	} else if n > 0 {
		logf("imported %d account(s) from %s into the database", n, usersFile)
	}
	logf("accounts are stored in %s", pc.Redacted())
	return auth.NewFallback(pgstore.NewAccounts(db), snapshotPath, auth.SnapshotKey(key)), nil, db, nil
}

// restoreGrants copies the permission overrides in force into the database,
// from the signed snapshot, on the start that gives the users table its
// grants column (migration 041). Until then the database never stored them,
// so every restart dropped them; the snapshot, written on every account
// change, still holds the ones set since the last restart. Before the
// accounts are loaded, because that load rewrites the snapshot from the
// database.
func restoreGrants(db *pgstore.DB, snapshotPath string, key []byte) {
	users, err := auth.ReadSnapshot(snapshotPath, auth.SnapshotKey(key), time.Now())
	if errors.Is(err, fs.ErrNotExist) {
		return // a new installation: there is nothing to restore
	}
	if err != nil {
		logf("permissions: the snapshot could not be read, so the overrides set since the last restart are not restored: %v", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	n, err := pgstore.RestoreGrants(ctx, db, users)
	if err != nil {
		logf("permissions: restoring the overrides from the snapshot FAILED after %d account(s): %v", n, err)
		return
	}
	logf("permissions: restored the overrides of %d account(s) from the snapshot; from now on they are kept in the database", n)
}
