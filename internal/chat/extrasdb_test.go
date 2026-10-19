package chat

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// Phase 11 against a real PostgreSQL (skipped without DOCFIND_PG_*).

func TestPollRules(t *testing.T) {
	s, users := testStore(t)
	ctx := context.Background()
	u := users(3)
	svc := New(s, fakeUsers(u), nil)
	chatID, _ := s.OpenPrivate(ctx, u[0], u[1])
	req := PollReq{Chat: chatID, CID: "01928c5e-7a3b-7c2d-8e9f-0a1b2c3d5a00", Question: "ناهار؟", Options: []string{"رستوران", "سفارش", "خانه"}}
	m, err := svc.SendPoll(ctx, u[0], req, nil)
	if err != nil || m.Kind != "poll" {
		t.Fatalf("send: %v", err)
	}
	var def PollDef
	json.Unmarshal(m.Extra, &def)
	again, err := svc.SendPoll(ctx, u[0], req, nil)
	var n int
	s.pool.QueryRow(ctx, `SELECT count(*) FROM chat_polls WHERE chat_id = $1`, chatID).Scan(&n)
	if err != nil || again.ID != m.ID || n != 1 {
		t.Fatalf("a resend made a second poll: %v %d %d polls=%d", err, m.ID, again.ID, n)
	}
	// Security check 1 for polls: an outsider neither votes nor reads.
	if _, err := svc.Vote(ctx, u[2], chatID, def.ID, []int{0}); !errors.Is(err, ErrNotMember) {
		t.Errorf("outsider vote: %v", err)
	}
	own, _ := s.OpenPrivate(ctx, u[2], u[2])
	if _, err := svc.Vote(ctx, u[2], own, def.ID, []int{0}); !errors.Is(err, ErrNotMember) {
		t.Errorf("vote through another chat: %v", err)
	}
	// Results show after voting (the creator sees them at once).
	p, _ := s.Poll(ctx, def.ID)
	if st, _ := svc.pollState(ctx, u[1], "member", p); st.Counts != nil {
		t.Error("results shown before voting")
	}
	if st, _ := svc.pollState(ctx, u[0], "member", p); st.Counts == nil || !st.MayClose {
		t.Error("the creator does not see the results or cannot close")
	}
	if _, err := svc.Vote(ctx, u[1], chatID, def.ID, []int{0, 1}); !errors.Is(err, ErrBadVote) {
		t.Errorf("two answers in a one-answer poll: %v", err)
	}
	st, err := svc.Vote(ctx, u[1], chatID, def.ID, []int{2})
	if err != nil || !st.Voted || st.Total != 1 || st.Counts[2] != 1 || len(st.Mine) != 1 {
		t.Fatalf("vote: %v %+v", err, st)
	}
	if err := svc.ClosePoll(ctx, u[1], chatID, def.ID); !errors.Is(err, ErrForbidden) {
		t.Errorf("closed by someone who did not make it: %v", err)
	}
	if err := svc.ClosePoll(ctx, u[0], chatID, def.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Vote(ctx, u[1], chatID, def.ID, []int{1}); !errors.Is(err, ErrPollClosed) {
		t.Errorf("vote after closing: %v", err)
	}
	// A quiz answer is final and shows the right one.
	q, _ := svc.SendPoll(ctx, u[0], PollReq{Chat: chatID, CID: "01928c5e-7a3b-7c2d-8e9f-0a1b2c3d5a01", Question: "پایتخت؟", Options: []string{"مونیخ", "برلین"}, Quiz: true, Correct: 1, Explanation: "برلین"}, nil)
	json.Unmarshal(q.Extra, &def)
	st, err = svc.Vote(ctx, u[1], chatID, def.ID, []int{0})
	if err != nil || st.Correct == nil || *st.Correct != 1 || st.Explanation != "برلین" {
		t.Fatalf("quiz: %v %+v", err, st)
	}
	if _, err := svc.Vote(ctx, u[1], chatID, def.ID, []int{1}); !errors.Is(err, ErrBadVote) {
		t.Errorf("a quiz answer changed: %v", err)
	}
	// A poll with a closing time closes on the scheduler's tick.
	c, _ := svc.SendPoll(ctx, u[0], PollReq{Chat: chatID, CID: "01928c5e-7a3b-7c2d-8e9f-0a1b2c3d5a02", Question: "؟", Options: []string{"a", "b"}, CloseIn: 60}, nil)
	json.Unmarshal(c.Extra, &def)
	svc.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	svc.closeDuePolls(ctx)
	if p, _ := s.Poll(ctx, def.ID); p.ClosedAt == nil {
		t.Error("a poll past its time is still open")
	}
}

func TestScheduledMessages(t *testing.T) {
	s, users := testStore(t)
	ctx := context.Background()
	u := users(3)
	svc := New(s, fakeUsers(u), nil)
	chatID, _ := s.OpenPrivate(ctx, u[0], u[1])
	at := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)
	cid := "01928c5e-7a3b-7c2d-8e9f-0a1b2c3d5b00"
	it, err := svc.Schedule(ctx, u[0], ScheduleReq{Chat: chatID, CID: cid, Text: "یادآوری جلسه", At: at})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Schedule(ctx, u[2], ScheduleReq{Chat: chatID, CID: "01928c5e-7a3b-7c2d-8e9f-0a1b2c3d5b01", Text: "x", At: at}); !errors.Is(err, ErrNotMember) {
		t.Errorf("an outsider scheduled into the chat: %v", err)
	}
	if err := svc.ScheduledAction(ctx, u[2], chatID, it.ID, false); !errors.Is(err, ErrNotMember) {
		t.Errorf("an outsider cancelled: %v", err)
	}
	if err := svc.ScheduledAction(ctx, u[1], chatID, it.ID, true); !errors.Is(err, ErrNotMember) {
		t.Errorf("the other member sent someone else's scheduled message: %v", err)
	}
	if _, err := s.byClientID(ctx, chatID, u[0], cid); err == nil {
		t.Fatal("sent before its time")
	}
	svc.runScheduled(ctx)
	if _, err := s.byClientID(ctx, chatID, u[0], cid); err == nil {
		t.Fatal("sent before its time by the tick")
	}
	s.pool.Exec(ctx, `UPDATE chat_scheduled SET send_at = now() - interval '1 second' WHERE id = $1`, it.ID)
	svc.runScheduled(ctx)
	m, err := s.byClientID(ctx, chatID, u[0], cid)
	if err != nil || m.Text != "یادآوری جلسه" {
		t.Fatalf("not sent at its time: %v", err)
	}
	if list, _ := s.ListScheduled(ctx, chatID, u[0]); len(list) != 0 {
		t.Errorf("%d still waiting after sending", len(list))
	}
	// "When online" waits for the other person, and is for private chats only.
	w, err := svc.Schedule(ctx, u[0], ScheduleReq{Chat: chatID, CID: "01928c5e-7a3b-7c2d-8e9f-0a1b2c3d5b02", Text: "وقتی آمدی", WhenOnline: true})
	if err != nil {
		t.Fatal(err)
	}
	svc.runScheduled(ctx)
	if list, _ := s.ListScheduled(ctx, chatID, u[0]); len(list) != 1 || list[0].ID != w.ID {
		t.Error("a when-online message left while its reader was away")
	}
	saved, _ := s.OpenPrivate(ctx, u[0], u[0])
	if _, err := svc.Schedule(ctx, u[0], ScheduleReq{Chat: saved, CID: "01928c5e-7a3b-7c2d-8e9f-0a1b2c3d5b03", Text: "x", WhenOnline: true}); !errors.Is(err, ErrForbidden) {
		t.Errorf("when-online outside a private chat: %v", err)
	}
	if _, err := svc.Schedule(ctx, u[0], ScheduleReq{Chat: chatID, CID: "01928c5e-7a3b-7c2d-8e9f-0a1b2c3d5b04", Text: "x", At: time.Now().Add(-time.Hour).Format(time.RFC3339)}); !errors.Is(err, ErrBadTime) {
		t.Errorf("a time in the past: %v", err)
	}
}

