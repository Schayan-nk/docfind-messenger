package find

import (
	"net/http"

	"github.com/Schayan-nk/docfind-messenger/internal/chat"
)

// routeChatLists adds the phase 5 routes: a person's settings for each chat,
// clearing a chat, folders (and their share links) and search. All of it is
// the caller's own; the chat package checks every chat id against their
// memberships.
func (s *Server) routeChatLists(mux *http.ServeMux) {
	api := func(f func(*chat.API, http.ResponseWriter, *http.Request)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) { f(s.chatAPI, w, r) }
	}
	read := func(pattern string, f func(*chat.API, http.ResponseWriter, *http.Request)) {
		mux.HandleFunc("GET "+pattern, s.chatAuth(api(f)))
	}
	write := func(pattern string, f func(*chat.API, http.ResponseWriter, *http.Request)) {
		mux.HandleFunc("POST "+pattern, s.mutating(s.chatAuth(api(f))))
	}
	write("/api/chat/chats/{id}/prefs", (*chat.API).PrefsSet)
	write("/api/chat/chats/{id}/clear", (*chat.API).ChatClear)
	write("/api/chat/pins/order", (*chat.API).PinsOrder)
	read("/api/chat/folders", (*chat.API).Folders)
	write("/api/chat/folders", (*chat.API).Folders)
	write("/api/chat/folders/order", (*chat.API).FoldersOrder)
	write("/api/chat/folders/{fid}/delete", (*chat.API).FolderDelete)
	write("/api/chat/folders/{fid}/share", (*chat.API).FolderShare)
	read("/api/chat/folder-link/{token}", (*chat.API).FolderLink)
	write("/api/chat/folder-link/{token}/join", (*chat.API).FolderJoin)
	read("/api/chat/search", (*chat.API).SearchAll)
	read("/api/chat/chats/{id}/search", (*chat.API).SearchChat)
	// phase 6: notifications
	read("/api/chat/notify-settings", (*chat.API).NotifyGet)
	write("/api/chat/notify-settings", (*chat.API).NotifySet)
	write("/api/chat/chats/{id}/read", (*chat.API).ChatRead)
	write("/api/chat/chats/{id}/reply", (*chat.API).ChatReply)
}
