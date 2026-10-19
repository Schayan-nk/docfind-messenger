package pgstore

import (
	"context"
	"testing"
	"time"

	"github.com/Schayan-nk/docfind-messenger/internal/auth"
)

// freshAccounts returns an auth.Backend over the real database, with the test
// rows cleaned up afterwards.
//
// Every username here starts "pgt-" so the cleanup can be precise. A test that
// truncates the table would work today and destroy somebody's accounts the
// first time it is pointed at a database with real ones in it.
func freshAccounts(t *testing.T) *Accounts {
	t.Helper()
	db := testDB(t)
	ctx := context.Background()
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	clean := func() { db.Pool().Exec(ctx, `DELETE FROM users WHERE username LIKE 'pgt-%'`) }
	clean()
	t.Cleanup(clean)
	return NewAccounts(db)
}

// A round trip must preserve EVERY field. Checked one at a time rather than
// with a struct comparison, so a field that is not persisted names itself in
// the failure instead of producing "structs differ".
func TestAnAccountSurvivesARoundTripWithEveryField(t *testing.T) {
	a := freshAccounts(t)
	// Truncated to the second: PostgreSQL keeps microseconds and Go keeps
	// nanoseconds, so comparing raw timestamps fails for a reason that has
	// nothing to do with whether the account was stored.
	now := time.Now().UTC().Truncate(time.Second)
	want := &auth.User{
		Username: "pgt-ali", DisplayName: "Ali", Email: "ali@example.com",
		PasswordHash: "pbkdf2-sha256$600000$c2FsdA$aGFzaA", Role: auth.RoleManager,
		Disabled: true, CreatedAt: now, LastLogin: now, TokenVersion: 3,
		Avatar: "png", EnglishName: "Ali Reza",
		Prefs: auth.Prefs{FontScale: 115, Density: "compact", ReduceMotion: true},
		SecondFactor: &auth.SecondFactor{Secret: "c2VhbGVkLWtleQ==", Enabled: true, LastStep: 58_000_123,
			Recovery: []string{"aa11", "bb22"}, EnabledAt: now},
		Passkeys: []auth.Passkey{{ID: []byte{1, 2, 3}, PublicKey: []byte{0xa5, 1}, Alg: -7,
			SignCount: 9, Name: "گوشی", CreatedAt: now}},
		PasskeyHandle: "aGFuZGxl",
		Grants:        map[string]string{"page:settings": "allow", "report:salaries": "deny"},
	}
	// A second account with no second factor: "password only" must come back
	// as nil, not as an empty document that reads as "set up, not confirmed".
	plain := &auth.User{Username: "pgt-plain", DisplayName: "Plain", Role: auth.RoleViewer,
		PasswordHash: "x", CreatedAt: now}
	if err := a.Save([]*auth.User{want, plain}); err != nil {
		t.Fatal(err)
	}
	loaded, err := a.Load()
	if err != nil {
		t.Fatal(err)
	}
	var got *auth.User
	for _, u := range loaded {
		if u.Username == want.Username {
			got = u
		}
		if u.Username == plain.Username && u.SecondFactor != nil {
			t.Errorf("an account with no second factor came back with one: %+v", u.SecondFactor)
		}
		if u.Username == plain.Username && u.Grants != nil {
			t.Errorf("an account with no overrides came back with some: %v", u.Grants)
		}
	}
	if got == nil {
		t.Fatalf("the account was not loaded back; got %d accounts", len(loaded))
	}
	for _, c := range []struct {
		field string
		a, b  any
	}{
		{"DisplayName", got.DisplayName, want.DisplayName},
		{"Email", got.Email, want.Email},
		{"PasswordHash", got.PasswordHash, want.PasswordHash},
		{"Role", got.Role, want.Role},
		{"Disabled", got.Disabled, want.Disabled},
		{"TokenVersion", got.TokenVersion, want.TokenVersion},
		{"Avatar", got.Avatar, want.Avatar},
		{"EnglishName", got.EnglishName, want.EnglishName},
		{"Prefs.FontScale", got.Prefs.FontScale, want.Prefs.FontScale},
		{"Prefs.Density", got.Prefs.Density, want.Prefs.Density},
		{"Prefs.ReduceMotion", got.Prefs.ReduceMotion, want.Prefs.ReduceMotion},
	} {
		if c.a != c.b {
			t.Errorf("%s: got %v, want %v", c.field, c.a, c.b)
		}
	}
	if !got.CreatedAt.Equal(want.CreatedAt) {
		t.Errorf("CreatedAt: got %v, want %v", got.CreatedAt, want.CreatedAt)
	}
	if !got.LastLogin.Equal(want.LastLogin) {
		t.Errorf("LastLogin: got %v, want %v", got.LastLogin, want.LastLogin)
	}
	f := got.SecondFactor
	if f == nil || f.Secret != want.SecondFactor.Secret || !f.Enabled || f.LastStep != 58_000_123 ||
		len(f.Recovery) != 2 || f.Recovery[1] != "bb22" || !f.EnabledAt.Equal(now) {
		t.Errorf("SecondFactor: got %+v, want %+v", f, want.SecondFactor)
	}
	if len(got.Passkeys) != 1 || got.Passkeys[0].SignCount != 9 || got.Passkeys[0].Name != "گوشی" ||
		string(got.Passkeys[0].ID) != "\x01\x02\x03" || got.PasskeyHandle != "aGFuZGxl" {
		t.Errorf("Passkeys: got %+v, handle %q", got.Passkeys, got.PasskeyHandle)
	}
	// The permission overrides: never stored before migration 041, so every
	// restart handed everybody back their role's default.
	if len(got.Grants) != 2 || got.Grants["page:settings"] != "allow" || got.Grants["report:salaries"] != "deny" {
		t.Errorf("Grants: got %v, want %v", got.Grants, want.Grants)
	}
}

