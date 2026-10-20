package find

import (
	_ "embed"
	"net/http"

	"github.com/Schayan-nk/docfind-messenger/internal/auth"
	"github.com/Schayan-nk/docfind-messenger/internal/chat"
)

// The messenger (internal/chat): its page, its socket and its REST API.
// Brief: docs/chat/SPEC.md.

//go:embed messages.html
var messagesHTML []byte

//go:embed messages.js
var messagesJS []byte

//go:embed chatcore.js
var chatcoreJS []byte

// The page's other modules (phase 2 on): shared state, formatting, the open
// chat, the message bar and the overlays.
var (
	//go:embed chatstate.js
	chatstateJS []byte
	//go:embed chatfmt.js
	chatfmtJS []byte
	//go:embed chatpane.js
	chatpaneJS []byte
	//go:embed chatcompose.js
	chatcomposeJS []byte
	//go:embed chatmenu.js
	chatmenuJS []byte
	//go:embed chatwire.js
	chatwireJS []byte
	// Phase 3: files, the viewer and recording (chatrec.js loads on first use).
	//go:embed chatmedia.js
	chatmediaJS []byte
	//go:embed chatattach.js
	chatattachJS []byte
	//go:embed chatviewer.js
	chatviewerJS []byte
	//go:embed chatrec.js
	chatrecJS []byte
	// Phase 4: groups, channels and topics (the admin screens load on first use).
	//go:embed chatgroup.js
	chatgroupJS []byte
	//go:embed chatgroupmake.js
	chatgroupmakeJS []byte
	//go:embed chatgroupadmin.js
	chatgroupadminJS []byte
	//go:embed chatgroup.css
	chatgroupCSS []byte
	// Phase 5: the list's folders and settings, the folder editor (loaded on
	// first use) and search.
	//go:embed chatlist.js
	chatlistJS []byte
	//go:embed chatfolders.js
	chatfoldersJS []byte
	//go:embed chatsearch.js
	chatsearchJS []byte
	//go:embed chatlist.css
	chatlistCSS []byte
	// The Telegram-like look over all of the above (loaded last).
	//go:embed chatglass.css
	chatglassCSS []byte
	// Phase 6: the service worker's messenger part (imported by sw.js) and
	// the page's notification settings and in-app alerts.
	//go:embed chatsw.js
	chatswJS []byte
	//go:embed chatnotify.js
	chatnotifyJS []byte
	// Phase 7: calls (the engine and its screens load when a call starts or
	// rings; the call list on first use).
	//go:embed chatcall.js
	chatcallJS []byte
	//go:embed chatcallrtc.js
	chatcallrtcJS []byte
	//go:embed chatcallui.js
	chatcalluiJS []byte
	//go:embed chatcalls.js
	chatcallsJS []byte
	//go:embed chatcall.css
	chatcallCSS []byte
	// A colleague's contact card from the chat header.
	//go:embed chatperson.js
	chatpersonJS []byte
	// Group calls: the entry (always loaded), the engine and the screen.
	//go:embed chatgcall.js
	chatgcallJS []byte
	//go:embed chatgcallrtc.js
	chatgcallrtcJS []byte
	//go:embed chatgcallui.js
	chatgcalluiJS []byte
)

// Secret chats (phase 10): the protocol, the open chat, the cryptography and
// the store on the device, and their styles.
var (
	//go:embed chatsecret.js
	chatsecretJS []byte
	//go:embed chatsecretpane.js
	chatsecretpaneJS []byte
	//go:embed chatsecretcrypto.js
	chatsecretcryptoJS []byte
	//go:embed chatsecretcss.js
	chatsecretcssJS []byte
)

// UseChat turns the messenger on. Without it every /api/chat route answers
// 404 and the page says the messenger is not set up on this server.
func (s *Server) UseChat(svc *chat.Service) {
	s.chat = svc
	// No document archive and no OCR in the standalone messenger: sharing an
	// archive document and reading text out of photos are off (chat checks
	// both for nil).
	svc.Notify = s.chatNotice
	svc.NotifyCall = s.chatCallNotice
	s.chatAPI = &chat.API{Svc: svc, User: func(r *http.Request) string {
		if u := auth.UserFrom(r); u != nil {
			return u.Username
		}
		return ""
	}}
}

