package chat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Phase 7 against a real database (skipped without DOCFIND_PG_*): the
// switchboard. Devices are sockets without a network: what the server sends
// them queues on their out channel.

type frame struct {
	T  string          `json:"t"`
	ID string          `json:"id"`
	D  json.RawMessage `json:"d"`
}

// device opens a socket for user on svc.
func device(svc *Service, user string) *conn {
	ctx, cancel := context.WithCancel(context.Background())
	c := &conn{svc: svc, user: user, host: "chat.example.ir", out: make(chan []byte, 256), ctx: ctx, cancel: cancel}
	svc.add(c)
	return c
}

// expect waits for c's next frame of type t, skipping others.
func expect(t *testing.T, c *conn, typ string) json.RawMessage {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case b := <-c.out:
			var f frame
			if json.Unmarshal(b, &f) == nil && f.T == typ {
				return f.D
			}
		case <-deadline:
			t.Fatalf("%s never got %s", c.user, typ)
			return nil
		}
	}
}

// ended waits for c's call_ended for call id and returns how it ended.
func ended(t *testing.T, c *conn, id int64) (reason string, duration int) {
	t.Helper()
	for {
		var e struct {
			Call     int64
			Reason   string
			Duration int
		}
		json.Unmarshal(expect(t, c, "call_ended"), &e)
		if e.Call == id {
			return e.Reason, e.Duration
		}
	}
}

// isCallLine says m is the line a call left in its chat.
func isCallLine(m *Message) bool {
	var p struct{ Action string }
	return m.Kind == "service" && json.Unmarshal(m.Service, &p) == nil && p.Action == "call"
}

// quiet says c received no frame of type typ (draining what it has).
func quiet(c *conn, typ string) bool {
	time.Sleep(100 * time.Millisecond)
	for {
		select {
		case b := <-c.out:
			var f frame
			if json.Unmarshal(b, &f) == nil && f.T == typ {
				return false
			}
		default:
			return true
		}
	}
}

