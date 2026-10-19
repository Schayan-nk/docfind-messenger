package chat

import (
	"context"
	"testing"
)

// Phase 2 rules against a real database (skipped without DOCFIND_PG_*).

func TestOutsidersCannotTouchMessages(t *testing.T) {
	s, users := testStore(t)
	ctx := context.Background()
	u := users(3)
	svc := New(s, fakeUsers(u), nil)
	chatID, _ := s.OpenPrivate(ctx, u[0], u[1])
	msgs, err := svc.SendText(ctx, u[0], SendReq{Chat: chatID, CID: "01928c5e-7a3b-7c2d-8e9f-0a1b2c3d4e70", Text: "سلام"}, nil)
	if err != nil || len(msgs) != 1 {
		t.Fatal(err)
	}
	id := msgs[0].ID
	// Security check 1: no read, edit, delete, react or pin from outside.
	if err := svc.Edit(ctx, u[2], chatID, id, "x", nil, nil); err != ErrNotMember {
		t.Errorf("outsider edit: %v", err)
	}
	if err := svc.Delete(ctx, u[2], chatID, []int64{id}, true, nil); err != ErrNotMember {
		t.Errorf("outsider delete: %v", err)
	}
	if err := svc.React(ctx, u[2], chatID, id, "👍", true, nil); err != ErrNotMember {
		t.Errorf("outsider react: %v", err)
	}
	if err := svc.Pin(ctx, u[2], chatID, id, true, nil); err != ErrNotMember {
		t.Errorf("outsider pin: %v", err)
	}
	// An outsider's own chat cannot be used to reach the message either.
	own, _ := s.OpenPrivate(ctx, u[2], u[2])
	if err := svc.React(ctx, u[2], own, id, "👍", true, nil); err != ErrNotMember {
		t.Errorf("react through another chat: %v", err)
	}
	if _, err := svc.Forward(ctx, u[2], ForwardReq{From: chatID, IDs: []int64{id}, To: own, CID: "01928c5e-7a3b-7c2d-8e9f-0a1b2c3d4e71"}, nil); err != ErrNotMember {
		t.Errorf("forward out of a chat one is not in: %v", err)
	}
	if _, err := svc.SendText(ctx, u[2], SendReq{Chat: own, CID: "01928c5e-7a3b-7c2d-8e9f-0a1b2c3d4e72", Text: "x", ReplyTo: id, ReplyChat: chatID}, nil); err != ErrNotMember {
		t.Errorf("reply across chats from outside: %v", err)
	}
	// Only the sender edits.
	if err := svc.Edit(ctx, u[1], chatID, id, "x", nil, nil); err != ErrForbidden {
		t.Errorf("the other side edited: %v", err)
	}
}

func TestEditDeleteReactPinInAPrivateChat(t *testing.T) {
	s, users := testStore(t)
	ctx := context.Background()
	u := users(2)
	svc := New(s, fakeUsers(u), nil)
	chatID, _ := s.OpenPrivate(ctx, u[0], u[1])
	msgs, err := svc.SendText(ctx, u[0], SendReq{Chat: chatID, CID: "01928c5e-7a3b-7c2d-8e9f-0a1b2c3d4e80", Text: "**نه** متن https://x.ir"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	id := msgs[0].ID
	if err := svc.Edit(ctx, u[0], chatID, id, "متن تازه", []Entity{{Type: "bold", Offset: 0, Length: 3}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := svc.React(ctx, u[1], chatID, id, "👍", true, nil); err != nil {
		t.Fatal(err)
	}
	if err := svc.React(ctx, u[1], chatID, id, "🔥", true, nil); err != nil { // replaces the 👍
		t.Fatal(err)
	}
	if err := svc.React(ctx, u[1], chatID, id, "<script>", true, nil); err != ErrForbidden {
		t.Errorf("an unknown reaction was taken: %v", err)
	}
	if err := svc.Pin(ctx, u[1], chatID, id, true, nil); err != nil {
		t.Fatal(err)
	}
	page, err := s.History(ctx, chatID, u[0], 0, 50)
	if err != nil || len(page) != 1 {
		t.Fatalf("history: %v %d", err, len(page))
	}
	m := page[0]
	if m.Text != "متن تازه" || m.Edited == nil || !m.Pinned || len(m.Reactions) != 1 || m.Reactions[0].Emoji != "🔥" {
		t.Fatalf("after edit/react/pin: %+v", m)
	}
	// Delete for me hides it only for u[1].
	if err := svc.Delete(ctx, u[1], chatID, []int64{id}, false, nil); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.History(ctx, chatID, u[1], 0, 50); len(p) != 0 {
		t.Errorf("hidden message still shown to the person who hid it")
	}
	if p, _ := s.History(ctx, chatID, u[0], 0, 50); len(p) != 1 {
		t.Errorf("hidden message vanished for the other side")
	}
	// Either side deletes for both in a private chat, and the text goes.
	if err := svc.Delete(ctx, u[1], chatID, []int64{id}, true, nil); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.History(ctx, chatID, u[0], 0, 50); len(p) != 0 {
		t.Errorf("deleted for everyone but still shown")
	}
	var text string
	s.pool.QueryRow(ctx, `SELECT text FROM chat_messages WHERE id = $1`, id).Scan(&text)
	if text != "" {
		t.Errorf("a deleted message kept its text in the database")
	}
}

func TestQuoteMustBeInTheRepliedMessage(t *testing.T) {
	s, users := testStore(t)
	ctx := context.Background()
	u := users(2)
	svc := New(s, fakeUsers(u), nil)
	chatID, _ := s.OpenPrivate(ctx, u[0], u[1])
	msgs, _ := svc.SendText(ctx, u[0], SendReq{Chat: chatID, CID: "01928c5e-7a3b-7c2d-8e9f-0a1b2c3d4e90", Text: "قیمت نهایی ۱۲۰ یورو است"}, nil)
	id := msgs[0].ID
	if _, err := svc.SendText(ctx, u[1], SendReq{Chat: chatID, CID: "01928c5e-7a3b-7c2d-8e9f-0a1b2c3d4e91", Text: "باشه", ReplyTo: id, Quote: &QuoteReq{Text: "۱۲۰ یورو", Offset: UTF16Len("قیمت نهایی ")}}, nil); err != nil {
		t.Fatalf("a true quote was refused: %v", err)
	}
	if _, err := svc.SendText(ctx, u[1], SendReq{Chat: chatID, CID: "01928c5e-7a3b-7c2d-8e9f-0a1b2c3d4e92", Text: "باشه", ReplyTo: id, Quote: &QuoteReq{Text: "۹۰ یورو", Offset: 0}}, nil); err != ErrForbidden {
		t.Fatalf("a made-up quote was taken: %v", err)
	}
}
