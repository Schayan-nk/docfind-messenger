package chat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

const (
	pingEvery    = 25 * time.Second
	deadAfter    = 60 * time.Second
	authWithin   = 10 * time.Second
	maxFrame     = maxGroupSignal + 4<<10 // the largest frame: a big group call's answer
	outQueue     = 256
	maxDiffBatch = 500
)

// conn is one open socket: one device of one person.
type conn struct {
	svc    *Service
	ws     *websocket.Conn
	user   string
	ver    int
	sid    string
	host   string // the Host the page reached docfind by (calls: where TURN is)
	out    chan []byte
	ctx    context.Context
	cancel context.CancelFunc
	seen   atomic.Int64 // unix nanos of the last frame received
	watch  atomic.Int64 // the chat this device shows, page visible (no push for it)
	once   sync.Once
}

// push queues a frame; a device that cannot keep up is disconnected rather
// than allowed to hold the sender back. It reconnects and catches up by pts.
func (c *conn) push(b []byte) {
	select {
	case c.out <- b:
	default:
		c.kill(websocket.StatusPolicyViolation, "too slow")
	}
}

func (c *conn) send(t, id string, d any) {
	b, err := json.Marshal(struct {
		T  string `json:"t"`
		ID string `json:"id,omitempty"`
		D  any    `json:"d,omitempty"`
	}{t, id, d})
	if err == nil {
		c.push(b)
	}
}

func (c *conn) fail(id string, err error) {
	code, text := errorText(err)
	c.send("err", id, map[string]string{"code": code, "text": text})
}

func (c *conn) kill(code websocket.StatusCode, reason string) {
	c.once.Do(func() {
		c.cancel()
		c.ws.Close(code, reason)
	})
}

// ServeWS is GET /api/chat/ws. The socket authenticates with its first frame,
// {"t":"auth","d":{"ticket":"..."}}, not with anything in the URL.
func (s *Service) ServeWS(w http.ResponseWriter, r *http.Request) {
	// Accept refuses an Origin that does not match the Host (security check 14).
	// No permessage-deflate: IIS in front of docfind passes the extension
	// header through but drops the connection at the first compressed frame,
	// so a browser (which always offers it) never got past "connecting" (6.0.1).
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	ws.SetReadLimit(maxFrame)
	actx, acancel := context.WithTimeout(r.Context(), authWithin)
	_, data, err := ws.Read(actx)
	acancel()
	if err != nil {
		ws.Close(4000, "no auth")
		return
	}
	var env Envelope
	var hello struct {
		Ticket string `json:"ticket"`
	}
	if json.Unmarshal(data, &env) != nil || env.T != "auth" || json.Unmarshal(env.D, &hello) != nil {
		ws.Close(4000, "no auth")
		return
	}
	claims, err := s.tickets.VerifyChatTicket(hello.Ticket)
	if err != nil {
		ws.Close(4001, "bad ticket")
		return
	}
	u, err := s.users.Get(claims.Subject)
	if err != nil || u.Disabled || u.TokenVersion != claims.Version {
		ws.Close(4001, "session ended")
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	c := &conn{svc: s, ws: ws, user: u.Username, ver: claims.Version, sid: claims.SID, host: r.Host, out: make(chan []byte, outQueue), ctx: ctx, cancel: cancel}
	c.seen.Store(time.Now().UnixNano())
	_ = s.Store.EnsureUser(ctx, c.user)
	first := s.add(c)
	defer func() {
		s.callsOnDisconnect(c)
		s.gcallsOnDisconnect(c)
		if s.remove(c) {
			_ = s.Store.TouchLastSeen(context.Background(), c.user, s.now())
			go s.announce(c.user)
		}
		c.kill(websocket.StatusNormalClosure, "")
	}()
	if first {
		go s.announce(c.user)
	}
	go c.writeLoop()
	name := u.DisplayName
	if name == "" {
		name = u.Username
	}
	c.send("ready", "", map[string]any{"user": u.Username, "name": name, "server_time": s.now().UTC().Format(time.RFC3339Nano), "reactions": DefaultReactions, "calls": true})
	s.callsOnConnect(c)
	go s.gcallsOnConnect(c)
	c.readLoop()
}

func (c *conn) writeLoop() {
	ping := time.NewTicker(pingEvery)
	defer ping.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case b := <-c.out:
			wctx, cancel := context.WithTimeout(c.ctx, 15*time.Second)
			err := c.ws.Write(wctx, websocket.MessageText, b)
			cancel()
			if err != nil {
				c.kill(websocket.StatusGoingAway, "write failed")
				return
			}
		case <-ping.C:
			if time.Since(time.Unix(0, c.seen.Load())) > deadAfter {
				c.kill(websocket.StatusGoingAway, "no answer")
				return
			}
			go func() {
				pctx, cancel := context.WithTimeout(c.ctx, 20*time.Second)
				defer cancel()
				if c.ws.Ping(pctx) == nil {
					c.seen.Store(time.Now().UnixNano())
				}
			}()
		}
	}
}

func (c *conn) readLoop() {
	for {
		_, data, err := c.ws.Read(c.ctx)
		if err != nil {
			return
		}
		c.seen.Store(time.Now().UnixNano())
		var env Envelope
		if json.Unmarshal(data, &env) != nil {
			continue
		}
		c.handle(env)
	}
}

