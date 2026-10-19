package chat

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Schayan-nk/docfind-messenger/internal/auth"
	"github.com/Schayan-nk/docfind-messenger/internal/pgstore"
)

// These run against a real PostgreSQL when DOCFIND_PG_* is set (the same
// switch as the pgstore tests) and skip otherwise. Each test makes its own
// users with random names and removes them at the end.

func testStore(t *testing.T) (*Store, func(n int) []string) {
	t.Helper()
	c := pgstore.FromEnv()
	if !c.Enabled() {
		t.Skip("no DOCFIND_PG_* configured; set them to run the chat database tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := pgstore.Open(ctx, c)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrating: %v", err)
	}
	t.Cleanup(db.Close)
	s := NewStore(db.Pool())
	var made []string
	t.Cleanup(func() {
		for _, u := range made {
			db.Pool().Exec(context.Background(), `DELETE FROM chats WHERE pair_key LIKE '%'||$1||'%'`, u)
			db.Pool().Exec(context.Background(), `DELETE FROM users WHERE username = $1`, u)
		}
	})
	return s, func(n int) []string {
		var out []string
		for i := 0; i < n; i++ {
			b := make([]byte, 5)
			rand.Read(b)
			u := "chat_t_" + hex.EncodeToString(b)
			if _, err := db.Pool().Exec(context.Background(), `INSERT INTO users (username, display_name, password_hash, role) VALUES ($1, $1, 'x', 'user')`, u); err != nil {
				t.Fatalf("making a test user: %v", err)
			}
			made = append(made, u)
			out = append(out, u)
		}
		return out
	}
}

func TestSendIsExactlyOnceAndNumbersEveryChange(t *testing.T) {
	s, users := testStore(t)
	ctx := context.Background()
	u := users(2)
	chatID, err := s.OpenPrivate(ctx, u[0], u[1])
	if err != nil {
		t.Fatal(err)
	}
	again, _ := s.OpenPrivate(ctx, u[1], u[0])
	if again != chatID {
		t.Fatalf("a pair got two private chats: %d and %d", chatID, again)
	}
	cid := "01928c5e-7a3b-7c2d-8e9f-0a1b2c3d4e5f"
	m1, up1, created1, err := s.Send(ctx, chatID, u[0], cid, Outgoing{Text: "سلام"})
	if err != nil || !created1 || up1 == nil {
		t.Fatalf("first send: %v created=%v", err, created1)
	}
	m2, _, created2, err := s.Send(ctx, chatID, u[0], cid, Outgoing{Text: "سلام"})
	if err != nil || created2 || m2.ID != m1.ID {
		t.Fatalf("a resend made a second message: %v %v %d/%d", err, created2, m1.ID, m2.ID)
	}
	if _, err := s.MarkRead(ctx, chatID, u[1], m1.PTS); err != nil {
		t.Fatal(err)
	}
	ups, more, err := s.Difference(ctx, chatID, 0, 100)
	if err != nil || more || len(ups) != 2 || ups[0].Kind != "message" || ups[1].Kind != "read" || ups[1].PTS != ups[0].PTS+1 {
		t.Fatalf("update log: %v more=%v %+v", err, more, ups)
	}
}

func TestNonMembersCannotReadOrWrite(t *testing.T) {
	s, users := testStore(t)
	ctx := context.Background()
	u := users(3)
	chatID, _ := s.OpenPrivate(ctx, u[0], u[1])
	if _, err := s.Role(ctx, chatID, u[2]); err != ErrNotMember {
		t.Fatalf("an outsider got role %v", err)
	}
	svc := New(s, fakeUsers(u), nil)
	if _, err := svc.SendText(ctx, u[2], SendReq{Chat: chatID, CID: "01928c5e-7a3b-7c2d-8e9f-0a1b2c3d4e60", Text: "سلام"}, nil); err != ErrNotMember {
		t.Fatalf("an outsider sent a message: %v", err)
	}
	if err := svc.MarkRead(ctx, u[2], chatID, 1, nil); err != ErrNotMember {
		t.Fatalf("an outsider moved a read position: %v", err)
	}
	// Blocking stops a private chat in both directions.
	if err := s.SetBlock(ctx, u[1], u[0], true); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SendText(ctx, u[0], SendReq{Chat: chatID, CID: "01928c5e-7a3b-7c2d-8e9f-0a1b2c3d4e61", Text: "سلام"}, nil); err != ErrBlocked {
		t.Fatalf("a blocked person could send: %v", err)
	}
}

