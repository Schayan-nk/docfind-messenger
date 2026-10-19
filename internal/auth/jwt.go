package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// JWT support, written against the standard library rather than pulled in as a
// dependency: a JWT is base64url segments joined by dots and signed, and the
// whole of what this application needs is under a hundred lines. The parts
// that are easy to get wrong are called out where they happen.
//
// Tokens are revocable. A signed token normally cannot be withdrawn before it
// expires, which would mean disabling an account or changing a password left
// that person signed in until their token aged out -- the wrong behaviour for
// an application gating access to company documents. Each token therefore
// carries the account's token version, and anything that should end a session
// bumps that version, invalidating every token already issued to them.

// ErrInvalidToken covers every verification failure. Callers get one error so
// that a malformed token, a bad signature and an expired one are
// indistinguishable to whoever sent it.
var ErrInvalidToken = errors.New("auth: invalid token")

// Claims is the payload carried in a token.
type Claims struct {
	Subject  string `json:"sub"`
	Version  int    `json:"ver"`
	IssuedAt int64  `json:"iat"`
	Expires  int64  `json:"exp"`
	// SID names this particular sign-in, so one device can be told apart from
	// another belonging to the same person. It exists so the account screen
	// can list "where am I signed in"; verification does not consult it, and a
	// token minted before this field existed simply has none.
	SID string `json:"sid,omitempty"`
	// Emergency marks a session issued while accounts were being served from
	// the snapshot, with the database down. Such a session is short and
	// restricted (see Can): the snapshot may be missing a change made since
	// it was written, so nothing that changes accounts or settings is trusted
	// to it.
	Emergency bool `json:"emg,omitempty"`
	// Purpose marks a token that is NOT a session: "2fa" is the few minutes
	// between a right password and the second step (SignChallenge). Verify
	// refuses any token that carries one, so a half-finished sign-in can never
	// be presented as a finished one.
	Purpose string `json:"use,omitempty"`
}

// ChallengeAge is how long somebody has to type the second-step code after
// their password was accepted.
const ChallengeAge = 5 * time.Minute

// ChatTicketAge bounds the ticket a page trades for a chat WebSocket.
//
// The session cookie is HttpOnly, so the page cannot hand the session token to
// the socket itself, and a token in the socket URL would land in proxy logs.
// The page asks GET /api/chat/ticket (cookie-authenticated) for one of these,
// sends it as the socket's first message, and it is useless a minute later.
// Like a 2FA challenge it carries a Purpose, so it can never pass as a session.
const ChatTicketAge = 60 * time.Second

// SignChatTicket issues a chat-socket ticket for an authenticated session.
func (s *Signer) SignChatTicket(username string, version int, sid string) (string, time.Time, error) {
	return s.sign(Claims{Subject: username, Version: version, SID: sid, Purpose: "chat"}, ChatTicketAge)
}

// VerifyChatTicket accepts only an unexpired chat ticket.
func (s *Signer) VerifyChatTicket(token string) (*Claims, error) {
	c, err := s.verify(token)
	if err != nil || c.Purpose != "chat" {
		return nil, ErrInvalidToken
	}
	return c, nil
}

// SignChallenge mints the token that says "this password was right" and
// nothing more; only VerifyChallenge accepts it.
func (s *Signer) SignChallenge(username string, version int) (string, time.Time, error) {
	return s.sign(Claims{Subject: username, Version: version, SID: NewSessionID(), Purpose: "2fa"}, ChallengeAge)
}

// VerifyChallenge checks a second-step token and refuses anything else --
// a session token above all.
func (s *Signer) VerifyChallenge(token string) (*Claims, error) {
	c, err := s.verify(token)
	if err != nil || c.Purpose != "2fa" || c.SID == "" {
		return nil, ErrInvalidToken
	}
	return c, nil
}

// EmergencySessionAge is how long an emergency session lasts.
const EmergencySessionAge = time.Hour

// SignEmergency mints an emergency session: EmergencySessionAge long and
// marked, whatever the signer's normal lifetime.
func (s *Signer) SignEmergency(username string, version int, sid string) (string, time.Time, error) {
	return s.sign(Claims{Subject: username, Version: version, SID: sid, Emergency: true}, EmergencySessionAge)
}

// Signer mints and verifies tokens.
type Signer struct {
	key    []byte
	maxAge time.Duration
}

// NewSigner creates a signer from a secret.
func NewSigner(key []byte, maxAge time.Duration) *Signer {
	if maxAge <= 0 {
		maxAge = 12 * time.Hour
	}
	return &Signer{key: key, maxAge: maxAge}
}

