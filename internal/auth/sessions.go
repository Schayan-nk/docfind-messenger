package auth

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Where someone is signed in.
//
// This registry is for showing people their own sessions and for telling an
// administrator who is currently using the application. It is deliberately NOT
// part of verifying a request.
//
// That is the important design decision here, so it is worth stating plainly.
// Authentication in this application is a signed token: the server holds no
// state, and a request is valid because its signature and expiry check out.
// Making the registry authoritative -- refusing a token whose session is not
// listed -- would give exact per-device revocation, which is tempting. It would
// also mean that losing or corrupting one JSON file signs every person out of a
// live system at once, and that a write failure during a busy moment locks
// people out of an application they are in the middle of using. For a tool
// that a company runs on one machine on its own network, that trade is wrong.
//
// So the registry observes, and the existing mechanism revokes: bumping an
// account's token version invalidates every token issued to it, which is what
// "sign out everywhere" does. That already worked and is not made less safe by
// anything here.
type Session struct {
	// ID is the token's sid claim. Empty for a token minted before sessions
	// were recorded, in which case the record is keyed by a synthetic id.
	ID       string    `json:"id"`
	Username string    `json:"username"`
	IP       string    `json:"ip"`
	Agent    string    `json:"agent"`
	Started  time.Time `json:"started"`
	LastSeen time.Time `json:"last_seen"`
}

// Device describes the client in the terms a person recognises, derived from
// the user-agent string.
type Device struct {
	// Kind is one of "phone", "tablet", "desktop" -- which icon to draw.
	Kind string `json:"kind"`
	// Label is what to write beside it: "iPhone · Safari".
	Label string `json:"label"`
}

// Sessions is the registry.
type Sessions struct {
	path string
	mu   sync.Mutex
	byID map[string]*Session
	// dirty and lastSave throttle writing. Every authenticated request
	// touches a session, and this file is not worth an fsync per request.
	dirty    bool
	lastSave time.Time
	// idle is how long after its last request a session stops being listed.
	// Not the token's own lifetime: a token can be valid for a week, and a
	// list of every browser someone has opened in a week is not a useful
	// answer to "where am I signed in".
	idle time.Duration
}

const (
	// sessionIdle is how long a session survives without a request. Long
	// enough to span a lunch break and a meeting, short enough that a machine
	// somebody used once last week is not still listed as a place they are
	// signed in.
	sessionIdle = 12 * time.Hour
	// onlineWithin is how recently a request must have arrived for an account
	// to read as online to an administrator. A browser with the dashboard
	// open polls, so anything much tighter flickers.
	onlineWithin = 5 * time.Minute
	// saveEvery bounds how often the file is rewritten, however many requests
	// arrive in between.
	saveEvery = 30 * time.Second
)

// OpenSessions loads the registry, starting empty if the file is absent or
// unreadable.
//
// Unreadable is deliberately not an error. This file is a convenience; if it
// is corrupt the right behaviour is to start a fresh one and carry on serving,
// not to refuse to start the application over a list of browsers.
func OpenSessions(path string) *Sessions {
	s := &Sessions{path: path, byID: map[string]*Session{}, idle: sessionIdle}
	raw, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	var list []*Session
	if err := json.Unmarshal(raw, &list); err != nil {
		return s
	}
	cut := time.Now().Add(-s.idle)
	for _, rec := range list {
		if rec == nil || rec.ID == "" || rec.LastSeen.Before(cut) {
			continue
		}
		s.byID[rec.ID] = rec
	}
	return s
}

