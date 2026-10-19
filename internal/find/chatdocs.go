package find

import (
	"net/http"

	"github.com/Schayan-nk/docfind-messenger/internal/auth"
	"github.com/Schayan-nk/docfind-messenger/internal/chat"
)

// What the messenger borrows from the rest of DocFind (phase 3): documents
// shared in a chat, checked against the same rules as the search page, and
// the text extractor for pictures and PDFs sent in chats.

// routeChatMedia adds the phase 3 routes: uploads, files, documents in chats,
// and the media folder setting.
func (s *Server) routeChatMedia(mux *http.ServeMux) {
	api := func(f func(*chat.API, http.ResponseWriter, *http.Request)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) { f(s.chatAPI, w, r) }
	}
	mux.HandleFunc("POST /api/chat/uploads", s.mutating(s.chatAuth(api((*chat.API).UploadStart))))
	mux.HandleFunc("GET /api/chat/uploads/{id}", s.chatAuth(api((*chat.API).UploadStatus)))
	mux.HandleFunc("PUT /api/chat/uploads/{id}", s.mutating(s.chatAuth(api((*chat.API).UploadChunk))))
	mux.HandleFunc("DELETE /api/chat/uploads/{id}", s.mutating(s.chatAuth(api((*chat.API).UploadCancel))))
	mux.HandleFunc("POST /api/chat/uploads/{id}/done", s.mutating(s.chatAuth(api((*chat.API).UploadDone))))
	mux.HandleFunc("GET /api/chat/media/{id}", s.chatAuth(api((*chat.API).MediaFile)))
	mux.HandleFunc("GET /api/chat/media/{id}/thumb", s.chatAuth(api((*chat.API).MediaFile)))
	mux.HandleFunc("GET /api/chat/chats/{id}/messages/{msg}/doc", s.chatAuth(api((*chat.API).DocOf)))
	mux.HandleFunc("GET /api/chat/settings/media", s.requirePermission(auth.ActionManageSettings, s.chatAuth(api((*chat.API).MediaFolderGet))))
	mux.HandleFunc("POST /api/chat/settings/media", s.mutating(s.requirePermission(auth.ActionManageSettings, s.chatAuth(api((*chat.API).MediaFolderSet)))))
}
