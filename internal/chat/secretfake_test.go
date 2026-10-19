package chat

import (
	"context"
	"sort"
	"sync"
	"time"
)

// fakeSecret is secretDB in memory, with the same semantics as the SQL in
// secretstore.go, so the rules in secret.go are tested without PostgreSQL.
type fakeSecret struct {
	mu      sync.Mutex
	nextID  int64
	members map[int64]map[string]string // chat -> user -> role
	chats   map[int64]*SecretChat
	keys    map[int64]map[string]SecretKey // chat -> device|alg -> key
	queue   []*SecretItem
	blocked map[[2]string]bool
	failPut error // makes SecretPut fail (logging tests)
}

func newFakeSecret() *fakeSecret {
	return &fakeSecret{members: map[int64]map[string]string{}, chats: map[int64]*SecretChat{}, keys: map[int64]map[string]SecretKey{}, blocked: map[[2]string]bool{}}
}

func (f *fakeSecret) Role(_ context.Context, chatID int64, user string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.members[chatID][user]
	if !ok || r == "left" || r == "banned" {
		return r, ErrNotMember
	}
	return r, nil
}

func (f *fakeSecret) Blocked(_ context.Context, a, b string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.blocked[[2]string{a, b}] || f.blocked[[2]string{b, a}], nil
}

func (f *fakeSecret) put(it *SecretItem) bool {
	if it.ClientID != "" {
		for _, q := range f.queue {
			if q.ChatID == it.ChatID && q.From == it.From && q.ClientID == it.ClientID {
				it.ID, it.At = q.ID, q.At
				return false
			}
		}
	}
	f.nextID++
	it.ID, it.At = f.nextID, time.Now().UTC()
	cp := *it
	f.queue = append(f.queue, &cp)
	return true
}

func (f *fakeSecret) SecretCreate(_ context.Context, sc *SecretChat, keys []SecretKey, _ []string, req *SecretItem) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	sc.ChatID = f.nextID
	f.members[sc.ChatID] = map[string]string{sc.Initiator: "member", sc.Responder: "member"}
	cp := *sc
	for _, k := range keys {
		cp.Offered = append(cp.Offered, k.Alg)
	}
	sort.Strings(cp.Offered)
	f.chats[sc.ChatID] = &cp
	f.keys[sc.ChatID] = map[string]SecretKey{}
	for _, k := range keys {
		f.keys[sc.ChatID][sc.InitiatorDevice+"|"+k.Alg] = k
	}
	req.ChatID = sc.ChatID
	f.put(req)
	return nil
}

func (f *fakeSecret) SecretGet(_ context.Context, chatID int64) (*SecretChat, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sc, ok := f.chats[chatID]
	if !ok {
		return nil, ErrForbidden
	}
	cp := *sc
	return &cp, nil
}

func (f *fakeSecret) SecretBind(_ context.Context, sc *SecretChat, key SecretKey, _ string, acc *SecretItem) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cur := f.chats[sc.ChatID]
	if cur == nil || cur.State != "requested" || cur.ResponderDevice != "" {
		return ErrSecretState
	}
	cur.ResponderDevice, cur.Alg, cur.State = sc.ResponderDevice, key.Alg, "active"
	f.keys[sc.ChatID][sc.ResponderDevice+"|"+key.Alg] = key
	f.dropQueue(func(q *SecretItem) bool { return q.ChatID == sc.ChatID && q.Kind == secretRequest })
	f.put(acc)
	return nil
}

func (f *fakeSecret) dropQueue(match func(*SecretItem) bool) []*SecretItem {
	var keep, gone []*SecretItem
	for _, q := range f.queue {
		if match(q) {
			gone = append(gone, q)
		} else {
			keep = append(keep, q)
		}
	}
	f.queue = keep
	return gone
}

func (f *fakeSecret) SecretPut(_ context.Context, it *SecretItem) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failPut != nil {
		return false, f.failPut
	}
	return f.put(it), nil
}

func (f *fakeSecret) SecretPending(_ context.Context, user, device string, _ time.Time) ([]*SecretItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*SecretItem
	for _, q := range f.queue {
		r := f.members[q.ChatID][q.ToUser]
		if q.ToUser == user && (q.ToDevice == device || q.ToDevice == "") && r != "" && r != "left" && r != "banned" {
			cp := *q
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (f *fakeSecret) SecretAck(_ context.Context, user, device string, ids []int64) ([]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	want := map[int64]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var closed []int64
	for _, q := range f.dropQueue(func(q *SecretItem) bool {
		return want[q.ID] && q.ToUser == user && (q.ToDevice == device || (q.ToDevice == "" && q.Kind == secretClose))
	}) {
		if q.Kind == secretClose {
			closed = append(closed, q.ChatID)
		}
	}
	return closed, nil
}

func (f *fakeSecret) SecretEnd(_ context.Context, chatID int64, user string, note *SecretItem) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	sc := f.chats[chatID]
	if sc == nil || sc.State == "closed" {
		return ErrSecretState
	}
	sc.State = "closed"
	delete(f.keys, chatID)
	f.dropQueue(func(q *SecretItem) bool { return q.ChatID == chatID })
	f.put(note)
	f.members[chatID][user] = "left"
	return nil
}

func (f *fakeSecret) SecretLeave(_ context.Context, chatID int64, user string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if m := f.members[chatID]; m != nil {
		m[user] = "left"
	}
	return nil
}

func (f *fakeSecret) SecretLive(_ context.Context, user, device string) ([]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []int64
	for id, sc := range f.chats {
		if sc.State == "closed" {
			continue
		}
		if (sc.Initiator == user && sc.InitiatorDevice == device) ||
			(sc.Responder == user && (sc.ResponderDevice == device || (sc.ResponderDevice == "" && sc.State == "requested"))) {
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

func (f *fakeSecret) waiting() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.queue)
}
