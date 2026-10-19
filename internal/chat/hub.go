package chat

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/Schayan-nk/docfind-messenger/internal/auth"
)

// Users is what the chat module needs from DocFind's account store.
type Users interface {
	Get(username string) (*auth.User, error)
	List() []*auth.User
}

// Tickets verifies the one-minute tickets a page trades for a socket.
type Tickets interface {
	VerifyChatTicket(token string) (*auth.Claims, error)
}

// Service is the messenger: the store, the open sockets and the rules that sit
// between them.
type Service struct {
	Store   *Store
	users   Users
	tickets Tickets
	// Logf records operational events. It is never given message text: rule 15
	// of the security checks is that no log line carries what people wrote.
	Logf func(format string, args ...any)

	mu    sync.RWMutex
	conns map[string]map[*conn]struct{}

	secret secretHub // secret chats: which socket is which device (secret.go)

	sendLimit   *limiter
	typingLimit *limiter
	uploadLimit *limiter
	joinLimit   *limiter
	searchLimit *limiter
	now         func() time.Time

	// Phase 3. Media is the chat media folder (nil: no uploads). OCR reads the
	// text of a picture or a PDF for search; Docs is DocFind's document index
	// as one reader may see it. Both are lent by the find package.
	Media            *MediaFolder
	OCR              func(path, mime string) (string, error)
	Docs             DocResolver
	AllowExecutables bool // DOCFIND_CHAT_ALLOW_EXECUTABLES=1

	// Phase 6. Notify takes a message's notice to DocFind's push queue (the
	// find package lends it); nil sends none.
	Notify func(ctx context.Context, n Notice)

	// Phase 7. ICE is what a person's page (session: their TokenVersion),
	// reached at host, gives its
	// RTCPeerConnection (docfind's own TURN server; nil: none, so calls work
	// where the two browsers reach each other directly). NotifyCall takes a
	// ringing or missed call to the push queue.
	ICE        func(user string, session int, host string) []ICEServer
	NotifyCall func(ctx context.Context, n CallNotice)
	calls      callHub
	callLimit  *limiter

	// Group calls (6.9): the media server, lent by the wiring (nil: none).
	GroupMedia MediaServer
	gcalls     gcallHub
}

// DocResolver is DocFind's document index as one reader may see it.
type DocResolver interface {
	// DocCard is the card of document ref for user; ok is false when it does
	// not exist or user may not open it (the same rules as the search page).
	DocCard(user, ref string) (DocCard, bool)
}

// DocCard is a DocFind document shared in a chat.
type DocCard struct {
	Ref   string `json:"ref"`
	Title string `json:"title"`
	Type  string `json:"type,omitempty"`
	URL   string `json:"url,omitempty"`
}

// New makes the service. Run must be started for session revocation to apply.
func New(store *Store, users Users, tickets Tickets) *Service {
	return &Service{
		Store: store, users: users, tickets: tickets,
		Logf:        func(string, ...any) {},
		conns:       map[string]map[*conn]struct{}{},
		sendLimit:   newLimiter(20, 20),
		typingLimit: newLimiter(1, 3),
		uploadLimit: newLimiter(0.2, 30),
		joinLimit:   newLimiter(0.05, 10),
		searchLimit: newLimiter(2, 20),  // search-as-you-type: a few a second
		callLimit:   newLimiter(0.1, 5), // five calls at once, then one each 10 s
		now:         time.Now,
	}
}

// watchdogEvery is how often open sockets are checked against their account.
// The brief allows five seconds from a TokenVersion bump to a closed socket.
const watchdogEvery = 2 * time.Second

