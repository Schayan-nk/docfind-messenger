package chat

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Secret chats (SPEC 6.12, docs/chat/SECRET_CHATS.md). Two devices agree on
// keys in the browser; this server only introduces them and carries sealed
// bodies it cannot open, to exactly one device, until that device confirms.
// Nothing here logs a body, a key or a frame (security check 15).

// What waits in a device's mailbox (secret_queue.kind).
const (
	secretRequest = "request" // a new secret chat, for every device of the invited person
	secretAccept  = "accept"  // the invited device's public key, back to the inviting device
	secretMsg     = "msg"     // one sealed body
	secretClose   = "close"   // the other side ended the chat
	secretGone    = "gone"    // live only: another of my devices took or ended this chat
)

const (
	maxSecretBody = 16 << 10            // decoded bytes; a 4,096-character message with its padding fits
	minSecretBody = 8 + 16 + 1          // counter, GCM tag, at least one byte
	secretKeep    = 30 * 24 * time.Hour // a body no device fetched is dropped after this
	maxSecretAcks = 500
)

var (
	ErrSecretState  = errors.New("chat: the secret chat is not in a state for this")
	ErrSecretDevice = errors.New("chat: the secret chat lives on another device")
	ErrSecretBody   = errors.New("chat: not a sealed body")
	ErrSecretKey    = errors.New("chat: not a usable public key")
)

// A device id is 128 random bits the page makes once and keeps with its keys.
var deviceRx = regexp.MustCompile(`^[0-9a-f]{32}$`)

// SecretKey is one public key a device offers: X25519 (32 bytes) or P-256
// (65 bytes, uncompressed point), base64url without padding.
type SecretKey struct {
	Alg string `json:"alg"`
	Key string `json:"key"`
}

// SecretChat is the server's record of one secret chat.
type SecretChat struct {
	ChatID          int64
	Initiator       string
	InitiatorDevice string
	Responder       string
	ResponderDevice string // "" until accepted
	Alg             string
	State           string   // requested | active | closed
	Offered         []string // the algorithms the inviting device offered
}

// ends returns user's device and the other side; ok is false for a stranger.
func (sc *SecretChat) ends(user string) (myDev, peer, peerDev string, ok bool) {
	switch user {
	case sc.Initiator:
		return sc.InitiatorDevice, sc.Responder, sc.ResponderDevice, true
	case sc.Responder:
		return sc.ResponderDevice, sc.Initiator, sc.InitiatorDevice, true
	}
	return "", "", "", false
}

// SecretItem is one frame for one device ("secret" on the socket).
type SecretItem struct {
	ID         int64     `json:"id,omitempty"`
	ChatID     int64     `json:"chat"`
	Kind       string    `json:"kind"`
	ToUser     string    `json:"-"`
	ToDevice   string    `json:"-"` // "" = any device of ToUser
	From       string    `json:"from,omitempty"`
	FromDevice string    `json:"-"`
	ClientID   string    `json:"cid,omitempty"`
	Body       string    `json:"body,omitempty"`
	At         time.Time `json:"at"`
	Peer       *Person   `json:"peer,omitempty"` // request only: who invites
}

// secretDB is the storage a secret chat needs. *Store is the real one
// (secretstore.go); the rules live here, in Go, so they are tested alone.
type secretDB interface {
	Role(ctx context.Context, chatID int64, user string) (string, error)
	Blocked(ctx context.Context, a, b string) (bool, error)
	SecretCreate(ctx context.Context, sc *SecretChat, keys []SecretKey, fps []string, req *SecretItem) error
	SecretGet(ctx context.Context, chatID int64) (*SecretChat, error)
	SecretBind(ctx context.Context, sc *SecretChat, key SecretKey, fp string, acc *SecretItem) error
	SecretPut(ctx context.Context, it *SecretItem) (created bool, err error)
	SecretPending(ctx context.Context, user, device string, before time.Time) ([]*SecretItem, error)
	SecretAck(ctx context.Context, user, device string, ids []int64) (closed []int64, err error)
	SecretEnd(ctx context.Context, chatID int64, user string, note *SecretItem) error
	SecretLeave(ctx context.Context, chatID int64, user string) error
	SecretLive(ctx context.Context, user, device string) ([]int64, error)
}

