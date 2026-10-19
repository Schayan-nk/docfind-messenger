package find

import (
	_ "embed"
	"net/http"

	"github.com/Schayan-nk/docfind-messenger/internal/auth"
	"github.com/Schayan-nk/docfind-messenger/internal/chat"
)

// Phase 11 of the messenger (docs/chat/SPEC.md): polls, scheduled messages,
// locations, sticker and GIF packs, translation. The page modules and their
// routes; the socket frames are in internal/chat/extras.go.
var (
	//go:embed chatextras.js
	chatextrasJS []byte
	//go:embed chatpoll.js
	chatpollJS []byte
	//go:embed chatplace.js
	chatplaceJS []byte
	//go:embed chatsticker.js
	chatstickerJS []byte
	//go:embed chatpackadmin.js
	chatpackadminJS []byte
	//go:embed chatsched.js
	chatschedJS []byte
	//go:embed chatextrascss.js
	chatextrascssJS []byte
)

func (s *Server) routeChatExtras(mux *http.ServeMux) {
	for path, body := range map[string][]byte{
		"/chatextras.js": chatextrasJS, "/chatpoll.js": chatpollJS, "/chatplace.js": chatplaceJS,
		"/chatsticker.js": chatstickerJS, "/chatpackadmin.js": chatpackadminJS, "/chatsched.js": chatschedJS,
		"/chatextrascss.js": chatextrascssJS,
	} {
		mux.HandleFunc("GET "+path, s.handleAsset(body, "text/javascript; charset=utf-8"))
	}
	api := func(f func(*chat.API, http.ResponseWriter, *http.Request)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) { f(s.chatAPI, w, r) }
	}
	mux.HandleFunc("GET /api/chat/polls/{id}", s.chatAuth(api((*chat.API).PollGet)))
	mux.HandleFunc("GET /api/chat/polls/{id}/voters", s.chatAuth(api((*chat.API).PollVoters)))
	mux.HandleFunc("GET /api/chat/chats/{id}/scheduled", s.chatAuth(api((*chat.API).ScheduledList)))
	mux.HandleFunc("GET /api/chat/translate", s.chatAuth(api((*chat.API).TranslateInfo)))
	mux.HandleFunc("GET /api/chat/chats/{id}/messages/{msg}/translate", s.chatAuth(api((*chat.API).Translate)))
	mux.HandleFunc("GET /api/chat/packs", s.chatAuth(api((*chat.API).Packs)))
	mux.HandleFunc("GET /api/chat/packs/search", s.chatAuth(api((*chat.API).PackSearch)))
	// Making and filling packs is for those who manage DocFind's settings.
	admin := func(f func(*chat.API, http.ResponseWriter, *http.Request)) http.HandlerFunc {
		return s.mutating(s.requirePermission(auth.ActionManageSettings, s.chatAuth(api(f))))
	}
	mux.HandleFunc("POST /api/chat/packs", admin((*chat.API).PackCreate))
	mux.HandleFunc("POST /api/chat/packs/{id}/items", admin((*chat.API).PackUpload))
	mux.HandleFunc("POST /api/chat/packs/{id}/delete", admin((*chat.API).PackDelete))
	mux.HandleFunc("POST /api/chat/packs/items/{id}/delete", admin((*chat.API).PackItemDelete))
}
