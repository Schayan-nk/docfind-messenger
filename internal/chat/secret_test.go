package chat

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Secret chats without PostgreSQL: the rules of secret.go over fakeSecret.
// secretstore_test.go runs the same flow against a real database.

const (
	devA  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	devB  = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	devB2 = "b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2"
	devX  = "cccccccccccccccccccccccccccccccc"
)

func pubKey(t *testing.T, alg string) SecretKey {
	t.Helper()
	c := ecdh.P256()
	if alg == "x25519" {
		c = ecdh.X25519()
	}
	k, err := c.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return SecretKey{Alg: alg, Key: base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes())}
}

// sealedBody is shaped like a page's sealed message: counter, then bytes.
func sealedBody(ctr uint64, n int) string {
	raw := make([]byte, 8+n)
	binary.BigEndian.PutUint64(raw, ctr)
	rand.Read(raw[8:])
	return base64.RawURLEncoding.EncodeToString(raw)
}

type secretRig struct {
	t    *testing.T
	svc  *Service
	db   *fakeSecret
	mu   sync.Mutex
	logs []string
}

func newSecretRig(t *testing.T, users ...string) *secretRig {
	r := &secretRig{t: t, db: newFakeSecret()}
	r.svc = New(nil, fakeUsers(users), nil)
	r.svc.secret.db = r.db
	r.svc.Logf = func(f string, a ...any) { r.mu.Lock(); r.logs = append(r.logs, fmt.Sprintf(f, a...)); r.mu.Unlock() }
	return r
}

// socket opens a stand-in socket for user; dev "" leaves it unannounced.
func (r *secretRig) socket(user, dev string) *conn {
	ctx, cancel := context.WithCancel(context.Background())
	r.t.Cleanup(cancel)
	c := &conn{svc: r.svc, user: user, out: make(chan []byte, 256), ctx: ctx, cancel: cancel}
	r.svc.add(c)
	if dev != "" {
		if f := r.frame(c, "secret_hello", map[string]any{"device": dev}); f.T != "ack" {
			r.t.Fatalf("hello: %s", f.D)
		}
		r.drain(c)
	}
	return c
}

// frame sends one frame and returns its answer (ack or err).
func (r *secretRig) frame(c *conn, t string, d any) Envelope {
	r.t.Helper()
	b, _ := json.Marshal(d)
	c.handleSecret(context.Background(), Envelope{T: t, ID: "q1", D: b})
	for _, f := range r.drain(c) {
		if f.ID == "q1" && (f.T == "ack" || f.T == "err") {
			return f
		}
	}
	r.t.Fatalf("%s: no answer", t)
	return Envelope{}
}

func (r *secretRig) drain(c *conn) []Envelope {
	var out []Envelope
	for {
		select {
		case b := <-c.out:
			var e Envelope
			json.Unmarshal(b, &e)
			out = append(out, e)
		default:
			return out
		}
	}
}

func items(fs []Envelope) []SecretItem {
	var out []SecretItem
	for _, f := range fs {
		if f.T == "secret" {
			var it SecretItem
			json.Unmarshal(f.D, &it)
			out = append(out, it)
		}
	}
	return out
}

func errCode(f Envelope) string {
	var d struct{ Code string }
	json.Unmarshal(f.D, &d)
	return d.Code
}

// active makes a's device A invite b and b's device B accept; it returns the chat.
func (r *secretRig) active(a, b string) int64 {
	ctx := context.Background()
	ch, err := r.svc.SecretOpen(ctx, a, devA, b, []SecretKey{pubKey(r.t, "p256"), pubKey(r.t, "x25519")})
	if err != nil {
		r.t.Fatal(err)
	}
	if err := r.svc.SecretAcceptReq(ctx, b, devB, ch.ID, pubKey(r.t, "x25519")); err != nil {
		r.t.Fatal(err)
	}
	return ch.ID
}