var _ secretDB = (*Store)(nil)

// secretHub knows which open socket speaks for which device. The zero value
// is ready; db nil means Service.Store.
type secretHub struct {
	mu   sync.Mutex
	devs map[*conn]string
	db   secretDB
}

func (h *secretHub) device(c *conn) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.devs[c]
}

func (h *secretHub) bind(c *conn, dev string) (first bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.devs == nil {
		h.devs = map[*conn]string{}
	}
	_, had := h.devs[c]
	h.devs[c] = dev
	return !had
}

func (h *secretHub) unbind(c *conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.devs, c)
}

func (s *Service) sdb() secretDB {
	if s.secret.db != nil {
		return s.secret.db
	}
	return s.Store
}

// toDevice pushes one frame to the sockets of one device of user ("" = every
// device of user that has announced itself). Other devices never see it:
// secret chats do not sync.
func (s *Service) toDevice(user, dev string, it *SecretItem) {
	b, err := json.Marshal(struct {
		T string      `json:"t"`
		D *SecretItem `json:"d"`
	}{"secret", it})
	if err != nil {
		return
	}
	s.mu.RLock()
	var targets []*conn
	for c := range s.conns[user] {
		if d := s.secret.device(c); d != "" && (dev == "" || d == dev) {
			targets = append(targets, c)
		}
	}
	s.mu.RUnlock()
	for _, c := range targets {
		c.push(b)
	}
}

// othersOf tells user's other devices that a chat is no longer theirs.
func (s *Service) othersOf(user, keep string, chatID int64) {
	s.mu.RLock()
	var targets []*conn
	for c := range s.conns[user] {
		if d := s.secret.device(c); d != "" && d != keep {
			targets = append(targets, c)
		}
	}
	s.mu.RUnlock()
	for _, c := range targets {
		c.send("secret", "", &SecretItem{ChatID: chatID, Kind: secretGone, At: s.now().UTC()})
	}
}