// chatAuth admits any signed-in person except an emergency session (issued
// while accounts came from the snapshot): a messenger write must not race an
// account change the snapshot has not seen.
func (s *Server) chatAuth(next http.HandlerFunc) http.HandlerFunc {
	inner := func(w http.ResponseWriter, r *http.Request) {
		if s.chat == nil {
			http.Error(w, `{"error":"پیام‌رسان روی این سرور فعال نیست."}`, http.StatusNotFound)
			return
		}
		u := auth.UserFrom(r)
		if u == nil || u.Emergency {
			forbidden(w)
			return
		}
		next(w, r)
	}
	if s.guard == nil {
		return func(w http.ResponseWriter, r *http.Request) { forbidden(w) }
	}
	return s.guard.RequireAuth(inner)
}

// chatScripts and chatStyles are the messenger page's own files by path: the
// routes below, and what the service worker keeps so the page opens from the
// phone's copy when the network stalls (workerAssets).
var chatScripts = map[string][]byte{
	"/messages.js": messagesJS, "/chatcore.js": chatcoreJS,
	"/chatstate.js": chatstateJS, "/chatfmt.js": chatfmtJS, "/chatpane.js": chatpaneJS,
	"/chatcompose.js": chatcomposeJS, "/chatmenu.js": chatmenuJS, "/chatwire.js": chatwireJS,
	"/chatmedia.js": chatmediaJS, "/chatattach.js": chatattachJS, "/chatviewer.js": chatviewerJS, "/chatrec.js": chatrecJS,
	"/chatgroup.js": chatgroupJS, "/chatgroupmake.js": chatgroupmakeJS, "/chatgroupadmin.js": chatgroupadminJS,
	"/chatlist.js": chatlistJS, "/chatfolders.js": chatfoldersJS, "/chatsearch.js": chatsearchJS,
	"/chatsw.js": chatswJS, "/chatnotify.js": chatnotifyJS,
	"/chatcall.js": chatcallJS, "/chatcallrtc.js": chatcallrtcJS, "/chatcallui.js": chatcalluiJS, "/chatcalls.js": chatcallsJS,
	"/chatperson.js": chatpersonJS,
	"/chatgcall.js":  chatgcallJS, "/chatgcallrtc.js": chatgcallrtcJS, "/chatgcallui.js": chatgcalluiJS,
	"/chatsecret.js": chatsecretJS, "/chatsecretpane.js": chatsecretpaneJS,
	"/chatsecretcrypto.js": chatsecretcryptoJS, "/chatsecretcss.js": chatsecretcssJS,
}

var chatStyles = map[string][]byte{
	"/chatgroup.css": chatgroupCSS, "/chatlist.css": chatlistCSS, "/chatglass.css": chatglassCSS, "/chatcall.css": chatcallCSS,
}

