package find

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Schayan-nk/docfind-messenger/internal/auth"
)

// accountsTestServer returns a server with accounts on and the permission
// catalogue installed as main does it, one administrator ("boss") and one
// viewer ("reader"), and their session tokens. The fourth value is the
// accounts file.
//
// The catalogue matters: without it every permission is denied to everybody
// but an administrator, so a test server that skipped it would be exercising
// a fail-closed default rather than the access model people actually get.
func accountsTestServer(t *testing.T) (*Server, string, string, string) {
	t.Helper()
	dir := tempDir(t)

	usersFile := filepath.Join(dir, "users.json")
	users, err := auth.OpenStore(usersFile)
	if err != nil {
		t.Fatalf("accounts: %v", err)
	}
	if _, err := users.Create("boss", "Boss", "correct horse battery", auth.RoleAdmin); err != nil {
		t.Fatalf("creating the admin: %v", err)
	}
	if _, err := users.Create("reader", "Reader", "correct horse battery", auth.RoleViewer); err != nil {
		t.Fatalf("creating the viewer: %v", err)
	}

	key, err := auth.LoadOrCreateKey(filepath.Join(dir, "key"))
	if err != nil {
		t.Fatalf("signing key: %v", err)
	}
	signer := auth.NewSigner(key, time.Hour)

	s := NewServer("")
	s.UseAccounts(users, signer, false)
	s.UsePermissions(auth.NewCatalogue())

	return s, signFor(t, signer, users, "boss"), signFor(t, signer, users, "reader"), usersFile
}