// checkKey validates one public key with crypto/ecdh and returns its
// fingerprint (hex SHA-256 of the raw key).
func checkKey(k SecretKey) (string, error) {
	raw, err := base64.RawURLEncoding.Strict().DecodeString(k.Key)
	if err != nil {
		return "", ErrSecretKey
	}
	switch k.Alg {
	case "x25519":
		_, err = ecdh.X25519().NewPublicKey(raw)
		if err == nil && bytes.Equal(raw, make([]byte, 32)) {
			err = ErrSecretKey
		}
	case "p256":
		_, err = ecdh.P256().NewPublicKey(raw)
	default:
		err = ErrSecretKey
	}
	if err != nil {
		return "", ErrSecretKey
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// checkOffer validates an invitation's keys: P-256 always (every browser can
// answer it), X25519 besides where the inviting browser has it.
func checkOffer(keys []SecretKey) ([]string, error) {
	if len(keys) == 0 || len(keys) > 2 {
		return nil, ErrSecretKey
	}
	seen := map[string]bool{}
	var fps []string
	for _, k := range keys {
		if seen[k.Alg] {
			return nil, ErrSecretKey
		}
		seen[k.Alg] = true
		fp, err := checkKey(k)
		if err != nil {
			return nil, err
		}
		fps = append(fps, fp)
	}
	if !seen["p256"] {
		return nil, ErrSecretKey
	}
	return fps, nil
}

// sealed reports whether body has the shape of a sealed message: base64url of
// an 8-byte counter (from 1) and an AES-GCM ciphertext with its tag. The
// server cannot prove a body is ciphertext; it refuses everything else, and
// a frame with a text field is refused before it gets here (secret_send).
func sealed(body string) bool {
	if body == "" || len(body) > base64.RawURLEncoding.EncodedLen(maxSecretBody) {
		return false
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(body)
	return err == nil && len(raw) >= minSecretBody && len(raw) <= maxSecretBody && binary.BigEndian.Uint64(raw[:8]) > 0
}

func (s *Service) secretPerson(ctx context.Context, viewer, target string) *Person {
	if s.Store == nil {
		p := &Person{ID: target, Name: target}
		if u, err := s.users.Get(target); err == nil && u.DisplayName != "" {
			p.Name = u.DisplayName
		}
		return p
	}
	profiles, _ := s.Store.Profiles(ctx, []string{viewer, target})
	return s.person(ctx, viewer, target, profiles)
}

// SecretOpen invites peer to a new secret chat from the device dev.
func (s *Service) SecretOpen(ctx context.Context, me, dev, peer string, keys []SecretKey) (*Chat, error) {
	if dev == "" {
		return nil, ErrSecretDevice
	}
	u, err := s.users.Get(strings.TrimSpace(peer))
	if err != nil || u.Disabled {
		return nil, ErrNoUser
	}
	if u.Username == me {
		return nil, ErrForbidden
	}
	db := s.sdb()
	if b, err := db.Blocked(ctx, me, u.Username); err != nil || b {
		return nil, ErrBlocked
	}
	fps, err := checkOffer(keys)
	if err != nil {
		return nil, err
	}
	if !s.sendLimit.allow(me) {
		return nil, ErrRate
	}
	body, _ := json.Marshal(map[string]any{"keys": keys})
	sc := &SecretChat{Initiator: me, InitiatorDevice: dev, Responder: u.Username, State: "requested"}
	req := &SecretItem{Kind: secretRequest, ToUser: u.Username, From: me, FromDevice: dev, Body: string(body)}
	if err := db.SecretCreate(ctx, sc, keys, fps, req); err != nil {
		return nil, s.secretErr("open", err)
	}
	req.Peer = s.secretPerson(ctx, u.Username, me)
	s.toDevice(u.Username, "", req)
	c := &Chat{ID: sc.ChatID, Type: "secret", Role: "member", Peer: s.secretPerson(ctx, me, u.Username)}
	c.Title = c.Peer.Name
	return c, nil
}

// SecretAcceptReq answers an invitation from the device dev with one key.
func (s *Service) SecretAcceptReq(ctx context.Context, me, dev string, chatID int64, key SecretKey) error {
	db := s.sdb()
	if _, err := db.Role(ctx, chatID, me); err != nil {
		return err
	}
	if dev == "" {
		return ErrSecretDevice
	}
	sc, err := db.SecretGet(ctx, chatID)
	if err != nil {
		return err
	}
	if sc.Responder != me {
		return ErrForbidden
	}
	if sc.State != "requested" {
		return ErrSecretState
	}
	offered := false
	for _, a := range sc.Offered {
		offered = offered || a == key.Alg
	}
	fp, err := checkKey(key)
	if err != nil || !offered {
		return ErrSecretKey
	}
	body, _ := json.Marshal(key)
	sc.ResponderDevice, sc.Alg = dev, key.Alg
	acc := &SecretItem{ChatID: chatID, Kind: secretAccept, ToUser: sc.Initiator, ToDevice: sc.InitiatorDevice, From: me, FromDevice: dev, Body: string(body)}
	if err := db.SecretBind(ctx, sc, key, fp, acc); err != nil {
		return s.secretErr("accept", err)
	}
	s.toDevice(sc.Initiator, sc.InitiatorDevice, acc)
	s.othersOf(me, dev, chatID)
	return nil
}

// SecretSend queues one sealed body for the other device of the chat.
func (s *Service) SecretSend(ctx context.Context, me, dev string, chatID int64, cid, body string) (*SecretItem, error) {
	db := s.sdb()
	if _, err := db.Role(ctx, chatID, me); err != nil {
		return nil, err
	}
	sc, err := db.SecretGet(ctx, chatID)
	if err != nil {
		return nil, err
	}
	myDev, peer, peerDev, ok := sc.ends(me)
	if !ok {
		return nil, ErrNotMember
	}
	if sc.State != "active" {
		return nil, ErrSecretState
	}
	if dev == "" || dev != myDev {
		return nil, ErrSecretDevice
	}
	if !ValidClientID(cid) {
		return nil, ErrBadID
	}
	if !sealed(body) {
		return nil, ErrSecretBody
	}
	if b, err := db.Blocked(ctx, me, peer); err != nil || b {
		return nil, ErrBlocked
	}
	if !s.sendLimit.allow(me) {
		return nil, ErrRate
	}
	it := &SecretItem{ChatID: chatID, Kind: secretMsg, ToUser: peer, ToDevice: peerDev, From: me, FromDevice: dev, ClientID: cid, Body: body}
	created, err := db.SecretPut(ctx, it)
	if err != nil {
		return nil, s.secretErr("send", err)
	}
	if created {
		s.toDevice(peer, peerDev, it)
	}
	return it, nil
}

// SecretClose ends a secret chat for both sides. Before it is accepted any
// device of the invited person may decline; after, only the chat's own devices.
func (s *Service) SecretClose(ctx context.Context, me, dev string, chatID int64) error {
	db := s.sdb()
	if _, err := db.Role(ctx, chatID, me); err != nil {
		return err
	}
	sc, err := db.SecretGet(ctx, chatID)
	if err != nil {
		return err
	}
	myDev, peer, peerDev, ok := sc.ends(me)
	if !ok {
		return ErrNotMember
	}
	if dev == "" || (myDev != "" && myDev != dev) {
		return ErrSecretDevice
	}
	if sc.State == "closed" {
		return db.SecretLeave(ctx, chatID, me)
	}
	note := &SecretItem{ChatID: chatID, Kind: secretClose, ToUser: peer, ToDevice: peerDev, From: me, FromDevice: dev}
	if err := db.SecretEnd(ctx, chatID, me, note); err != nil {
		return s.secretErr("close", err)
	}
	s.toDevice(peer, peerDev, note)
	s.othersOf(me, dev, chatID)
	return nil
}

// secretAck removes what this device has stored. A "close" it confirms also
// takes the chat off this person's list.
func (s *Service) secretAck(ctx context.Context, me, dev string, ids []int64) error {
	if dev == "" {
		return ErrSecretDevice
	}
	if len(ids) > maxSecretAcks {
		ids = ids[:maxSecretAcks]
	}
	db := s.sdb()
	closed, err := db.SecretAck(ctx, me, dev, ids)
	if err != nil {
		return s.secretErr("ack", err)
	}
	for _, id := range closed {
		_ = db.SecretLeave(ctx, id, me)
	}
	return nil
}

// secretHello binds a socket to its device, sends what waited for it, and
// answers with the secret chats this device still holds.
func (s *Service) secretHello(ctx context.Context, c *conn, dev string) (any, error) {
	if !deviceRx.MatchString(dev) {
		return nil, ErrSecretDevice
	}
	if cur := s.secret.device(c); cur != "" && cur != dev {
		return nil, ErrSecretDevice
	}
	if s.secret.bind(c, dev) && c.ctx != nil {
		go func() { <-c.ctx.Done(); s.secret.unbind(c) }()
	}
	db := s.sdb()
	items, err := db.SecretPending(ctx, c.user, dev, s.now().Add(-secretKeep))
	if err != nil {
		return nil, s.secretErr("pending", err)
	}
	for _, it := range items {
		if it.Kind == secretRequest {
			it.Peer = s.secretPerson(ctx, c.user, it.From)
		}
		c.send("secret", "", it)
	}
	live, err := db.SecretLive(ctx, c.user, dev)
	if err != nil {
		return nil, s.secretErr("live", err)
	}
	return map[string]any{"live": nonNilIDs(live)}, nil
}

func nonNilIDs(v []int64) []int64 {
	if v == nil {
		return []int64{}
	}
	return v
}

// secretErr logs a storage failure by operation and error class only: never
// the frame, the body or the database's own message, which can quote a row.
func (s *Service) secretErr(op string, err error) error {
	for _, known := range []error{ErrNotMember, ErrForbidden, ErrSecretState, ErrSecretDevice, ErrSecretBody, ErrSecretKey, ErrBlocked} {
		if errors.Is(err, known) {
			return err
		}
	}
	s.Logf("chat: secret %s failed (%s)", op, errClass(err))
	return errors.New("chat: secret storage")
}