// Touch records that a request arrived on a session.
//
// Called from the middleware on every authenticated request, so it does the
// least possible work: it takes the lock, updates a timestamp, and marks the
// file for writing later. The write itself happens at most every saveEvery.
func (s *Sessions) Touch(sid, username string, r *http.Request, trustProxy bool) {
	if s == nil || username == "" {
		return
	}
	id := sid
	if id == "" {
		// A token from before sessions existed. Key it by the account and the
		// client so that it still appears as one device rather than a new one
		// on every request, and mark it so the UI can say the age is unknown.
		id = "legacy-" + username + "-" + shortHash(clientIP(r, trustProxy)+"|"+r.UserAgent())
	}
	now := time.Now()

	s.mu.Lock()
	rec, ok := s.byID[id]
	if !ok {
		rec = &Session{
			ID: id, Username: username, Started: now,
			IP: clientIP(r, trustProxy), Agent: trimAgent(r.UserAgent()),
		}
		s.byID[id] = rec
	}
	rec.LastSeen = now
	// The address can change mid-session -- a phone moving from the office
	// network to mobile data -- and the current one is the useful one to show.
	if ip := clientIP(r, trustProxy); ip != "" {
		rec.IP = ip
	}
	s.dirty = true
	due := now.Sub(s.lastSave) >= saveEvery
	if due {
		s.lastSave = now
	}
	var snapshot []*Session
	if due {
		snapshot = s.listLocked("")
	}
	s.mu.Unlock()

	if due {
		s.write(snapshot)
	}
}

// ForgetUser drops every session of one account, for "sign out everywhere".
func (s *Sessions) ForgetUser(username string) {
	if s == nil {
		return
	}
	key := normalizeName(username)
	s.mu.Lock()
	for id, rec := range s.byID {
		if normalizeName(rec.Username) == key {
			delete(s.byID, id)
		}
	}
	snapshot := s.listLocked("")
	s.mu.Unlock()
	s.write(snapshot)
}

// For returns one account's live sessions, newest activity first.
func (s *Sessions) For(username string) []*Session {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listLocked(username)
}

// Online reports which accounts have made a request recently.
func (s *Sessions) Online() map[string]time.Time {
	out := map[string]time.Time{}
	if s == nil {
		return out
	}
	cut := time.Now().Add(-onlineWithin)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rec := range s.byID {
		if rec.LastSeen.Before(cut) {
			continue
		}
		k := normalizeName(rec.Username)
		if prev, ok := out[k]; !ok || rec.LastSeen.After(prev) {
			out[k] = rec.LastSeen
		}
	}
	return out
}

// LastSeen returns when an account last made a request, zero if never.
func (s *Sessions) LastSeen(username string) time.Time {
	var out time.Time
	if s == nil {
		return out
	}
	key := normalizeName(username)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rec := range s.byID {
		if normalizeName(rec.Username) == key && rec.LastSeen.After(out) {
			out = rec.LastSeen
		}
	}
	return out
}

// listLocked collects sessions, dropping the ones that have gone idle. An
// empty username means every account. Callers hold the lock.
func (s *Sessions) listLocked(username string) []*Session {
	cut := time.Now().Add(-s.idle)
	key := normalizeName(username)
	out := make([]*Session, 0, len(s.byID))
	for id, rec := range s.byID {
		if rec.LastSeen.Before(cut) {
			// Expiry happens here, on read, rather than on a timer: this
			// application is a single process with no scheduler, and a list
			// nobody is looking at does not need tidying.
			delete(s.byID, id)
			continue
		}
		if key != "" && normalizeName(rec.Username) != key {
			continue
		}
		cp := *rec
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastSeen.After(out[j].LastSeen) })
	return out
}

// write persists a snapshot taken under the lock.
//
// Atomic replace, and 0600: the file names the addresses and devices of
// everybody who uses the system, which is not as sensitive as a password hash
// but is nobody else's business either.
func (s *Sessions) write(list []*Session) {
	if s == nil || s.path == "" {
		return
	}
	raw, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return
	}
	if dir := filepath.Dir(s.path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return
		}
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return
	}
	// A failed rename leaves the previous file in place, which is the right
	// outcome for a registry: stale is better than absent, and nothing here
	// is worth surfacing an error to somebody's browser over.
	_ = os.Rename(tmp, s.path)
}

