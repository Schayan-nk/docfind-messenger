package pgstore

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// writeAccountsFile builds an accounts file in the shape the real one has.
//
// Written as raw JSON rather than by marshalling auth.User, so the test
// exercises the same parsing path a real file does -- including the field
// names, which are what an actual users.json contains.
func writeAccountsFile(t *testing.T, dir string, rows []map[string]any) string {
	t.Helper()
	raw, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "users.json")
	if err := os.WriteFile(p, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// emptyDB gives an empty users table, or skips.
//
// Skipped rather than emptied when rows exist: this test cannot truncate a
// table that might hold somebody's real accounts, and saying so is better than
// a test that is destructive on one machine and green on another.
func emptyDB(t *testing.T) *DB {
	t.Helper()
	db := testDB(t)
	ctx := context.Background()
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	// Only the test rows are cleared -- never the whole table.
	db.Pool().Exec(ctx, `DELETE FROM users WHERE username LIKE 'imp-%'`)
	t.Cleanup(func() {
		db.Pool().Exec(context.Background(), `DELETE FROM users WHERE username LIKE 'imp-%'`)
	})
	var n int
	if err := db.Pool().QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n > 0 {
		t.Skipf("the database already holds %d account(s); the import path only runs on an empty table", n)
	}
	return db
}

// The import runs once, and the hashes survive exactly.
func TestImportMovesTheAccountsAndKeepsTheHashes(t *testing.T) {
	db := emptyDB(t)
	ctx := context.Background()
	path := writeAccountsFile(t, t.TempDir(), []map[string]any{
		{"username": "imp-ali", "display_name": "Ali", "role": "admin",
			"password_hash": "pbkdf2-sha256$600000$c2FsdA==$aGFzaCsvPQ==",
			"token_version": 2},
		{"username": "imp-reza", "display_name": "Reza", "role": "viewer",
			"password_hash": "pbkdf2-sha256$600000$b3RoZXI=$b3RoZXJoYXNo"},
	})

	n, err := ImportAccounts(ctx, db, path)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("imported %d accounts, want 2", n)
	}

	// The hash must survive byte for byte, or every imported account is locked
	// out and the only recovery is resetting the whole company's passwords.
	var hash string
	if err := db.Pool().QueryRow(ctx,
		`SELECT password_hash FROM users WHERE username = 'imp-ali'`).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if hash != "pbkdf2-sha256$600000$c2FsdA==$aGFzaCsvPQ==" {
		t.Errorf("the hash was altered on import: %q", hash)
	}
	// And the other fields came across.
	var role string
	var tv int
	if err := db.Pool().QueryRow(ctx,
		`SELECT role, token_version FROM users WHERE username = 'imp-ali'`).
		Scan(&role, &tv); err != nil {
		t.Fatal(err)
	}
	if role != "admin" || tv != 2 {
		t.Errorf("fields did not survive: role=%q token_version=%d", role, tv)
	}
}

// THE IMPORTANT ONE: a second run must be a no-op. The accounts file is a
// starting point, not a master copy, so re-importing over a populated database
// would silently undo every change made since the move.
func TestASecondImportIsANoOp(t *testing.T) {
	db := emptyDB(t)
	ctx := context.Background()
	path := writeAccountsFile(t, t.TempDir(), []map[string]any{
		{"username": "imp-ali", "display_name": "Ali", "role": "admin",
			"password_hash": "original-hash"},
	})

	if n, err := ImportAccounts(ctx, db, path); err != nil || n != 1 {
		t.Fatalf("first import: %d, %v", n, err)
	}

	// Simulate a change made after the move: the password was reset.
	if _, err := db.Pool().Exec(ctx,
		`UPDATE users SET password_hash = 'changed-since-the-move' WHERE username = 'imp-ali'`); err != nil {
		t.Fatal(err)
	}

	again, err := ImportAccounts(ctx, db, path)
	if err != nil {
		t.Fatal(err)
	}
	if again != 0 {
		t.Errorf("a second import moved %d account(s); it must be a no-op", again)
	}

	var hash string
	if err := db.Pool().QueryRow(ctx,
		`SELECT password_hash FROM users WHERE username = 'imp-ali'`).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if hash != "changed-since-the-move" {
		t.Errorf("the import overwrote a change made after the move: %q", hash)
	}
}

// A missing accounts file is the ordinary case for a new deployment.
func TestImportWithNoFileIsNotAnError(t *testing.T) {
	db := emptyDB(t)
	n, err := ImportAccounts(context.Background(), db, filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Errorf("a missing accounts file was an error: %v", err)
	}
	if n != 0 {
		t.Errorf("imported %d from a file that does not exist", n)
	}
}

// An accounts file with no usable rows imports nothing rather than tripping
// Save's empty-set guard and failing the whole start-up.
func TestAnEmptyAccountsFileImportsNothingWithoutFailing(t *testing.T) {
	db := emptyDB(t)
	for _, rows := range [][]map[string]any{
		{},
		{{"username": "", "role": "viewer", "password_hash": "x"}},
	} {
		path := writeAccountsFile(t, t.TempDir(), rows)
		n, err := ImportAccounts(context.Background(), db, path)
		if err != nil {
			t.Errorf("%v: %v", rows, err)
		}
		if n != 0 {
			t.Errorf("%v imported %d", rows, n)
		}
	}
}

// A corrupt accounts file is reported rather than treated as empty. Treating
// it as empty would leave the database unpopulated and hand the application to
// the first-administrator screen.
func TestACorruptAccountsFileIsReported(t *testing.T) {
	db := emptyDB(t)
	p := filepath.Join(t.TempDir(), "users.json")
	if err := os.WriteFile(p, []byte("{not json at all"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportAccounts(context.Background(), db, p); err == nil {
		t.Error("a corrupt accounts file was accepted as empty")
	}
}
