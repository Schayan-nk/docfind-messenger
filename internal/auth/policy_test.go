package auth

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestThePolicyNamesEveryRuleAPasswordBreaks(t *testing.T) {
	p := Policy{MinLength: 12, RequireUpper: true, RequireLower: true, RequireDigit: true,
		RequireSymbol: true, ForbidUsername: true}
	if got := p.Check("z.moradi", "Sunrise.Over-2026"); len(got) != 0 {
		t.Errorf("a good password broke %v", got)
	}
	got := p.Check("z.moradi", "temp.56785678")
	if len(got) != 1 || !strings.Contains(got[0], "بزرگ") {
		t.Errorf("temp.56785678 broke %v, want only the capital", got)
	}
	got = p.Check("z.moradi", "xz.moradix")
	if len(got) < 3 {
		t.Errorf("a password made of the user name broke only %v", got)
	}
	// Persian letters have no case: they count as neither upper nor lower.
	if got := (Policy{RequireUpper: true}).Check("", "رمزعبورمن۱۲۳۴"); len(got) != 1 {
		t.Errorf("a Persian password: %v", got)
	}
	// The floor of ten cannot be lowered.
	if got := (Policy{MinLength: 4}).Normalised().MinLength; got != 10 {
		t.Errorf("min length 4 became %d", got)
	}
	n := (Policy{LockoutAttempts: 1, LockoutMinutes: 99999, MaxAgeDays: -3}).Normalised()
	if n.LockoutAttempts != 3 || n.LockoutMinutes != 1440 || n.MaxAgeDays != 0 {
		t.Errorf("clamped to %+v", n)
	}
	// Every rule in words, for the forms: an empty password breaks them all.
	if got := p.Check("", ""); len(got) != 5 {
		t.Errorf("the rules in words: %v", got)
	}
}

func TestAPasswordGrowsOldOnlyUnderAMaximumAge(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	u := &User{CreatedAt: now.AddDate(-1, 0, 0), PasswordChangedAt: now.AddDate(0, 0, -31)}
	if (Policy{}).Expired(u, now) {
		t.Error("expired with no maximum age")
	}
	if !(Policy{MaxAgeDays: 30}).Expired(u, now) || (Policy{MaxAgeDays: 60}).Expired(u, now) {
		t.Error("31 days against 30 and 60")
	}
	u.PasswordChangedAt = time.Time{}
	if !(Policy{MaxAgeDays: 90}).Expired(u, now) {
		t.Error("an account from a year ago with no recorded change is not judged from its creation")
	}
}

// The lockout the policy sets: N wrong in a row lock for M minutes, the same
// M every time when doubling is off, and the list shows who.
func TestTheThrottleLocksAsThePolicySays(t *testing.T) {
	th := NewThrottle(8, 15*time.Minute, 15*time.Minute)
	th.Configure(3, 2*time.Minute, false)
	k := LoginKey("f.ebrahimi")
	for i := 0; i < 2; i++ {
		th.Fail(k)
	}
	if blocked, _ := th.Blocked(k); blocked {
		t.Fatal("locked after 2 of 3")
	}
	th.Fail(k)
	blocked, wait := th.Blocked(k)
	if !blocked || wait > 2*time.Minute || wait < time.Minute {
		t.Fatalf("after 3: blocked=%v for %v", blocked, wait)
	}
	locks := th.Lockouts()
	if len(locks) != 1 || locks[0].Kind != "user" || locks[0].Name != "f.ebrahimi" {
		t.Fatalf("lockouts %+v", locks)
	}
	th.Unlock(k)
	if blocked, _ := th.Blocked(k); blocked || len(th.Lockouts()) != 0 {
		t.Error("still locked after unlocking")
	}
	// A second lock in the day is not doubled.
	for i := 0; i < 3; i++ {
		th.Fail(k)
	}
	th.mu.Lock()
	th.fails[k].strikes = 3
	th.mu.Unlock()
	for i := 0; i < 3; i++ {
		th.Fail(k)
	}
	if _, wait := th.Blocked(k); wait > 2*time.Minute {
		t.Errorf("doubling off, yet locked for %v", wait)
	}
}

// With two steps required, an account without one reaches the setup and
// nothing else.
func TestTwoStepRequiredLetsOnlyTheSetupThrough(t *testing.T) {
	for path, want := range map[string]bool{
		"GET /api/account/two-step": true, "POST /api/account/two-step/begin": true,
		"POST /api/account/two-step/email/confirm": true, "POST /api/account/passkeys/options": true,
		"POST /api/account/passkeys": true, "GET /api/status": false, "POST /api/account/password/x": false,
		"POST /api/account/two-step/../../users": false, "DELETE /api/account/two-step": false,
	} {
		method, p, _ := strings.Cut(path, " ")
		r := httptest.NewRequest(method, "http://x/", nil)
		r.URL.Path = p
		if got := twoStepSetupAllows(r); got != want {
			t.Errorf("%s: %v, want %v", path, got, want)
		}
	}
	if HasTwoStep(&User{}) || !HasTwoStep(&User{Passkeys: []Passkey{{}}}) {
		t.Error("HasTwoStep")
	}
}
