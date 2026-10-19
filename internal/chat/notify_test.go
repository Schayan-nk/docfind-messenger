package chat

import (
	"context"
	"testing"
	"time"
)

// Phase 6 against a real database (skipped without DOCFIND_PG_*): who hears
// about a message.

// notices captures what the service would hand to the push queue.
func notices(svc *Service) chan Notice {
	ch := make(chan Notice, 16)
	svc.Notify = func(_ context.Context, n Notice) { ch <- n }
	return ch
}

func next(t *testing.T, ch chan Notice) *Notice {
	t.Helper()
	select {
	case n := <-ch:
		return &n
	case <-time.After(3 * time.Second):
		return nil
	}
}

func reached(n *Notice) map[string]NoticeTo {
	out := map[string]NoticeTo{}
	if n != nil {
		for _, to := range n.To {
			out[to.User] = to
		}
	}
	return out
}

func TestWhoHearsAboutAMessage(t *testing.T) {
	svc, u, id := groupFixture(t, "supergroup", 5, 4, false)
	ctx := context.Background()
	ch := notices(svc)
	owner, a, b, c, d := u[0], u[1], u[2], u[3], u[4]

	// Everyone but the sender, with the group's title and the sender's name.
	if err := send(svc, owner, id, "جلسه ساعت ۱۰"); err != nil {
		t.Fatal(err)
	}
	n := next(t, ch)
	got := reached(n)
	if n == nil || len(got) != 4 || got[owner].User != "" || n.Title != "گروه آزمایشی" || n.Text != owner+": جلسه ساعت ۱۰" {
		t.Fatalf("first notice: %+v", n)
	}

	// a muted, b mentions only, c switched groups off, d is reading the chat.
	on := true
	if _, err := svc.SetPrefs(ctx, a, id, PrefsReq{Muted: &on}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetPrefs(ctx, b, id, PrefsReq{MentionsOnly: &on}); err != nil {
		t.Fatal(err)
	}
	off := defaultNotify()
	off.Groups, off.Preview = false, false
	if err := svc.Store.SetNotifySettings(ctx, c, off); err != nil {
		t.Fatal(err)
	}
	reader := &conn{user: d, out: make(chan []byte, 256)} // a device that shows the chat; its frames just queue
	reader.watch.Store(id)
	svc.mu.Lock()
	if svc.conns == nil {
		svc.conns = map[string]map[*conn]struct{}{}
	}
	svc.conns[d] = map[*conn]struct{}{reader: {}}
	svc.mu.Unlock()

	if err := send(svc, owner, id, "یادآوری"); err != nil {
		t.Fatal(err)
	}
	if n := next(t, ch); n != nil {
		t.Errorf("nobody should hear of this one: %+v", n.To)
	}
	// A reply names the person it answers: a and b hear of it (muted or
	// mentions only), c does not (groups off), d is still reading.
	msgs, _ := svc.Store.Page(ctx, id, owner, PageQuery{})
	var fromA int64
	for _, m := range msgs {
		if m.Sender == a {
			fromA = m.ID
		}
	}
	if fromA == 0 {
		if err := send(svc, a, id, "سلام از من"); err != nil {
			t.Fatal(err)
		}
		next(t, ch)
		msgs, _ = svc.Store.Page(ctx, id, owner, PageQuery{Limit: 1})
		fromA = msgs[0].ID
	}
	if _, err := svc.SendText(ctx, owner, SendReq{Chat: id, CID: uuidV7(), Text: "پاسخ به شما", ReplyTo: fromA}, nil); err != nil {
		t.Fatal(err)
	}
	got = reached(next(t, ch))
	if _, ok := got[a]; !ok || !got[a].Mention {
		t.Errorf("a reply did not reach the muted person it answers: %v", got)
	}
	if _, ok := got[c]; ok {
		t.Error("someone with groups switched off heard of a reply in a group")
	}
	if _, ok := got[d]; ok {
		t.Error("someone reading the chat was sent a notification")
	}

	// A spoiler stays hidden in the notification.
	reader.watch.Store(0)
	if _, err := svc.SendText(ctx, owner, SendReq{Chat: id, CID: uuidV7(), Text: "رمز ۱۲۳۴", Entities: []Entity{{Type: "spoiler", Offset: 4, Length: 4}}}, nil); err != nil {
		t.Fatal(err)
	}
	if n := next(t, ch); n == nil || n.Text != owner+": پیام" {
		t.Errorf("a spoiler in a notification: %+v", n)
	}
}

func TestPrivateAndSavedNotices(t *testing.T) {
	s, users := testStore(t)
	ctx := context.Background()
	u := users(2)
	svc := New(s, fakeUsers(u), nil)
	ch := notices(svc)
	id, _ := s.OpenPrivate(ctx, u[0], u[1])
	off := defaultNotify()
	off.Preview = false
	if err := s.SetNotifySettings(ctx, u[1], off); err != nil {
		t.Fatal(err)
	}
	if err := send(svc, u[0], id, "سلام"); err != nil {
		t.Fatal(err)
	}
	n := next(t, ch)
	if n == nil || n.Title != u[0] || n.Text != "سلام" || len(n.To) != 1 || n.To[0].User != u[1] || n.To[0].Preview {
		t.Fatalf("private notice: %+v", n)
	}
	saved, _ := s.OpenPrivate(ctx, u[0], u[0])
	if err := send(svc, u[0], saved, "یادداشت"); err != nil {
		t.Fatal(err)
	}
	if n := next(t, ch); n != nil {
		t.Errorf("Saved Messages notified: %+v", n)
	}
	got, err := s.NotifySettings(ctx, u[1])
	if err != nil || got.Preview || !got.Groups {
		t.Errorf("settings read back: %+v %v", got, err)
	}
}
