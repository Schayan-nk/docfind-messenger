package auth

import (
	"strings"
	"sync/atomic"
	"time"
	"unicode"
)

// The sign-in policy an administrator sets in the security centre (asked for
// on 1405/07/12): how many wrong passwords lock an account and for how long,
// what a password must contain, how old it may get, and whether everybody
// must sign in in two steps. Kept as one document in app_settings
// (find/securitypolicy.go); applied here, by the Guard and its Throttle.
//
// A password that fails the policy is refused when somebody chooses it, and
// an account whose current password fails it -- or has grown too old -- is
// asked for a new one at its next sign-in, through the same update_password
// gate a temporary password uses. Nobody is locked out by a policy change:
// the one moment the server sees a password is the sign-in, and that is when
// it asks.
type Policy struct {
	// LockoutAttempts wrong passwords in a row lock the account for
	// LockoutMinutes. LockoutDoubles doubles each further lockout within a
	// day, which is what stops a slow guesser who waits out every lock.
	LockoutAttempts int  `json:"lockout_attempts"`
	LockoutMinutes  int  `json:"lockout_minutes"`
	LockoutDoubles  bool `json:"lockout_doubles"`

	MinLength      int  `json:"min_length"`
	RequireUpper   bool `json:"require_upper"`
	RequireLower   bool `json:"require_lower"`
	RequireDigit   bool `json:"require_digit"`
	RequireSymbol  bool `json:"require_symbol"`
	ForbidUsername bool `json:"forbid_username"`
	// MaxAgeDays asks for a new password once the current one is this old;
	// zero never does.
	MaxAgeDays int `json:"max_age_days"`

	// RequireTwoStep: an account with neither two-step sign-in nor a passkey
	// may do nothing but set one up.
	RequireTwoStep bool `json:"require_two_step"`
}

// Bounds. The floor of ten characters is the one this program has always
// had; a policy can raise it, never lower it.
const (
	policyMinLength   = 10
	policyMaxLength   = 128
	policyMinAttempts = 3
	policyMaxAttempts = 50
	policyMaxMinutes  = 24 * 60
	policyMaxAgeDays  = 3650
)

// DefaultPolicy is what applies until an administrator saves one: what the
// server did before there was a policy -- eight tries, fifteen minutes,
// doubling, ten characters, nothing else.
func DefaultPolicy() Policy {
	return Policy{LockoutAttempts: 8, LockoutMinutes: 15, LockoutDoubles: true, MinLength: policyMinLength}
}

// Normalised clamps every number into its range.
func (p Policy) Normalised() Policy {
	clampInt := func(v, lo, hi, def int) int {
		if v == 0 {
			return def
		}
		return max(lo, min(hi, v))
	}
	d := DefaultPolicy()
	p.LockoutAttempts = clampInt(p.LockoutAttempts, policyMinAttempts, policyMaxAttempts, d.LockoutAttempts)
	p.LockoutMinutes = clampInt(p.LockoutMinutes, 1, policyMaxMinutes, d.LockoutMinutes)
	p.MinLength = clampInt(p.MinLength, policyMinLength, policyMaxLength, d.MinLength)
	if p.MaxAgeDays < 0 {
		p.MaxAgeDays = 0
	}
	p.MaxAgeDays = min(p.MaxAgeDays, policyMaxAgeDays)
	return p
}

// Check returns, in Persian, every rule password breaks; none means it passes.
func (p Policy) Check(username, password string) []string {
	p = p.Normalised()
	var out []string
	if n := len([]rune(password)); n < p.MinLength {
		out = append(out, "دست‌کم "+faDigits(p.MinLength)+" نویسه")
	}
	var upper, lower, digit, symbol bool
	for _, r := range password {
		switch {
		case unicode.IsUpper(r):
			upper = true
		case unicode.IsLower(r):
			lower = true
		case unicode.IsDigit(r):
			digit = true
		case unicode.IsLetter(r):
			// A letter with no case -- Persian -- counts as neither.
		case !unicode.IsSpace(r):
			symbol = true
		}
	}
	if p.RequireUpper && !upper {
		out = append(out, "یک حرف بزرگ لاتین (A تا Z)")
	}
	if p.RequireLower && !lower {
		out = append(out, "یک حرف کوچک لاتین (a تا z)")
	}
	if p.RequireDigit && !digit {
		out = append(out, "یک رقم")
	}
	if p.RequireSymbol && !symbol {
		out = append(out, "یک نماد مانند ! @ # . -")
	}
	if p.ForbidUsername && len(username) >= 3 &&
		strings.Contains(strings.ToLower(password), strings.ToLower(username)) {
		out = append(out, "نام کاربری نباید در گذرواژه باشد")
	}
	return out
}

// Expired reports whether u's password is older than the policy allows. An
// account that has never recorded a change is judged from when it was made.
func (p Policy) Expired(u *User, now time.Time) bool {
	if p.MaxAgeDays <= 0 || u == nil {
		return false
	}
	since := u.PasswordChangedAt
	if since.IsZero() {
		since = u.CreatedAt
	}
	if since.IsZero() {
		return false
	}
	return now.Sub(since) > time.Duration(p.MaxAgeDays)*24*time.Hour
}

// HasTwoStep is whether u signs in with more than a password: a second
// factor, or a passkey (which replaces the password and the code together).
func HasTwoStep(u *User) bool {
	return u != nil && (u.SecondFactor.On() || len(u.Passkeys) > 0)
}

func faDigits(n int) string {
	const fa = "۰۱۲۳۴۵۶۷۸۹"
	s := []rune{}
	for _, r := range []rune(itoa(n)) {
		s = append(s, []rune(fa)[r-'0'])
	}
	return string(s)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

// policyHolder is the Guard's current policy, swapped whole when an
// administrator saves one.
type policyHolder struct{ p atomic.Pointer[Policy] }

func (h *policyHolder) get() Policy {
	if p := h.p.Load(); p != nil {
		return *p
	}
	return DefaultPolicy()
}

func (h *policyHolder) set(p Policy) { p = p.Normalised(); h.p.Store(&p) }