// --- the socket, end to end (security checks 4, 14, 15) ---------------------

type fakeUsersT struct {
	mu sync.Mutex
	m  map[string]*auth.User
}

func fakeUsers(names []string) *fakeUsersT {
	f := &fakeUsersT{m: map[string]*auth.User{}}
	for _, n := range names {
		f.m[n] = &auth.User{Username: n, DisplayName: n}
	}
	return f
}
func (f *fakeUsersT) Get(n string) (*auth.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.m[n]
	if !ok {
		return nil, auth.ErrNoSuchUser
	}
	c := *u
	return &c, nil
}
func (f *fakeUsersT) List() []*auth.User { return nil }
func (f *fakeUsersT) bump(n string) {
	f.mu.Lock()
	f.m[n].TokenVersion++
	f.mu.Unlock()
}

func TestSocketClosesWhenTheSessionIsRevokedAndLogsNoText(t *testing.T) {
	s, users := testStore(t)
	u := users(2)
	signer := auth.NewSigner([]byte(strings.Repeat("k", 32)), time.Hour)
	fu := fakeUsers(u)
	svc := New(s, fu, signer)
	var logged strings.Builder
	var lmu sync.Mutex
	svc.Logf = func(f string, a ...any) { lmu.Lock(); logged.WriteString(f); lmu.Unlock() }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.Run(ctx)
	srv := httptest.NewServer(http.HandlerFunc(svc.ServeWS))
	defer srv.Close()
	chatID, _ := s.OpenPrivate(ctx, u[0], u[1])

	tok, _, _ := signer.SignChatTicket(u[0], 0, "sid")
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	send := func(v any) { b, _ := json.Marshal(v); c.Write(ctx, websocket.MessageText, b) }
	send(map[string]any{"t": "auth", "d": map[string]string{"ticket": tok}})
	read := func() map[string]any {
		rctx, rc := context.WithTimeout(ctx, 5*time.Second)
		defer rc()
		_, b, err := c.Read(rctx)
		if err != nil {
			return map[string]any{"err": err.Error()}
		}
		var m map[string]any
		json.Unmarshal(b, &m)
		return m
	}
	if f := read(); f["t"] != "ready" {
		t.Fatalf("no ready frame: %v", f)
	}
	secret := "متن محرمانه ۱۲۳"
	send(map[string]any{"t": "send", "id": "r1", "d": map[string]any{"chat": chatID, "cid": "01928c5e-7a3b-7c2d-8e9f-0a1b2c3d4e70", "text": secret}})
	if f := read(); f["t"] != "ack" {
		t.Fatalf("send not acknowledged: %v", f)
	}
	fu.bump(u[0]) // an admin signs this person out everywhere
	deadline := time.Now().Add(5 * time.Second)
	closed := false
	for time.Now().Before(deadline) {
		if f := read(); f["err"] != nil {
			closed = true
			break
		}
	}
	if !closed {
		t.Fatal("the socket stayed open after TokenVersion changed")
	}
	lmu.Lock()
	defer lmu.Unlock()
	if strings.Contains(logged.String(), secret) {
		t.Fatal("message text reached the log")
	}
}

func TestSocketRefusesAForeignOrigin(t *testing.T) {
	svc := New(nil, fakeUsers(nil), auth.NewSigner([]byte(strings.Repeat("k", 32)), time.Hour))
	srv := httptest.NewServer(http.HandlerFunc(svc.ServeWS))
	defer srv.Close()
	h := http.Header{}
	h.Set("Origin", "https://evil.example")
	_, resp, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(srv.URL, "http"), &websocket.DialOptions{HTTPHeader: h})
	if err == nil {
		t.Fatal("a cross-origin socket was accepted")
	}
	if resp != nil && resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status %d, want 403", resp.StatusCode)
	}
}

func TestSocketRefusesASessionTokenAsATicket(t *testing.T) {
	signer := auth.NewSigner([]byte(strings.Repeat("k", 32)), time.Hour)
	svc := New(nil, fakeUsers([]string{"alice"}), signer)
	srv := httptest.NewServer(http.HandlerFunc(svc.ServeWS))
	defer srv.Close()
	ctx := context.Background()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	session, _, _ := signer.Sign("alice", 0)
	b, _ := json.Marshal(map[string]any{"t": "auth", "d": map[string]string{"ticket": session}})
	c.Write(ctx, websocket.MessageText, b)
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, _, err := c.Read(rctx); websocket.CloseStatus(err) != 4001 {
		t.Fatalf("a session token opened the socket: %v", err)
	}
}