func TestSecretOutsiderCannotReadOrSend(t *testing.T) {
	r := newSecretRig(t, "a", "b", "x")
	ctx := context.Background()
	id := r.active("a", "b")
	if _, err := r.svc.SecretSend(ctx, "a", devA, id, "01928c5e-7a3b-7c2d-8e9f-0a1b2c3d4e80", sealedBody(1, 40)); err != nil {
		t.Fatal(err)
	}
	// An outsider can neither send, accept, close nor collect anything.
	if _, err := r.svc.SecretSend(ctx, "x", devX, id, "01928c5e-7a3b-7c2d-8e9f-0a1b2c3d4e81", sealedBody(1, 40)); !errors.Is(err, ErrNotMember) {
		t.Errorf("outsider send: %v", err)
	}
	if err := r.svc.SecretAcceptReq(ctx, "x", devX, id, pubKey(t, "p256")); !errors.Is(err, ErrNotMember) {
		t.Errorf("outsider accept: %v", err)
	}
	if err := r.svc.SecretClose(ctx, "x", devX, id); !errors.Is(err, ErrNotMember) {
		t.Errorf("outsider close: %v", err)
	}
	x := r.socket("x", "")
	f := r.frame(x, "secret_hello", map[string]any{"device": devB}) // even claiming b's device id
	if got := items(r.drain(x)); len(got) != 0 {
		t.Errorf("an outsider collected %d frames", len(got))
	}
	var live struct{ Live []int64 }
	json.Unmarshal(f.D, &live)
	if len(live.Live) != 0 {
		t.Errorf("an outsider was told of chats %v", live.Live)
	}
	before := r.db.waiting()
	if f := r.frame(x, "secret_ack", map[string]any{"ids": []int64{1, 2, 3, 4, 5, 6}}); f.T != "ack" {
		t.Fatal(f)
	}
	if r.db.waiting() != before {
		t.Error("an outsider's ack removed someone else's frames")
	}
	if f := r.frame(x, "secret_send", map[string]any{"chat": id, "cid": "01928c5e-7a3b-7c2d-8e9f-0a1b2c3d4e82", "body": sealedBody(2, 40)}); errCode(f) != "not_member" {
		t.Errorf("outsider socket send: %s", f.D)
	}
	// The member's other device is not the chat's device.
	if _, err := r.svc.SecretSend(ctx, "b", devB2, id, "01928c5e-7a3b-7c2d-8e9f-0a1b2c3d4e83", sealedBody(1, 40)); !errors.Is(err, ErrSecretDevice) {
		t.Errorf("send from another device of a member: %v", err)
	}
	// A secret frame naming a chat that is not secret is refused too.
	if _, err := r.svc.SecretSend(ctx, "a", devA, 999, "01928c5e-7a3b-7c2d-8e9f-0a1b2c3d4e84", sealedBody(1, 40)); !errors.Is(err, ErrNotMember) {
		t.Errorf("send to a chat one is not in: %v", err)
	}
}

func TestSecretRejectsPlaintext(t *testing.T) {
	r := newSecretRig(t, "a", "b")
	id := r.active("a", "b")
	a := r.socket("a", devA)
	before := r.db.waiting() // the accept a's device has not confirmed yet
	cases := map[string]map[string]any{
		"text beside the body": {"chat": id, "cid": "01928c5e-7a3b-7c2d-8e9f-0a1b2c3d4e90", "body": sealedBody(1, 40), "text": "سلام"},
		"text instead":         {"chat": id, "cid": "01928c5e-7a3b-7c2d-8e9f-0a1b2c3d4e91", "text": "سلام"},
		"entities":             {"chat": id, "cid": "01928c5e-7a3b-7c2d-8e9f-0a1b2c3d4e92", "body": sealedBody(1, 40), "entities": []any{}},
		"words as the body":    {"chat": id, "cid": "01928c5e-7a3b-7c2d-8e9f-0a1b2c3d4e93", "body": "سلام، این متن ساده است"},
		"standard base64":      {"chat": id, "cid": "01928c5e-7a3b-7c2d-8e9f-0a1b2c3d4e94", "body": base64.StdEncoding.EncodeToString([]byte("hello hello hello hello hello hello"))},
		"too short":            {"chat": id, "cid": "01928c5e-7a3b-7c2d-8e9f-0a1b2c3d4e95", "body": sealedBody(1, 8)},
		"counter zero":         {"chat": id, "cid": "01928c5e-7a3b-7c2d-8e9f-0a1b2c3d4e96", "body": sealedBody(0, 40)},
		"too long":             {"chat": id, "cid": "01928c5e-7a3b-7c2d-8e9f-0a1b2c3d4e97", "body": sealedBody(1, maxSecretBody)},
	}
	for name, d := range cases {
		if f := r.frame(a, "secret_send", d); f.T != "err" || errCode(f) != "secret_body" {
			t.Errorf("%s: %s %s", name, f.T, f.D)
		}
	}
	if r.db.waiting() != before {
		t.Fatalf("%d refused frames were queued", r.db.waiting()-before)
	}
	if f := r.frame(a, "secret_send", map[string]any{"chat": id, "cid": "01928c5e-7a3b-7c2d-8e9f-0a1b2c3d4e98", "body": sealedBody(1, 40)}); f.T != "ack" {
		t.Fatalf("a sealed body was refused: %s", f.D)
	}
}