// Run closes sockets whose session was revoked (TokenVersion bumped, account
// disabled or deleted) until ctx ends.
func (s *Service) Run(ctx context.Context) {
	// Calls a stopped server left open: nobody is in them any more.
	if s.Store != nil {
		if n, err := s.Store.CloseOpenCalls(ctx, s.now()); err == nil && n > 0 {
			s.Logf("messenger: %d call(s) left open by the last run were closed", n)
		}
	}
	t := time.NewTicker(watchdogEvery)
	defer t.Stop()
	sweep := time.NewTicker(time.Hour)
	sched := time.NewTicker(scheduleEvery) // scheduled messages and poll closing (schedule.go)
	defer sched.Stop()
	defer sweep.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.revokeStale()
		case <-sched.C:
			s.runScheduled(ctx)
		case <-sweep.C:
			s.sweepUploads(ctx)
		}
	}
}

// sweepUploads clears uploads nobody finished within a day.
func (s *Service) sweepUploads(ctx context.Context) {
	if s.Media == nil || s.Store == nil {
		return
	}
	ids, err := s.Store.ExpiredUploads(ctx)
	if err != nil {
		return
	}
	for _, id := range ids {
		_ = os.Remove(s.Media.partFor(id))
	}
}

func (s *Service) revokeStale() {
	s.mu.RLock()
	var stale []*conn
	for user, set := range s.conns {
		u, err := s.users.Get(user)
		for c := range set {
			if err != nil || u.Disabled || u.TokenVersion != c.ver {
				stale = append(stale, c)
			}
		}
	}
	s.mu.RUnlock()
	for _, c := range stale {
		c.kill(4001, "session ended")
	}
}

func (s *Service) add(c *conn) (first bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	set := s.conns[c.user]
	if set == nil {
		set = map[*conn]struct{}{}
		s.conns[c.user] = set
	}
	first = len(set) == 0
	set[c] = struct{}{}
	return first
}

func (s *Service) remove(c *conn) (last bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	set := s.conns[c.user]
	if _, ok := set[c]; !ok {
		return false
	}
	delete(set, c)
	if len(set) == 0 {
		delete(s.conns, c.user)
		return true
	}
	return false
}

// Online reports whether user has an open socket.
func (s *Service) Online(user string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.conns[user]) > 0
}

// ConnCount is the number of open sockets (for the health check).
func (s *Service) ConnCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for _, set := range s.conns {
		n += len(set)
	}
	return n
}

// deliver sends one frame to every open socket of every listed user, except
// the one that caused it (which gets its own acknowledgement instead).
func (s *Service) deliver(users []string, t string, d any, except *conn) {
	b, err := json.Marshal(struct {
		T string `json:"t"`
		D any    `json:"d"`
	}{t, d})
	if err != nil {
		return
	}
	s.mu.RLock()
	var targets []*conn
	for _, u := range users {
		for c := range s.conns[u] {
			if c != except {
				targets = append(targets, c)
			}
		}
	}
	s.mu.RUnlock()
	for _, c := range targets {
		c.push(b)
	}
}

// person builds how target is shown to viewer, presence included.
func (s *Service) person(ctx context.Context, viewer, target string, profiles map[string]Profile) *Person {
	p := &Person{ID: target}
	if u, err := s.users.Get(target); err == nil {
		p.Name = u.DisplayName
		if p.Name == "" {
			p.Name = u.Username
		}
		p.Photo = u.Avatar != ""
	}
	tp := profiles[target]
	p.Handle = tp.Handle
	contact := false
	if tp.Privacy.LastSeen.Mode == "contacts" {
		contact, _ = s.Store.IsContact(ctx, target, viewer)
	}
	presenceFor(p, viewer, tp.Privacy.LastSeen, profiles[viewer].Privacy.LastSeen, contact, s.Online(target), tp.LastSeen, s.now())
	return p
}

// announce tells everyone who shares a chat with user that user came online
// or went away, each through the filter of user's privacy settings.
func (s *Service) announce(user string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	peers, err := s.Store.Peers(ctx, user)
	if err != nil || len(peers) == 0 {
		return
	}
	profiles, err := s.Store.Profiles(ctx, append(peers, user))
	if err != nil {
		return
	}
	for _, viewer := range peers {
		if !s.Online(viewer) {
			continue
		}
		s.deliver([]string{viewer}, "presence", s.person(ctx, viewer, user, profiles), nil)
	}
}
