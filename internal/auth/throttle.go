package auth

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// Throttle slows down repeated failed sign-ins.
//
// Counted against the user name and the source address separately: per-name
// alone lets one address spray a common password across many accounts, and
// per-address alone lets a botnet grind one account from many addresses.
//
// FOUR THINGS IT GOT WRONG BEFORE, each a way round it:
//
//   - The name was counted as TYPED. Sign-in folds case and spaces, so
//     "Boss", "BOSS" and " boss" were one account and three fresh counters.
//     LoginKey normalises the way the account store does.
//   - A successful sign-in cleared the ADDRESS counter too. Anybody holding one
//     valid account could sign in as themselves every seventh guess and spray
//     everybody else's for ever. Success now clears only that account's own
//     counter; the address limit is higher instead, so an office sharing one
//     address is not locked out by a morning of typos.
//   - A lockout never grew. Eight guesses every fifteen minutes is 768 a day
//     against one account, indefinitely. Each lockout within a day now doubles
//     the next, up to a day.
//   - Nothing bounded the table. Failures under random names grew it until the
//     next clean-up; now it holds at most maxThrottleKeys, shedding counters
//     that are not locked before it sheds anything that is.
type Throttle struct {
	mu      sync.Mutex
	fails   map[string]*failCount
	limit   int // failures per window for a name (and anything else)
	ipLimit int // failures per window for an address
	window  time.Duration
	lockout time.Duration // the first lockout; each repeat doubles it
	// noDouble keeps every lockout at lockout, when the policy says so.
	noDouble bool
	lastGC   time.Time
}

type failCount struct {
	n        int
	first    time.Time
	last     time.Time
	lockedTo time.Time
	strikes  int // lockouts in the last day
}

const (
	maxThrottleKeys = 20000
	maxLockout      = 24 * time.Hour
	// strikeMemory is how long a lockout counts towards doubling the next.
	strikeMemory = 24 * time.Hour
)

// NewThrottle creates a limiter allowing limit failures per window before
// locking that key out for lockout. Addresses get four times the limit.
func NewThrottle(limit int, window, lockout time.Duration) *Throttle {
	if limit <= 0 {
		limit = 8
	}
	if window <= 0 {
		window = 15 * time.Minute
	}
	if lockout <= 0 {
		lockout = 15 * time.Minute
	}
	return &Throttle{fails: map[string]*failCount{}, limit: limit, ipLimit: limit * 4,
		window: window, lockout: lockout}
}

// LoginKey is the counter for an account name, normalised exactly as the
// account store normalises it and bounded in length.
func LoginKey(username string) string {
	n := normalizeName(username)
	if r := []rune(n); len(r) > 64 {
		n = string(r[:64])
	}
	return "user:" + n
}

// IPKey is the counter for a source address.
func IPKey(ip string) string { return "ip:" + strings.TrimSpace(ip) }

// PasswordKey is the counter for re-entering one's own current password.
func PasswordKey(username string) string { return "pw:" + normalizeName(username) }

// ResetKey is the counter for wrong codes when resetting a forgotten password.
func ResetKey(username string) string {
	return "reset:" + strings.TrimPrefix(LoginKey(username), "user:")
}

// ResetAddressKey is the counter for reset codes an address asks for: every
// request, whether a code was sent or not, because the answer is the same.
func ResetAddressKey(ip string) string { return "resetip:" + strings.TrimSpace(ip) }

// Blocked reports whether a key is currently locked out, and for how long.
func (t *Throttle) Blocked(key string) (bool, time.Duration) {
	if t == nil {
		return false, 0
	}
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.gcLocked(now, false)

	f, ok := t.fails[key]
	if !ok {
		return false, 0
	}
	if now.Before(f.lockedTo) {
		return true, f.lockedTo.Sub(now)
	}
	return false, 0
}

// Fail records one failed attempt.
func (t *Throttle) Fail(key string) {
	if t == nil {
		return
	}
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()

	f, ok := t.fails[key]
	if !ok {
		if len(t.fails) >= maxThrottleKeys {
			t.gcLocked(now, true)
		}
		f = &failCount{first: now}
		t.fails[key] = f
	}
	if now.Sub(f.first) > t.window {
		f.n, f.first = 0, now
	}
	if now.Sub(f.last) > strikeMemory {
		f.strikes = 0
	}
	f.last = now
	f.n++
	limit := t.limit
	if strings.HasPrefix(key, "ip:") {
		limit = t.ipLimit
	}
	if f.n >= limit {
		d := t.lockout << min(f.strikes, 10)
		if t.noDouble {
			d = t.lockout
		}
		f.lockedTo = now.Add(min(d, maxLockout))
		f.strikes++
		f.n = 0
		f.first = now
	}
}

// Succeed clears a key's failures after a successful sign-in. Its lockout
// history is cleared with it: the person got in, so they are not guessing.
func (t *Throttle) Succeed(key string) { t.Unlock(key) }

// Configure applies the sign-in policy (auth.Policy): limit wrong passwords
// in a row lock a name for lockout, doubling on each repeat within a day
// unless doubles is false. An address gets four times the tries, as before.
// "In a row" is a day: a success clears the count, and nobody's failures
// from yesterday add to today's.
func (t *Throttle) Configure(limit int, lockout time.Duration, doubles bool) {
	if t == nil || limit <= 0 || lockout <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.limit, t.ipLimit, t.lockout, t.noDouble = limit, limit*4, lockout, !doubles
	t.window = 24 * time.Hour
}

// Lockout is one name or address that is locked now, for the security
// centre's list.
type Lockout struct {
	Key   string    `json:"key"`
	Kind  string    `json:"kind"` // "user", "ip", "pw" (the password change's own counter), "reset" or "resetip" (forgotten passwords)
	Name  string    `json:"name"`
	Until time.Time `json:"until"`
	// Strikes is how many times it has been locked in the last day.
	Strikes int `json:"strikes"`
}

// Lockouts lists what is locked now, soonest to unlock first.
func (t *Throttle) Lockouts() []Lockout {
	if t == nil {
		return nil
	}
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []Lockout
	for k, f := range t.fails {
		if !now.Before(f.lockedTo) {
			continue
		}
		kind, name, _ := strings.Cut(k, ":")
		out = append(out, Lockout{Key: k, Kind: kind, Name: name, Until: f.lockedTo, Strikes: f.strikes})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Until.Before(out[j].Until) })
	return out
}

// Unlock forgets a key entirely -- what an administrator resetting somebody's
// password does, so the person can sign in with it straight away.
func (t *Throttle) Unlock(key string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	delete(t.fails, key)
	t.mu.Unlock()
}

// gcLocked drops stale records; force does it now and, if the table is still
// full, sheds counters that are not locked. Callers hold the lock.
func (t *Throttle) gcLocked(now time.Time, force bool) {
	if !force && now.Sub(t.lastGC) < 10*time.Minute {
		return
	}
	t.lastGC = now
	for k, f := range t.fails {
		if now.After(f.lockedTo) && now.Sub(f.last) > strikeMemory {
			delete(t.fails, k)
		}
	}
	if !force || len(t.fails) < maxThrottleKeys {
		return
	}
	// Still full: under a spray of made-up names. Partial counts go before
	// lockouts do, so flooding the table cannot free an account that is
	// currently locked.
	for k, f := range t.fails {
		if len(t.fails) < maxThrottleKeys*9/10 {
			return
		}
		if !now.Before(f.lockedTo) {
			delete(t.fails, k)
		}
	}
}