// Flush writes the registry now, for shutdown.
func (s *Sessions) Flush() error {
	if s == nil || s.path == "" {
		return nil
	}
	s.mu.Lock()
	snapshot := s.listLocked("")
	s.dirty = false
	s.mu.Unlock()
	raw, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	if dir := filepath.Dir(s.path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("auth: saving sessions: %w", err)
	}
	return nil
}

// clientIP is the address to record.
//
// X-Forwarded-For is read only when the operator has said there is a proxy in
// front, for the same reason the rest of this application does: otherwise
// anyone can set the header and choose what the session list says about them.
func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
			// The left-most entry is the original client; the rest are proxies.
			// StripPort because IIS's Application Request Routing appends the
			// source port, and a port in the session list is noise.
			if first := strings.TrimSpace(strings.Split(fwd, ",")[0]); first != "" {
				return StripPort(first)
			}
		}
		if ip := strings.TrimSpace(r.Header.Get("X-Real-IP")); ip != "" {
			return StripPort(ip)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// trimAgent caps a user-agent string. They are attacker-controlled and can be
// arbitrarily long; the whole string is never shown, only what Describe makes
// of it, but the raw value is kept for support questions.
func trimAgent(ua string) string {
	ua = strings.TrimSpace(ua)
	if len(ua) > 300 {
		return ua[:300]
	}
	return ua
}

// shortHash is a stable, non-cryptographic label for a client, used only to
// keep pre-session tokens from creating a record per request.
func shortHash(s string) string {
	var h uint64 = 14695981039346656037
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return fmt.Sprintf("%016x", h)
}

// Describe turns a user-agent into a device kind and a readable label.
//
// Rough on purpose. Nothing here is a security decision -- it chooses an icon
// and a few words -- so the cost of getting one obscure browser wrong is that
// somebody sees "Windows · Chrome" instead of something more exact, and the
// cost of a dependency that keeps up with every user-agent string in the world
// is a dependency. Order matters: Edge and Opera both claim to be Chrome, iPads
// claim to be Macs, so the most specific token is tested first.
func Describe(ua string) Device {
	l := strings.ToLower(ua)
	if l == "" {
		return Device{Kind: "desktop", Label: "دستگاه نامشخص"}
	}

	var os string
	switch {
	case strings.Contains(l, "iphone"):
		os = "iPhone"
	case strings.Contains(l, "ipad"):
		os = "iPad"
	case strings.Contains(l, "android"):
		os = "Android"
	case strings.Contains(l, "windows"):
		os = "Windows"
	case strings.Contains(l, "mac os x"), strings.Contains(l, "macintosh"):
		os = "macOS"
	case strings.Contains(l, "cros"):
		os = "ChromeOS"
	case strings.Contains(l, "linux"):
		os = "Linux"
	}

	var browser string
	switch {
	case strings.Contains(l, "edg/"), strings.Contains(l, "edga/"), strings.Contains(l, "edgios/"):
		browser = "Edge"
	case strings.Contains(l, "opr/"), strings.Contains(l, "opera"):
		browser = "Opera"
	case strings.Contains(l, "firefox"), strings.Contains(l, "fxios"):
		browser = "Firefox"
	case strings.Contains(l, "crios"):
		browser = "Chrome"
	case strings.Contains(l, "chrome"):
		browser = "Chrome"
	case strings.Contains(l, "safari"):
		browser = "Safari"
	}

	kind := "desktop"
	switch {
	case strings.Contains(l, "ipad"), strings.Contains(l, "tablet"):
		kind = "tablet"
	case strings.Contains(l, "iphone"), strings.Contains(l, "mobile"),
		strings.Contains(l, "android"):
		// An Android tablet says "Android" without "Mobile"; a phone says
		// both. Checked after the tablet test so the ordering holds.
		if strings.Contains(l, "android") && !strings.Contains(l, "mobile") {
			kind = "tablet"
		} else {
			kind = "phone"
		}
	}

	label := strings.TrimSpace(strings.Join([]string{os, browser}, " · "))
	label = strings.Trim(label, " ·")
	if label == "" {
		label = "دستگاه نامشخص"
	}
	return Device{Kind: kind, Label: label}
}