// The first start with the grants column restores them from the snapshot,
// only where the database holds none.
func TestRestoreGrantsFillsOnlyAccountsWithNone(t *testing.T) {
	a := freshAccounts(t)
	now := time.Now().UTC()
	kept := &auth.User{Username: "pgt-kept", Role: auth.RoleViewer, PasswordHash: "x", CreatedAt: now,
		Grants: map[string]string{"page:settings": "deny"}}
	bare := &auth.User{Username: "pgt-bare", Role: auth.RoleViewer, PasswordHash: "x", CreatedAt: now}
	if err := a.Save([]*auth.User{kept, bare}); err != nil {
		t.Fatal(err)
	}
	snap := []*auth.User{
		{Username: "pgt-kept", Grants: map[string]string{"page:settings": "allow"}},
		{Username: "pgt-bare", Grants: map[string]string{"action:notifications": "allow"}},
		{Username: "pgt-gone", Grants: map[string]string{"page:settings": "allow"}},
	}
	n, err := RestoreGrants(context.Background(), a.db, snap)
	if err != nil || n != 1 {
		t.Fatalf("restored %d (%v), want 1", n, err)
	}
	loaded, err := a.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range loaded {
		switch u.Username {
		case "pgt-kept":
			if u.Grants["page:settings"] != "deny" {
				t.Errorf("the database's own grants were overwritten: %v", u.Grants)
			}
		case "pgt-bare":
			if u.Grants["action:notifications"] != "allow" {
				t.Errorf("the snapshot's grants were not restored: %v", u.Grants)
			}
		case "pgt-gone":
			t.Error("an account only in the snapshot was created")
		}
	}
}

// THE PASSWORD HASH IS OPAQUE AND MUST SURVIVE BYTE FOR BYTE. It carries $
// separators and base64 that includes + and / and =, any of which a careless
// round trip could mangle -- and an altered hash locks the account out with no
// way back except an administrator resetting it.
func TestThePasswordHashSurvivesByteForByte(t *testing.T) {
	a := freshAccounts(t)
	hashes := []string{
		"pbkdf2-sha256$600000$YWJjZGVmZ2hpams=$Zm9vYmFyL2Jhei0rXzE9",
		"pbkdf2-sha256$600000$+/=$+/=",
		"pbkdf2-sha256$600000$c2FsdHk$aGFzaHk",
	}
	users := make([]*auth.User, 0, len(hashes))
	for i, h := range hashes {
		users = append(users, &auth.User{
			Username: "pgt-h" + string(rune('a'+i)), PasswordHash: h, Role: auth.RoleViewer,
		})
	}
	if err := a.Save(users); err != nil {
		t.Fatal(err)
	}
	loaded, err := a.Load()
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]string{}
	for _, u := range loaded {
		byName[u.Username] = u.PasswordHash
	}
	for i, want := range hashes {
		name := "pgt-h" + string(rune('a'+i))
		if got := byName[name]; got != want {
			t.Errorf("%s: hash changed\n got  %q\n want %q", name, got, want)
		}
	}
}

// Save replaces the whole set, which is what writing the accounts file has
// always meant. An account removed from the set is removed from the database.
func TestSaveReplacesTheWholeSet(t *testing.T) {
	a := freshAccounts(t)
	two := []*auth.User{
		{Username: "pgt-one", PasswordHash: "h", Role: auth.RoleViewer},
		{Username: "pgt-two", PasswordHash: "h", Role: auth.RoleViewer},
	}
	if err := a.Save(two); err != nil {
		t.Fatal(err)
	}
	if n := countPGT(t, a); n != 2 {
		t.Fatalf("after saving two, %d rows", n)
	}
	// Drop one, keep the other.
	if err := a.Save(two[:1]); err != nil {
		t.Fatal(err)
	}
	if n := countPGT(t, a); n != 1 {
		t.Errorf("after saving one, %d rows; Save must replace, not merge", n)
	}
	loaded, _ := a.Load()
	for _, u := range loaded {
		if u.Username == "pgt-two" {
			t.Error("pgt-two survived a Save that did not include it")
		}
	}
}

