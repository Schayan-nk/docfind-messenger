package chat

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

// Group calls against a real database (skipped without DOCFIND_PG_*), with a
// stand-in for the media server (internal/calls has its own tests with real
// WebRTC clients).

type fakeMedia struct {
	mu    sync.Mutex
	peers map[string]*fakePeer // room:user
	muted map[string]bool
	ended map[int64]bool
}

type fakePeer struct {
	left    bool
	answers int
	gone    func()
}

func (f *fakeMedia) Join(room int64, user string, send func(map[string]any), gone func()) (MediaPeer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := &fakePeer{gone: gone}
	f.peers[key(room, user)] = p
	go send(map[string]any{"sdp": map[string]string{"type": "offer", "sdp": "v=0"}})
	return p, nil
}
func (f *fakeMedia) Mute(room int64, user string, on bool) {
	f.mu.Lock()
	f.muted[key(room, user)] = on
	f.mu.Unlock()
}
func (f *fakeMedia) End(room int64) {
	f.mu.Lock()
	f.ended[room] = true
	f.mu.Unlock()
}
func key(room int64, user string) string {
	return strings.Join([]string{user, json.Number(itoa(room)).String()}, ":")
}
func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

func (p *fakePeer) Answer(string) error        { p.answers++; return nil }
func (p *fakePeer) CandidateJSON([]byte) error { return nil }
func (p *fakePeer) Leave()                     { p.left = true }

func gcallFixture(t *testing.T) (*Service, []string, int64, *fakeMedia) {
	t.Helper()
	svc, u, id := groupFixture(t, "supergroup", 4, 2, false) // u[0] owner, u[1] u[2] members, u[3] outsider
	f := &fakeMedia{peers: map[string]*fakePeer{}, muted: map[string]bool{}, ended: map[int64]bool{}}
	svc.GroupMedia = f
	return svc, u, id, f
}

func gdo(c *conn, typ string, d any) (map[string]json.RawMessage, string) {
	b, _ := json.Marshal(d)
	c.handleGroupCall(context.Background(), Envelope{T: typ, ID: "x" + typ, D: b})
	var other [][]byte // frames that are not the answer stay queued for the test
	defer func() {
		for _, raw := range other {
			c.out <- raw
		}
	}()
	for {
		select {
		case raw := <-c.out:
			var f frame
			if json.Unmarshal(raw, &f) != nil || f.ID != "x"+typ {
				other = append(other, raw)
				continue
			}
			if f.T == "err" {
				var e struct{ Code string }
				json.Unmarshal(f.D, &e)
				return nil, e.Code
			}
			var m map[string]json.RawMessage
			json.Unmarshal(f.D, &m)
			return m, ""
		case <-time.After(3 * time.Second):
			return nil, "no answer"
		}
	}
}

func gcallID(t *testing.T, ack map[string]json.RawMessage) int64 {
	t.Helper()
	var in GroupCallInfo
	if err := json.Unmarshal(ack["call"], &in); err != nil || in.ID == 0 {
		t.Fatalf("no call in %s", ack["call"])
	}
	return in.ID
}

// stateWhere waits for a gcall_state on c that ok accepts.
func stateWhere(t *testing.T, c *conn, what string, ok func([]GroupMember) bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case raw := <-c.out:
			var f frame
			var in GroupCallInfo
			if json.Unmarshal(raw, &f) == nil && f.T == "gcall_state" && json.Unmarshal(f.D, &in) == nil && ok(in.Members) {
				return
			}
		case <-time.After(100 * time.Millisecond):
		}
	}
	t.Fatalf("never: %s", what)
}