func (c *conn) handle(env Envelope) {
	ctx, cancel := context.WithTimeout(c.ctx, 15*time.Second)
	defer cancel()
	s := c.svc
	switch env.T {
	case "ping":
		c.send("pong", env.ID, nil)
	case "sync":
		var d struct {
			State map[string]int64 `json:"state"` // chat id (as a string, JSON keys) -> last pts seen
		}
		if json.Unmarshal(env.D, &d) != nil {
			return
		}
		c.sync(ctx, env.ID, d.State)
	case "send":
		var d SendReq
		if json.Unmarshal(env.D, &d) != nil {
			c.fail(env.ID, ErrEntities)
			return
		}
		msgs, err := s.SendText(ctx, c.user, d, c)
		if err != nil {
			c.fail(env.ID, err)
			return
		}
		c.send("ack", env.ID, map[string]any{"messages": msgs})
	case "read":
		var d struct {
			Chat  int64  `json:"chat"`
			PTS   int64  `json:"pts"`
			Topic *int64 `json:"topic"`
		}
		if json.Unmarshal(env.D, &d) != nil {
			return
		}
		if d.Topic != nil {
			if _, err := s.Store.Role(ctx, d.Chat, c.user); err == nil && s.Store.MarkTopicRead(ctx, d.Chat, *d.Topic, c.user, d.PTS) == nil {
				s.deliver([]string{c.user}, "topic_read", map[string]any{"chat": d.Chat, "topic": *d.Topic, "pts": d.PTS}, c)
			}
			return
		}
		if err := s.MarkRead(ctx, c.user, d.Chat, d.PTS, c); err != nil {
			c.fail(env.ID, err)
		}
	case "focus": // {"chat": id}, or 0 when the page is hidden or no chat is open
		var d struct {
			Chat int64 `json:"chat"`
		}
		if json.Unmarshal(env.D, &d) == nil && d.Chat >= 0 {
			c.watch.Store(d.Chat)
		}
	case "typing":
		var d struct {
			Chat   int64  `json:"chat"`
			Action string `json:"action"`
		}
		if json.Unmarshal(env.D, &d) != nil || !validAction(d.Action) || !s.typingLimit.allow(c.user) {
			return
		}
		if _, err := s.Store.Role(ctx, d.Chat, c.user); err != nil {
			return
		}
		members, _ := s.Store.Members(ctx, d.Chat)
		s.deliver(without(members, c.user), "typing", map[string]any{"chat": d.Chat, "user": c.user, "action": d.Action}, nil)
	case "draft":
		var d struct {
			Chat int64  `json:"chat"`
			Text string `json:"text"`
		}
		if json.Unmarshal(env.D, &d) != nil || UTF16Len(d.Text) > 4*MaxText {
			return
		}
		if _, err := s.Store.Role(ctx, d.Chat, c.user); err != nil {
			return
		}
		if typ, _, _ := s.Store.ChatType(ctx, d.Chat, c.user); typ == "secret" {
			return // a draft would be plain text on the server
		}
		if s.Store.SaveDraft(ctx, c.user, d.Chat, d.Text) == nil {
			s.deliver([]string{c.user}, "draft", map[string]any{"chat": d.Chat, "text": d.Text}, c)
		}
	default:
		c.handleAction(ctx, env)
	}
}

func validAction(a string) bool {
	switch a {
	case "typing", "voice", "video", "photo", "file", "sticker", "cancel":
		return true
	}
	return false
}

func without(list []string, drop string) []string {
	out := list[:0:0]
	for _, x := range list {
		if x != drop {
			out = append(out, x)
		}
	}
	return out
}

// sync sends each chat's missed updates (Telegram's getDifference). A chat
// that fell too far behind gets "reset" instead: the page reloads its history.
func (c *conn) sync(ctx context.Context, id string, state map[string]int64) {
	for key, after := range state {
		var chatID int64
		if _, err := fmtSscan(key, &chatID); err != nil {
			continue
		}
		if _, err := c.svc.Store.Role(ctx, chatID, c.user); err != nil {
			continue // membership is checked for every chat named, never assumed
		}
		if from := c.svc.visibleFrom(ctx, chatID, c.user); after < from {
			after = from
		}
		ups, more, err := c.svc.Store.Difference(ctx, chatID, after, maxDiffBatch)
		if err != nil {
			continue
		}
		if more {
			c.send("reset", id, map[string]any{"chat": chatID})
			continue
		}
		if len(ups) > 0 {
			c.send("diff", id, map[string]any{"chat": chatID, "updates": ups})
		}
	}
	c.send("synced", id, nil)
}

// MarkRead moves the reader's position and tells the chat.
func (s *Service) MarkRead(ctx context.Context, user string, chatID, pts int64, except *conn) error {
	if _, err := s.Store.Role(ctx, chatID, user); err != nil {
		return err
	}
	up, err := s.Store.MarkRead(ctx, chatID, user, pts)
	if err != nil || up == nil {
		return err
	}
	members, err := s.Store.Members(ctx, chatID)
	if err != nil {
		return err
	}
	s.deliver(members, "update", up, except)
	return nil
}

var errNotNumber = errors.New("not a number")
