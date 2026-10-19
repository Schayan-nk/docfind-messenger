package auth

import (
	"errors"
	"slices"
	"strings"
	"time"
)

// The notification topics a person has switched off.
//
// They live in Prefs so they travel with the account -- PostgreSQL's
// user_prefs row and the emergency snapshot -- with no schema change. The
// store knows nothing of what a topic means; it only keeps the list tidy.
// Which names are real is the sender's business, and the sender checks.

// maxMuted is more topics than exist; it bounds what a hand-made request can
// make the store keep.
const maxMuted = 32

// MutedTopic reports whether p has switched topic off.
func (p Prefs) MutedTopic(topic string) bool {
	return slices.Contains(p.Muted, topic)
}

// SetMuted replaces the topics username has switched off.
// SMSTopic reports whether a topic also arrives by SMS.
func (p Prefs) SMSTopic(topic string) bool {
	return slices.Contains(p.SMS, topic)
}

func (s *Store) SetMuted(username string, topics []string) (Prefs, error) {
	clean := cleanTopics(topics)
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[normalizeName(username)]
	if !ok {
		return Prefs{}, ErrNoSuchUser
	}
	before := u.Prefs.Muted
	u.Prefs.Muted = clean
	if err := s.save(); err != nil {
		u.Prefs.Muted = before
		return Prefs{}, err
	}
	return u.Prefs, nil
}

// ErrQuietHours is a quiet-hours setting outside 0 to 23.
var ErrQuietHours = errors.New("auth: quiet hours must be whole hours from 0 to 23")

func quietHour(h int) bool { return h >= 0 && h <= 23 }

// Quiet reports whether t falls inside p's quiet hours. From 22 to 7 wraps
// midnight; from 13 to 14 is one lunch hour; equal hours are no quiet hours.
func (p Prefs) Quiet(t time.Time) bool {
	from, to := p.QuietFrom, p.QuietTo
	if from == to {
		return false
	}
	h := t.Hour()
	if from < to {
		return h >= from && h < to
	}
	return h >= from || h < to
}

// SetQuiet replaces username's quiet hours. from == to switches them off.
func (s *Store) SetQuiet(username string, from, to int) (Prefs, error) {
	if !quietHour(from) || !quietHour(to) {
		return Prefs{}, ErrQuietHours
	}
	if from == to {
		from, to = 0, 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[normalizeName(username)]
	if !ok {
		return Prefs{}, ErrNoSuchUser
	}
	bf, bt := u.Prefs.QuietFrom, u.Prefs.QuietTo
	u.Prefs.QuietFrom, u.Prefs.QuietTo = from, to
	if err := s.save(); err != nil {
		u.Prefs.QuietFrom, u.Prefs.QuietTo = bf, bt
		return Prefs{}, err
	}
	return u.Prefs, nil
}

// cleanTopics lower-cases, de-duplicates and sorts, and drops anything that
// is not a plain topic name. Empty comes back nil, so an account with nothing
// switched off stores no "muted" key at all.
func cleanTopics(in []string) []string {
	var out []string
	for _, t := range in {
		t = strings.ToLower(strings.TrimSpace(t))
		if !topicName(t) || slices.Contains(out, t) {
			continue
		}
		out = append(out, t)
		if len(out) == maxMuted {
			break
		}
	}
	slices.Sort(out)
	return out
}

// topicName is a-z, 0-9 and hyphens, 1 to 32 of them.
func topicName(t string) bool {
	if t == "" || len(t) > 32 {
		return false
	}
	for _, r := range t {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}
