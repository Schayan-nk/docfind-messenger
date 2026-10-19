package pgstore

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/Schayan-nk/docfind-messenger/internal/auth"
)

// ImportAccounts copies the accounts file into an EMPTY database, once.
//
// ONLY INTO AN EMPTY TABLE, and that is the whole safety property. The
// accounts file is where accounts lived before the database: it is the
// starting point, not a master copy. Running this against a populated database
// would undo every change made since the move -- a password reset, a disabled
// account, a revoked token -- and would do it silently, because the file still
// looks plausible. So it counts first and does nothing if anything is there.
//
// THE PASSWORD HASH IS COPIED BYTE FOR BYTE. It is a PBKDF2-SHA256 hash and
// this is not the place to re-derive anything: an altered hash locks the
// account out, and an import that locks out the whole company is worse than no
// import at all.
//
// Returns how many accounts it moved, so the caller can say so in a log line
// -- "imported 0" and "did not run" look identical otherwise.
func ImportAccounts(ctx context.Context, db *DB, jsonPath string) (int, error) {
	if db == nil {
		return 0, fmt.Errorf("pgstore: no connection")
	}
	if jsonPath == "" {
		return 0, nil
	}

	var existing int
	if err := db.Pool().QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&existing); err != nil {
		return 0, fmt.Errorf("pgstore: counting accounts before import: %w", err)
	}
	if existing > 0 {
		return 0, nil
	}

	raw, err := os.ReadFile(jsonPath)
	if err != nil {
		if os.IsNotExist(err) {
			// A new deployment has no accounts file. Not an error: the
			// first-administrator screen handles it.
			return 0, nil
		}
		return 0, fmt.Errorf("pgstore: reading %s: %w", jsonPath, err)
	}

	var users []*auth.User
	if err := json.Unmarshal(raw, &users); err != nil {
		return 0, fmt.Errorf("pgstore: %s is not a valid accounts file: %w", jsonPath, err)
	}

	keep := make([]*auth.User, 0, len(users))
	for _, u := range users {
		if u == nil || u.Username == "" {
			continue
		}
		keep = append(keep, u)
	}
	if len(keep) == 0 {
		return 0, nil
	}

	// One Save, so the import is one transaction: a half-imported account list
	// is a list somebody is locked out of, and re-running would then see a
	// non-empty table and refuse to finish the job.
	if err := NewAccounts(db).Save(keep); err != nil {
		return 0, fmt.Errorf("pgstore: importing %s: %w", jsonPath, err)
	}
	return len(keep), nil
}
