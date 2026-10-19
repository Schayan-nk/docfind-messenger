package find

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Schayan-nk/docfind-messenger/internal/chat"
	"github.com/Schayan-nk/docfind-messenger/internal/notify"
)

// The messenger's notifications (docs/chat/SPEC.md 6.10) through DocFind's
// push queue: the chat package decides who should hear about a message (its
// own rules: muted, mentions only, the person's switches, not someone reading
// the chat); this end applies DocFind's (a disabled account, the «پیام‌رسان»
// topic switched off, quiet hours) and queues one notification per person.
// Never an SMS: a chat message is not worth a text.

const topicChat = "chat"

// chatNotice queues the notifications of one message.
func (s *Server) chatNotice(ctx context.Context, n chat.Notice) {
	if !s.NotificationsEnabled() {
		return
	}
	now := time.Now()
	queued := 0
	for _, to := range n.To {
		u := s.accountFor(to.User)
		if u == nil || u.Disabled || u.Prefs.MutedTopic(topicChat) {
			continue
		}
		body := n.Text
		if !to.Preview {
			body = "پیام تازه"
		}
		urgency := "normal"
		if to.Mention || n.ChatType == "private" {
			urgency = "high"
		}
		nt := quieten(u, notify.Notification{
			Username:  u.Username,
			Kind:      topicChat,
			DedupeKey: fmt.Sprintf("chat:%d:%d", n.Chat, n.MsgID),
			Title:     clipRunes(strings.TrimSpace(n.Title), 80),
			Body:      body,
			URL:       fmt.Sprintf("/messages#c%d", n.Chat),
			Tag:       fmt.Sprintf("chat-%d", n.Chat), // one notification per chat on a device
			Urgency:   urgency,
			Quiet:     n.Silent,
			ExpiresAt: now.Add(24 * time.Hour),
		}, now)
		_, created, devices, err := s.push.store.Enqueue(context.WithoutCancel(ctx), nt)
		if err != nil {
			logReportError("notification enqueue chat", err)
			continue
		}
		if created {
			queued += devices
		}
	}
	if queued > 0 {
		s.push.dispatcher.Wake()
	}
}

// chatCallNotice queues a call's notification for the person called (SPEC
// 6.8): ringing now (high urgency, gone after the 45 s a call rings, so a
// phone never rings late), or missed. Both carry the tag call-<id>, so the
// missed call replaces the ringing one on the device.
func (s *Server) chatCallNotice(ctx context.Context, n chat.CallNotice) {
	if !s.NotificationsEnabled() {
		return
	}
	u := s.accountFor(n.To)
	if u == nil || u.Disabled || u.Prefs.MutedTopic(topicChat) {
		return
	}
	now := time.Now()
	kind := "تماس صوتی"
	if n.Video {
		kind = "تماس تصویری"
	}
	nt := notify.Notification{
		Username:  u.Username,
		Kind:      topicChat,
		DedupeKey: fmt.Sprintf("call:%d:ring", n.Call),
		Title:     clipRunes(strings.TrimSpace(n.From), 80),
		Body:      kind + "…",
		URL:       fmt.Sprintf("/messages#call%d", n.Call),
		Tag:       fmt.Sprintf("call-%d", n.Call),
		Urgency:   "high",
		ExpiresAt: now.Add(45 * time.Second),
	}
	if n.Missed {
		nt.DedupeKey = fmt.Sprintf("call:%d:missed", n.Call)
		nt.Body = kind + " از دست رفته"
		nt.URL = fmt.Sprintf("/messages#c%d", n.Chat)
		nt.Urgency = "normal"
		nt.ExpiresAt = now.Add(24 * time.Hour)
	}
	nt = quieten(u, nt, now)
	_, created, devices, err := s.push.store.Enqueue(context.WithoutCancel(ctx), nt)
	if err != nil {
		logReportError("notification enqueue call", err)
		return
	}
	if created && devices > 0 {
		s.push.dispatcher.Wake()
	}
}
