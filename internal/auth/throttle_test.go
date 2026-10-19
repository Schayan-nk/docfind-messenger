package auth

import (
	"fmt"
	"testing"
	"time"
)

// Sign-in folds case and spaces, so the counter must too: otherwise every
// spelling of one account is a fresh set of guesses.
func TestEverySpellingOfANameSharesOneCounter(t *testing.T) {
	th := NewThrottle(3, time.Minute, time.Minute)
	for _, typed := range []string{"Boss", "BOSS", " boss "} {
		th.Fail(LoginKey(typed))
	}
	if blocked, _ := th.Blocked(LoginKey("boss")); !blocked {
		t.Error("three spellings of one account were three separate counters")
	}
}

func TestAnAddressGetsMoreRoomThanAName(t *testing.T) {
	th := NewThrottle(3, time.Minute, time.Minute)
	ip := IPKey("192.168.77.50")
	for range 11 {
		th.Fail(ip)
	}
	if blocked, _ := th.Blocked(ip); blocked {
		t.Fatal("an address was locked at the per-name limit; an office behind one address would be locked out by typos")
	}
	th.Fail(ip)
	if blocked, _ := th.Blocked(ip); !blocked {
		t.Error("an address was never locked")
	}
}

// Somebody's own successful sign-in must not wipe the address's record of
// guesses at other accounts.
func TestSucceedingAsOneAccountDoesNotResetTheAddress(t *testing.T) {
	th := NewThrottle(3, time.Minute, time.Minute)
	ip := IPKey("10.0.0.9")
	for i := range 11 {
		th.Fail(ip)
		th.Fail(LoginKey(fmt.Sprintf("victim%d", i)))
		th.Succeed(LoginKey("attacker")) // their own account, every time
	}
	th.Fail(ip)
	if blocked, _ := th.Blocked(ip); !blocked {
		t.Error("the attacker's own sign-ins kept the address counter clear")
	}
}

func TestRepeatedLockoutsGrow(t *testing.T) {
	th := NewThrottle(2, time.Minute, 40*time.Millisecond)
	key := LoginKey("target")
	lockFor := func() time.Duration {
		th.Fail(key)
		th.Fail(key)
		_, wait := th.Blocked(key)
		return wait
	}
	first := lockFor()
	time.Sleep(first + 10*time.Millisecond)
	second := lockFor()
	if second <= first+10*time.Millisecond {
		t.Errorf("the second lockout (%v) was not longer than the first (%v)", second, first)
	}
}

// Flooding the table with made-up names must neither grow it without bound
// nor release an account that is locked.
func TestAFloodOfNamesCannotFreeALockedAccount(t *testing.T) {
	th := NewThrottle(2, time.Minute, time.Hour)
	locked := LoginKey("boss")
	th.Fail(locked)
	th.Fail(locked)
	for i := range maxThrottleKeys + 500 {
		th.Fail(LoginKey(fmt.Sprintf("junk-%d", i)))
	}
	th.mu.Lock()
	n := len(th.fails)
	th.mu.Unlock()
	if n > maxThrottleKeys {
		t.Errorf("the table grew to %d entries", n)
	}
	if blocked, _ := th.Blocked(locked); !blocked {
		t.Error("a flood of junk names released a locked account")
	}
}

func TestUnlockLetsSomebodyStraightBackIn(t *testing.T) {
	th := NewThrottle(1, time.Minute, time.Hour)
	key := LoginKey("sadeghi")
	th.Fail(key)
	if blocked, _ := th.Blocked(key); !blocked {
		t.Fatal("not locked")
	}
	th.Unlock(key)
	if blocked, _ := th.Blocked(key); blocked {
		t.Error("still locked after Unlock")
	}
}

func TestLoginKeysAreBounded(t *testing.T) {
	long := string(make([]rune, 5000))
	if n := len([]rune(LoginKey(long))); n > 70 {
		t.Errorf("a 5000-character name made a %d-character key", n)
	}
}
