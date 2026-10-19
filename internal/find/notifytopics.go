package find

import (
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/Schayan-nk/docfind-messenger/internal/auth"
	"github.com/Schayan-nk/docfind-messenger/internal/notify"
)

// What a person can choose to be told about.
//
// Everything is on until someone switches it off, so a new topic reaches
// everybody the day it ships and a person who never opens this card loses
// nothing. The choice is per person, not per device: a phone and a laptop
// that both buzz for a new case are one decision, made once.
//
// A switched-off topic is still WATCHED: the cartable's seen-list and the
// calendar snapshot move on as usual, so switching it back on later brings
// what happens from then on, not a backlog of everything missed.

type notifyTopic struct {
	ID, Label, Hint string
}

var notifyTopics = []notifyTopic{
	// The messenger. Its own settings (groups, channels, the preview, each
	// chat's mute) are in the messenger.
	{topicChat, "پیام‌رسان", "پیام تازه در گفت‌وگوها؛ جزئیات در تنظیمات اعلان خود پیام‌رسان."},
}

// quieten makes n arrive without sound when now is inside u's quiet hours.
// It is still sent and still listed: quiet hours stop a phone buzzing at
// night, they do not hide what happened.
func quieten(u *auth.User, n notify.Notification, now time.Time) notify.Notification {
	if u.Prefs.Quiet(now) {
		n.Quiet = true
	}
	return n
}

// topicReaches reports whether anything under topic can ever be sent to u:
// a person a topic cannot reach gets nothing from it, whatever they choose.
func (s *Server) topicReaches(u *auth.User, id string) bool {
	if reach, ok := s.newTopicReaches(u, id); ok {
		return reach
	}
	// The standalone messenger has no reminders, tasks, cases, mail or
	// calendar: nothing else can reach anybody.
	return false
}

// handlePushTopics lists the topics and whether each is on for the caller.
func (s *Server) handlePushTopics(w http.ResponseWriter, r *http.Request) {
	if s.pushOff(w) {
		return
	}
	s.writeTopics(w, auth.UserFrom(r), auth.UserFrom(r).Prefs)
}

// handleSetPushTopics replaces the caller's switched-off topics. The body
// names every topic that is off, {"off": [...]}; a name that is not a topic
// is refused rather than stored, so a typo cannot silently mute nothing.
func (s *Server) handleSetPushTopics(w http.ResponseWriter, r *http.Request) {
	if s.pushOff(w) {
		return
	}
	var req struct {
		Off []string `json:"off"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, `{"error":"درخواست نامعتبر است."}`)
		return
	}
	for _, id := range req.Off {
		if !slices.ContainsFunc(notifyTopics, func(t notifyTopic) bool { return t.ID == id }) {
			writeJSONError(w, http.StatusBadRequest, `{"error":"موضوع اعلان ناشناخته است."}`)
			return
		}
	}
	u := auth.UserFrom(r)
	prefs, err := s.guard.Users.SetMuted(u.Username, req.Off)
	if err != nil {
		writeUserError(w, err)
		return
	}
	s.writeTopics(w, u, prefs)
}

func (s *Server) writeTopics(w http.ResponseWriter, u *auth.User, p auth.Prefs) {
	type item struct {
		ID        string `json:"id"`
		Label     string `json:"label"`
		Hint      string `json:"hint"`
		On        bool   `json:"on"`
		Available bool   `json:"available"`
		SMS       bool   `json:"sms"`
	}
	out := make([]item, 0, len(notifyTopics))
	for _, t := range notifyTopics {
		out = append(out, item{t.ID, t.Label, t.Hint, !p.MutedTopic(t.ID), s.topicReaches(u, t.ID), p.SMSTopic(t.ID)})
	}
	phone := ""
	if cur, err := s.guard.Users.Get(u.Username); err == nil {
		phone = cur.Phone
	}
	writeJSON(w, map[string]any{"topics": out, "quiet": map[string]int{"from": p.QuietFrom, "to": p.QuietTo},
		// Whether a topic can be asked for by SMS at all: a panel, and a phone.
		"sms_available": s.smsConfig().Enabled() && phone != "", "phone": maskPhone(phone)})
}

// handleSetPushQuiet sets the caller's quiet hours: {"from": 22, "to": 7}.
// Equal hours switch them off.
func (s *Server) handleSetPushQuiet(w http.ResponseWriter, r *http.Request) {
	if s.pushOff(w) {
		return
	}
	var req struct {
		From *int `json:"from"`
		To   *int `json:"to"`
	}
	if err := readJSON(r, &req); err != nil || req.From == nil || req.To == nil {
		writeJSONError(w, http.StatusBadRequest, `{"error":"درخواست نامعتبر است."}`)
		return
	}
	u := auth.UserFrom(r)
	prefs, err := s.guard.Users.SetQuiet(u.Username, *req.From, *req.To)
	if errors.Is(err, auth.ErrQuietHours) {
		writeJSONError(w, http.StatusBadRequest, `{"error":"ساعت آرام باید عددی از ۰ تا ۲۳ باشد."}`)
		return
	}
	if err != nil {
		writeUserError(w, err)
		return
	}
	s.writeTopics(w, u, prefs)
}
