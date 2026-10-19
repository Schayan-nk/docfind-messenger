package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// The second sign-in step: a six-digit code from an authenticator app
// (RFC 6238, the kind every phone app reads from a QR code), or one of ten
// single-use recovery codes for the day the phone is lost.
//
// SHA-1, six digits, thirty seconds -- not because they are the strongest
// choices but because they are the ones every authenticator app implements;
// a stronger setting some app silently ignores is a lock-out. The secret is
// 160 random bits, which is what makes the code hard to guess, not the hash.
//
// A code is accepted for the thirty seconds before and after its own, for a
// phone whose clock is a little out, and ONCE: the step it came from is
// recorded and anything at or before it is refused, so a code read over a
// shoulder cannot be replayed in the same minute.
//
// Recovery codes are stored as SHA-256 hashes. Fifty random bits each is too
// many to guess against a throttled sign-in and too many to brute-force from
// a stolen hash in any useful time, so a slow hash would add nothing but
// latency. Each one works once.

const (
	totpPeriod = 30
	totpDigits = 6
	totpSkew   = 1 // steps either side
	// RecoveryCodeCount is how many recovery codes are issued at a time.
	RecoveryCodeCount = 10
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// SecondFactor is an account's authenticator-app step. Carried on the User so
// it travels with the account -- into the database, the JSON file and the
// emergency snapshot alike, which is what keeps emergency sign-in from being
// a way round it.
type SecondFactor struct {
	// Method is how the code arrives: MethodApp (an authenticator app such
	// as Google Authenticator; "" in anything stored before email existed)
	// or MethodEmail (six digits mailed to the account's address).
	Method string `json:"method,omitempty"`
	// Secret is the shared key, SEALED (see Store.UseSealer): base64 of the
	// sealed bytes, never the key itself. Empty for MethodEmail.
	Secret string `json:"secret,omitempty"`
	// Enabled is false while the key is set up but not yet confirmed with a
	// first code -- a key nobody has proved they can read yet.
	Enabled bool `json:"enabled"`
	// LastStep is the time step of the last accepted code (replay guard).
	LastStep int64 `json:"last_step,omitempty"`
	// Recovery is the SHA-256 of each unused recovery code.
	Recovery  []string  `json:"recovery,omitempty"`
	EnabledAt time.Time `json:"enabled_at,omitempty"`
}

// The ways a second-step code arrives. MethodSMS (asked for on 1405/07/08)
// is six digits sent by the SMS panel to the account's phone number, and is
// checked the way a mailed code is: by whoever sent it.
const (
	MethodApp   = "app"
	MethodEmail = "email"
	MethodSMS   = "sms"
)

// sent reports whether codes for this method are sent rather than computed.
func sent(method string) bool { return method == MethodEmail || method == MethodSMS }

// On reports whether this account needs a second step.
func (f *SecondFactor) On() bool {
	return f != nil && f.Enabled && (sent(f.Method) || f.Secret != "")
}

// Via reports how the code arrives: MethodApp, MethodEmail or MethodSMS.
func (f *SecondFactor) Via() string {
	if f != nil && sent(f.Method) {
		return f.Method
	}
	return MethodApp
}

// NewTOTPSecret returns 160 random bits in base32, the form apps expect.
func NewTOTPSecret() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: second-factor key: %w", err)
	}
	return b32.EncodeToString(b), nil
}

func totpAt(secret string, step int64) (string, error) {
	key, err := b32.DecodeString(strings.ToUpper(strings.ReplaceAll(secret, " ", "")))
	if err != nil {
		return "", fmt.Errorf("auth: second-factor key is not base32")
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	bin := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", totpDigits, bin%1_000_000), nil
}

// VerifyTOTP checks code against secret at now, refusing any step at or
// before lastStep. It returns the step that matched, to be stored as the new
// lastStep.
func VerifyTOTP(secret, code string, now time.Time, lastStep int64) (int64, bool) {
	code = strings.TrimSpace(code)
	if len(code) != totpDigits {
		return 0, false
	}
	cur := now.Unix() / totpPeriod
	for d := -totpSkew; d <= totpSkew; d++ {
		step := cur + int64(d)
		if step <= lastStep {
			continue
		}
		want, err := totpAt(secret, step)
		if err != nil {
			return 0, false
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return step, true
		}
	}
	return 0, false
}

// TOTPURI is the address an authenticator app reads from the QR code. Only
// the issuer and the secret: SHA-1, six digits and thirty seconds are every
// app's defaults, and leaving them out keeps the QR code small.
func TOTPURI(issuer, account, secret string) string {
	label := url.PathEscape(issuer) + ":" + url.PathEscape(account)
	q := url.Values{"secret": {secret}, "issuer": {issuer}}
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// Recovery codes: ten characters from an alphabet with no 0/O or 1/I/L to
// misread, shown as xxxxx-xxxxx.
const recoveryAlphabet = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"

// NewRecoveryCodes returns n fresh codes and the hashes to store.
func NewRecoveryCodes(n int) (codes, hashes []string, err error) {
	for i := 0; i < n; i++ {
		raw := make([]byte, 10)
		if _, err := rand.Read(raw); err != nil {
			return nil, nil, fmt.Errorf("auth: recovery code: %w", err)
		}
		var b strings.Builder
		for j, v := range raw {
			if j == 5 {
				b.WriteByte('-')
			}
			// 256 is not a multiple of 31, so this is very slightly uneven;
			// at ten characters it costs well under a bit of the fifty.
			b.WriteByte(recoveryAlphabet[int(v)%len(recoveryAlphabet)])
		}
		code := b.String()
		codes = append(codes, code)
		hashes = append(hashes, hashRecovery(code))
	}
	return codes, hashes, nil
}

func hashRecovery(code string) string {
	norm := strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(code)))
	sum := sha256.Sum256([]byte("docfind-recovery:" + norm))
	return hex.EncodeToString(sum[:])
}

// useRecoveryCode reports whether code is one of hashes, and returns the
// hashes without it. Every entry is compared, so the time taken does not say
// which one matched.
func useRecoveryCode(hashes []string, code string) ([]string, bool) {
	h := hashRecovery(code)
	found := -1
	for i, stored := range hashes {
		if subtle.ConstantTimeCompare([]byte(stored), []byte(h)) == 1 {
			found = i
		}
	}
	if found < 0 {
		return hashes, false
	}
	rest := append(append([]string(nil), hashes[:found]...), hashes[found+1:]...)
	return rest, true
}
