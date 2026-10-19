package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
)

// handleSecret answers the secret_* socket frames (secret.go has the rules).
// Every frame is decoded strictly: a field the frame does not define, such as
// "text" in a secret_send, refuses the whole frame. Frames that are not
// secret_* are ignored here.
func (c *conn) handleSecret(ctx context.Context, env Envelope) {
	s := c.svc
	dev := s.secret.device(c)
	var ack any
	var err error
	switch env.T {
	case "secret_hello":
		var d struct {
			Device string `json:"device"`
		}
		if err = strictJSON(env.D, &d); err == nil {
			ack, err = s.secretHello(ctx, c, d.Device)
		}
	case "secret_open":
		var d struct {
			Peer string      `json:"peer"`
			Keys []SecretKey `json:"keys"`
		}
		if err = strictJSON(env.D, &d); err == nil {
			var ch *Chat
			ch, err = s.SecretOpen(ctx, c.user, dev, d.Peer, d.Keys)
			ack = map[string]any{"chat": ch}
		}
	case "secret_accept":
		var d struct {
			Chat int64     `json:"chat"`
			Key  SecretKey `json:"key"`
		}
		if err = strictJSON(env.D, &d); err == nil {
			err = s.SecretAcceptReq(ctx, c.user, dev, d.Chat, d.Key)
		}
	case "secret_send":
		var d struct {
			Chat int64  `json:"chat"`
			CID  string `json:"cid"`
			Body string `json:"body"`
		}
		if err = strictJSON(env.D, &d); err != nil {
			err = ErrSecretBody // plain text, entities or anything else besides a sealed body
		} else {
			var it *SecretItem
			if it, err = s.SecretSend(ctx, c.user, dev, d.Chat, d.CID, d.Body); err == nil {
				ack = map[string]any{"id": it.ID, "at": it.At}
			}
		}
	case "secret_ack":
		var d struct {
			IDs []int64 `json:"ids"`
		}
		if err = strictJSON(env.D, &d); err == nil {
			err = s.secretAck(ctx, c.user, dev, d.IDs)
		}
	case "secret_close":
		var d struct {
			Chat int64 `json:"chat"`
		}
		if err = strictJSON(env.D, &d); err == nil {
			err = s.SecretClose(ctx, c.user, dev, d.Chat)
		}
	default:
		c.handleExtras(ctx, env) // phase 11 frames (extras.go)
		return
	}
	if err != nil {
		c.secretFail(env.ID, err)
		return
	}
	c.send("ack", env.ID, ack)
}

var errStrict = errors.New("chat: frame does not match")

// strictJSON decodes exactly one object with only the fields v defines.
func strictJSON(raw json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return errStrict
	}
	if dec.More() {
		return errStrict
	}
	return nil
}

// secretFail answers a refused frame in Persian, with no internals.
func (c *conn) secretFail(id string, err error) {
	var code, text string
	switch {
	case errors.Is(err, ErrSecretState):
		code, text = "secret_state", "این گفت‌وگوی محرمانه دیگر فعال نیست."
	case errors.Is(err, ErrSecretDevice):
		code, text = "secret_device", "این گفت‌وگوی محرمانه روی دستگاه دیگری است."
	case errors.Is(err, ErrSecretBody):
		code, text = "secret_body", "در گفت‌وگوی محرمانه فقط پیام رمزشده پذیرفته می‌شود."
	case errors.Is(err, ErrSecretKey):
		code, text = "secret_key", "کلید رمزنگاری این دستگاه پذیرفته نشد."
	case errors.Is(err, errStrict):
		code, text = "forbidden", "درخواست نامعتبر است."
	default:
		c.fail(id, err)
		return
	}
	c.send("err", id, map[string]string{"code": code, "text": text})
}

// errClass names an error without its text: a PostgreSQL code, or the Go type.
func errClass(err error) string {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return "sqlstate " + pe.Code
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "timeout"
	}
	return fmt.Sprintf("%T", err)
}