// do sends a frame from c as a page would and returns the ack or the error.
func do(c *conn, typ string, d any) (map[string]json.RawMessage, string) {
	b, _ := json.Marshal(d)
	c.handleCall(context.Background(), Envelope{T: typ, ID: "x" + typ, D: b})
	for {
		select {
		case raw := <-c.out:
			var f frame
			if json.Unmarshal(raw, &f) != nil || f.ID != "x"+typ {
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

func callFixture(t *testing.T, n int) (*Service, []string, *fakeUsersT) {
	t.Helper()
	s, users := testStore(t)
	u := users(n)
	f := fakeUsers(u)
	svc := New(s, f, nil)
	svc.ICE = func(user string, _ int, host string) []ICEServer {
		return []ICEServer{{URLs: []string{"turn:" + host + ":3478"}, Username: "1:" + user, Credential: "x"}}
	}
	return svc, u, f
}

func callID(t *testing.T, ack map[string]json.RawMessage) int64 {
	t.Helper()
	var in CallInfo
	if err := json.Unmarshal(ack["call"], &in); err != nil || in.ID == 0 {
		t.Fatalf("no call in %s", ack["call"])
	}
	return in.ID
}

func TestACallRingsIsAnsweredOnOneDeviceAndEnds(t *testing.T) {
	svc, u, _ := callFixture(t, 2)
	ctx := context.Background()
	notices := make(chan CallNotice, 4)
	svc.NotifyCall = func(_ context.Context, n CallNotice) { notices <- n }
	a, b1, b2 := device(svc, u[0]), device(svc, u[1]), device(svc, u[1])

	ack, code := do(a, "call_start", map[string]any{"peer": u[1], "video": false})
	if code != "" {
		t.Fatalf("start: %s", code)
	}
	id := callID(t, ack)
	if !strings.Contains(string(ack["ice"]), "turn:chat.example.ir:3478") {
		t.Errorf("the caller got no ICE servers: %s", ack["ice"])
	}
	expect(t, b1, "call_incoming")
	expect(t, b2, "call_incoming")
	if n := <-notices; n.To != u[1] || n.Missed || n.Call != id {
		t.Errorf("ringing notice: %+v", n)
	}

	if _, code := do(b1, "call_accept", map[string]any{"call": id}); code != "" {
		t.Fatalf("accept: %s", code)
	}
	expect(t, a, "call_accepted")
	expect(t, b2, "call_taken")
	if _, code := do(b2, "call_accept", map[string]any{"call": id}); code != "no_call" {
		t.Errorf("a second device answered the same call: %q", code)
	}

	// Offer and answer pass between the two devices in the call only.
	do(a, "call_signal", map[string]any{"call": id, "d": map[string]string{"type": "offer", "sdp": "v=0"}})
	if d := expect(t, b1, "call_signal"); !strings.Contains(string(d), "offer") {
		t.Errorf("the offer arrived as %s", d)
	}
	if !quiet(b2, "call_signal") {
		t.Error("the callee's other device saw the offer")
	}
	do(b1, "call_signal", map[string]any{"call": id, "d": map[string]string{"type": "answer", "sdp": "v=0"}})
	expect(t, a, "call_signal")

	do(a, "call_state", map[string]any{"call": id, "state": "connected"})
	time.Sleep(1100 * time.Millisecond)
	if _, code := do(b1, "call_hangup", map[string]any{"call": id}); code != "" {
		t.Fatalf("hangup: %s", code)
	}
	if r, d := ended(t, a, id); r != "ended" || d < 1 {
		t.Errorf("ended as %s after %d s", r, d)
	}

	// The record: both lists, and a line in the chat.
	calls, _ := svc.Store.Calls(ctx, u[0], 0, 10)
	if len(calls) != 1 || !calls[0].Outgoing || calls[0].Peer.ID != u[1] || calls[0].Reason != "ended" || calls[0].Duration < 1 {
		t.Errorf("the caller's list: %+v", calls)
	}
	calls, _ = svc.Store.Calls(ctx, u[1], 0, 10)
	if len(calls) != 1 || calls[0].Outgoing || calls[0].Missed {
		t.Errorf("the callee's list: %+v", calls)
	}
	msgs, _ := svc.Store.Page(ctx, calls[0].Chat, u[0], PageQuery{Limit: 5})
	if len(msgs) == 0 || !isCallLine(msgs[len(msgs)-1]) {
		t.Errorf("no call line in the chat: %d messages", len(msgs))
	}
	select {
	case n := <-notices:
		t.Errorf("an answered call was notified as missed: %+v", n)
	default:
	}
}

// Security check 10, one to one: only the two devices in a call may
// accept, signal, resume or end it.
func TestOnlyTheTwoDevicesInACallMayActInIt(t *testing.T) {
	svc, u, _ := callFixture(t, 3)
	a, b, c := device(svc, u[0]), device(svc, u[1]), device(svc, u[2])
	ack, _ := do(a, "call_start", map[string]any{"peer": u[1], "tab": "page-a"})
	id := callID(t, ack)
	if _, code := do(c, "call_accept", map[string]any{"call": id}); code != "no_call" {
		t.Errorf("a stranger answered: %q", code)
	}
	if _, code := do(a, "call_signal", map[string]any{"call": id, "d": "x"}); code != "no_call" {
		t.Errorf("signals passed before anyone answered: %q", code)
	}
	do(b, "call_accept", map[string]any{"call": id})
	for _, f := range []string{"call_signal", "call_media", "call_state", "call_hangup", "call_resume"} {
		if _, code := do(c, f, map[string]any{"call": id, "d": map[string]bool{"cam": true}, "state": "connected"}); code != "no_call" {
			t.Errorf("a stranger's %s was taken: %q", f, code)
		}
	}
	a2 := device(svc, u[0])
	if _, code := do(a2, "call_resume", map[string]any{"call": id, "tab": "another page"}); code != "no_call" {
		t.Errorf("another device took a call its first device is still in: %q", code)
	}
	if _, code := do(a, "call_signal", map[string]any{"call": id, "d": strings.Repeat("x", maxSignal+1)}); code == "" {
		t.Error("an oversized signal was passed on")
	}
}

func TestWhoMayCall(t *testing.T) {
	svc, u, f := callFixture(t, 3)
	ctx := context.Background()
	a := device(svc, u[0])
	if _, code := do(a, "call_start", map[string]any{"peer": u[0]}); code != "forbidden" {
		t.Errorf("called themselves: %q", code)
	}
	svc.Store.SetPrivacy(ctx, u[1], Privacy{Calls: Rule{Mode: "nobody"}})
	if _, code := do(a, "call_start", map[string]any{"peer": u[1]}); code != "call_privacy" {
		t.Errorf("«nobody» was called: %q", code)
	}
	svc.Store.SetPrivacy(ctx, u[1], Privacy{Calls: Rule{Mode: "nobody", Always: []string{u[0]}}})
	ack, code := do(a, "call_start", map[string]any{"peer": u[1]})
	if code != "" {
		t.Fatalf("an «always» exception could not call: %q", code)
	}
	do(a, "call_hangup", map[string]any{"call": callID(t, ack)})
	svc.Store.SetBlock(ctx, u[2], u[0], true)
	if _, code := do(a, "call_start", map[string]any{"peer": u[2]}); code != "blocked" {
		t.Errorf("someone who blocked the caller was called: %q", code)
	}
	svc.Store.SetBlock(ctx, u[2], u[0], false)
	f.mu.Lock()
	f.m[u[2]].Disabled = true
	f.mu.Unlock()
	if _, code := do(a, "call_start", map[string]any{"peer": u[2]}); code != "no_user" {
		t.Errorf("a disabled account was called: %q", code)
	}
}

func TestBusyMissedCancelledAndDeclined(t *testing.T) {
	svc, u, _ := callFixture(t, 3)
	ctx := context.Background()
	defer func(r time.Duration) { ringFor = r }(ringFor)
	ringFor = 400 * time.Millisecond
	notices := make(chan CallNotice, 8)
	svc.NotifyCall = func(_ context.Context, n CallNotice) { notices <- n }
	a, b, c := device(svc, u[0]), device(svc, u[1]), device(svc, u[2])

	// b is being called by a; c finds b busy.
	ack, _ := do(a, "call_start", map[string]any{"peer": u[1]})
	first := callID(t, ack)
	var ab CallInfo
	json.Unmarshal(ack["call"], &ab)
	<-notices
	ack, code := do(c, "call_start", map[string]any{"peer": u[1]})
	var in CallInfo
	json.Unmarshal(ack["call"], &in)
	if code != "" || in.State != "ended" || in.Reason != "busy" {
		t.Errorf("a busy line: %q %+v", code, in)
	}
	if n := <-notices; !n.Missed || n.To != u[1] {
		t.Errorf("the busy call was not a missed call for them: %+v", n)
	}
	if _, code := do(a, "call_start", map[string]any{"peer": u[2]}); code != "in_call" {
		t.Errorf("a started a second call: %q", code)
	}

	// Nobody answers: missed.
	if r, _ := ended(t, b, first); r != "missed" {
		t.Errorf("an unanswered call ended as %q", r)
	}
	if n := <-notices; !n.Missed || n.Call != first {
		t.Errorf("no missed-call notice: %+v", n)
	}
	list, _ := svc.Store.Calls(ctx, u[1], 0, 10)
	missed := 0
	for _, e := range list {
		if e.Missed {
			missed++
		}
	}
	if missed != 2 {
		t.Errorf("b's list shows %d missed calls, want 2: %+v", missed, list)
	}

	// The caller puts it down while it rings: cancelled, missed for b.
	ack, _ = do(a, "call_start", map[string]any{"peer": u[1]})
	id := callID(t, ack)
	<-notices
	do(a, "call_hangup", map[string]any{"call": id})
	if r, _ := ended(t, b, id); r != "cancelled" {
		t.Errorf("a call put down while ringing ended as %q", r)
	}

	// b declines with a message: a hears «declined» and gets the words.
	svc.callLimit = newLimiter(100, 100)
	ack, _ = do(a, "call_start", map[string]any{"peer": u[1]})
	id = callID(t, ack)
	if _, code := do(b, "call_decline", map[string]any{"call": id, "text": "در جلسه هستم؛ بعد تماس می‌گیرم."}); code != "" {
		t.Fatalf("decline: %q", code)
	}
	if r, _ := ended(t, a, id); r != "declined" {
		t.Errorf("declined as %q", r)
	}
	msgs, _ := svc.Store.Page(ctx, ab.Chat, u[0], PageQuery{Limit: 10})
	found := false
	for _, m := range msgs {
		found = found || (m.Sender == u[1] && strings.Contains(m.Text, "در جلسه هستم"))
	}
	if !found {
		t.Error("the message sent with the decline did not arrive")
	}
}

func TestADroppedDeviceTakesItsCallBackOrTheCallEnds(t *testing.T) {
	svc, u, _ := callFixture(t, 2)
	ctx := context.Background()
	defer func(r time.Duration) { rejoinWithin = r }(rejoinWithin)
	rejoinWithin = 500 * time.Millisecond
	a, b := device(svc, u[0]), device(svc, u[1])
	ack, _ := do(a, "call_start", map[string]any{"peer": u[1], "video": false})
	id := callID(t, ack)
	do(b, "call_accept", map[string]any{"call": id})
	do(a, "call_state", map[string]any{"call": id, "state": "connected"})

	// a's socket drops; a new one is told and takes the call back.
	svc.remove(a)
	svc.callsOnDisconnect(a)
	a2 := device(svc, u[0])
	svc.callsOnConnect(a2)
	expect(t, a2, "call_active")
	if _, code := do(a2, "call_resume", map[string]any{"call": id}); code != "" {
		t.Fatalf("resume: %q", code)
	}
	expect(t, b, "call_back")
	time.Sleep(700 * time.Millisecond)
	if _, code := do(a2, "call_media", map[string]any{"call": id, "d": map[string]bool{"cam": true}}); code != "" {
		t.Fatalf("the resumed call is gone: %q", code)
	}
	var video bool
	svc.Store.pool.QueryRow(ctx, `SELECT is_video FROM chat_calls WHERE id = $1`, id).Scan(&video)
	if !video {
		t.Error("turning the camera on did not make it a video call")
	}

	// It drops again and nobody comes back: the call ends, failed.
	svc.remove(a2)
	svc.callsOnDisconnect(a2)
	if r, _ := ended(t, b, id); r != "failed" {
		t.Errorf("a call whose device never came back ended as %q", r)
	}
}

func TestRatingsAndARestartedServer(t *testing.T) {
	svc, u, _ := callFixture(t, 2)
	ctx := context.Background()
	chatID, _ := svc.Store.OpenPrivate(ctx, u[0], u[1])
	id, _, err := svc.Store.CallCreate(ctx, chatID, u[0], u[1], true)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := svc.Store.CallRate(ctx, id, u[0], 4, ""); ok {
		t.Error("a call still open was rated")
	}
	if n, err := svc.Store.CloseOpenCalls(ctx, time.Now()); err != nil || n < 1 {
		t.Errorf("open calls at start-up: %d %v", n, err)
	}
	if ok, _ := svc.Store.CallRate(ctx, id, u[0], 4, "صدا قطع و وصل می‌شد"); !ok {
		t.Error("an ended call could not be rated")
	}
	if ok, _ := svc.Store.CallRate(ctx, id, "someone.else", 5, ""); ok {
		t.Error("someone not in the call rated it")
	}
	svc.Store.CallHide(ctx, id, u[0])
	if list, _ := svc.Store.Calls(ctx, u[0], 0, 10); len(list) != 0 {
		t.Errorf("a hidden call is still listed: %+v", list)
	}
	if list, _ := svc.Store.Calls(ctx, u[1], 0, 10); len(list) != 1 || list[0].Reason != "failed" || !list[0].Video {
		t.Errorf("the other person's list: %+v", list)
	}
}

// The page in a call reconnects before the server has noticed its old socket
// died: it takes the call back at once, and the old socket's late close
// does not end the call.
func TestThePageInACallTakesItBackOnANewSocket(t *testing.T) {
	svc, u, _ := callFixture(t, 2)
	defer func(r time.Duration) { rejoinWithin = r }(rejoinWithin)
	rejoinWithin = 300 * time.Millisecond
	a, b := device(svc, u[0]), device(svc, u[1])
	ack, _ := do(a, "call_start", map[string]any{"peer": u[1], "tab": "page-a"})
	id := callID(t, ack)
	do(b, "call_accept", map[string]any{"call": id, "tab": "page-b"})
	a2 := device(svc, u[0])
	if _, code := do(a2, "call_resume", map[string]any{"call": id, "tab": "page-a"}); code != "" {
		t.Fatalf("the same page could not take its call back: %q", code)
	}
	svc.remove(a)
	svc.callsOnDisconnect(a) // the old socket's close, noticed late
	time.Sleep(500 * time.Millisecond)
	if _, code := do(a2, "call_signal", map[string]any{"call": id, "d": "x"}); code != "" {
		t.Errorf("the call ended when the old socket closed: %q", code)
	}
}

// A call waiting for its device to come back is that page's: another page of
// the same person cannot take it over (and end it).
func TestAnotherPageCannotTakeOverAWaitingCall(t *testing.T) {
	svc, u, _ := callFixture(t, 2)
	a, b := device(svc, u[0]), device(svc, u[1])
	ack, _ := do(a, "call_start", map[string]any{"peer": u[1], "tab": "page-a"})
	id := callID(t, ack)
	do(b, "call_accept", map[string]any{"call": id, "tab": "page-b"})
	svc.remove(a)
	svc.callsOnDisconnect(a)
	other := device(svc, u[0])
	if _, code := do(other, "call_resume", map[string]any{"call": id, "tab": "page-x"}); code != "no_call" {
		t.Errorf("another page took over a waiting call: %q", code)
	}
	back := device(svc, u[0])
	if _, code := do(back, "call_resume", map[string]any{"call": id, "tab": "page-a"}); code != "" {
		t.Errorf("the page in the call could not come back: %q", code)
	}
	expect(t, back, "call_accepted") // it had not heard the call was answered
}

// The caller's socket drops while it rings, the call is answered meanwhile,
// and the caller never comes back: it failed; it was not missed.
func TestAnAnsweredCallWhoseCallerNeverCameBackFailed(t *testing.T) {
	svc, u, _ := callFixture(t, 2)
	defer func(r time.Duration) { rejoinWithin = r }(rejoinWithin)
	rejoinWithin = 300 * time.Millisecond
	notices := make(chan CallNotice, 4)
	svc.NotifyCall = func(_ context.Context, n CallNotice) { notices <- n }
	a, b := device(svc, u[0]), device(svc, u[1])
	ack, _ := do(a, "call_start", map[string]any{"peer": u[1]})
	id := callID(t, ack)
	<-notices
	svc.remove(a)
	svc.callsOnDisconnect(a)
	do(b, "call_accept", map[string]any{"call": id})
	if r, _ := ended(t, b, id); r != "failed" {
		t.Errorf("ended as %q", r)
	}
	select {
	case n := <-notices:
		t.Errorf("an answered call was notified as missed: %+v", n)
	case <-time.After(200 * time.Millisecond):
	}
}
