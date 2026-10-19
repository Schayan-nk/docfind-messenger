package chat

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// The secret chat flow against a real PostgreSQL (skipped without DOCFIND_PG_*).

func TestSecretStoreFlow(t *testing.T) {
	s, users := testStore(t)
	ctx := context.Background()
	u := users(3)
	svc := New(s, fakeUsers(u), nil)
	var logs []string
	svc.Logf = func(f string, a ...any) { logs = append(logs, f) }

	ch, err := svc.SecretOpen(ctx, u[0], devA, u[1], []SecretKey{pubKey(t, "p256"), pubKey(t, "x25519")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.pool.Exec(context.Background(), `DELETE FROM chats WHERE id = $1`, ch.ID) })
	if ch.Type != "secret" || ch.Peer == nil || ch.Peer.ID != u[1] {
		t.Fatalf("the new chat: %+v", ch)
	}
	waiting, err := s.SecretPending(ctx, u[1], devB2, time.Now().Add(-time.Hour))
	if err != nil || len(waiting) != 1 || waiting[0].Kind != secretRequest || !strings.Contains(waiting[0].Body, `"keys"`) {
		t.Fatalf("the invitation for any of u1's devices: %v %+v", err, waiting)
	}

	// Plain text has no way in: not the ordinary send, not a draft.
	if _, err := svc.SendText(ctx, u[0], SendReq{Chat: ch.ID, CID: "01928c5e-7a3b-7c2d-8e9f-0a1b2c3d4f00", Text: "سلام"}, nil); !errors.Is(err, ErrForbidden) {
		t.Errorf("plain text into a secret chat: %v", err)
	}
	if typ, _, _ := s.ChatType(ctx, ch.ID, u[0]); typ != "secret" {
		t.Errorf("chat type %q", typ)
	}

	if err := svc.SecretAcceptReq(ctx, u[2], devX, ch.ID, pubKey(t, "p256")); !errors.Is(err, ErrNotMember) {
		t.Errorf("outsider accept: %v", err)
	}
	if err := svc.SecretAcceptReq(ctx, u[1], devB, ch.ID, pubKey(t, "x25519")); err != nil {
		t.Fatal(err)
	}
	if err := svc.SecretAcceptReq(ctx, u[1], devB2, ch.ID, pubKey(t, "x25519")); !errors.Is(err, ErrSecretState) {
		t.Errorf("second accept: %v", err)
	}
	var keys int
	s.pool.QueryRow(ctx, `SELECT count(*) FROM secret_chat_keys WHERE chat_id = $1 AND alg = 'x25519'`, ch.ID).Scan(&keys)
	var all int
	s.pool.QueryRow(ctx, `SELECT count(*) FROM secret_chat_keys WHERE chat_id = $1`, ch.ID).Scan(&all)
	if keys != 2 || all != 2 {
		t.Errorf("keys after accept: %d x25519 of %d", keys, all)
	}

	body := sealedBody(1, 48)
	it, err := svc.SecretSend(ctx, u[0], devA, ch.ID, "01928c5e-7a3b-7c2d-8e9f-0a1b2c3d4f01", body)
	if err != nil || it.ID == 0 {
		t.Fatalf("send: %v", err)
	}
	again, err := svc.SecretSend(ctx, u[0], devA, ch.ID, "01928c5e-7a3b-7c2d-8e9f-0a1b2c3d4f01", body)
	if err != nil || again.ID != it.ID {
		t.Errorf("a resend made a second frame: %v %d %d", err, it.ID, again.ID)
	}
	if _, err := svc.SecretSend(ctx, u[2], devX, ch.ID, "01928c5e-7a3b-7c2d-8e9f-0a1b2c3d4f02", body); !errors.Is(err, ErrNotMember) {
		t.Errorf("outsider send: %v", err)
	}
	if got, _ := s.SecretPending(ctx, u[2], devB, time.Now().Add(-time.Hour)); len(got) != 0 {
		t.Errorf("an outsider collected %d frames", len(got))
	}
	if got, _ := s.SecretPending(ctx, u[1], devB2, time.Now().Add(-time.Hour)); len(got) != 0 {
		t.Errorf("u1's other device collected %d frames", len(got))
	}
	got, _ := s.SecretPending(ctx, u[1], devB, time.Now().Add(-time.Hour))
	if len(got) != 1 || got[0].Body != body || got[0].Kind != secretMsg {
		t.Fatalf("the chat's device: %+v", got)
	}
	if err := svc.secretAck(ctx, u[2], devB, []int64{got[0].ID}); err != nil {
		t.Fatal(err)
	}
	if left, _ := s.SecretPending(ctx, u[1], devB, time.Now().Add(-time.Hour)); len(left) != 1 {
		t.Error("an outsider's ack removed the frame")
	}
	if err := svc.secretAck(ctx, u[1], devB, []int64{got[0].ID}); err != nil {
		t.Fatal(err)
	}
	if left, _ := s.SecretPending(ctx, u[1], devB, time.Now().Add(-time.Hour)); len(left) != 0 {
		t.Error("the server kept a confirmed body")
	}
	if live, _ := s.SecretLive(ctx, u[0], devA); len(live) != 1 || live[0] != ch.ID {
		t.Errorf("live for the inviting device: %v", live)
	}
	if live, _ := s.SecretLive(ctx, u[1], devB2); len(live) != 0 {
		t.Errorf("live for a device without the chat: %v", live)
	}

	if err := svc.SecretClose(ctx, u[0], devA, ch.ID); err != nil {
		t.Fatal(err)
	}
	notes, _ := s.SecretPending(ctx, u[1], devB, time.Now().Add(-time.Hour))
	if len(notes) != 1 || notes[0].Kind != secretClose {
		t.Fatalf("close note: %+v", notes)
	}
	if err := svc.secretAck(ctx, u[1], devB, []int64{notes[0].ID}); err != nil {
		t.Fatal(err)
	}
	var n int
	s.pool.QueryRow(ctx, `SELECT count(*) FROM chats WHERE id = $1`, ch.ID).Scan(&n)
	if n != 0 {
		t.Error("a closed secret chat both sides left is still stored")
	}
	if len(logs) != 0 {
		t.Errorf("storage failures were logged: %v", logs)
	}
}