// LoadOrCreateKey reads the signing key, generating one on first run.
//
// Kept on disk so that restarting the server does not sign everybody out, and
// written 0600 because anyone holding this key can mint a token for any
// account, including an administrator.
func LoadOrCreateKey(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err == nil {
		key, derr := base64.RawStdEncoding.DecodeString(strings.TrimSpace(string(raw)))
		if derr != nil || len(key) < 32 {
			return nil, fmt.Errorf("auth: %s does not hold a usable signing key; delete it to generate a new one", path)
		}
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("auth: reading %s: %w", path, err)
	}

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("auth: generating a signing key: %w", err)
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
	}
	if err := os.WriteFile(path, []byte(base64.RawStdEncoding.EncodeToString(key)), 0o600); err != nil {
		return nil, fmt.Errorf("auth: writing %s: %w", path, err)
	}
	return key, nil
}

// header is fixed: this application issues and accepts exactly one algorithm.
const jwtHeader = `{"alg":"HS256","typ":"JWT"}`

// Sign mints a token for a user.
func (s *Signer) Sign(username string, version int) (string, time.Time, error) {
	return s.SignSession(username, version, "")
}

// NewSessionID returns an identifier for one sign-in.
//
// Sixteen bytes from crypto/rand: it is only an identifier and never a secret
// -- knowing it grants nothing, because the token's signature is what is
// actually checked -- but it must not collide between two people signing in at
// the same moment, and it must not be guessable enough to let someone
// enumerate other sessions on the account screen.
func NewSessionID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Falling back to the clock would be worse than failing: two sessions
		// opened in the same nanosecond would share an identifier and appear
		// as one device. An empty id is handled everywhere as "unknown".
		return ""
	}
	return hex.EncodeToString(b[:])
}

// SignSession is Sign with an explicit session identifier.
func (s *Signer) SignSession(username string, version int, sid string) (string, time.Time, error) {
	return s.sign(Claims{Subject: username, Version: version, SID: sid}, s.maxAge)
}

func (s *Signer) sign(claims Claims, age time.Duration) (string, time.Time, error) {
	now := time.Now()
	expires := now.Add(age)
	claims.IssuedAt, claims.Expires = now.Unix(), expires.Unix()
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", time.Time{}, err
	}

	b64 := base64.RawURLEncoding
	signing := b64.EncodeToString([]byte(jwtHeader)) + "." + b64.EncodeToString(payload)
	return signing + "." + b64.EncodeToString(s.mac(signing)), expires, nil
}

// Verify checks a token's signature and expiry and returns its claims.
//
// The algorithm is never read from the token's own header. Trusting that field
// is the classic JWT failure: a caller who sets it to "none", or swaps HS256
// for a key-confusion attack, gets their token accepted. Here the header must
// match the one this application issues, byte for byte.
//
// A SESSION token only: anything carrying a Purpose (a second-step challenge)
// is refused here.
func (s *Signer) Verify(token string) (*Claims, error) {
	c, err := s.verify(token)
	if err != nil || c.Purpose != "" {
		return nil, ErrInvalidToken
	}
	return c, nil
}

func (s *Signer) verify(token string) (*Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, ErrInvalidToken
	}
	b64 := base64.RawURLEncoding

	head, err := b64.DecodeString(parts[0])
	if err != nil || string(head) != jwtHeader {
		return nil, ErrInvalidToken
	}

	sig, err := b64.DecodeString(parts[2])
	if err != nil {
		return nil, ErrInvalidToken
	}
	// Constant time, so the signature cannot be guessed a byte at a time by
	// measuring how long the comparison took.
	if subtle.ConstantTimeCompare(sig, s.mac(parts[0]+"."+parts[1])) != 1 {
		return nil, ErrInvalidToken
	}

	// Only decode the payload once the signature is known to be ours.
	payload, err := b64.DecodeString(parts[1])
	if err != nil {
		return nil, ErrInvalidToken
	}
	var claims Claims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, ErrInvalidToken
	}
	if claims.Subject == "" || claims.Expires == 0 {
		return nil, ErrInvalidToken
	}
	if time.Now().After(time.Unix(claims.Expires, 0)) {
		return nil, ErrInvalidToken
	}
	return &claims, nil
}

func (s *Signer) mac(signing string) []byte {
	h := hmac.New(sha256.New, s.key)
	h.Write([]byte(signing))
	return h.Sum(nil)
}

// MaxAge reports how long a freshly minted token lasts.
func (s *Signer) MaxAge() time.Duration { return s.maxAge }
