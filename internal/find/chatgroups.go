package find

import (
	"net/http"

	"github.com/Schayan-nk/docfind-messenger/internal/chat"
)

// routeChatGroups adds the phase 4 routes: groups, channels, their members,
// invite links, join requests, topics, the admin log and statistics. What a
// person may do inside a chat is decided by chat.can on every call; here every
// route only needs a signed-in person, and every write the same-site check.
func (s *Server) routeChatGroups(mux *http.ServeMux) {
	api := func(f func(*chat.API, http.ResponseWriter, *http.Request)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) { f(s.chatAPI, w, r) }
	}
	read := func(pattern string, f func(*chat.API, http.ResponseWriter, *http.Request)) {
		mux.HandleFunc("GET "+pattern, s.chatAuth(api(f)))
	}
	write := func(pattern string, f func(*chat.API, http.ResponseWriter, *http.Request)) {
		mux.HandleFunc("POST "+pattern, s.mutating(s.chatAuth(api(f))))
	}
	const c = "/api/chat/chats/{id}"
	write("/api/chat/groups", (*chat.API).CreateGroup)
	read(c+"/info", (*chat.API).GroupInfo)
	write(c+"/info", (*chat.API).GroupEdit)
	read(c+"/members", (*chat.API).GroupMembers)
	write(c+"/members", (*chat.API).GroupAdd)
	write(c+"/members/{user}", (*chat.API).GroupMember)
	write(c+"/leave", (*chat.API).GroupLeave)
	write(c+"/transfer", (*chat.API).GroupTransfer)
	write(c+"/delete", (*chat.API).GroupDelete)
	read(c+"/invites", (*chat.API).GroupInvites)
	write(c+"/invites", (*chat.API).GroupInvites)
	write(c+"/invites/{link}/revoke", (*chat.API).GroupInviteRevoke)
	read(c+"/invites/{link}/joined", (*chat.API).GroupInviteJoined)
	read("/api/chat/invite/{token}", (*chat.API).InviteInfo)
	write("/api/chat/invite/{token}/join", (*chat.API).InviteJoin)
	read(c+"/requests", (*chat.API).GroupRequests)
	write(c+"/requests", (*chat.API).GroupRequests)
	read(c+"/log", (*chat.API).GroupLog)
	read(c+"/topics", (*chat.API).GroupTopics)
	write(c+"/topics", (*chat.API).GroupTopics)
	write(c+"/topics/{topic}", (*chat.API).GroupTopic)
	read(c+"/messages/{msg}/readers", (*chat.API).MessageReaders)
	read(c+"/stats", (*chat.API).GroupStats)
}
