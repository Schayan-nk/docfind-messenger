package chat

import (
	"net/http"
	"strconv"
	"time"
)

// The REST side of phase 5: a person's settings for a chat, clearing a chat,
// folders and search. Everything here is the caller's own; a chat id is
// checked against their memberships before anything is read or changed.

// PrefsSet is POST /api/chat/chats/{id}/prefs: pin, archive, mute, notify
// only for mentions, mark unread.
func (a *API) PrefsSet(w http.ResponseWriter, r *http.Request) {
	var req PrefsReq
	if !decode(w, r, &req) {
		return
	}
	p, err := a.Svc.SetPrefs(r.Context(), a.User(r), pathID(r, "id"), req)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"prefs": p})
}

// PinsOrder is POST /api/chat/pins/order {"chats":[...]}.
func (a *API) PinsOrder(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Chats []int64 `json:"chats"`
	}
	if !decode(w, r, &body) {
		return
	}
	me := a.User(r)
	if err := a.Svc.Store.OrderPins(r.Context(), me, body.Chats); err != nil {
		writeErr(w, err)
		return
	}
	a.Svc.deliver([]string{me}, "pins_order", map[string]any{"chats": body.Chats}, nil)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ChatClear is POST /api/chat/chats/{id}/clear {"for_all": bool}.
func (a *API) ChatClear(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ForAll bool `json:"for_all"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := a.Svc.ClearChat(r.Context(), a.User(r), pathID(r, "id"), body.ForAll); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// Folders is GET (list) and POST (make or change one) /api/chat/folders.
func (a *API) Folders(w http.ResponseWriter, r *http.Request) {
	ctx, me := r.Context(), a.User(r)
	if r.Method == http.MethodGet {
		list, err := a.Svc.Store.Folders(ctx, me)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"folders": list})
		return
	}
	var f Folder
	if !decode(w, r, &f) {
		return
	}
	out, err := a.Svc.Store.SaveFolder(ctx, me, &f)
	if err != nil {
		writeErr(w, err)
		return
	}
	a.Svc.deliver([]string{me}, "folders", map[string]any{}, nil)
	writeJSON(w, http.StatusOK, map[string]any{"folder": out})
}

// FolderDelete is POST /api/chat/folders/{fid}/delete.
func (a *API) FolderDelete(w http.ResponseWriter, r *http.Request) {
	me := a.User(r)
	if err := a.Svc.Store.DeleteFolder(r.Context(), me, pathID(r, "fid")); err != nil {
		writeErr(w, err)
		return
	}
	a.Svc.deliver([]string{me}, "folders", map[string]any{}, nil)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// FoldersOrder is POST /api/chat/folders/order {"ids":[...]}.
func (a *API) FoldersOrder(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs []int64 `json:"ids"`
	}
	if !decode(w, r, &body) {
		return
	}
	me := a.User(r)
	if err := a.Svc.Store.OrderFolders(r.Context(), me, body.IDs); err != nil {
		writeErr(w, err)
		return
	}
	a.Svc.deliver([]string{me}, "folders", map[string]any{}, nil)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// FolderShare is POST /api/chat/folders/{fid}/share: its link token.
func (a *API) FolderShare(w http.ResponseWriter, r *http.Request) {
	tok, err := a.Svc.Store.ShareFolder(r.Context(), a.User(r), pathID(r, "fid"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": tok})
}

// FolderLink is GET /api/chat/folder-link/{token}: what a shared folder offers.
func (a *API) FolderLink(w http.ResponseWriter, r *http.Request) {
	p, err := a.Svc.FolderPreview(r.Context(), r.PathValue("token"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// FolderJoin is POST /api/chat/folder-link/{token}/join.
func (a *API) FolderJoin(w http.ResponseWriter, r *http.Request) {
	f, joined, err := a.Svc.JoinFolder(r.Context(), a.User(r), r.PathValue("token"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"folder": f, "joined": joined})
}

// searchReq reads a search from the query string.
func searchReq(r *http.Request) SearchReq {
	q := r.URL.Query()
	req := SearchReq{Q: q.Get("q"), From: q.Get("from"), Kind: q.Get("kind")}
	req.Before, _ = strconv.ParseInt(q.Get("before"), 10, 64)
	req.Limit, _ = strconv.Atoi(q.Get("limit"))
	for _, x := range []struct {
		key string
		to  **time.Time
	}{{"since", &req.Since}, {"until", &req.Until}} {
		if t, err := time.Parse(time.RFC3339, q.Get(x.key)); err == nil {
			*x.to = &t
		}
	}
	if len([]rune(req.Q)) > 256 {
		req.Q = string([]rune(req.Q)[:256])
	}
	return req
}

// SearchAll is GET /api/chat/search?q=&kind=&before=: messages, files and
// links across the caller's chats.
func (a *API) SearchAll(w http.ResponseWriter, r *http.Request) {
	msgs, err := a.Svc.Search(r.Context(), a.User(r), searchReq(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": nonNilMsgs(msgs)})
}

// SearchChat is GET /api/chat/chats/{id}/search?q=&from=&kind=&since=&until=&before=.
func (a *API) SearchChat(w http.ResponseWriter, r *http.Request) {
	req := searchReq(r)
	req.Chat = pathID(r, "id")
	if req.Chat <= 0 {
		writeErr(w, ErrNotMember)
		return
	}
	msgs, err := a.Svc.Search(r.Context(), a.User(r), req)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": nonNilMsgs(msgs)})
}

func nonNilMsgs(m []*Message) []*Message {
	if m == nil {
		return []*Message{}
	}
	return m
}
