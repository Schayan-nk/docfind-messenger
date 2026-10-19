package chat

import (
	"net/http"
	"strconv"
)

// Phase 2 reads: pins, unread mentions, who reacted. Each checks membership
// first and answers an outsider exactly as it answers a chat that does not
// exist (404), so ids cannot be probed.

// memberChat reads {id} from the path and checks that the caller is in it.
func (a *API) memberChat(w http.ResponseWriter, r *http.Request) (int64, string, bool) {
	me := a.User(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, ErrNotMember)
		return 0, "", false
	}
	if _, err := a.Svc.Store.Role(r.Context(), id, me); err != nil {
		writeErr(w, ErrNotMember)
		return 0, "", false
	}
	return id, me, true
}

// Pins is GET /api/chat/chats/{id}/pins: the pinned messages, newest pin first.
func (a *API) Pins(w http.ResponseWriter, r *http.Request) {
	id, me, ok := a.memberChat(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	ids, err := a.Svc.Store.Pins(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	msgs, err := a.Svc.Store.ByIDs(ctx, id, me, ids)
	if err != nil {
		writeErr(w, err)
		return
	}
	a.Svc.dressDocs(me, msgs)
	order := map[int64]int{}
	for i, x := range ids {
		order[x] = i
	}
	out := make([]*Message, len(msgs))
	copy(out, msgs)
	for i := 1; i < len(out); i++ { // newest pin first, the order Pins gave
		for j := i; j > 0 && order[out[j].ID] < order[out[j-1].ID]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": out})
}

// Mentions is GET /api/chat/chats/{id}/mentions: the pts of unread messages
// that mention the caller, oldest first.
func (a *API) Mentions(w http.ResponseWriter, r *http.Request) {
	id, me, ok := a.memberChat(w, r)
	if !ok {
		return
	}
	pts, err := a.Svc.Store.UnreadMentions(r.Context(), id, me)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pts": pts})
}

// Reactors is GET /api/chat/chats/{id}/messages/{msg}/reactions: who reacted
// with what, with display names.
func (a *API) Reactors(w http.ResponseWriter, r *http.Request) {
	id, _, ok := a.memberChat(w, r)
	if !ok {
		return
	}
	msg, err := strconv.ParseInt(r.PathValue("msg"), 10, 64)
	if err != nil {
		writeErr(w, ErrNotMember)
		return
	}
	ctx := r.Context()
	if c, _, _, err := a.Svc.Store.msgOwner(ctx, msg); err != nil || c != id {
		writeErr(w, ErrNotMember)
		return
	}
	by, err := a.Svc.Store.Reactors(ctx, msg)
	if err != nil {
		writeErr(w, err)
		return
	}
	type who struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	out := map[string][]who{}
	for emoji, users := range by {
		for _, u := range users {
			name := u
			if x, err := a.Svc.users.Get(u); err == nil && x.DisplayName != "" {
				name = x.DisplayName
			}
			out[emoji] = append(out[emoji], who{u, name})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"reactions": out})
}
