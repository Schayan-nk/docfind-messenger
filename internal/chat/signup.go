package chat

import (
	"context"
	"encoding/json"
)

// Joined tells inviter that newcomer has just signed up with one of their
// invite codes.
//
// It is a service line in the private chat between the two -- so a welcome is
// one tap away -- and a notification on the inviter's devices. A service line
// is otherwise silent (notifyMessage skips it), which is why the notification
// is sent here, on its own.
//
// name is the newcomer's display name, handed over by whoever created the
// account: the directory this service reads catches up a moment later, and
// asked at once it answered with the bare username («reza» for «رضا کریمی»).
func (s *Service) Joined(ctx context.Context, inviter, newcomer, name string) error {
	if inviter == "" || newcomer == "" || inviter == newcomer {
		return nil
	}
	chatID, err := s.Store.OpenPrivate(ctx, inviter, newcomer)
	if err != nil {
		return err
	}
	if name == "" {
		name = s.displayName(newcomer)
	}
	payload, _ := json.Marshal(map[string]any{"action": "signup", "users": []string{newcomer}, "names": []string{name}})
	members, _ := s.Store.Members(ctx, chatID)
	m, up, created, err := s.Store.Send(ctx, chatID, newcomer, uuidV7(),
		Outgoing{Kind: "service", Service: payload, Silent: true, FromName: name})
	if err != nil {
		return err
	}
	if created {
		s.deliver(members, "update", up, nil)
	}
	if s.Notify != nil && m != nil {
		s.Notify(ctx, Notice{
			Chat: chatID, ChatType: "private", Title: name,
			Text:  "با دعوت‌نامهٔ شما عضو پیام‌رسان شد",
			MsgID: m.ID,
			To:    []NoticeTo{{User: inviter, Preview: true}},
		})
	}
	return nil
}