func TestAGroupCallStartsJoinsAndEnds(t *testing.T) {
	svc, u, chatID, f := gcallFixture(t)
	ctx := context.Background()
	owner, a, b := device(svc, u[0]), device(svc, u[1]), device(svc, u[2])

	ack, code := gdo(owner, "gcall_join", map[string]any{"chat": chatID, "tab": "p-owner"})
	if code != "" {
		t.Fatalf("start: %s", code)
	}
	id := gcallID(t, ack)
	expect(t, owner, "gcall_signal") // the media server's offer reaches the page
	var up struct {
		Chat int64
		Call *struct{ ID, Count int64 }
	}
	json.Unmarshal(expect(t, b, "gcall_update"), &up)
	if up.Chat != chatID || up.Call == nil || up.Call.ID != id || up.Call.Count != 1 {
		t.Errorf("the chat's members were not told of the call: %+v", up)
	}
	if _, code := gdo(a, "gcall_join", map[string]any{"chat": chatID, "video": true}); code != "" {
		t.Fatalf("join: %s", code)
	}
	stateWhere(t, a, "both in, a with the camera", func(ms []GroupMember) bool { return len(ms) == 2 && ms[1].Cam && ms[0].ID == u[0] })
	gdo(a, "gcall_signal", map[string]any{"call": id, "d": map[string]any{"sdp": map[string]string{"type": "answer", "sdp": "v=0"}}})
	if f.peers[key(id, u[1])].answers != 1 {
		t.Error("the answer did not reach the media server")
	}
	// A big call's answer (a part per stream of each person) goes through;
	// one past the limit does not.
	big := "v=0" + strings.Repeat("a", 60<<10)
	if _, code := gdo(a, "gcall_signal", map[string]any{"call": id, "d": map[string]any{"sdp": map[string]string{"type": "answer", "sdp": big}}}); code != "" || f.peers[key(id, u[1])].answers != 2 {
		t.Errorf("a 60 KB answer was refused: %q", code)
	}
	if _, code := gdo(a, "gcall_signal", map[string]any{"call": id, "d": strings.Repeat("x", maxGroupSignal+1)}); code == "" {
		t.Error("a signal over the limit went through")
	}
	gdo(a, "gcall_media", map[string]any{"call": id, "mic": false, "cam": true, "hand": true})
	stateWhere(t, owner, "a's raised hand and muted microphone", func(ms []GroupMember) bool { return len(ms) == 2 && !ms[1].Mic && ms[1].Hand })

	// The last one out ends it: a line in the chat, the bar gone.
	gdo(a, "gcall_leave", map[string]any{"call": id})
	if !f.peers[key(id, u[1])].left {
		t.Error("leaving did not leave the media server")
	}
	gdo(owner, "gcall_leave", map[string]any{"call": id})
	for {
		json.Unmarshal(expect(t, b, "gcall_update"), &up)
		if up.Call == nil {
			break
		}
	}
	var typ string
	var ended bool
	svc.Store.pool.QueryRow(ctx, `SELECT type, ended_at IS NOT NULL FROM chat_calls WHERE id = $1`, id).Scan(&typ, &ended)
	if typ != "group" || !ended {
		t.Errorf("the call's row: %s %v", typ, ended)
	}
	msgs, _ := svc.Store.Page(ctx, chatID, u[0], PageQuery{Limit: 10})
	lines := 0
	for _, m := range msgs {
		var p struct{ Action, Reason string }
		if m.Kind == "service" && json.Unmarshal(m.Service, &p) == nil && p.Action == "gcall" {
			lines++
		}
	}
	if lines != 2 {
		t.Errorf("%d call lines in the chat, want started and ended", lines)
	}
}

func TestOnlyMembersMayJoinAGroupCall(t *testing.T) {
	svc, u, chatID, _ := gcallFixture(t)
	ctx := context.Background()
	outsider := device(svc, u[3])
	if _, code := gdo(outsider, "gcall_join", map[string]any{"chat": chatID}); code != "not_member" {
		t.Errorf("an outsider joined: %q", code)
	}
	priv, _ := svc.Store.OpenPrivate(ctx, u[0], u[1])
	owner := device(svc, u[0])
	if _, code := gdo(owner, "gcall_join", map[string]any{"chat": priv}); code != "forbidden" {
		t.Errorf("a group call in a private chat: %q", code)
	}
	// Someone in a one-to-one call is busy.
	a := device(svc, u[1])
	if _, code := do(a, "call_start", map[string]any{"peer": u[2]}); code != "" {
		t.Fatalf("one-to-one: %q", code)
	}
	if _, code := gdo(a, "gcall_join", map[string]any{"chat": chatID}); code != "in_call" {
		t.Errorf("joined a group call during a one-to-one call: %q", code)
	}
	// ...and someone in a group call cannot start a one-to-one one.
	ack, _ := gdo(owner, "gcall_join", map[string]any{"chat": chatID})
	_ = gcallID(t, ack)
	if _, code := do(owner, "call_start", map[string]any{"peer": u[2]}); code != "in_call" {
		t.Errorf("a one-to-one call from inside a group call: %q", code)
	}
	// Signals from a stranger to the call are refused.
	if _, code := gdo(outsider, "gcall_signal", map[string]any{"call": gcallID(t, ack), "d": map[string]any{"cand": map[string]string{}}}); code != "no_call" {
		t.Errorf("a stranger signalled into the call: %q", code)
	}
}