func TestSecretNeverLogsCiphertext(t *testing.T) {
	r := newSecretRig(t, "a", "b")
	id := r.active("a", "b")
	a := r.socket("a", devA)
	body := sealedBody(7, 64)
	// A storage error that quotes the row (as a constraint error can) must not
	// carry the body into the log.
	r.db.failPut = fmt.Errorf("failing row contains (%s)", body)
	if f := r.frame(a, "secret_send", map[string]any{"chat": id, "cid": "01928c5e-7a3b-7c2d-8e9f-0a1b2c3d4ea0", "body": body}); f.T != "err" {
		t.Fatalf("a failed store was acknowledged: %s", f.D)
	}
	r.db.failPut = nil
	r.frame(a, "secret_send", map[string]any{"chat": id, "cid": "01928c5e-7a3b-7c2d-8e9f-0a1b2c3d4ea1", "body": body})
	r.frame(a, "secret_send", map[string]any{"chat": id, "cid": "01928c5e-7a3b-7c2d-8e9f-0a1b2c3d4ea2", "body": body, "text": "x"})
	if len(r.logs) == 0 {
		t.Fatal("the storage failure was not logged at all")
	}
	for _, l := range r.logs {
		if strings.Contains(l, body) || strings.Contains(l, body[:16]) || strings.Contains(l, "failing row") {
			t.Errorf("a log line carries the body: %q", l)
		}
	}
	// And by construction: every Logf call in the secret files passes only a
	// constant format, the operation name and errClass(err).
	files, _ := filepath.Glob("secret*.go")
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (sel.Sel.Name != "Logf" && sel.Sel.Name != "Printf" && sel.Sel.Name != "Println") {
				return true
			}
			for _, arg := range call.Args {
				switch v := arg.(type) {
				case *ast.BasicLit:
				case *ast.Ident:
					if v.Name != "op" {
						t.Errorf("%s: %s logs %s", fset.Position(call.Pos()), sel.Sel.Name, v.Name)
					}
				case *ast.CallExpr:
					if id, ok := v.Fun.(*ast.Ident); !ok || id.Name != "errClass" {
						t.Errorf("%s: %s logs a computed value", fset.Position(call.Pos()), sel.Sel.Name)
					}
				default:
					t.Errorf("%s: %s logs an expression", fset.Position(call.Pos()), sel.Sel.Name)
				}
			}
			return true
		})
	}
}

func TestSecretGoesToOneDeviceOnly(t *testing.T) {
	r := newSecretRig(t, "a", "b")
	ctx := context.Background()
	b1, b2, bPlain := r.socket("b", devB), r.socket("b", devB2), r.socket("b", "")
	ch, err := r.svc.SecretOpen(ctx, "a", devA, "b", []SecretKey{pubKey(t, "p256")})
	if err != nil {
		t.Fatal(err)
	}
	// The invitation reaches every announced device of b, nothing else.
	if n1, n2, n3 := len(items(r.drain(b1))), len(items(r.drain(b2))), len(items(r.drain(bPlain))); n1 != 1 || n2 != 1 || n3 != 0 {
		t.Fatalf("invitation reached %d, %d, %d sockets", n1, n2, n3)
	}
	if err := r.svc.SecretAcceptReq(ctx, "b", devB, ch.ID, pubKey(t, "x25519")); !errors.Is(err, ErrSecretKey) {
		t.Errorf("an algorithm that was not offered: %v", err)
	}
	if err := r.svc.SecretAcceptReq(ctx, "b", devB, ch.ID, pubKey(t, "p256")); err != nil {
		t.Fatal(err)
	}
	if got := items(r.drain(b2)); len(got) != 1 || got[0].Kind != secretGone {
		t.Errorf("b's other device was not told the chat is taken: %+v", got)
	}
	if err := r.svc.SecretAcceptReq(ctx, "b", devB2, ch.ID, pubKey(t, "p256")); !errors.Is(err, ErrSecretState) {
		t.Errorf("a second accept: %v", err)
	}
	if err := r.svc.SecretAcceptReq(ctx, "a", devA, ch.ID, pubKey(t, "p256")); !errors.Is(err, ErrForbidden) {
		t.Errorf("the inviter accepted: %v", err)
	}
	a := r.socket("a", devA) // hello collects the accept that waited
	r.drain(b1)
	if f := r.frame(a, "secret_send", map[string]any{"chat": ch.ID, "cid": "01928c5e-7a3b-7c2d-8e9f-0a1b2c3d4eb0", "body": sealedBody(1, 40)}); f.T != "ack" {
		t.Fatal(string(f.D))
	}
	got1, got2, got3 := items(r.drain(b1)), items(r.drain(b2)), items(r.drain(bPlain))
	if len(got1) != 1 || got1[0].Kind != secretMsg || len(got2) != 0 || len(got3) != 0 {
		t.Fatalf("a message reached %d, %d, %d sockets", len(got1), len(got2), len(got3))
	}
	// A resend with the same client id is the same frame.
	r.frame(a, "secret_send", map[string]any{"chat": ch.ID, "cid": "01928c5e-7a3b-7c2d-8e9f-0a1b2c3d4eb0", "body": sealedBody(1, 40)})
	if len(items(r.drain(b1))) != 0 {
		t.Error("a resend was delivered twice")
	}
	// Once confirmed, the server keeps nothing.
	r.frame(b1, "secret_ack", map[string]any{"ids": []int64{got1[0].ID}})
	if left, _ := r.db.SecretPending(ctx, "b", devB, time.Time{}); len(left) != 0 {
		t.Errorf("%d frames kept after the device confirmed", len(left))
	}
}

