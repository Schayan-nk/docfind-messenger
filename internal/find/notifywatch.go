package find

import (
	"sync"
	"time"

	"github.com/Schayan-nk/docfind-messenger/internal/auth"
)

// The watchers: the server doing what the home page used to do in the browser.
//
// The page polled its own cartable and calendar and announced what was new.
// That only worked while the page was open. These run in the server, for
// every person who has registered a device, on a timer -- so a case that lands
// at 11pm reaches the phone at 11pm, with nothing of docfind open anywhere.
//
// WHAT IS NEW is decided the way the page decided it, and now in one place: an
// event's identity (case number and step, appointment id) recorded as seen per
// person. The FIRST pass for a person records everything already waiting and
// announces none of it -- the page's rule too, for the same reason: switching
// notifications on should not be greeted by a burst of alerts for a week of
// work the person has been looking at.
//
// PERMISSIONS ARE CHECKED AT ANNOUNCE TIME, per person. A notification is a
// copy of the data it describes, arriving on a lock screen; nobody is sent a
// case summary for a panel their role cannot open.

// pushWatchState is what the watchers last did, for the admin status.
type pushWatchState struct {
	mu       sync.Mutex
	lastRun  time.Time
	lastErr  string
	users    int
	queued   int
	bySource map[string]string // source -> last error, "" when fine
}

func (p *pushWatchState) snapshot() map[string]any {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	src := map[string]string{}
	for k, v := range p.bySource {
		src[k] = v
	}
	return map[string]any{"last_run": p.lastRun, "last_error": p.lastErr,
		"users": p.users, "queued_last_run": p.queued, "sources": src}
}

// accountFor is the live account, re-read on every pass so a permission
// removed a minute ago is already in force.
func (s *Server) accountFor(username string) *auth.User {
	if s.guard == nil || s.guard.Users == nil {
		return nil
	}
	u, err := s.guard.Users.Get(username)
	if err != nil {
		return nil
	}
	return u
}