// Saving twice must update rather than fail or duplicate.
func TestSaveTwiceUpdatesRatherThanDuplicating(t *testing.T) {
	a := freshAccounts(t)
	u := &auth.User{Username: "pgt-b", PasswordHash: "h", Role: auth.RoleViewer}
	if err := a.Save([]*auth.User{u}); err != nil {
		t.Fatal(err)
	}
	u.DisplayName = "Renamed"
	u.Prefs.FontScale = 130
	if err := a.Save([]*auth.User{u}); err != nil {
		t.Fatalf("second save: %v", err)
	}
	if n := countPGT(t, a); n != 1 {
		t.Errorf("%d rows for one account", n)
	}
	loaded, _ := a.Load()
	for _, x := range loaded {
		if x.Username != "pgt-b" {
			continue
		}
		if x.DisplayName != "Renamed" {
			t.Errorf("the update did not take: %q", x.DisplayName)
		}
		if x.Prefs.FontScale != 130 {
			t.Errorf("preferences did not update: %d", x.Prefs.FontScale)
		}
	}
}

// AN EMPTY WHOLE-SET SAVE IS REFUSED WHEN THE TABLE IS NOT EMPTY.
//
// Not in the original plan, and added because the consequence is
// unrecoverable. Save's contract is "replace everything", so an empty slice
// reaching it -- from a bug anywhere above, a failed load treated as success,
// a slice that was filtered too hard -- would delete every account in the
// company and lock everybody out of the application. There is no legitimate
// path to it: auth.Store refuses to remove the last administrator, so the set
// never legitimately empties. Refusing costs nothing and removes a
// catastrophic failure mode.
func TestAnEmptySaveIsRefusedWhenAccountsExist(t *testing.T) {
	a := freshAccounts(t)
	if err := a.Save([]*auth.User{
		{Username: "pgt-keep", PasswordHash: "h", Role: auth.RoleAdmin},
	}); err != nil {
		t.Fatal(err)
	}
	err := a.Save(nil)
	if err == nil {
		t.Fatal("an empty Save was accepted; that deletes every account")
	}
	if n := countPGT(t, a); n != 1 {
		t.Errorf("the refused Save still changed the table: %d rows", n)
	}
	// And the same for an explicitly empty slice, not just nil.
	if err := a.Save([]*auth.User{}); err == nil {
		t.Error("an empty (non-nil) Save was accepted")
	}
}

// A first save into an empty database is allowed, because that is a fresh
// install and there is nothing to lose.
func TestAnEmptySaveIsFineWhenThereAreNoAccounts(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var existing int
	if err := db.Pool().QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&existing); err != nil {
		t.Fatal(err)
	}
	if existing > 0 {
		t.Skip("this database has accounts in it; the empty-database case cannot be tested non-destructively")
	}
	if err := NewAccounts(db).Save(nil); err != nil {
		t.Errorf("an empty save into an empty database failed: %v", err)
	}
}

// Healthy is what decides whether the application offers a write control at
// all, so a cached yes from a minute ago is worse than no answer.
func TestHealthyIsFalseWhenTheDatabaseIsGone(t *testing.T) {
	a := freshAccounts(t)
	if !a.Healthy() {
		t.Fatal("a live database reports unhealthy")
	}
	if a.Describe() == "" {
		t.Error("Describe() says nothing; it reaches the health page")
	}
	a.db.Close()
	if a.Healthy() {
		t.Error("a closed database still reports healthy")
	}
}

// A never-signed-in account has a ZERO LastLogin, not a date pretending to be
// one. The column is nullable for exactly this reason, and the settings page
// prints "never" from it.
func TestANeverSignedInAccountHasAZeroLastLogin(t *testing.T) {
	a := freshAccounts(t)
	if err := a.Save([]*auth.User{
		{Username: "pgt-new", PasswordHash: "h", Role: auth.RoleViewer},
	}); err != nil {
		t.Fatal(err)
	}
	loaded, _ := a.Load()
	for _, u := range loaded {
		if u.Username != "pgt-new" {
			continue
		}
		if !u.LastLogin.IsZero() {
			t.Errorf("LastLogin is %v for an account that never signed in", u.LastLogin)
		}
	}
}

func countPGT(t *testing.T, a *Accounts) int {
	t.Helper()
	var n int
	if err := a.db.Pool().QueryRow(context.Background(),
		`SELECT count(*) FROM users WHERE username LIKE 'pgt-%'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