func TestLiveLocation(t *testing.T) {
	s, users := testStore(t)
	ctx := context.Background()
	u := users(2)
	svc := New(s, fakeUsers(u), nil)
	chatID, _ := s.OpenPrivate(ctx, u[0], u[1])
	m, err := svc.SendLocation(ctx, u[0], LocationReq{Chat: chatID, CID: "01928c5e-7a3b-7c2d-8e9f-0a1b2c3d5c00", Lat: 35.7, Lng: 51.4, Live: 900}, nil)
	if err != nil || m.Kind != "location" {
		t.Fatalf("send: %v", err)
	}
	if err := svc.MoveLocation(ctx, u[1], chatID, m.ID, 35.8, 51.5, 5, false); !errors.Is(err, ErrForbidden) {
		t.Errorf("moved by someone else: %v", err)
	}
	if err := svc.MoveLocation(ctx, u[0], chatID, m.ID, 35.8, 51.5, 5, false); err != nil {
		t.Fatal(err)
	}
	got, _ := s.ByIDs(ctx, chatID, u[1], []int64{m.ID})
	var p Place
	json.Unmarshal(got[0].Extra, &p)
	if p.Lat != 35.8 || p.Updated == nil {
		t.Errorf("the other member does not see the move: %+v", p)
	}
	if err := svc.MoveLocation(ctx, u[0], chatID, m.ID, 0, 0, 0, true); err != nil {
		t.Fatal(err)
	}
	if err := svc.MoveLocation(ctx, u[0], chatID, m.ID, 35.9, 51.6, 5, false); !errors.Is(err, ErrNotLive) {
		t.Errorf("moved after stopping: %v", err)
	}
	if _, err := svc.SendLocation(ctx, u[0], LocationReq{Chat: chatID, CID: "01928c5e-7a3b-7c2d-8e9f-0a1b2c3d5c01", Lat: 95, Lng: 51.4}, nil); !errors.Is(err, ErrBadPlace) {
		t.Errorf("a point off the globe: %v", err)
	}
	if _, err := svc.SendLocation(ctx, u[0], LocationReq{Chat: chatID, CID: "01928c5e-7a3b-7c2d-8e9f-0a1b2c3d5c02", Lat: 35, Lng: 51, Live: 60}, nil); !errors.Is(err, ErrBadPlace) {
		t.Errorf("a live time that is not offered: %v", err)
	}
}