func (s *Server) routeChat(mux *http.ServeMux) {
	mux.HandleFunc("GET /messages", s.signedInPage(s.handlePage(injectAssets(messagesHTML))))
	for path, body := range chatScripts {
		mux.HandleFunc("GET "+path, s.handleAsset(body, "text/javascript; charset=utf-8"))
	}
	for path, body := range chatStyles {
		mux.HandleFunc("GET "+path, s.handleAsset(body, "text/css; charset=utf-8"))
	}

	mux.HandleFunc("GET /api/chat/ticket", s.chatAuth(s.handleChatTicket))
	// The socket authenticates with a ticket in its first frame (chat.ServeWS),
	// so it is not behind chatAuth: a cookie alone never opens it.
	mux.HandleFunc("GET /api/chat/ws", func(w http.ResponseWriter, r *http.Request) {
		if s.chat == nil {
			http.NotFound(w, r)
			return
		}
		s.chat.ServeWS(w, r)
	})
	mux.HandleFunc("GET /api/chat/chats", s.chatAuth(func(w http.ResponseWriter, r *http.Request) { s.chatAPI.Chats(w, r) }))
	mux.HandleFunc("GET /api/chat/chats/{id}/messages", s.chatAuth(func(w http.ResponseWriter, r *http.Request) { s.chatAPI.Messages(w, r) }))
	mux.HandleFunc("GET /api/chat/people", s.chatAuth(func(w http.ResponseWriter, r *http.Request) {
		// Only an administrator (manage-users) is shown everybody (chat.API.People).
		if s.can(r, auth.ActionManageUsers) {
			r = r.WithContext(chat.WithDirectoryAdmin(r.Context()))
		}
		s.chatAPI.People(w, r)
	}))
	mux.HandleFunc("GET /api/chat/people/{user}/card", s.chatAuth(func(w http.ResponseWriter, r *http.Request) {
		// An administrator (manage-users) may see any colleague's full contact
		// details; everyone else sees PII only for people they share a chat with
		// or who are their contact (chat.API.Card decides from this).
		if s.can(r, auth.ActionManageUsers) {
			r = r.WithContext(chat.WithDirectoryAdmin(r.Context()))
		}
		s.chatAPI.Card(w, r)
	}))
	s.routeChatMedia(mux)
	s.routeChatGroups(mux)
	s.routeChatLists(mux)
	s.routeChatExtras(mux) // phase 11 (chatextrasroutes.go)
	mux.HandleFunc("GET /api/chat/chats/{id}/pins", s.chatAuth(func(w http.ResponseWriter, r *http.Request) { s.chatAPI.Pins(w, r) }))
	mux.HandleFunc("GET /api/chat/chats/{id}/mentions", s.chatAuth(func(w http.ResponseWriter, r *http.Request) { s.chatAPI.Mentions(w, r) }))
	mux.HandleFunc("GET /api/chat/chats/{id}/messages/{msg}/reactions", s.chatAuth(func(w http.ResponseWriter, r *http.Request) { s.chatAPI.Reactors(w, r) }))
	mux.HandleFunc("GET /api/chat/privacy", s.chatAuth(func(w http.ResponseWriter, r *http.Request) { s.chatAPI.PrivacyGet(w, r) }))
	mux.HandleFunc("POST /api/chat/open", s.mutating(s.chatAuth(func(w http.ResponseWriter, r *http.Request) { s.chatAPI.Open(w, r) })))
	mux.HandleFunc("POST /api/chat/block", s.mutating(s.chatAuth(func(w http.ResponseWriter, r *http.Request) { s.chatAPI.Block(w, r) })))
	mux.HandleFunc("POST /api/chat/privacy", s.mutating(s.chatAuth(func(w http.ResponseWriter, r *http.Request) { s.chatAPI.PrivacySet(w, r) })))
	// Phase 7: the call list, rating a call, declining from a notification.
	mux.HandleFunc("GET /api/chat/calls", s.chatAuth(func(w http.ResponseWriter, r *http.Request) { s.chatAPI.CallList(w, r) }))
	mux.HandleFunc("POST /api/chat/calls/{id}/rate", s.mutating(s.chatAuth(func(w http.ResponseWriter, r *http.Request) { s.chatAPI.CallRate(w, r) })))
	mux.HandleFunc("POST /api/chat/calls/{id}/decline", s.mutating(s.chatAuth(func(w http.ResponseWriter, r *http.Request) { s.chatAPI.CallDeclineREST(w, r) })))
	mux.HandleFunc("POST /api/chat/calls/{id}/hide", s.mutating(s.chatAuth(func(w http.ResponseWriter, r *http.Request) { s.chatAPI.CallHideREST(w, r) })))
}

// handleChatTicket is GET /api/chat/ticket: a one-minute ticket the page sends
// as the socket's first frame (auth.ChatTicketAge explains why not the cookie).
func (s *Server) handleChatTicket(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r)
	tok, exp, err := s.guard.Signer.SignChatTicket(u.Username, u.TokenVersion, auth.SessionIDFrom(r))
	if err != nil {
		http.Error(w, `{"error":"خطایی پیش آمد؛ دوباره تلاش کنید."}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, map[string]any{"ticket": tok, "expires": exp.UTC()})
}
