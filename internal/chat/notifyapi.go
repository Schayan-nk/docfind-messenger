package chat

import (
	"net/http"
	"strings"
)

// The REST side of phase 6: one's notification switches, and the two things
// a notification's buttons do without opening the app (SPEC 6.10): mark the
// chat read, reply.

// NotifyGet is GET /api/chat/notify-settings; NotifySet is POST with the same shape.
func (a *API) NotifyGet(w http.ResponseWriter, r *http.Request) {
	n, err := a.Svc.Store.NotifySettings(r.Context(), a.User(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": n})
}

func (a *API) NotifySet(w http.ResponseWriter, r *http.Request) {
	n := defaultNotify()
	if !decode(w, r, &n) {
		return
	}
	me := a.User(r)
	if err := a.Svc.Store.SetNotifySettings(r.Context(), me, n); err != nil {
		writeErr(w, err)
		return
	}
	a.Svc.deliver([]string{me}, "notify_settings", n, nil)
	writeJSON(w, http.StatusOK, map[string]any{"settings": n})
}

// ChatRead is POST /api/chat/chats/{id}/read: everything in the chat read
// (a notification's «خوانده شد»).
func (a *API) ChatRead(w http.ResponseWriter, r *http.Request) {
	if err := a.Svc.MarkRead(r.Context(), a.User(r), pathID(r, "id"), 1<<62, nil); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ChatReply is POST /api/chat/chats/{id}/reply {"text"}: a reply typed into a
// notification. The same rules as any message; the chat is read up to it.
func (a *API) ChatReply(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
	}
	if !decode(w, r, &body) {
		return
	}
	ctx, me, id := r.Context(), a.User(r), pathID(r, "id")
	if strings.TrimSpace(body.Text) == "" {
		writeErr(w, ErrEmpty)
		return
	}
	msgs, err := a.Svc.SendText(ctx, me, SendReq{Chat: id, CID: uuidV7(), Text: body.Text}, nil)
	if err != nil {
		writeErr(w, err)
		return
	}
	_ = a.Svc.MarkRead(ctx, me, id, 1<<62, nil)
	writeJSON(w, http.StatusOK, map[string]any{"messages": msgs})
}