func TestStickersAndPackFiles(t *testing.T) {
	s, users := testStore(t)
	ctx := context.Background()
	u := users(3)
	svc := New(s, fakeUsers(u), nil)
	chatID, _ := s.OpenPrivate(ctx, u[0], u[1])
	pack, err := s.CreatePack(ctx, "sticker", "آزمون", u[0])
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.pool.Exec(context.Background(), `DELETE FROM chat_packs WHERE id = $1`, pack) })
	mid, err := s.InsertMedia(ctx, &Media{Kind: "sticker", Mime: "image/webp", Size: 10, sha: "ab"}, u[0], false)
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.AddPackItem(ctx, &PackItem{Pack: pack, Media: mid, Format: "webp", Emoji: "👍"}, "👍 تایید")
	if err != nil {
		t.Fatal(err)
	}
	m, err := svc.SendSticker(ctx, u[0], StickerReq{Chat: chatID, CID: "01928c5e-7a3b-7c2d-8e9f-0a1b2c3d5d00", Item: item}, nil)
	if err != nil || m.Kind != "sticker" || m.MediaID == nil || *m.MediaID != mid {
		t.Fatalf("send: %v %+v", err, m)
	}
	if _, err := svc.SendSticker(ctx, u[2], StickerReq{Chat: chatID, CID: "01928c5e-7a3b-7c2d-8e9f-0a1b2c3d5d01", Item: item}, nil); !errors.Is(err, ErrNotMember) {
		t.Errorf("an outsider sent into the chat: %v", err)
	}
	if _, err := svc.SendSticker(ctx, u[0], StickerReq{Chat: chatID, CID: "01928c5e-7a3b-7c2d-8e9f-0a1b2c3d5d02", Item: item + 99999}, nil); !errors.Is(err, ErrNoItem) {
		t.Errorf("a sticker that does not exist: %v", err)
	}
	// Pack files are open to everyone; a file sent in a chat is not.
	if ok, _ := s.MayReadMedia(ctx, u[2], mid); !ok {
		t.Error("a pack file is closed to a signed-in person")
	}
	other, _ := s.InsertMedia(ctx, &Media{Kind: "photo", Mime: "image/jpeg", Size: 10, sha: "cd"}, u[0], false)
	if ok, _ := s.MayReadMedia(ctx, u[2], other); ok {
		t.Error("someone else's file opened through the pack rule")
	}
	if found, _ := s.SearchPacks(ctx, "sticker", Normalize("تایید"), 10); len(found) != 1 {
		t.Errorf("search found %d", len(found))
	}
}