func TestSecretClose(t *testing.T) {
	r := newSecretRig(t, "a", "b")
	ctx := context.Background()
	id := r.active("a", "b")
	b := r.socket("b", devB)
	if err := r.svc.SecretClose(ctx, "a", devB, id); !errors.Is(err, ErrSecretDevice) {
		t.Errorf("closed from a device that is not the chat's: %v", err)
	}
	if err := r.svc.SecretClose(ctx, "a", devA, id); err != nil {
		t.Fatal(err)
	}
	got := items(r.drain(b))
	if len(got) != 1 || got[0].Kind != secretClose {
		t.Fatalf("the other side was not told: %+v", got)
	}
	if _, err := r.svc.SecretSend(ctx, "a", devA, id, "01928c5e-7a3b-7c2d-8e9f-0a1b2c3d4ec0", sealedBody(1, 40)); !errors.Is(err, ErrNotMember) {
		t.Errorf("send after leaving: %v", err)
	}
	if _, err := r.svc.SecretSend(ctx, "b", devB, id, "01928c5e-7a3b-7c2d-8e9f-0a1b2c3d4ec1", sealedBody(1, 40)); !errors.Is(err, ErrSecretState) {
		t.Errorf("send into a closed chat: %v", err)
	}
	r.frame(b, "secret_ack", map[string]any{"ids": []int64{got[0].ID}})
	if _, err := r.db.Role(ctx, id, "b"); !errors.Is(err, ErrNotMember) {
		t.Error("confirming the close did not take the chat off b's list")
	}
}

func TestSecretKeysAndOffers(t *testing.T) {
	good := []SecretKey{pubKey(t, "p256"), pubKey(t, "x25519")}
	if _, err := checkOffer(good); err != nil {
		t.Fatal(err)
	}
	bad := map[string][]SecretKey{
		"none":          nil,
		"no P-256":      {pubKey(t, "x25519")},
		"twice":         {pubKey(t, "p256"), pubKey(t, "p256")},
		"unknown":       {pubKey(t, "p256"), {Alg: "rsa", Key: "AAAA"}},
		"short x25519":  {pubKey(t, "p256"), {Alg: "x25519", Key: base64.RawURLEncoding.EncodeToString(make([]byte, 31))}},
		"zero x25519":   {pubKey(t, "p256"), {Alg: "x25519", Key: base64.RawURLEncoding.EncodeToString(make([]byte, 32))}},
		"off the curve": {{Alg: "p256", Key: base64.RawURLEncoding.EncodeToString(append([]byte{4}, make([]byte, 64)...))}},
		"not base64url": {{Alg: "p256", Key: "a+b/c="}},
	}
	for name, keys := range bad {
		if _, err := checkOffer(keys); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	r := newSecretRig(t, "a", "b")
	ctx := context.Background()
	if _, err := r.svc.SecretOpen(ctx, "a", devA, "a", good); !errors.Is(err, ErrForbidden) {
		t.Errorf("a secret chat with oneself: %v", err)
	}
	if _, err := r.svc.SecretOpen(ctx, "a", "", "b", good); !errors.Is(err, ErrSecretDevice) {
		t.Errorf("an invitation from a socket with no device: %v", err)
	}
	if _, err := r.svc.SecretOpen(ctx, "a", devA, "nobody", good); !errors.Is(err, ErrNoUser) {
		t.Errorf("an invitation to nobody: %v", err)
	}
	r.db.blocked[[2]string{"b", "a"}] = true
	if _, err := r.svc.SecretOpen(ctx, "a", devA, "b", good); !errors.Is(err, ErrBlocked) {
		t.Errorf("an invitation to someone who blocked me: %v", err)
	}
	a := r.socket("a", "")
	if f := r.frame(a, "secret_hello", map[string]any{"device": "../../etc"}); errCode(f) != "secret_device" {
		t.Errorf("a malformed device id: %s", f.D)
	}
}