func TestAdminsHoldMicrophonesAndEndForEveryone(t *testing.T) {
	svc, u, chatID, f := gcallFixture(t)
	owner, a, b := device(svc, u[0]), device(svc, u[1]), device(svc, u[2])
	ack, _ := gdo(a, "gcall_join", map[string]any{"chat": chatID}) // a member starts it
	id := gcallID(t, ack)
	gdo(owner, "gcall_join", map[string]any{"chat": chatID})
	gdo(b, "gcall_join", map[string]any{"chat": chatID})
	if _, code := gdo(b, "gcall_mute", map[string]any{"call": id, "user": u[0], "on": true}); code != "forbidden" {
		t.Errorf("a member held the owner's microphone: %q", code)
	}
	if _, code := gdo(owner, "gcall_mute", map[string]any{"call": id, "user": u[2], "on": true}); code != "" {
		t.Fatalf("the owner holding b's microphone: %q", code)
	}
	var held struct{ On bool }
	json.Unmarshal(expect(t, b, "gcall_held"), &held)
	if !held.On || !f.muted[key(id, u[2])] {
		t.Error("b's microphone was not held back")
	}
	if _, code := gdo(b, "gcall_end", map[string]any{"call": id}); code != "forbidden" {
		t.Errorf("a member ended the call for everyone: %q", code)
	}
	if _, code := gdo(a, "gcall_end", map[string]any{"call": id}); code != "" { // its starter may
		t.Fatalf("the starter ending it: %q", code)
	}
	expect(t, owner, "gcall_ended")
	expect(t, b, "gcall_ended")
	if !f.ended[id] {
		t.Error("the media server's room was not closed")
	}
}

func TestAParticipantComesBackOrIsLetGo(t *testing.T) {
	svc, u, chatID, f := gcallFixture(t)
	defer func(r time.Duration) { rejoinWithin = r }(rejoinWithin)
	rejoinWithin = 300 * time.Millisecond
	owner, a := device(svc, u[0]), device(svc, u[1])
	ack, _ := gdo(owner, "gcall_join", map[string]any{"chat": chatID, "tab": "p-owner"})
	id := gcallID(t, ack)
	gdo(a, "gcall_join", map[string]any{"chat": chatID, "tab": "p-a"})

	svc.remove(a)
	svc.gcallsOnDisconnect(a)
	stateWhere(t, owner, "a shown as dropped", func(ms []GroupMember) bool { return len(ms) == 2 && ms[1].Lost })
	other := device(svc, u[1])
	if _, code := gdo(other, "gcall_resume", map[string]any{"call": id, "tab": "another page"}); code != "no_call" {
		t.Errorf("another page took the place: %q", code)
	}
	a2 := device(svc, u[1])
	if _, code := gdo(a2, "gcall_resume", map[string]any{"call": id, "tab": "p-a"}); code != "" {
		t.Fatalf("resume: %q", code)
	}
	time.Sleep(500 * time.Millisecond)
	if _, code := gdo(a2, "gcall_media", map[string]any{"call": id, "mic": true}); code != "" {
		t.Errorf("the resumed participant was let go: %q", code)
	}

	// The media connection fails by itself: they are out.
	f.mu.Lock()
	p := f.peers[key(id, u[1])]
	f.mu.Unlock()
	p.gone()
	stateWhere(t, owner, "a participant whose media failed is out", func(ms []GroupMember) bool { return len(ms) == 1 })
}
