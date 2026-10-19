package chat

import (
	"context"
	"encoding/json"
	"strings"
)

// handleAction is the socket side of the phase 2 actions. Each answers with
// "ack" (and the changed messages where there are any) or "err"; the change
// itself reaches every device, this one included, as an "update".
func (c *conn) handleAction(ctx context.Context, env Envelope) {
	s := c.svc
	var err error
	var ack any
	switch env.T {
	case "edit":
		var d struct {
			Chat     int64    `json:"chat"`
			ID       int64    `json:"id"`
			Text     string   `json:"text"`
			Entities []Entity `json:"entities"`
		}
		if err = json.Unmarshal(env.D, &d); err == nil {
			err = s.Edit(ctx, c.user, d.Chat, d.ID, d.Text, d.Entities, nil)
		}
	case "delete":
		var d struct {
			Chat   int64   `json:"chat"`
			IDs    []int64 `json:"ids"`
			ForAll bool    `json:"for_all"`
		}
		if err = json.Unmarshal(env.D, &d); err == nil {
			err = s.Delete(ctx, c.user, d.Chat, d.IDs, d.ForAll, c)
		}
	case "pin":
		var d struct {
			Chat int64 `json:"chat"`
			ID   int64 `json:"id"`
			On   bool  `json:"on"`
		}
		if err = json.Unmarshal(env.D, &d); err == nil {
			err = s.Pin(ctx, c.user, d.Chat, d.ID, d.On, nil)
		}
	case "react":
		var d struct {
			Chat  int64  `json:"chat"`
			ID    int64  `json:"id"`
			Emoji string `json:"emoji"`
			On    bool   `json:"on"`
		}
		if err = json.Unmarshal(env.D, &d); err == nil {
			err = s.React(ctx, c.user, d.Chat, d.ID, d.Emoji, d.On, nil)
		}
	case "preview":
		var d struct {
			Chat  int64 `json:"chat"`
			ID    int64 `json:"id"`
			On    bool  `json:"on"`
			Above bool  `json:"above"`
		}
		if err = json.Unmarshal(env.D, &d); err == nil {
			err = s.PreviewPlace(ctx, c.user, d.Chat, d.ID, d.On, d.Above)
		}
	case "forward":
		var d ForwardReq
		if err = json.Unmarshal(env.D, &d); err == nil {
			var msgs []*Message
			msgs, err = s.Forward(ctx, c.user, d, c)
			ack = map[string]any{"messages": msgs}
		}
	default:
		// call_* frames (callws.go), secret_* frames (secretws.go); anything
		// else is ignored, as a newer page talking to an older server expects.
		if strings.HasPrefix(env.T, "call_") {
			c.handleCall(ctx, env)
			return
		}
		if strings.HasPrefix(env.T, "gcall_") {
			c.handleGroupCall(ctx, env)
			return
		}
		c.handleSecret(ctx, env)
		return
	}
	if err != nil {
		if _, ok := err.(*json.UnmarshalTypeError); ok {
			err = ErrForbidden
		}
		c.fail(env.ID, err)
		return
	}
	c.send("ack", env.ID, ack)
}
