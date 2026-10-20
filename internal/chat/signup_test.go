package chat

import (
	"context"
	"encoding/json"
	"testing"
)

// A sign-up with somebody's invite (against a real database; skipped without
// DOCFIND_PG_*): a service line in the private chat between the two, sent as
// the newcomer, and a notification for the invite's maker alone.
func TestJoinedTellsTheInviter(t *testing.T) {
	s, users := testStore(t)
	u := users(2)
	inviter, newcomer := u[0], u[1]
	svc := New(s, pwUsers{fakeUsers(u), "درست"}, nil)
	ch := notices(svc)
	ctx := context.Background()

	if err := svc.Joined(ctx, inviter, newcomer, "تازه‌وارد آزمایشی"); err != nil {
		t.Fatal(err)
	}
	chatID, err := s.OpenPrivate(ctx, inviter, newcomer) // the same chat, found again
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.History(ctx, chatID, inviter, 0, 10)
	if err != nil || len(page) != 1 {
		t.Fatalf("the inviter's chat: %v, %d messages, want 1", err, len(page))
	}
	m := page[0]
	var p struct {
		Action string   `json:"action"`
		Users  []string `json:"users"`
		Names  []string `json:"names"`
	}
	_ = json.Unmarshal(m.Service, &p)
	if m.Kind != "service" || p.Action != "signup" || len(p.Users) != 1 || p.Users[0] != newcomer || m.Sender != newcomer {
		t.Fatalf("service line = kind %q, action %q, users %v, from %q", m.Kind, p.Action, p.Users, m.Sender)
	}
	// The line carries the name the account was made with, not the bare
	// username the directory answered with a moment after sign-up. (A private
	// chat's history keeps no sender name; the page reads it from the line.)
	if len(p.Names) != 1 || p.Names[0] != "تازه‌وارد آزمایشی" {
		t.Fatalf("the line names %v, want the name the account was made with", p.Names)
	}

	n := next(t, ch)
	if n == nil {
		t.Fatal("the inviter was not notified")
	}
	if got := reached(n); len(got) != 1 || !got[inviter].Preview {
		t.Fatalf("notified %v, want only the inviter", got)
	}
	if n.Chat != chatID || n.Text == "" || n.Title == "" {
		t.Fatalf("notice = %+v", n)
	}

	// Nobody to tell: no chat with oneself, no inviter, no error.
	if err := svc.Joined(ctx, newcomer, newcomer, ""); err != nil {
		t.Fatal(err)
	}
	if err := svc.Joined(ctx, "", newcomer, ""); err != nil {
		t.Fatal(err)
	}
	if n := next(t, ch); n != nil {
		t.Fatalf("a notification with nobody to tell: %+v", n)
	}
}
